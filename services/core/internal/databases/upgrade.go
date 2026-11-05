package databases

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Aggiornamento minor su richiesta (v3-d, GIT-553, ADR 0003 punto 2, DB6).
//
// GIT-551 fissa il digest per database e non aggiorna mai da solo un database esistente quando cambia la
// tabella dell'istanza. Qui c'e' il percorso esplicito: la richiesta (del singolo database, o di tutti
// dall'amministratore d'istanza) fa prima il dump di sicurezza; solo con il dump riuscito il database passa
// al digest corrente della tabella (stesso PVC, riavvio dello StatefulSet). Dump fallito: nessun cambio di
// immagine, il motivo e' in upgrade_error. La major non cambia mai sul posto: si usa SOLO la voce della
// tabella per la major del database, e una voce che non e' di quella major e' rifiutata.

// ErrNoMinorUpdate: il database e' gia' sul digest della tabella.
var ErrNoMinorUpdate = errors.New("il database e' gia' sull'immagine corrente della tabella")

// MinorUpdateAvailable dice se la tabella dell'istanza ha, per la major del database, un digest diverso da
// quello fissato nel database.
func (c *Controller) MinorUpdateAvailable(d store.ManagedDB) bool {
	img, ok := c.cfg.Images[d.PgMajor]
	return ok && d.ImageDigest != nil && *d.ImageDigest != img.Digest
}

// RequestMinorUpgrade registra la richiesta di aggiornamento minor del database. ErrNotReady se non e'
// `running`; ErrNoMinorUpdate se non c'e' niente da aggiornare. Lo esegue il controller (runMinorUpgrade).
func (c *Controller) RequestMinorUpgrade(ctx context.Context, d store.ManagedDB, actor *store.AppActor) error {
	if d.Status != store.DBRunning {
		return ErrNotReady
	}
	if !c.MinorUpdateAvailable(d) {
		return ErrNoMinorUpdate
	}
	if _, err := c.st.RequestManagedDBMinorUpgrade(ctx, d, actor, c.cfg.Now()); err != nil {
		if errors.Is(err, store.ErrDBBusy) {
			return store.ErrDBBusy
		}
		return err
	}
	c.Kick()
	return nil
}

// RequestMinorUpgradeAll e' la richiesta dell'amministratore d'istanza (dopo `gitstack upgrade`): tutti i database
// `running` con un aggiornamento disponibile. Ritorna quanti.
func (c *Controller) RequestMinorUpgradeAll(ctx context.Context, actor *store.AppActor) (int, error) {
	dbs, err := c.st.ListAllManagedDBs(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range dbs {
		if d.Status != store.DBRunning || !c.MinorUpdateAvailable(d) {
			continue
		}
		ok, err := c.st.RequestManagedDBMinorUpgrade(ctx, d, actor, c.cfg.Now())
		if errors.Is(err, store.ErrDBBusy) {
			continue
		}
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	if n > 0 {
		c.Kick()
	}
	return n, nil
}

// sameMajor controlla che il tag dell'immagine sia della major del database (`17-alpine` per la 17).
func sameMajor(tag string, major int) bool {
	want := strconv.Itoa(major)
	if len(tag) < len(want) || tag[:len(want)] != want {
		return false
	}
	if len(tag) == len(want) {
		return true
	}
	return !unicode.IsDigit(rune(tag[len(want)]))
}

// runMinorUpgrade esegue l'aggiornamento pendente e ritorna lo stato con cui il database prosegue: `updating`
// se l'immagine e' cambiata (il giro dopo aspetta il riavvio), `running` se non e' cambiato niente.
func (c *Controller) runMinorUpgrade(ctx context.Context, d store.ManagedDB, req store.UpgradeRequest) (string, *string) {
	fail := func(err error) (string, *string) {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		msg := fmt.Errorf("aggiornamento minor non eseguito, l'immagine non e' cambiata: %s", shortErr(err))
		if ferr := c.st.FinishManagedDBMinorUpgrade(wctx, d, req.RequestedBy, "", msg, c.cfg.Now()); ferr != nil {
			c.cfg.Log.Error("databases: esito dell'aggiornamento non scritto", "db", d.ID, "err", ferr)
		}
		c.cfg.Log.Warn("databases: aggiornamento minor non riuscito", "db", d.ID, "err", err)
		return store.DBRunning, nil
	}
	img, ok := c.cfg.Images[d.PgMajor]
	if !ok {
		return fail(fmt.Errorf("la major %d non e' nella tabella delle immagini dell'istanza", d.PgMajor))
	}
	if !sameMajor(img.Tag, d.PgMajor) {
		return fail(fmt.Errorf("la tabella ha per la major %d l'immagine %s di un'altra major: la major non cambia mai sul posto (DB6)", d.PgMajor, img.Tag))
	}
	if d.ImageDigest != nil && *d.ImageDigest == img.Digest {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = c.st.FinishManagedDBMinorUpgrade(wctx, d, req.RequestedBy, img.Digest, nil, c.cfg.Now())
		return store.DBRunning, nil
	}
	if c.cfg.Verifier == nil {
		return fail(errors.New("verifica cosign non configurata"))
	}
	if err := c.cfg.Verifier.Verify(ctx, img.Ref()); err != nil {
		return fail(err)
	}
	reason := "aggiornamento minor: dump di sicurezza"
	_ = c.st.SetManagedDBStatus(ctx, d.ID, store.DBUpdating, &reason, c.cfg.Now())
	if _, err := c.Backup(ctx, d, store.BackupSafety, req.RequestedBy); err != nil {
		return fail(fmt.Errorf("dump di sicurezza non riuscito: %w", err))
	}
	now := c.cfg.Now()
	if err := c.st.SetManagedDBImage(ctx, d.ID, img.Digest, now); err != nil {
		return fail(err)
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := c.st.FinishManagedDBMinorUpgrade(wctx, d, req.RequestedBy, img.Digest, nil, now); err != nil {
		c.cfg.Log.Error("databases: esito dell'aggiornamento non scritto", "db", d.ID, "err", err)
	}
	c.Kick()
	return store.DBUpdating, nil
}

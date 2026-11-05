package databases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Collegamento app-database (v3-d, GIT-552, ADR 0003 punto 4, DB9-DB12). I ruoli per link e l'ordine di
// scollegamento sono in roles.go (GIT-551): qui si orchestrano insieme alle variabili dell'app.

const (
	// CAMountDir e CAMountFile: dove l'app trova la CA pubblica (PGSSLROOTCERT, DB10).
	CAMountDir  = "/etc/gitstack/db"
	CAMountFile = CAMountDir + "/ca.crt"
	// RootCAFileName e' il file con la radice pubblica accanto a tls.crt/tls.key dell'intermedia: i client si
	// fidano della radice, non dell'intermedia (OpenSSL non accetta come àncora un'intermedia).
	RootCAFileName = "ca.crt"
)

// ErrNoRootCA: la radice pubblica non e' nel Secret della CA dei database (va riscritto con `gitstack databases ensure`).
var ErrNoRootCA = errors.New("la CA radice pubblica non e' nel Secret dei database: esegui `gitstack databases ensure`")

// ErrLinkUnlinking: il collegamento e' in scollegamento, non si ruota.
var ErrLinkUnlinking = errors.New("il collegamento e' in scollegamento")

// RootCA legge la radice pubblica (PEM) da caDir.
func RootCA(caDir string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(caDir, RootCAFileName))
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return nil, ErrNoRootCA
	}
	return b, nil
}

// LinkLabel e' la label del namespace dell'app collegata: la NetworkPolicy del database ammette l'ingresso da li'.
func LinkLabel(dbID uuid.UUID) string { return LabelDBLinkPrefix + dbID.String() }

// PodLabels sono le label dei pod del database: la regola di uscita dell'app punta a loro.
func PodLabels(dbID uuid.UUID) map[string]string { return selectorLabels(dbID) }

// LinkCredentials sono le credenziali di un collegamento (DatabaseLinkCredentials del contratto).
type LinkCredentials struct {
	Username, Password, Host, Database, SSLMode, SSLRootCert string
	Port                                                     int
}

// NewLinkCredentials dice con quali credenziali l'app si collega.
func NewLinkCredentials(d store.ManagedDB, linkID uuid.UUID, password string) LinkCredentials {
	return LinkCredentials{Username: LinkRole(linkID), Password: password, Host: ServiceHost(d.ID), Port: PostgresPort,
		Database: d.Name, SSLMode: "verify-full", SSLRootCert: CAMountFile}
}

// Env sono le variabili iniettate nell'app come segreti (DB10).
func (c LinkCredentials) Env() map[string]string {
	u := url.URL{Scheme: "postgresql", User: url.UserPassword(c.Username, c.Password), Host: c.Host + ":" + strconv.Itoa(c.Port), Path: "/" + c.Database,
		RawQuery: "sslmode=" + c.SSLMode + "&sslrootcert=" + url.QueryEscape(c.SSLRootCert)}
	return map[string]string{
		"DATABASE_URL":  u.String(),
		"PGHOST":        c.Host,
		"PGPORT":        strconv.Itoa(c.Port),
		"PGDATABASE":    c.Database,
		"PGUSER":        c.Username,
		"PGPASSWORD":    c.Password,
		"PGSSLMODE":     c.SSLMode,
		"PGSSLROOTCERT": c.SSLRootCert,
	}
}

// linkLock serializza collegamento, scollegamento e pulizia dei ruoli di UN database.
func (c *Controller) linkLock(id uuid.UUID) *sync.Mutex {
	m, _ := c.linkLocks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// LinkApp crea il ruolo di login del collegamento, scrive le variabili come segreti dell'app e crea la
// versione con il suo deploy (stessa transazione: se il ruolo non si crea non resta niente). La rete la
// apre il controller delle app al deploy (label sul namespace e regola di uscita).
func (c *Controller) LinkApp(ctx context.Context, keys store.AppSealer, d store.ManagedDB, app store.App, mode string, actor *store.AppActor) (store.ManagedDBLink, LinkCredentials, error) {
	if d.Status != store.DBRunning {
		return store.ManagedDBLink{}, LinkCredentials{}, ErrNotReady
	}
	if c.sql == nil {
		return store.ManagedDBLink{}, LinkCredentials{}, errors.New("nessun esecutore SQL configurato")
	}
	if mode != ModeRW && mode != ModeRO {
		return store.ManagedDBLink{}, LinkCredentials{}, fmt.Errorf("modalita' del collegamento sconosciuta: %q", mode)
	}
	if _, err := RootCA(c.cfg.CADir); err != nil {
		return store.ManagedDBLink{}, LinkCredentials{}, err
	}
	pw, err := NewPassword()
	if err != nil {
		return store.ManagedDBLink{}, LinkCredentials{}, err
	}
	c.remember(pw)
	linkID := uuid.New()
	creds := NewLinkCredentials(d, linkID, pw)
	mu := c.linkLock(d.ID)
	mu.Lock()
	defer mu.Unlock()
	created := false
	link, _, err := c.st.LinkAppDB(ctx, keys, store.LinkAppDBInput{
		DB: d, LinkID: linkID, AppID: app.ID, Mode: mode, Env: creds.Env(), Actor: actor,
		App: store.ManagedDBAppRef{Owner: app.Owner, Repo: app.RepoName, Name: app.Name},
	}, func(ctx context.Context) error {
		if err := CreateLinkRole(ctx, c.sql, d, linkID, mode, pw); err != nil {
			return fmt.Errorf("ruolo del collegamento: %w", err)
		}
		created = true
		return nil
	}, c.cfg.Now())
	if err != nil {
		if created { // il commit e' fallito dopo il ruolo: non resta un ruolo senza collegamento
			c.dropOrphan(context.WithoutCancel(ctx), d, linkID)
		}
		return store.ManagedDBLink{}, LinkCredentials{}, err
	}
	c.Kick()
	return link, creds, nil
}

// SecretLinkRotatePrefix: Secret nel namespace del database con l'INTENTO di rotazione di un collegamento
// (GIT-552). La password nuova vi sta cifrata col portachiavi dei segreti (stessa chiave delle app) e resta
// finche' ruolo, segreti dell'app e versione non sono allineati: e' cio' che rende la rotazione riprendibile
// dopo un errore o il riavvio di core.
const SecretLinkRotatePrefix = "pg-link-rotate-"

type rotateIntent struct {
	password string
	actor    *store.AppActor
}

func rotateSecretName(linkID uuid.UUID) string { return SecretLinkRotatePrefix + linkID.String() }

func (c *Controller) loadIntent(ctx context.Context, keys store.AppSealer, d store.ManagedDB, linkID uuid.UUID) (*rotateIntent, error) {
	sec, err := c.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, rotateSecretName(linkID), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pw, err := keys.Open(linkID, sec.Data["ciphertext"], sec.Data["nonce"], string(sec.Data["key-id"]))
	if err != nil {
		return nil, fmt.Errorf("intento di rotazione non decifrabile: %w", err)
	}
	in := &rotateIntent{password: pw}
	if raw := sec.Data["actor"]; len(raw) > 0 {
		var a store.AppActor
		if json.Unmarshal(raw, &a) == nil {
			in.actor = &a
		}
	}
	return in, nil
}

func (c *Controller) saveIntent(ctx context.Context, keys store.AppSealer, d store.ManagedDB, linkID uuid.UUID, pw string, actor *store.AppActor) error {
	ct, nonce, keyID, err := keys.Seal(linkID, pw)
	if err != nil {
		return err
	}
	data := map[string][]byte{"ciphertext": ct, "nonce": nonce, "key-id": []byte(keyID)}
	if actor != nil {
		if raw, err := json.Marshal(actor); err == nil {
			data["actor"] = raw
		}
	}
	ns := Namespace(d.ID)
	_, err = c.kube.CoreV1().Secrets(ns).Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: rotateSecretName(linkID), Namespace: ns, Labels: objectLabels(d.ID)}, Data: data}, metav1.CreateOptions{})
	return err
}

func (c *Controller) dropIntent(ctx context.Context, d store.ManagedDB, linkID uuid.UUID) {
	err := c.kube.CoreV1().Secrets(Namespace(d.ID)).Delete(ctx, rotateSecretName(linkID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) && ctx.Err() == nil {
		c.cfg.Log.Warn("databases: intento di rotazione non rimosso", "db", d.ID, "link", linkID, "err", err)
	}
}

// RotateLink ruota la password del collegamento in modo riprendibile (DB12). Prima si PERSISTE l'intento (la
// password nuova, cifrata); poi, in modo idempotente: ALTER ROLE e chiusura delle sessioni, riscrittura dei segreti
// dell'app con la nuova versione e il suo deploy (una transazione), infine si toglie l'intento. Se un passo
// fallisce (o core riparte) l'intento resta e ResumeRotations porta tutto a convergenza; una nuova richiesta di
// rotazione riusa la stessa password invece di generarne un'altra.
func (c *Controller) RotateLink(ctx context.Context, keys store.AppSealer, d store.ManagedDB, link store.ManagedDBLink, actor *store.AppActor) (LinkCredentials, error) {
	if d.Status != store.DBRunning {
		return LinkCredentials{}, ErrNotReady
	}
	mu := c.linkLock(d.ID)
	mu.Lock()
	defer mu.Unlock()
	// Rilegge sotto serratura: lo stato puo' essere cambiato dopo la lettura dell'API.
	cur, err := c.st.GetManagedDBLink(ctx, d.ID, link.ID)
	if err != nil {
		return LinkCredentials{}, err
	}
	if cur.State != "active" {
		return LinkCredentials{}, ErrLinkUnlinking
	}
	in, err := c.loadIntent(ctx, keys, d, cur.ID)
	if err != nil {
		return LinkCredentials{}, err
	}
	if in == nil {
		pw, err := NewPassword()
		if err != nil {
			return LinkCredentials{}, err
		}
		if err := c.saveIntent(ctx, keys, d, cur.ID, pw, actor); err != nil {
			return LinkCredentials{}, fmt.Errorf("intento di rotazione non salvato: %w", err)
		}
		in = &rotateIntent{password: pw, actor: actor}
	}
	c.remember(in.password)
	if err := c.applyRotation(ctx, keys, d, cur, in); err != nil {
		return LinkCredentials{}, err
	}
	return NewLinkCredentials(d, cur.ID, in.password), nil
}

// applyRotation e' idempotente: se i segreti dell'app hanno gia' la password dell'intento la rotazione e'
// arrivata in fondo e resta solo da togliere l'intento.
func (c *Controller) applyRotation(ctx context.Context, keys store.AppSealer, d store.ManagedDB, link store.ManagedDBLink, in *rotateIntent) error {
	have, err := c.st.AppDBLinkPassword(ctx, keys, link.AppID)
	if err != nil {
		return err
	}
	if have == in.password {
		c.dropIntent(ctx, d, link.ID)
		return nil
	}
	if err := RotateLinkRole(ctx, c.sql, d, link.ID, in.password); err != nil {
		return fmt.Errorf("rotazione del ruolo del collegamento: %w", err)
	}
	creds := NewLinkCredentials(d, link.ID, in.password)
	if _, err := c.st.RotateAppDBLink(ctx, keys, d, link.ID, link.AppID, creds.Env(), in.actor, c.cfg.Now()); err != nil {
		return err
	}
	c.dropIntent(ctx, d, link.ID)
	return nil
}

// ResumeRotations riprende le rotazioni rimaste a meta' (intento presente) dei collegamenti attivi di un
// database pronto. Serve il portachiavi (Config.Secrets).
func (c *Controller) ResumeRotations(ctx context.Context, d store.ManagedDB) {
	keys := c.cfg.Secrets
	if keys == nil || c.sql == nil {
		return
	}
	links, err := c.st.ListManagedDBLinks(ctx, d.ID)
	if err != nil {
		return
	}
	for _, l := range links {
		if l.State != "active" {
			continue
		}
		mu := c.linkLock(d.ID)
		mu.Lock()
		if cur, err := c.st.GetManagedDBLink(ctx, d.ID, l.ID); err == nil && cur.State == "active" {
			in, err := c.loadIntent(ctx, keys, d, cur.ID)
			switch {
			case err != nil:
				c.cfg.Log.Warn("databases: intento di rotazione illeggibile", "db", d.ID, "link", cur.ID, "err", err)
			case in != nil:
				c.remember(in.password)
				if err := c.applyRotation(ctx, keys, d, cur, in); err != nil && ctx.Err() == nil {
					c.cfg.Log.Warn("databases: rotazione non completata, si ritenta", "db", d.ID, "link", cur.ID, "err", err)
				}
			}
		}
		mu.Unlock()
	}
}

// RoleExists dice se il ruolo del collegamento c'e' ancora nel database.
func RoleExists(ctx context.Context, r SQLRunner, d store.ManagedDB, role string) (bool, error) {
	rows, err := r.Query(ctx, d, "postgres", fmt.Sprintf(`SELECT 1 FROM pg_roles WHERE rolname = %s`, QuoteLiteral(role)))
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

// Unlink scollega nell'ordine dell'ADR (DB12): NOLOGIN, chiusura delle sessioni, DROP OWNED/DROP ROLE, e SOLO
// dopo variabili e Secret dell'app (nuova versione e deploy); label e regola di uscita le toglie il controller
// delle app a quel deploy. Ritorna done=false se le sessioni non si chiudono entro il timeout: il collegamento
// resta `unlinking` e il ciclo ritenta.
func (c *Controller) Unlink(ctx context.Context, d store.ManagedDB, link store.ManagedDBLink, actor *store.AppActor) (done bool, err error) {
	mu := c.linkLock(d.ID)
	mu.Lock()
	defer mu.Unlock()
	return c.unlinkLocked(ctx, d, link, actor, "unlink")
}

func (c *Controller) unlinkLocked(ctx context.Context, d store.ManagedDB, link store.ManagedDBLink, actor *store.AppActor, reason string) (bool, error) {
	// Lo snapshot preso prima della serratura puo' essere obsoleto (scollegamento gia' completato da un altro
	// giro, app ricollegata): si rilegge la riga ESATTA; se non c'e' piu' non c'e' niente da fare.
	cur, err := c.st.GetManagedDBLink(ctx, d.ID, link.ID)
	if errors.Is(err, store.ErrManagedDBLinkNotFound) || (err == nil && cur.AppID != link.AppID) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	link = cur
	if link.State != "unlinking" {
		if err := c.st.SetManagedDBLinkState(ctx, link.ID, "unlinking"); err != nil {
			return false, err
		}
		link.State = "unlinking"
	}
	if d.Status != store.DBRunning {
		return false, nil // senza pod non si tocca il ruolo: ritenta il ciclo quando e' `running`
	}
	if c.sql == nil {
		return false, errors.New("nessun esecutore SQL configurato")
	}
	exists, err := RoleExists(ctx, c.sql, d, LinkRole(link.ID))
	if err != nil {
		return false, err
	}
	if exists {
		if err := DropLinkRole(ctx, c.sql, d, link.ID, c.cfg.Unlink); err != nil {
			if errors.Is(err, ErrSessionsRemain) {
				return false, nil
			}
			return false, err
		}
	}
	return true, c.completeUnlink(ctx, d, link, actor, reason)
}

func (c *Controller) completeUnlink(ctx context.Context, d store.ManagedDB, link store.ManagedDBLink, actor *store.AppActor, reason string) error {
	ref := store.ManagedDBAppRef{}
	if app, err := c.st.GetAppByID(ctx, link.AppID); err == nil {
		ref = store.ManagedDBAppRef{Owner: app.Owner, Repo: app.RepoName, Name: app.Name}
	}
	if _, _, err := c.st.CompleteDBUnlink(ctx, d, link, ref, reason, actor, c.cfg.Now()); err != nil {
		return err
	}
	c.dropIntent(ctx, d, link.ID)
	return nil
}

// ResumeUnlinks riprende gli scollegamenti rimasti a meta' (`unlinking`) di un database pronto.
func (c *Controller) ResumeUnlinks(ctx context.Context, d store.ManagedDB) {
	links, err := c.st.ListManagedDBLinks(ctx, d.ID)
	if err != nil {
		c.cfg.Log.Warn("databases: collegamenti non letti", "db", d.ID, "err", err)
		return
	}
	for _, l := range links {
		if l.State != "unlinking" {
			continue
		}
		mu := c.linkLock(d.ID)
		mu.Lock()
		done, err := c.unlinkLocked(ctx, d, l, nil, "unlink")
		mu.Unlock()
		if err != nil && ctx.Err() == nil {
			c.cfg.Log.Warn("databases: scollegamento non completato, si ritenta", "db", d.ID, "link", l.ID, "err", err)
		}
		_ = done
	}
}

var linkRoleRE = regexp.MustCompile(`^l_[0-9a-f]{32}$`)

// SweepOrphanRoles toglie i ruoli l_<id> che non hanno piu' un collegamento: l'app o il repo eliminati portano
// via la riga a cascata (DB9), un commit fallito lascia il ruolo. Lo stesso ordine dell'ADR (DB12). Al piu' una
// volta ogni LinkSweepEvery per database.
func (c *Controller) SweepOrphanRoles(ctx context.Context, d store.ManagedDB) {
	c.mu.Lock()
	last := c.sweepAt[d.ID]
	due := c.cfg.Now().Sub(last) >= c.cfg.SweepEvery
	if due {
		c.sweepAt[d.ID] = c.cfg.Now()
	}
	c.mu.Unlock()
	if !due || c.sql == nil {
		return
	}
	mu := c.linkLock(d.ID)
	mu.Lock()
	defer mu.Unlock()
	roles, err := c.sql.Query(ctx, d, "postgres", `SELECT rolname FROM pg_roles WHERE rolname ~ '^l_[0-9a-f]{32}$'`)
	if err != nil {
		c.cfg.Log.Warn("databases: ruoli dei collegamenti non letti", "db", d.ID, "err", err)
		return
	}
	known, err := c.st.ManagedDBLinkIDs(ctx, d.ID)
	if err != nil {
		return
	}
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if !linkRoleRE.MatchString(role) {
			continue
		}
		id, err := uuid.Parse(role[2:])
		if err != nil || known[id] {
			continue
		}
		c.dropOrphan(ctx, d, id)
	}
}

// LinkSweepEvery: ogni quanto si cercano ruoli senza collegamento.
const LinkSweepEvery = time.Minute

func (c *Controller) dropOrphan(ctx context.Context, d store.ManagedDB, id uuid.UUID) {
	if err := DropLinkRole(ctx, c.sql, d, id, c.cfg.Unlink); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("databases: ruolo senza collegamento non rimosso", "db", d.ID, "role", LinkRole(id), "err", err)
		return
	}
	c.cfg.Log.Info("databases: ruolo senza collegamento rimosso", "db", d.ID, "role", LinkRole(id))
}

// detachApps toglie i collegamenti di un database in eliminazione: le app perdono le variabili (nuova
// versione e deploy, DB19). I ruoli spariscono col namespace: qui non serve il DROP ROLE.
func (c *Controller) detachApps(ctx context.Context, d store.ManagedDB, actor *store.AppActor) error {
	links, err := c.st.ListManagedDBLinks(ctx, d.ID)
	if err != nil {
		return err
	}
	for _, l := range links {
		if err := c.completeUnlink(ctx, d, l, actor, "database_deleted"); err != nil {
			return err
		}
	}
	return nil
}

package databases

import (
	"context"
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

// RotateLink cambia la password del ruolo del collegamento e chiude le sue sessioni (DB12), poi riscrive i
// segreti dell'app con una versione nuova: l'app riparte con la password nuova, la vecchia non entra piu'.
func (c *Controller) RotateLink(ctx context.Context, keys store.AppSealer, d store.ManagedDB, link store.ManagedDBLink, actor *store.AppActor) (LinkCredentials, error) {
	if d.Status != store.DBRunning {
		return LinkCredentials{}, ErrNotReady
	}
	if link.State != "active" {
		return LinkCredentials{}, ErrLinkUnlinking
	}
	pw, err := NewPassword()
	if err != nil {
		return LinkCredentials{}, err
	}
	c.remember(pw)
	mu := c.linkLock(d.ID)
	mu.Lock()
	defer mu.Unlock()
	if err := RotateLinkRole(ctx, c.sql, d, link.ID, pw); err != nil {
		return LinkCredentials{}, fmt.Errorf("rotazione del ruolo del collegamento: %w", err)
	}
	creds := NewLinkCredentials(d, link.ID, pw)
	if _, err := c.st.RotateAppDBLink(ctx, keys, d, link.ID, link.AppID, creds.Env(), actor, c.cfg.Now()); err != nil {
		return LinkCredentials{}, err
	}
	return creds, nil
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
	_, _, err := c.st.CompleteDBUnlink(ctx, d, link, ref, reason, actor, c.cfg.Now())
	return err
}

// finishUnlinks riprende gli scollegamenti rimasti a meta' (`unlinking`) di un database pronto.
func (c *Controller) finishUnlinks(ctx context.Context, d store.ManagedDB) {
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

// sweepOrphanRoles toglie i ruoli l_<id> che non hanno piu' un collegamento: l'app o il repo eliminati portano
// via la riga a cascata (DB9), un commit fallito lascia il ruolo. Lo stesso ordine dell'ADR (DB12). Al piu' una
// volta ogni LinkSweepEvery per database.
func (c *Controller) sweepOrphanRoles(ctx context.Context, d store.ManagedDB) {
	c.mu.Lock()
	last := c.sweepAt[d.ID]
	due := c.cfg.Now().Sub(last) >= LinkSweepEvery
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

//go:build integration

package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// pgRunner e' un SQLRunner su un PostgreSQL vero (il container dei test) come superuser: stessa semantica di
// psql come gitstack_admin sul socket locale, senza passare dal pod. La logica dei ruoli (DB7, DB9, DB12) e'
// quella di produzione; le prove su k3d con le immagini del chart sono nei test *_k3d_integration_test.go.
type pgRunner struct {
	t          *testing.T
	dsn        string
	skipKill   bool // simula l'assenza di pg_terminate_backend (test negativo di DB12)
	terminated int
}

func (p *pgRunner) conn(ctx context.Context, database string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(p.dsn)
	if err != nil {
		return nil, err
	}
	if database != "" {
		cfg.Database = database
	}
	return pgx.ConnectConfig(ctx, cfg)
}

func (p *pgRunner) Exec(ctx context.Context, _ store.ManagedDB, database, sql string) error {
	if p.skipKill && strings.Contains(sql, "pg_terminate_backend") {
		return nil
	}
	c, err := p.conn(ctx, p.databaseName(database))
	if err != nil {
		return err
	}
	defer func() { _ = c.Close(ctx) }()
	_, err = c.Exec(ctx, sql)
	return err
}

func (p *pgRunner) Query(ctx context.Context, _ store.ManagedDB, database, sql string) ([]string, error) {
	c, err := p.conn(ctx, p.databaseName(database))
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close(ctx) }()
	rows, err := c.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprint(v))
	}
	return out, rows.Err()
}

// "postgres" e' il database di servizio del pod; nel container dei test e' quello del template.
func (p *pgRunner) databaseName(database string) string {
	if database == "postgres" {
		return ""
	}
	return database
}

func (p *pgRunner) loginAs(ctx context.Context, user, password, database string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(p.dsn)
	if err != nil {
		return nil, err
	}
	cfg.User, cfg.Password, cfg.Database = user, password, database
	return pgx.ConnectConfig(ctx, cfg)
}

type rolesEnv struct {
	r *pgRunner
	d store.ManagedDB
}

// newRolesEnv crea ruoli e database di un database gestito con nomi unici (i ruoli sono globali al container).
func newRolesEnv(t *testing.T) *rolesEnv {
	t.Helper()
	_, dsn := dbtest.NewPool(t)
	name := "t" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	d := store.ManagedDB{ID: uuid.New(), OwnerName: "alice", Name: name, PgMajor: 17, SizeBytes: 1 << 30}
	r := &pgRunner{t: t, dsn: dsn}
	ctx := context.Background()
	if err := SetupRoles(ctx, r, d, "ownerpw"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := r.conn(context.Background(), "")
		if err != nil {
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, _ = c.Exec(context.Background(), `DROP DATABASE IF EXISTS `+QuoteIdent(name)+` WITH (FORCE)`)
		for _, role := range []string{OwnerRole(name), ReadOnlyRole(name)} {
			_, _ = c.Exec(context.Background(), `DROP ROLE IF EXISTS `+QuoteIdent(role))
		}
	})
	return &rolesEnv{r: r, d: d}
}

func (e *rolesEnv) scalar(t *testing.T, sql string, args ...any) string {
	t.Helper()
	c, err := e.r.conn(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	var v any
	if err := c.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return fmt.Sprint(v)
}

// DB7: <name>_owner non e' superuser (ne' CREATEROLE/CREATEDB), possiede il database; <name>_ro e' un gruppo
// nologin membro di pg_read_all_data, in sola lettura; PUBLIC non puo' connettersi.
func TestDatabases_RuoliDB7(t *testing.T) {
	e := newRolesEnv(t)
	ctx := context.Background()
	owner, ro := OwnerRole(e.d.Name), ReadOnlyRole(e.d.Name)
	if got := e.scalar(t, `SELECT rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication FROM pg_roles WHERE rolname = $1`, owner); got != "false" {
		t.Fatalf("%s non deve avere privilegi di istanza: %s", owner, got)
	}
	if got := e.scalar(t, `SELECT rolcanlogin FROM pg_roles WHERE rolname = $1`, owner); got != "true" {
		t.Fatalf("%s deve poter fare login", owner)
	}
	if got := e.scalar(t, `SELECT rolcanlogin FROM pg_roles WHERE rolname = $1`, ro); got != "false" {
		t.Fatalf("%s e' un gruppo nologin", ro)
	}
	if got := e.scalar(t, `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1`, e.d.Name); got != owner {
		t.Fatalf("proprietario del database: %s", got)
	}
	if got := e.scalar(t, `SELECT pg_has_role($1, 'pg_read_all_data', 'member')`, ro); got != "true" {
		t.Fatalf("%s deve essere membro di pg_read_all_data", ro)
	}
	// L'owner crea e scrive; il membro di _ro legge e non scrive.
	c, err := e.r.loginAs(ctx, owner, "ownerpw", e.d.Name)
	if err != nil {
		t.Fatalf("login dell'owner: %v", err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, `CREATE TABLE items (id int); INSERT INTO items VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	// SetupRoles e' idempotente e, dopo una tabella nuova, ReapplyROPrivileges le da' SELECT a _ro (DB15).
	if err := SetupRoles(ctx, e.r, e.d, "ownerpw"); err != nil {
		t.Fatalf("SetupRoles ripetuto: %v", err)
	}
	linkID := uuid.New()
	if err := CreateLinkRole(ctx, e.r, e.d, linkID, ModeRO, "ropw"); err != nil {
		t.Fatal(err)
	}
	roConn, err := e.r.loginAs(ctx, LinkRole(linkID), "ropw", e.d.Name)
	if err != nil {
		t.Fatalf("login ro: %v", err)
	}
	defer func() { _ = roConn.Close(ctx) }()
	var n int
	if err := roConn.QueryRow(ctx, `SELECT count(*) FROM items`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("lettura ro: %d %v", n, err)
	}
	if _, err := roConn.Exec(ctx, `INSERT INTO items VALUES (2)`); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("la scrittura ro doveva essere negata: %v", err)
	}
	// Un ruolo qualunque senza grant non si connette al database (CONNECT tolto a PUBLIC).
	if err := e.r.Exec(ctx, e.d, "postgres", `CREATE ROLE "estraneo_`+e.d.Name+`" LOGIN PASSWORD 'x'`); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.r.Exec(ctx, e.d, "postgres", `DROP ROLE "estraneo_`+e.d.Name+`"`) }()
	if _, err := e.r.loginAs(ctx, "estraneo_"+e.d.Name, "x", e.d.Name); err == nil {
		t.Fatal("PUBLIC non deve poter connettersi al database")
	}
}

// DB9: il ruolo di un collegamento rw crea oggetti che appartengono al proprietario; DROP ROLE riesce; il
// ruolo non ha CREATEROLE e non possiede nulla.
func TestDatabaseLinks_ProprietaOggetti(t *testing.T) {
	e := newRolesEnv(t)
	ctx := context.Background()
	linkID := uuid.New()
	if err := CreateLinkRole(ctx, e.r, e.d, linkID, ModeRW, "linkpw"); err != nil {
		t.Fatal(err)
	}
	role := LinkRole(linkID)
	if got := e.scalar(t, `SELECT rolsuper OR rolcreaterole OR rolcreatedb FROM pg_roles WHERE rolname = $1`, role); got != "false" {
		t.Fatalf("il ruolo del link non deve avere CREATEROLE/CREATEDB/superuser: %s", got)
	}
	c, err := e.r.loginAs(ctx, role, "linkpw", e.d.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, `CREATE TABLE creata_dal_link (id int); CREATE SEQUENCE seq_link; INSERT INTO creata_dal_link VALUES (7)`); err != nil {
		t.Fatalf("il link rw deve poter creare e scrivere: %v", err)
	}
	if _, err := c.Exec(ctx, `CREATE ROLE intruso`); err == nil {
		t.Fatal("il link non deve poter creare ruoli")
	}
	_ = c.Close(ctx)
	oc, err := e.r.conn(ctx, e.d.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = oc.Close(ctx) }()
	var tableOwner, seqOwner string
	if err := oc.QueryRow(ctx, `SELECT tableowner FROM pg_tables WHERE tablename = 'creata_dal_link'`).Scan(&tableOwner); err != nil || tableOwner != OwnerRole(e.d.Name) {
		t.Fatalf("proprietario della tabella: %q (%v), voluto %s", tableOwner, err, OwnerRole(e.d.Name))
	}
	if err := oc.QueryRow(ctx, `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE relname = 'seq_link'`).Scan(&seqOwner); err != nil || seqOwner != OwnerRole(e.d.Name) {
		t.Fatalf("proprietario della sequenza: %q (%v)", seqOwner, err)
	}
	if err := DropLinkRole(ctx, e.r, e.d, linkID, UnlinkOptions{Every: 10 * time.Millisecond}); err != nil {
		t.Fatalf("DROP ROLE del link doveva riuscire: %v", err)
	}
	if got := e.scalar(t, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, role); got != "0" {
		t.Fatal("il ruolo del link esiste ancora")
	}
	var cnt int
	if err := oc.QueryRow(ctx, `SELECT count(*) FROM creata_dal_link`).Scan(&cnt); err != nil || cnt != 1 {
		t.Fatalf("i dati creati dal link restano all'owner: %d %v", cnt, err)
	}
}

// persistent apre una sessione che esegue query in loop e conta i successi; termina con l'errore che la ferma.
type persistent struct {
	conn *pgx.Conn
	ok   chan struct{}
	done chan error
}

func startPersistent(t *testing.T, e *rolesEnv, user, password string) *persistent {
	t.Helper()
	ctx := context.Background()
	c, err := e.r.loginAs(ctx, user, password, e.d.Name)
	if err != nil {
		t.Fatalf("login di %s: %v", user, err)
	}
	p := &persistent{conn: c, ok: make(chan struct{}, 1024), done: make(chan error, 1)}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop); _ = c.Close(context.Background()) })
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			var one int
			if err := c.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
				p.done <- err
				return
			}
			select {
			case p.ok <- struct{}{}:
			default:
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return p
}

func (p *persistent) waitOK(t *testing.T, what string) {
	t.Helper()
	select {
	case <-p.ok:
	case err := <-p.done:
		t.Fatalf("%s: la sessione si e' fermata: %v", what, err)
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: nessuna query riuscita", what)
	}
}

// DB11/DB12: una connessione persistente che esegue query in loop riceve «terminating connection» allo
// scollegamento e smette; il nuovo login e' respinto; la sessione persistente di un altro link continua.
func TestDatabaseLinks_SessionePersistenteChiusaAlloScollegamento(t *testing.T) {
	e := newRolesEnv(t)
	ctx := context.Background()
	l1, l2 := uuid.New(), uuid.New()
	for _, l := range []uuid.UUID{l1, l2} {
		if err := CreateLinkRole(ctx, e.r, e.d, l, ModeRW, "pw-"+l.String()[:8]); err != nil {
			t.Fatal(err)
		}
	}
	s1 := startPersistent(t, e, LinkRole(l1), "pw-"+l1.String()[:8])
	s2 := startPersistent(t, e, LinkRole(l2), "pw-"+l2.String()[:8])
	s1.waitOK(t, "link 1 prima")
	s2.waitOK(t, "link 2 prima")

	if err := DropLinkRole(ctx, e.r, e.d, l1, UnlinkOptions{Every: 20 * time.Millisecond}); err != nil {
		t.Fatalf("scollegamento: %v", err)
	}
	select {
	case err := <-s1.done:
		if !isTerminated(err) {
			t.Fatalf("la sessione doveva ricevere «terminating connection» (57P01): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("la sessione persistente del link scollegato continua a eseguire query")
	}
	if _, err := e.r.loginAs(ctx, LinkRole(l1), "pw-"+l1.String()[:8], e.d.Name); err == nil {
		t.Fatal("il nuovo login del link scollegato doveva essere respinto")
	}
	// L'altro link continua, anche dopo lo scollegamento del primo.
	for len(s2.ok) > 0 {
		<-s2.ok
	}
	s2.waitOK(t, "link 2 dopo")
	select {
	case err := <-s2.done:
		t.Fatalf("la sessione dell'altro link e' stata toccata: %v", err)
	default:
	}
}

// DB12 (negativo): senza pg_terminate_backend la sessione persistente continua e lo scollegamento NON si
// completa: il ruolo resta, il link resta `unlinking`. Il test fallirebbe se il passo mancasse nel codice.
func TestDatabaseLinks_SenzaTerminateLoScollegamentoNonCompleta(t *testing.T) {
	e := newRolesEnv(t)
	e.r.skipKill = true
	ctx := context.Background()
	l := uuid.New()
	if err := CreateLinkRole(ctx, e.r, e.d, l, ModeRW, "pw-negativo"); err != nil {
		t.Fatal(err)
	}
	s := startPersistent(t, e, LinkRole(l), "pw-negativo")
	s.waitOK(t, "prima")
	err := DropLinkRole(ctx, e.r, e.d, l, UnlinkOptions{Timeout: 400 * time.Millisecond, Every: 50 * time.Millisecond})
	if !errors.Is(err, ErrSessionsRemain) {
		t.Fatalf("errore: %v", err)
	}
	if got := e.scalar(t, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, LinkRole(l)); got != "1" {
		t.Fatal("il ruolo non si elimina con sessioni aperte")
	}
	for len(s.ok) > 0 {
		<-s.ok
	}
	s.waitOK(t, "dopo (solo NOLOGIN non chiude la sessione)")
	// Con il passo presente lo scollegamento riprende e completa.
	e.r.skipKill = false
	if err := DropLinkRole(ctx, e.r, e.d, l, UnlinkOptions{Every: 20 * time.Millisecond}); err != nil {
		t.Fatalf("ripresa dello scollegamento: %v", err)
	}
}

// DB12: la rotazione cambia la password e termina le sessioni del link: la vecchia e' respinta, la nuova entra.
func TestDatabaseLinks_RotazioneTerminaLeSessioni(t *testing.T) {
	e := newRolesEnv(t)
	ctx := context.Background()
	l := uuid.New()
	if err := CreateLinkRole(ctx, e.r, e.d, l, ModeRO, "vecchia"); err != nil {
		t.Fatal(err)
	}
	s := startPersistent(t, e, LinkRole(l), "vecchia")
	s.waitOK(t, "prima")
	if err := RotateLinkRole(ctx, e.r, e.d, l, "nuova"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-s.done:
		if !isTerminated(err) {
			t.Fatalf("errore: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nessuna connessione deve sopravvivere alla password vecchia")
	}
	if _, err := e.r.loginAs(ctx, LinkRole(l), "vecchia", e.d.Name); err == nil {
		t.Fatal("la vecchia password doveva essere respinta")
	}
	c, err := e.r.loginAs(ctx, LinkRole(l), "nuova", e.d.Name)
	if err != nil {
		t.Fatalf("la nuova password: %v", err)
	}
	_ = c.Close(ctx)
}

// isTerminated dice se err e' la fine imposta da pg_terminate_backend: su Linux il client legge il FATAL
// 57P01 «terminating connection»; su Windows (Docker Desktop) il reset TCP puo' arrivare prima del messaggio
// e la lettura fallisce con «connection aborted» senza il testo. In entrambi i casi la sessione e' chiusa dal
// server; qualunque altro errore (SQL, permessi) non lo e'.
func isTerminated(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "57P01" && strings.Contains(pgErr.Message, "terminating connection")
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, io.EOF) || strings.Contains(err.Error(), "connection") {
		return runtime.GOOS == "windows" || errors.Is(err, io.EOF)
	}
	return false
}

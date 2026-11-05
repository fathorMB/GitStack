//go:build integration

package databases

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Backup, ripristino e aggiornamento minor (GIT-553, DB6, DB13, DB15). Il Postgres e' quello vero dei test
// (container postgres:16-alpine di dbtest) e pg_dump/pg_restore girano DENTRO il container con `docker exec`,
// con la stessa riga di comando che PodDumper manda nel pod (DumpArgs, RestoreArgs): cambiano solo il canale
// (docker exec invece di pods/exec) e il superuser del container (gitstack invece di gitstack_admin).

// dockerExec e' uno StreamExecutor che esegue il comando nel container dei test.
type dockerExec struct {
	ctr   string
	calls atomic.Int32
}

func (x *dockerExec) ExecStream(ctx context.Context, _, _, _ string, cmd []string, stdin io.Reader, stdout io.Writer) error {
	x.calls.Add(1)
	var args []string
	for i := 0; i < len(cmd); i++ {
		switch {
		case cmd[i] == "-h" && i+1 < len(cmd) && cmd[i+1] == pgSocketDir:
			i++ // il socket locale e' il default nel container
		case cmd[i] == AdminRole:
			args = append(args, "gitstack")
		default:
			args = append(args, cmd[i])
		}
	}
	c := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", x.ctr}, args...)...)
	c.Stdin = stdin
	c.Stdout = stdout
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (x *dockerExec) Exec(ctx context.Context, ns, pod, container string, cmd []string, stdin io.Reader) (string, error) {
	var out bytes.Buffer
	err := x.ExecStream(ctx, ns, pod, container, cmd, stdin, &out)
	return out.String(), err
}

type dumpEnv struct {
	*ctlEnv
	run *pgRunner
	dir string
	dx  *dockerExec
}

// newDumpEnv prepara un controller con SQL, dump e cartella dei dump veri (la cartella e' in t.TempDir()).
func newDumpEnv(t *testing.T, tune func(*Config)) *dumpEnv {
	t.Helper()
	_, dsn := dbtest.NewPool(t)
	run := &pgRunner{t: t, dsn: dsn}
	dx := &dockerExec{ctr: dbtest.ContainerID(t)}
	dir := t.TempDir()
	e := newCtlEnv(t, func(c *Config) {
		c.SQL, c.DumpKey, c.DataDir = run, testDumpKey, dir
		c.Dumper = &PodDumper{Exec: dx}
		if tune != nil {
			tune(c)
		}
	})
	return &dumpEnv{ctlEnv: e, run: run, dir: dir, dx: dx}
}

// running crea un database gestito e lo porta a `running` (ruoli e database veri nel container).
func (e *dumpEnv) running(owner, name string) store.ManagedDB {
	e.t.Helper()
	d := e.create(owner, name, 17)
	e.track(name)
	ctx := context.Background()
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	e.markReady(d)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if got := e.reload(d.ID); got.Status != store.DBRunning {
		e.t.Fatalf("%s: stato %s (%v), voluto running", name, got.Status, got.StatusReason)
	}
	return e.reload(d.ID)
}

// track ripulisce database e ruoli creati nel container (i ruoli sono globali al container).
func (e *dumpEnv) track(name string) {
	e.t.Cleanup(func() {
		c, err := e.run.conn(context.Background(), "")
		if err != nil {
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, _ = c.Exec(context.Background(), `DROP DATABASE IF EXISTS `+QuoteIdent(name)+` WITH (FORCE)`)
		rows, _ := c.Query(context.Background(), `SELECT rolname FROM pg_roles WHERE rolname LIKE 'l\_%' OR rolname IN ($1, $2)`, OwnerRole(name), ReadOnlyRole(name))
		var roles []string
		for rows != nil && rows.Next() {
			var r string
			if rows.Scan(&r) == nil {
				roles = append(roles, r)
			}
		}
		if rows != nil {
			rows.Close()
		}
		for _, r := range roles {
			_, _ = c.Exec(context.Background(), `DROP OWNED BY `+QuoteIdent(r)+` CASCADE`)
			_, _ = c.Exec(context.Background(), `DROP ROLE IF EXISTS `+QuoteIdent(r))
		}
	})
}

// seed scrive i dati noti come proprietario del database: tabella, sequenza, tre righe.
func (e *dumpEnv) seed(d store.ManagedDB) {
	e.t.Helper()
	ctx := context.Background()
	c, err := e.run.loginAs(ctx, OwnerRole(d.Name), "owner-pw-"+d.Name, d.Name)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, `CREATE TABLE negozio (id integer PRIMARY KEY, nome text NOT NULL);
		INSERT INTO negozio VALUES (1, 'uno'), (2, 'due'), (3, 'tre');`); err != nil {
		e.t.Fatal(err)
	}
}

// rows legge id:nome di negozio come superuser, in ordine.
func (e *dumpEnv) rows(d store.ManagedDB) string {
	e.t.Helper()
	ctx := context.Background()
	c, err := e.run.conn(ctx, d.Name)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	rs, err := c.Query(ctx, `SELECT id::text || ':' || nome FROM negozio ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

func (e *dumpEnv) execAs(d store.ManagedDB, sql string) {
	e.t.Helper()
	ctx := context.Background()
	c, err := e.run.conn(ctx, d.Name)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, sql); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *dumpEnv) scalarIn(d store.ManagedDB, sql string) string {
	e.t.Helper()
	ctx := context.Background()
	c, err := e.run.conn(ctx, d.Name)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	var v any
	if err := c.QueryRow(ctx, sql).Scan(&v); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return fmt.Sprint(v)
}

const knownRows = "1:uno,2:due,3:tre"

// Criterio 1 (DB13, DB15): un database con dati noti -> backup -> dati modificati -> ripristino sul posto:
// tornano i dati noti. Il dump e' cifrato su disco, il ripristino lascia proprietario e privilegi dei ruoli
// del database (non quelli del dump) e fa prima un dump di sicurezza.
func TestDatabaseBackup_RipristinoStessoDatabase(t *testing.T) {
	e := newDumpEnv(t, nil)
	ctx := context.Background()
	d := e.running("alice", "r"+strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	e.seed(d)
	if got := e.rows(d); got != knownRows {
		t.Fatalf("dati iniziali: %s", got)
	}

	rec, err := e.ctl.Backup(ctx, d, store.BackupOnDemand, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != store.BackupCompleted || rec.SizeBytes == nil || *rec.SizeBytes <= 0 || rec.File == nil {
		t.Fatalf("backup: %+v", rec)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(*rec.File)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, dumpMagic) || bytes.Contains(raw, []byte("PGDMP")) || bytes.Contains(raw, []byte("negozio")) {
		t.Fatal("il dump su disco non e' cifrato")
	}
	if int64(len(raw)) != *rec.SizeBytes {
		t.Fatalf("dimensione registrata %d, sul disco %d", *rec.SizeBytes, len(raw))
	}

	// Dati modificati: righe cambiate e cancellata, riga nuova.
	e.execAs(d, `UPDATE negozio SET nome = 'cambiato' WHERE id = 1; DELETE FROM negozio WHERE id = 2; INSERT INTO negozio VALUES (9, 'nove')`)
	if e.rows(d) == knownRows {
		t.Fatal("la modifica non ha avuto effetto")
	}

	if err := e.ctl.RestoreBackup(ctx, d, rec, store.RestoreSame, nil); err != nil {
		t.Fatal(err)
	}
	if got := e.reload(d.ID); got.Status != store.DBRestoring {
		t.Fatalf("subito dopo la richiesta lo stato e' %s, voluto restoring", got.Status)
	}
	e.ctl.Reconcile(ctx, e.reload(d.ID))

	got := e.reload(d.ID)
	ops, _ := e.st.ManagedDBOperations(ctx, d.ID)
	if got.Status != store.DBRunning || ops.RestoreError != nil || ops.Restore != nil {
		t.Fatalf("dopo il ripristino: stato %s, errore %v, pendente %v", got.Status, ops.RestoreError, ops.Restore)
	}
	if r := e.rows(d); r != knownRows {
		t.Fatalf("dopo il ripristino i dati sono %q, voluti %q", r, knownRows)
	}
	// Proprietario e privilegi: il proprietario del database, non superuser; _ro legge.
	if o := e.scalarIn(d, `SELECT tableowner FROM pg_tables WHERE tablename = 'negozio'`); o != OwnerRole(d.Name) {
		t.Fatalf("proprietario della tabella %s, voluto %s", o, OwnerRole(d.Name))
	}
	if s := e.scalarIn(d, fmt.Sprintf(`SELECT rolsuper FROM pg_roles WHERE rolname = %s`, QuoteLiteral(OwnerRole(d.Name)))); s != "false" {
		t.Fatalf("l'owner e' superuser: %s", s)
	}
	if p := e.scalarIn(d, fmt.Sprintf(`SELECT has_table_privilege(%s, 'negozio', 'SELECT')`, QuoteLiteral(ReadOnlyRole(d.Name)))); p != "true" {
		t.Fatalf("_ro non legge dopo il ripristino: %s", p)
	}
	// Dump di sicurezza prima del ripristino.
	all, err := e.st.ListManagedDBBackups(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, b := range all {
		if b.Status == store.BackupCompleted {
			kinds[b.Kind]++
		}
	}
	if kinds[store.BackupOnDemand] != 1 || kinds[store.BackupSafety] != 1 {
		t.Fatalf("dump completati per tipo: %v", kinds)
	}
	// Eventi e audit senza segreti.
	audit, _ := e.st.ManagedDBAudit(ctx, d.ID)
	var actions []string
	for _, a := range audit {
		actions = append(actions, a.Action)
		if strings.Contains(string(a.Detail), "owner-pw") {
			t.Fatalf("segreto nell'audit: %s", a.Detail)
		}
	}
	for _, want := range []string{"backup_completed", "restore_requested", "restored"} {
		found := false
		for _, a := range actions {
			found = found || a == want
		}
		if !found {
			t.Errorf("manca l'azione %q nell'audit: %v", want, actions)
		}
	}
}

// DB15: ripristino in un'istanza vuota con un NOME DIVERSO (database nuovo da dump): i ruoli sono quelli nuovi,
// nessun riferimento ai vecchi ruoli in ACL e nello schema, un link rw scrive come owner, un link ro legge e non scrive.
func TestDatabaseRestore_IstanzaVuotaNomeDiverso(t *testing.T) {
	e := newDumpEnv(t, nil)
	ctx := context.Background()
	src := e.running("alice", "o"+strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	e.seed(src)
	e.execAs(src, fmt.Sprintf(`GRANT SELECT ON negozio TO %s`, QuoteIdent(ReadOnlyRole(src.Name))))
	rec, err := e.ctl.Backup(ctx, src, store.BackupOnDemand, nil)
	if err != nil {
		t.Fatal(err)
	}

	newName := "n" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	nd, err := e.st.CreateManagedDB(ctx, store.NewManagedDB{ID: uuid.New(), OwnerName: "alice", OwnerType: "user", OwnerID: src.OwnerID,
		Name: newName, PgMajor: 17, SizeBytes: 1 << 30, CPUMillis: 100, MemoryBytes: 256 << 20}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	e.track(newName)
	if err := e.ctl.RestoreBackup(ctx, nd, rec, store.RestoreNew, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ctl.Provision(ctx, nd, "owner-pw-"+newName); err != nil {
		t.Fatal(err)
	}
	e.ctl.Reconcile(ctx, e.reload(nd.ID))
	e.markReady(nd)
	e.ctl.Reconcile(ctx, e.reload(nd.ID))

	got := e.reload(nd.ID)
	ops, _ := e.st.ManagedDBOperations(ctx, nd.ID)
	if got.Status != store.DBRunning || ops.RestoreError != nil || ops.Restore != nil {
		t.Fatalf("stato %s, errore %v, pendente %v", got.Status, ops.RestoreError, ops.Restore)
	}
	if r := e.rows(got); r != knownRows {
		t.Fatalf("dati nel database nuovo: %q", r)
	}
	oldOwner, oldRO := OwnerRole(src.Name), ReadOnlyRole(src.Name)
	if o := e.scalarIn(got, `SELECT tableowner FROM pg_tables WHERE tablename = 'negozio'`); o != OwnerRole(newName) {
		t.Fatalf("proprietario %s, voluto %s", o, OwnerRole(newName))
	}
	if n := e.scalarIn(got, fmt.Sprintf(`SELECT count(*) FROM pg_class WHERE relacl::text LIKE '%%%s%%' OR relacl::text LIKE '%%%s%%'`, oldOwner, oldRO)); n != "0" {
		t.Fatalf("%s oggetti con ACL che citano i vecchi ruoli", n)
	}
	if n := e.scalarIn(got, fmt.Sprintf(`SELECT count(*) FROM pg_class c JOIN pg_roles r ON r.oid = c.relowner WHERE r.rolname IN (%s, %s)`, QuoteLiteral(oldOwner), QuoteLiteral(oldRO))); n != "0" {
		t.Fatalf("%s oggetti dei vecchi ruoli", n)
	}
	// Nessun riferimento ai vecchi ruoli nemmeno nello schema (pg_dump -s del database nuovo).
	schema, err := e.dx.Exec(ctx, "", "", "", []string{"pg_dump", "-s", "-U", AdminRole, "-d", newName}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(schema, oldOwner) || strings.Contains(schema, oldRO) {
		t.Fatalf("lo schema del database nuovo cita i vecchi ruoli:\n%s", schema)
	}
	e.checkLinks(got)
}

// checkLinks: dopo un ripristino i link si ricreano con password nuove (DB9): rw scrive e gli oggetti sono dell'owner,
// ro legge e non scrive.
func (e *dumpEnv) checkLinks(d store.ManagedDB) {
	e.t.Helper()
	ctx := context.Background()
	rw, ro := uuid.New(), uuid.New()
	if err := CreateLinkRole(ctx, e.run, d, rw, ModeRW, "pw-rw"); err != nil {
		e.t.Fatal(err)
	}
	if err := CreateLinkRole(ctx, e.run, d, ro, ModeRO, "pw-ro"); err != nil {
		e.t.Fatal(err)
	}
	crw, err := e.run.loginAs(ctx, LinkRole(rw), "pw-rw", d.Name)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = crw.Close(ctx) }()
	if _, err := crw.Exec(ctx, `INSERT INTO negozio VALUES (10, 'dal-link'); CREATE TABLE creata_dal_link (x int)`); err != nil {
		e.t.Fatalf("il link rw non scrive: %v", err)
	}
	if o := e.scalarIn(d, `SELECT tableowner FROM pg_tables WHERE tablename = 'creata_dal_link'`); o != OwnerRole(d.Name) {
		e.t.Fatalf("l'oggetto creato dal link e' di %s, voluto %s", o, OwnerRole(d.Name))
	}
	cro, err := e.run.loginAs(ctx, LinkRole(ro), "pw-ro", d.Name)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = cro.Close(ctx) }()
	var n int
	if err := cro.QueryRow(ctx, `SELECT count(*) FROM negozio`).Scan(&n); err != nil || n < 3 {
		e.t.Fatalf("il link ro non legge: n=%d err=%v", n, err)
	}
	if _, err := cro.Exec(ctx, `INSERT INTO negozio VALUES (11, 'no')`); err == nil {
		e.t.Fatal("il link ro ha scritto")
	}
}

// DB15: ripristino in un'istanza vuota con lo STESSO nome (il caso di `gitstack restore` su un cluster pulito):
// database e ruoli spariscono, il controller li ricrea (ruoli con password nuova) e il dump li riempie.
func TestDatabaseRestore_IstanzaVuotaStessoNome(t *testing.T) {
	e := newDumpEnv(t, nil)
	ctx := context.Background()
	d := e.running("alice", "s"+strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	e.seed(d)
	rec, err := e.ctl.Backup(ctx, d, store.BackupOnDemand, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Cluster pulito: database e ruoli non esistono piu'; il Secret dei ruoli e' quello di una installazione nuova.
	c, err := e.run.conn(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP DATABASE ` + QuoteIdent(d.Name) + ` WITH (FORCE)`, `DROP ROLE ` + QuoteIdent(ReadOnlyRole(d.Name)), `DROP ROLE ` + QuoteIdent(OwnerRole(d.Name))} {
		if _, err := c.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = c.Close(ctx)
	sec, _ := e.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretAuth, metav1.GetOptions{})
	delete(sec.Annotations, annotationRoles)
	sec.Data["owner-password"] = []byte("owner-pw-" + d.Name) // il controller riapplica la password del Secret
	if _, err := e.kube.CoreV1().Secrets(Namespace(d.ID)).Update(ctx, sec, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := e.ctl.RestoreBackup(ctx, e.reload(d.ID), rec, store.RestoreNew, nil); err != nil {
		t.Fatal(err)
	}
	e.ctl.Reconcile(ctx, e.reload(d.ID)) // ensureRoles ricrea ruoli e database vuoti, poi il ripristino

	got := e.reload(d.ID)
	ops, _ := e.st.ManagedDBOperations(ctx, d.ID)
	if got.Status != store.DBRunning || ops.RestoreError != nil || ops.Restore != nil {
		t.Fatalf("stato %s, errore %v, pendente %v", got.Status, ops.RestoreError, ops.Restore)
	}
	if r := e.rows(got); r != knownRows {
		t.Fatalf("dati dopo il ripristino su istanza vuota: %q", r)
	}
	if o := e.scalarIn(got, `SELECT tableowner FROM pg_tables WHERE tablename = 'negozio'`); o != OwnerRole(d.Name) {
		t.Fatalf("proprietario %s", o)
	}
	e.checkLinks(got)
}

// fakeDumper produce dump finti (la magia PGDMP) per i test che non hanno bisogno di pg_dump vero.
type fakeDumper struct {
	mu      sync.Mutex
	err     error
	order   *[]string
	dumps   int
	payload string
}

func (f *fakeDumper) Dump(_ context.Context, _ store.ManagedDB, w io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.order != nil {
		*f.order = append(*f.order, "dump")
	}
	if f.err != nil {
		_, _ = w.Write([]byte("PGDMP parziale"))
		return f.err
	}
	f.dumps++
	_, err := w.Write([]byte("PGDMP" + f.payload))
	return err
}

func (f *fakeDumper) Restore(context.Context, store.ManagedDB, io.Reader, RestoreOptions) error {
	return f.err
}

// newFakeEnv: controller con SQL registrato (nessun Postgres da avviare per i dump) e dump finti.
func newFakeEnv(t *testing.T, tune func(*Config)) (*ctlEnv, *fakeDumper, string) {
	t.Helper()
	fd := &fakeDumper{payload: " dati"}
	dir := t.TempDir()
	e := newCtlEnv(t, func(c *Config) {
		c.Dumper, c.DumpKey, c.DataDir = fd, testDumpKey, dir
		if tune != nil {
			tune(c)
		}
	})
	return e, fd, dir
}

func (e *ctlEnv) runningFake(owner, name string) store.ManagedDB {
	e.t.Helper()
	d := e.create(owner, name, 17)
	ctx := context.Background()
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	e.markReady(d)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if got := e.reload(d.ID); got.Status != store.DBRunning {
		e.t.Fatalf("stato %s (%v)", got.Status, got.StatusReason)
	}
	return e.reload(d.ID)
}

func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// DB13: ultimi 7 programmati e ultimi 5 su richiesta, a parte; i file dei dump tolti spariscono dal disco.
func TestDatabaseBackup_Retention(t *testing.T) {
	e, _, dir := newFakeEnv(t, nil)
	ctx := context.Background()
	d := e.runningFake("alice", "ret")
	for i := 0; i < 9; i++ {
		if _, err := e.ctl.Backup(ctx, d, store.BackupScheduled, nil); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 7; i++ {
		if _, err := e.ctl.Backup(ctx, d, store.BackupOnDemand, nil); err != nil {
			t.Fatal(err)
		}
	}
	all, err := e.st.ListManagedDBBackups(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, b := range all {
		if b.Status == store.BackupCompleted {
			count[b.Kind]++
		}
	}
	if count[store.BackupScheduled] != 7 || count[store.BackupOnDemand] != 5 {
		t.Fatalf("dump tenuti: %v (voluti 7 programmati e 5 su richiesta)", count)
	}
	if n := len(filesUnder(t, dir)); n != 12 {
		t.Fatalf("file sul disco %d, voluti 12 (uno per dump tenuto): %v", n, filesUnder(t, dir))
	}
	// I piu' vecchi sono quelli tolti: il piu' recente dei programmati c'e'.
	latest, err := e.st.LatestManagedDBBackup(ctx, d.ID, store.BackupScheduled)
	if err != nil || latest.Status != store.BackupCompleted || !e.ctl.DumpExists(*latest.File) {
		t.Fatalf("ultimo programmato: %+v %v", latest, err)
	}
	// Eliminare il database toglie anche i dump (DB19).
	if _, err := e.st.MarkManagedDBDeleting(ctx, d.ID, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if n := len(filesUnder(t, dir)); n != 0 {
		t.Fatalf("dopo l'eliminazione restano %d file di dump", n)
	}
}

// Un backup fallito e' visibile (stato del database: backup_error), non lascia file e non cancella i dump buoni;
// il primo successo dello stesso tipo azzera l'errore.
func TestDatabaseBackup_FallitoSegnalato(t *testing.T) {
	e, fd, dir := newFakeEnv(t, nil)
	ctx := context.Background()
	d := e.runningFake("alice", "ko")
	good, err := e.ctl.Backup(ctx, d, store.BackupOnDemand, nil)
	if err != nil {
		t.Fatal(err)
	}

	fd.err = errors.New("pg_dump: connessione rifiutata")
	if _, err := e.ctl.Backup(ctx, d, store.BackupOnDemand, nil); err == nil {
		t.Fatal("il backup doveva fallire")
	}
	ops, _ := e.st.ManagedDBOperations(ctx, d.ID)
	if ops.BackupError == nil || !strings.Contains(*ops.BackupError, "connessione rifiutata") {
		t.Fatalf("backup_error: %v", ops.BackupError)
	}
	rows, _ := e.st.ListManagedDBBackups(ctx, d.ID)
	failed := 0
	for _, b := range rows {
		if b.Status == store.BackupFailed {
			failed++
			if b.Error == nil || b.File != nil {
				t.Fatalf("dump fallito: %+v", b)
			}
		}
	}
	if failed != 1 || len(filesUnder(t, dir)) != 1 || !e.ctl.DumpExists(*good.File) {
		t.Fatalf("falliti %d, file %v: il dump buono deve restare e quello fallito non lasciare file", failed, filesUnder(t, dir))
	}
	problems, _ := e.st.ManagedDBsWithBackupProblems(ctx)
	if len(problems) != 1 || problems[0].ID != d.ID {
		t.Fatalf("database con problemi: %v", problems)
	}
	audit, _ := e.st.ManagedDBAudit(ctx, d.ID)
	found := false
	for _, a := range audit {
		found = found || a.Action == "backup_failed"
	}
	if !found {
		t.Fatal("manca backup_failed nell'audit")
	}
	// Un dump di un altro tipo non azzera l'errore; uno dello stesso tipo si'.
	fd.err = nil
	if _, err := e.ctl.Backup(ctx, d, store.BackupScheduled, nil); err != nil {
		t.Fatal(err)
	}
	if ops, _ := e.st.ManagedDBOperations(ctx, d.ID); ops.BackupError == nil {
		t.Fatal("un programmato riuscito ha nascosto l'errore del su richiesta")
	}
	if _, err := e.ctl.Backup(ctx, d, store.BackupOnDemand, nil); err != nil {
		t.Fatal(err)
	}
	if ops, _ := e.st.ManagedDBOperations(ctx, d.ID); ops.BackupError != nil || ops.LastBackupAt == nil {
		t.Fatalf("errore non azzerato: %v", ops.BackupError)
	}
	// Senza chiave i backup non partono mai in chiaro.
	e2, _, dir2 := newFakeEnv(t, func(c *Config) { c.DumpKey = nil })
	d2 := e2.runningFake("alice", "nokey")
	if _, err := e2.ctl.Backup(ctx, d2, store.BackupOnDemand, nil); !errors.Is(err, ErrBackupUnavailable) || len(filesUnder(t, dir2)) != 0 {
		t.Fatalf("senza chiave: %v", err)
	}
}

// DB13: il dump programmato parte una volta al giorno, dopo un errore si riprova ogni ora, un solo dump alla volta.
func TestDatabaseBackup_Programmato(t *testing.T) {
	now := time.Now().UTC()
	clock := &now
	e, fd, _ := newFakeEnv(t, func(c *Config) {
		c.Now = func() time.Time { return *clock }
		c.BackupEvery, c.BackupRetryEvery = 24*time.Hour, time.Hour
	})
	ctx := context.Background()
	d := e.runningFake("alice", "sched")
	count := func() int {
		rows, _ := e.st.ListManagedDBBackups(ctx, d.ID)
		return len(rows)
	}
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if count() != 0 {
		t.Fatal("un database appena creato non ha ancora bisogno del dump")
	}
	*clock = clock.Add(25 * time.Hour)
	fd.err = errors.New("disco pieno")
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if count() != 1 {
		t.Fatalf("dopo un giorno doveva partire il programmato, righe %d", count())
	}
	*clock = clock.Add(10 * time.Minute)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if count() != 1 {
		t.Fatal("dopo un errore non si riprova prima dell'ora")
	}
	fd.err = nil
	*clock = clock.Add(2 * time.Hour)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if count() != 2 || fd.dumps != 1 {
		t.Fatalf("il nuovo tentativo doveva riuscire: righe %d, dump %d", count(), fd.dumps)
	}
	if ops, _ := e.st.ManagedDBOperations(ctx, d.ID); ops.BackupError != nil {
		t.Fatalf("errore ancora aperto dopo il successo: %v", ops.BackupError)
	}
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if count() != 2 {
		t.Fatal("un secondo programmato nello stesso giorno")
	}
	// Un dump gia' in corso: 409, e se core si e' riavviato lo si chiude come interrotto.
	rec, err := e.ctl.BeginBackup(ctx, d, store.BackupOnDemand, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ctl.BeginBackup(ctx, d, store.BackupOnDemand, nil); !errors.Is(err, store.ErrBackupInProgress) {
		t.Fatalf("secondo dump in parallelo: %v", err)
	}
	e.ctl.mu.Lock()
	delete(e.ctl.liveBackups, rec.ID) // simula il riavvio di core
	e.ctl.mu.Unlock()
	*clock = clock.Add(time.Hour)
	e.ctl.Once(ctx)
	if b, _ := e.st.GetManagedDBBackup(ctx, d.ID, rec.ID); b.Status != store.BackupFailed {
		t.Fatalf("dump orfano: %s", b.Status)
	}
}

// DB6 (criterio 4): database su digest A, tabella a B. La richiesta di aggiornamento minor fa il dump di
// sicurezza e SOLO DOPO passa a B (stesso PVC: lo StatefulSet cambia immagine, non il volume); con il dump che
// fallisce resta su A con il motivo visibile; la major non cambia mai.
func TestDatabases_AggiornamentoMinoreSuRichiesta(t *testing.T) {
	var order []string
	digestB := "sha256:" + strings.Repeat("c", 64)
	tableB := map[int]Image{17: {Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: digestB}}
	e, fd, _ := newFakeEnv(t, nil)
	fd.order = &order
	ctx := context.Background()
	stsImage := func(d store.ManagedDB) string {
		sts, err := e.kube.AppsV1().StatefulSets(Namespace(d.ID)).Get(ctx, WorkloadName, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return sts.Spec.Template.Spec.Containers[0].Image
	}
	imgA := "docker.io/library/postgres@" + testImage.Digest
	imgB := "docker.io/library/postgres@" + digestB
	d := e.runningFake("alice", "up")
	pvcBefore := d.ID

	// La release cambia la tabella (B): senza richiesta il database resta su A.
	cfg := e.ctl.cfg
	cfg.Images = tableB
	cfg.Verifier = verifierFunc(func(context.Context, string) error { order = append(order, "verify"); return nil })
	ctl2 := New(e.st, e.kube, cfg)
	ctl2.Reconcile(ctx, e.reload(d.ID))
	if stsImage(d) != imgA {
		t.Fatalf("senza richiesta l'immagine e' %s", stsImage(d))
	}
	if !ctl2.MinorUpdateAvailable(e.reload(d.ID)) {
		t.Fatal("l'aggiornamento doveva risultare disponibile")
	}

	// Dump che fallisce: resta su A, motivo visibile, nessun cambio di immagine.
	fd.err = errors.New("disco pieno")
	if err := ctl2.RequestMinorUpgrade(ctx, e.reload(d.ID), nil); err != nil {
		t.Fatal(err)
	}
	ctl2.Reconcile(ctx, e.reload(d.ID))
	got := e.reload(d.ID)
	ops, _ := e.st.ManagedDBOperations(ctx, d.ID)
	if stsImage(d) != imgA || *got.ImageDigest != testImage.Digest || got.Status != store.DBRunning {
		t.Fatalf("dump fallito: immagine %s digest %s stato %s", stsImage(d), *got.ImageDigest, got.Status)
	}
	if ops.UpgradeError == nil || !strings.Contains(*ops.UpgradeError, "disco pieno") || !strings.Contains(*ops.UpgradeError, "dump di sicurezza") || ops.UpgradeRequest != nil {
		t.Fatalf("upgrade_error: %v richiesta %v", ops.UpgradeError, ops.UpgradeRequest)
	}

	// Dump riuscito: prima il dump, poi B.
	fd.err = nil
	order = order[:0]
	if err := ctl2.RequestMinorUpgrade(ctx, e.reload(d.ID), nil); err != nil {
		t.Fatal(err)
	}
	ctl2.Reconcile(ctx, e.reload(d.ID))
	got = e.reload(d.ID)
	if *got.ImageDigest != digestB || got.Status != store.DBUpdating {
		t.Fatalf("dopo il dump: digest %s stato %s", *got.ImageDigest, got.Status)
	}
	// Il giro dopo porta lo StatefulSet al nuovo digest (stesso PVC: cambia solo il template del pod).
	ctl2.Reconcile(ctx, e.reload(d.ID))
	if stsImage(d) != imgB {
		t.Fatalf("lo StatefulSet e' ancora su %s", stsImage(d))
	}
	if strings.Join(order, ",") != "verify,dump" {
		t.Fatalf("ordine %v: cosign e dump di sicurezza prima del cambio", order)
	}
	if ops, _ := e.st.ManagedDBOperations(ctx, d.ID); ops.UpgradeError != nil || ops.UpgradeRequest != nil {
		t.Fatalf("esito: %v %v", ops.UpgradeError, ops.UpgradeRequest)
	}
	rows, _ := e.st.ListManagedDBBackups(ctx, d.ID)
	if len(rows) != 2 || rows[0].Kind != store.BackupSafety || rows[0].Status != store.BackupCompleted {
		t.Fatalf("dump di sicurezza: %+v", rows)
	}
	if got.ID != pvcBefore {
		t.Fatal("l'id (e il namespace con il PVC) deve restare lo stesso")
	}
	e.markReady(d)
	ctl2.Reconcile(ctx, e.reload(d.ID))
	if g := e.reload(d.ID); g.Status != store.DBRunning || stsImage(d) != imgB {
		t.Fatalf("dopo il riavvio: %s %s", g.Status, stsImage(d))
	}
	if err := ctl2.RequestMinorUpgrade(ctx, e.reload(d.ID), nil); !errors.Is(err, ErrNoMinorUpdate) {
		t.Fatalf("gia' aggiornato: %v", err)
	}

	// Nessun cambio di major: una voce che non e' della major del database e' rifiutata, il database resta su B.
	cfg.Images = map[int]Image{17: {Repository: "docker.io/library/postgres", Tag: "18-alpine", Digest: "sha256:" + strings.Repeat("d", 64)}}
	ctl3 := New(e.st, e.kube, cfg)
	if err := ctl3.RequestMinorUpgrade(ctx, e.reload(d.ID), nil); err != nil {
		t.Fatal(err)
	}
	ctl3.Reconcile(ctx, e.reload(d.ID))
	ops, _ = e.st.ManagedDBOperations(ctx, d.ID)
	if stsImage(d) != imgB || ops.UpgradeError == nil || !strings.Contains(*ops.UpgradeError, "major") {
		t.Fatalf("major: immagine %s errore %v", stsImage(d), ops.UpgradeError)
	}
	if g := e.reload(d.ID); g.PgMajor != 17 {
		t.Fatalf("la major e' cambiata: %d", g.PgMajor)
	}

	// L'amministratore d'istanza li chiede tutti: solo i running con un aggiornamento disponibile.
	cfg.Images = tableB
	cfg.Images[17] = Image{Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: "sha256:" + strings.Repeat("e", 64)}
	ctl4 := New(e.st, e.kube, cfg)
	n, err := ctl4.RequestMinorUpgradeAll(ctx, nil)
	if err != nil || n != 1 {
		t.Fatalf("richiesta per tutti: n=%d err=%v", n, err)
	}
}

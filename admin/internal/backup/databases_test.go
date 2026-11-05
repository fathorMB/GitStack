package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Database gestiti nel backup e nel restore (GIT-553, DB14, DB15).

const (
	dbShopID = "dddddddd-0000-4000-8000-000000000001"
	dbErpID  = "dddddddd-0000-4000-8000-000000000002"
	dbNewID  = "dddddddd-0000-4000-8000-000000000003"
)

// dbCluster è fakeCluster più i database gestiti: dumps è il contenuto di pg_dump per id, restored quello
// ricevuto da pg_restore, notReady quante volte l'istanza risponde «non pronta» prima di esserlo.
type dbCluster struct {
	*fakeCluster
	dumps    map[string]string
	dumpErr  map[string]error
	restored map[string]string
	notReady map[string]int
	order    []string
	restoreE error
}

func (c *dbCluster) DumpDatabase(_ context.Context, d DatabaseRef, w io.Writer) error {
	c.order = append(c.order, "dump "+d.Name)
	c.fakeCluster.note("dump " + d.Name)
	if err := c.dumpErr[d.ID]; err != nil {
		return err
	}
	_, err := io.WriteString(w, c.dumps[d.ID])
	return err
}

func (c *dbCluster) DatabaseReady(_ context.Context, d DatabaseRef) (bool, error) {
	c.order = append(c.order, "ready? "+d.Name)
	if c.notReady[d.ID] > 0 {
		c.notReady[d.ID]--
		return false, nil
	}
	return true, nil
}

func (c *dbCluster) RestoreDatabase(_ context.Context, d DatabaseRef, r io.Reader) error {
	c.order = append(c.order, "restore "+d.Name)
	b, _ := io.ReadAll(r)
	if c.restoreE != nil {
		return c.restoreE
	}
	c.restored[d.ID] = string(b)
	return nil
}

func newDBEnv(t *testing.T) (*env, *dbCluster) {
	t.Helper()
	e := newEnv(t)
	c := &dbCluster{fakeCluster: e.f, dumps: map[string]string{}, dumpErr: map[string]error{}, restored: map[string]string{}, notReady: map[string]int{}}
	c.dumps[dbShopID] = "PGDMP dati di shop"
	c.dumps[dbErpID] = "PGDMP dati di erp"
	e.o.Cluster = c
	e.o.Databases = func(context.Context) ([]DatabaseRef, error) {
		return []DatabaseRef{
			{ID: dbShopID, Owner: "alice", Name: "shop", PgMajor: 17, Entry: DatabasesPrefix + dbShopID + "/shop.dump", Status: "running"},
			{ID: dbErpID, Owner: "acme", Name: "erp", PgMajor: 16, Entry: DatabasesPrefix + dbErpID + "/erp.dump", Status: "running"},
			{ID: dbNewID, Owner: "alice", Name: "nuovo", PgMajor: 17, Entry: DatabasesPrefix + dbNewID + "/nuovo.dump", Status: "creating"},
		}, nil
	}
	e.o.DatabaseReadyTimeout, e.o.DatabaseReadyPoll = time.Second, time.Millisecond
	return e, c
}

// DB14 e criterio 2: gitstack backup mette un dump fresco di ogni database in databases/<id>/<nome>.dump, dopo la
// finestra di sola lettura, cifrato come l'archivio (con la chiave il testo del dump non e' in chiaro nel file);
// gitstack restore su un cluster pulito aspetta le istanze ricreate dal controller e le riempie, nell'ordine.
func TestBackupDatabasesEPoiRestoreSuClusterPulito(t *testing.T) {
	e, c := newDBEnv(t)
	e.o.Key = []byte("0123456789abcdef0123456789abcdef")
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Manifest.Databases) != 2 {
		t.Fatalf("database nel manifest: %+v (il terzo e' in creazione e non entra)", res.Manifest.Databases)
	}
	for _, f := range res.Manifest.Files {
		if strings.HasPrefix(f.Name, AppVolumesPrefix) {
			t.Errorf("i PVC dei database non entrano nel backup dei volumi: %s", f.Name)
		}
	}
	if !strings.Contains(e.log.String(), "database nuovo") && !strings.Contains(e.log.String(), "alice/nuovo") {
		t.Errorf("il database non incluso va dichiarato nel log: %s", e.log.String())
	}
	raw, _ := os.ReadFile(res.Path)
	if bytes.Contains(raw, []byte("PGDMP")) || bytes.Contains(raw, []byte("dati di shop")) {
		t.Fatal("l'archivio cifrato contiene il dump in chiaro")
	}
	// I dump si fanno a finestra chiusa: prima gli ingressi tornano su, poi pg_dump (la finestra resta corta).
	if up, dump := indexOf(c.fakeCluster.calls, "scale gs-gateway 2"), indexOf(c.fakeCluster.calls, "dump shop"); up < 0 || dump < up {
		t.Errorf("ordine delle chiamate: %v", c.fakeCluster.calls)
	}

	// Restore su un cluster pulito: la prima istanza non e' pronta per due controlli.
	r, rc := newDBEnv(t)
	r.o.Key = e.o.Key
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	rc.notReady[dbShopID] = 2
	if err := Restore(context.Background(), r.o, res.Path); err != nil {
		t.Fatal(err)
	}
	if rc.restored[dbShopID] != c.dumps[dbShopID] || rc.restored[dbErpID] != c.dumps[dbErpID] || len(rc.restored) != 2 {
		t.Fatalf("database ripristinati: %v", rc.restored)
	}
	// Prima i servizi su (core ricrea le istanze), poi i database in ordine di voce.
	ready := indexOf(r.f.calls, "ready gs-core")
	if ready < 0 {
		t.Fatalf("core non e' ripartito: %v", r.f.calls)
	}
	// In ordine di voce; di shop due controlli negativi e uno positivo prima del ripristino.
	if got, want := strings.Join(rc.order, ","), "ready? shop,ready? shop,ready? shop,restore shop,ready? erp,restore erp"; got != want {
		t.Errorf("ordine dei database: %s, atteso %s", got, want)
	}
	// Le righe dei dump programmati (file su databases-data, non nell'archivio) si tolgono con il restore di core.
	if !strings.Contains(r.f.psqlIn, "DELETE FROM core.managed_db_backups") {
		t.Errorf("il restore di core non ripulisce i dump che non ci sono piu': %s", r.f.psqlIn)
	}
}

// Un dump che fallisce fa fallire il backup, col nome del database: nessun archivio e gli ingressi tornano su.
func TestBackupDatabaseFallitoNonPassaInSilenzio(t *testing.T) {
	e, c := newDBEnv(t)
	c.dumpErr[dbErpID] = errors.New("pg_dump: connessione persa")
	_, err := Backup(context.Background(), e.o)
	if err == nil || !strings.Contains(err.Error(), "acme/erp") || !strings.Contains(err.Error(), "connessione persa") {
		t.Fatalf("errore: %v", err)
	}
	if entries, _ := os.ReadDir(e.dest); len(entries) != 0 {
		for _, en := range entries {
			if strings.HasPrefix(en.Name(), ArchivePrefix) {
				t.Errorf("archivio rimasto dopo l'errore: %s", en.Name())
			}
		}
	}
	if e.f.replicas["gs-gateway"] != 2 {
		t.Errorf("gli ingressi non sono tornati su: %v", e.f.replicas)
	}
	// Un output che non e' un archivio -Fc (un errore su stdout) e' un errore, non un dump vuoto.
	e2, c2 := newDBEnv(t)
	c2.dumps[dbShopID] = "psql: error: boh"
	if _, err := Backup(context.Background(), e2.o); err == nil || !strings.Contains(err.Error(), "archivio valido") {
		t.Fatalf("output non valido accettato: %v", err)
	}
}

// Restore: un'istanza che non diventa pronta e un pg_restore che fallisce sono errori espliciti, con i dati di
// core gia' a posto e i servizi accesi.
func TestRestoreDatabaseNonProntoOFallito(t *testing.T) {
	e, _ := newDBEnv(t)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	r, rc := newDBEnv(t)
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	rc.notReady[dbShopID] = 1 << 30
	r.o.DatabaseReadyTimeout = 20 * time.Millisecond
	err = Restore(context.Background(), r.o, res.Path)
	if err == nil || !strings.Contains(err.Error(), "alice/shop") || !strings.Contains(err.Error(), "non pronto") {
		t.Fatalf("istanza non pronta: %v", err)
	}
	r2, rc2 := newDBEnv(t)
	r2.o.DestDir = filepath.Join(t.TempDir(), "b")
	rc2.restoreE = errors.New("pg_restore: errore")
	if err := Restore(context.Background(), r2.o, res.Path); err == nil || !strings.Contains(err.Error(), "pg_restore") {
		t.Fatalf("pg_restore fallito: %v", err)
	}
	// Un cluster che non sa ripristinare i database rifiuta l'archivio PRIMA di fermare qualsiasi cosa.
	r3 := newEnv(t)
	r3.o.DestDir = filepath.Join(t.TempDir(), "b")
	if err := Restore(context.Background(), r3.o, res.Path); err == nil || !strings.Contains(err.Error(), "database gestiti") {
		t.Fatalf("cluster senza supporto: %v", err)
	}
	if len(r3.f.calls) != 0 {
		t.Errorf("il restore ha toccato il cluster prima di rifiutare: %v", r3.f.calls)
	}
}

// Il dump entra anche nell'elenco letto da core.managed_dbs: righe, validazione e database non running.
func TestParseDatabaseRow(t *testing.T) {
	d, err := parseDatabaseRow(dbShopID + "|alice|shop|17|running")
	if err != nil || d.Entry != "databases/"+dbShopID+"/shop.dump" || d.Namespace() != "gitstack-db-"+dbShopID || d.PgMajor != 17 {
		t.Fatalf("%+v %v", d, err)
	}
	for _, bad := range []string{"", "x|y", dbShopID + "|alice|Shop|17|running", "../etc|alice|shop|17|running", dbShopID + "|alice|shop|boh|running"} {
		if _, err := parseDatabaseRow(bad); err == nil {
			t.Errorf("riga %q accettata", bad)
		}
	}
	e := newEnv(t)
	e.o.Databases = nil
	e.f.databases = "" // senza la tabella core.managed_dbs la query non stampa niente
	if dbs, err := e.o.databases(context.Background()); err != nil || len(dbs) != 0 {
		t.Fatalf("elenco senza tabella: %v %v", dbs, err)
	}
}

// I comandi kubectl: pg_dump e pg_restore girano nel pod pg-0 del namespace gitstack-db-<id> come gitstack_admin sul
// socket locale; il ripristino e' sempre --no-owner --no-acl --role=<nome>_owner, in una transazione sola (DB15).
func TestKubectlCluster_DatabaseComandi(t *testing.T) {
	var got [][]string
	var stdin []string
	k := &KubectlCluster{Bin: "kubectl", Namespace: "ns", Release: "gs",
		Exec: func(_ context.Context, _ []string, in io.Reader, out io.Writer, name string, args ...string) error {
			got = append(got, append([]string{name}, args...))
			if in != nil {
				b, _ := io.ReadAll(in)
				stdin = append(stdin, string(b))
			}
			if out != nil && strings.Contains(join(args), "get pod") {
				_, _ = out.Write([]byte("True"))
			}
			if out != nil && strings.Contains(join(args), "get secret") {
				_, _ = out.Write([]byte("true"))
			}
			return nil
		}}
	d := DatabaseRef{ID: dbShopID, Owner: "alice", Name: "shop", PgMajor: 17}
	ns := "gitstack-db-" + dbShopID
	if err := k.DumpDatabase(context.Background(), d, io.Discard); err != nil {
		t.Fatal(err)
	}
	if want := "kubectl -n " + ns + " exec pg-0 -c pg -- pg_dump -Fc -h /var/run/postgresql -U gitstack_admin -d shop"; join(got[0]) != want {
		t.Errorf("dump: %q, atteso %q", join(got[0]), want)
	}
	if err := k.RestoreDatabase(context.Background(), d, strings.NewReader("PGDMP")); err != nil {
		t.Fatal(err)
	}
	rest := join(got[1])
	for _, must := range []string{"-n " + ns, "exec -i pg-0 -c pg --", "pg_restore", "-U gitstack_admin", "-d shop", "--no-owner", "--no-acl", "--role=shop_owner", "--single-transaction", "--exit-on-error"} {
		if !strings.Contains(rest, must) {
			t.Errorf("pg_restore senza %q: %s", must, rest)
		}
	}
	if strings.Contains(rest, "--clean") {
		t.Errorf("in un'istanza vuota non serve --clean: %s", rest)
	}
	if !strings.Contains(stdin[1], `GRANT SELECT ON ALL TABLES IN SCHEMA public TO "shop_ro"`) || !strings.Contains(stdin[1], `OWNER TO "shop_owner"`) {
		t.Errorf("privilegi di _ro non riapplicati: %s", stdin[1])
	}
	ok, err := k.DatabaseReady(context.Background(), d)
	if err != nil || !ok {
		t.Fatalf("pronto: %v %v", ok, err)
	}
	// Un nome non valido non arriva mai a kubectl.
	n := len(got)
	if err := k.DumpDatabase(context.Background(), DatabaseRef{ID: "../x", Name: "shop"}, io.Discard); err == nil || len(got) != n {
		t.Errorf("riferimento non valido accettato: %v", err)
	}
}

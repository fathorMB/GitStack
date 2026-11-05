//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// realSQL e' un SQLRunner su un PostgreSQL vero come superuser (il container dei test): stessa semantica di psql
// come gitstack_admin dentro il pod, senza passare dal pod.
type realSQL struct{ dsn string }

func (r *realSQL) conn(ctx context.Context, database string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(r.dsn)
	if err != nil {
		return nil, err
	}
	if database != "" && database != "postgres" {
		cfg.Database = database
	}
	return pgx.ConnectConfig(ctx, cfg)
}

func (r *realSQL) Exec(ctx context.Context, _ store.ManagedDB, database, sql string) error {
	c, err := r.conn(ctx, database)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close(ctx) }()
	_, err = c.Exec(ctx, sql)
	return err
}

func (r *realSQL) Query(ctx context.Context, _ store.ManagedDB, database, sql string) ([]string, error) {
	c, err := r.conn(ctx, database)
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

// dockerPG esegue pg_dump e pg_restore nel container dei test con la riga di comando di produzione.
type dockerPG struct{ ctr string }

func (x *dockerPG) ExecStream(ctx context.Context, _, _, _ string, cmd []string, stdin io.Reader, stdout io.Writer) error {
	var args []string
	for i := 0; i < len(cmd); i++ {
		switch {
		case cmd[i] == "-h" && i+1 < len(cmd) && cmd[i+1] == "/var/run/postgresql":
			i++
		case cmd[i] == databases.AdminRole:
			args = append(args, "gitstack")
		default:
			args = append(args, cmd[i])
		}
	}
	c := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", x.ctr}, args...)...)
	c.Stdin, c.Stdout = stdin, stdout
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (x *dockerPG) Exec(ctx context.Context, ns, pod, container string, cmd []string, stdin io.Reader) (string, error) {
	var out bytes.Buffer
	err := x.ExecStream(ctx, ns, pod, container, cmd, stdin, &out)
	return out.String(), err
}

// DB15, passo 4 (criterio di GIT-553): dopo un ripristino i collegamenti ripartono con password NUOVE. Postgres
// vero: il collegamento rw creato da API entra con la sua password; si fa il backup, si cambiano i dati, si
// ripristina sul posto. Il controller (AfterRestore predefinito = RelinkAfterRestore) ruota il collegamento: la
// password nuova entra, la vecchia no, i segreti dell'app hanno la nuova e l'app e' ridistribuita (nuova versione
// e deploy). Poi il ruolo del collegamento sparisce (istanza vuota): il secondo ripristino lo ricrea con una
// password ancora diversa.
func TestDatabaseLinks_RipristinoRicreaIlCollegamentoConPasswordNuova(t *testing.T) {
	ctx := context.Background()
	ctr := dbtest.ContainerID(t)
	img := databases.Image{Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: "sha256:" + strings.Repeat("a", 64)}
	e := newLinkEnvWith(t, func(dsn string) databases.SQLRunner { return &realSQL{dsn: dsn} }, func(c *databases.Config) {
		dir, rootPEM := k3dCA(t)
		if err := os.WriteFile(filepath.Join(dir, "ca.crt"), rootPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		c.CADir, c.Images, c.Verifier = dir, map[int]databases.Image{17: img}, okVerifier{}
		c.DumpKey, c.DataDir, c.Dumper = []byte("0123456789abcdef0123456789abcdef"), t.TempDir(), &databases.PodDumper{Exec: &dockerPG{ctr: ctr}}
	})
	dbID := uuid.UUID(e.db.Id)
	t.Cleanup(func() {
		c, err := (&realSQL{dsn: e.dsn}).conn(context.Background(), "")
		if err != nil {
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, _ = c.Exec(context.Background(), `DROP DATABASE IF EXISTS "shop" WITH (FORCE)`)
		rows, _ := c.Query(context.Background(), `SELECT rolname FROM pg_roles WHERE rolname ~ '^l_[0-9a-f]{32}$' OR rolname IN ('shop_owner','shop_ro')`)
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
			_, _ = c.Exec(context.Background(), `DROP OWNED BY "`+r+`" CASCADE`)
			_, _ = c.Exec(context.Background(), `DROP ROLE IF EXISTS "`+r+`"`)
		}
	})
	cur := func() store.ManagedDB {
		d, err := e.st.GetManagedDBByID(ctx, dbID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	// Il database arriva a `running` con ruoli e database VERI (shop_owner, shop_ro, shop).
	e.ctl.Reconcile(ctx, cur())
	sts, err := e.kube.AppsV1().StatefulSets(databases.Namespace(dbID)).Get(ctx, databases.WorkloadName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sts.Status.ReadyReplicas, sts.Status.UpdatedReplicas = 1, 1
	if _, err := e.kube.AppsV1().StatefulSets(databases.Namespace(dbID)).UpdateStatus(ctx, sts, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	e.ctl.Reconcile(ctx, cur())
	if d := cur(); d.Status != store.DBRunning {
		t.Fatalf("stato %s (%v)", d.Status, d.StatusReason)
	}
	super := &realSQL{dsn: e.dsn}
	if err := super.Exec(ctx, cur(), "shop", `CREATE TABLE negozio (id integer PRIMARY KEY, nome text); INSERT INTO negozio VALUES (1,'uno'),(2,'due'); ALTER TABLE negozio OWNER TO shop_owner`); err != nil {
		t.Fatal(err)
	}

	appID, _ := e.mkApp("alice", "web")
	created := e.link("alice", "alice", "web", "rw", http.StatusCreated)
	oldPW := created.Credentials.Password
	user := created.Credentials.Username
	login := func(password string) error {
		cfg, err := pgx.ParseConfig(e.dsn)
		if err != nil {
			return err
		}
		cfg.User, cfg.Password, cfg.Database = user, password, "shop"
		c, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() { _ = c.Close(ctx) }()
		var n int
		return c.QueryRow(ctx, `SELECT count(*) FROM negozio`).Scan(&n)
	}
	if err := login(oldPW); err != nil {
		t.Fatalf("il collegamento appena creato non entra: %v", err)
	}

	rec, err := e.ctl.Backup(ctx, cur(), store.BackupOnDemand, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := super.Exec(ctx, cur(), "shop", `DELETE FROM negozio WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	restore := func() {
		t.Helper()
		if err := e.ctl.RestoreBackup(ctx, cur(), rec, store.RestoreSame, nil); err != nil {
			t.Fatal(err)
		}
		e.ctl.Reconcile(ctx, cur())
		ops, _ := e.st.ManagedDBOperations(ctx, dbID)
		if d := cur(); d.Status != store.DBRunning || ops.RestoreError != nil || ops.Restore != nil {
			t.Fatalf("dopo il ripristino: stato %s, errore %v, pendente %v", d.Status, ops.RestoreError, ops.Restore)
		}
	}

	// 1) Ripristino sul posto: il ruolo del collegamento esiste con la vecchia password.
	versions, deploys := e.versionCount(appID), e.deployCount(appID)
	restore()
	newPW := e.secrets(appID)["PGPASSWORD"]
	if newPW == "" || newPW == oldPW || !hex32.MatchString(newPW) {
		t.Fatalf("password dopo il ripristino: %q (vecchia %q)", newPW, oldPW)
	}
	if err := login(newPW); err != nil {
		t.Fatalf("la password nuova non entra: %v", err)
	}
	if err := login(oldPW); err == nil {
		t.Fatal("la vecchia password entra ancora dopo il ripristino")
	}
	if got := e.secrets(appID); !strings.Contains(got["DATABASE_URL"], newPW) || strings.Contains(got["DATABASE_URL"], oldPW) {
		t.Fatalf("i segreti dell'app non hanno la password nuova: %v", got)
	}
	if e.versionCount(appID) != versions+1 || e.deployCount(appID) != deploys+1 {
		t.Fatal("dopo il ripristino l'app deve essere ridistribuita (una versione e un deploy)")
	}
	var n int
	if err := super.Exec(ctx, cur(), "shop", `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	rows, _ := super.Query(ctx, cur(), "shop", `SELECT count(*) FROM negozio`)
	if fmt.Sprint(rows) != "[2]" {
		t.Fatalf("righe dopo il ripristino %v (n=%d), volute 2", rows, n)
	}

	// 2) Istanza vuota: il ruolo del collegamento non c'e' piu'; il ripristino lo ricrea con una password ancora nuova.
	if err := super.Exec(ctx, cur(), "postgres", `DROP OWNED BY "`+user+`"; DROP ROLE "`+user+`"`); err != nil {
		t.Fatal(err)
	}
	if err := login(newPW); err == nil {
		t.Fatal("il ruolo era stato tolto: nessuna password dovrebbe entrare")
	}
	versions, deploys = e.versionCount(appID), e.deployCount(appID)
	restore()
	again := e.secrets(appID)["PGPASSWORD"]
	if again == "" || again == newPW || again == oldPW {
		t.Fatalf("password dopo il secondo ripristino: %q", again)
	}
	if err := login(again); err != nil {
		t.Fatalf("il collegamento ricreato non entra: %v", err)
	}
	if err := login(newPW); err == nil {
		t.Fatal("la password precedente entra sul ruolo ricreato")
	}
	if e.versionCount(appID) != versions+1 || e.deployCount(appID) != deploys+1 {
		t.Fatal("anche il secondo ripristino ridistribuisce l'app")
	}
}

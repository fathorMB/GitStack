//go:build integration

package databases

import (
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

type fakeVerifier struct {
	calls atomic.Int32
	err   error
}

func (v *fakeVerifier) Verify(context.Context, string) error {
	v.calls.Add(1)
	return v.err
}

type ctlEnv struct {
	t     *testing.T
	pool  *pgxpool.Pool
	st    *store.Store
	kube  *fake.Clientset
	ctl   *Controller
	root  *x509.Certificate
	sql   *recorder
	ver   *fakeVerifier
	order []string // eventi in ordine: "verify", "create statefulsets"...
}

func newCtlEnv(t *testing.T, tune func(*Config)) *ctlEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	root, caDir := writeTestCA(t)
	e := &ctlEnv{t: t, root: root, pool: pool, st: store.New(pool), kube: fake.NewSimpleClientset(), sql: &recorder{}, ver: &fakeVerifier{}}
	e.kube.PrependReactor("create", "statefulsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		e.order = append(e.order, "create statefulsets")
		return false, nil, nil
	})
	cfg := Config{
		ControllerServiceAccount: "gitstack/gitstack-core", WorkloadRole: "gitstack-db-workload",
		Images: map[int]Image{16: {Repository: "docker.io/library/postgres", Tag: "16-alpine", Digest: "sha256:" + strings.Repeat("b", 64)}, 17: testImage},
		CADir:  caDir, SQL: e.sql, UsageEvery: time.Nanosecond, ProgressTimeout: time.Hour,
		Verifier: verifierFunc(func(ctx context.Context, ref string) error {
			e.order = append(e.order, "verify")
			return e.ver.Verify(ctx, ref)
		}),
	}
	if tune != nil {
		tune(&cfg)
	}
	e.ctl = New(e.st, e.kube, cfg)
	return e
}

type verifierFunc func(ctx context.Context, ref string) error

func (f verifierFunc) Verify(ctx context.Context, ref string) error { return f(ctx, ref) }

func (e *ctlEnv) create(owner, name string, major int) store.ManagedDB {
	e.t.Helper()
	d, err := e.st.CreateManagedDB(context.Background(), store.NewManagedDB{ID: uuid.New(), OwnerName: owner, OwnerType: "user", OwnerID: uuid.New(),
		Name: name, PgMajor: major, SizeBytes: 1 << 30, CPUMillis: 100, MemoryBytes: 256 << 20}, time.Now().UTC())
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.ctl.Provision(context.Background(), d, "owner-pw-"+name); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *ctlEnv) reload(id uuid.UUID) store.ManagedDB {
	e.t.Helper()
	d, err := e.st.GetManagedDBByID(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *ctlEnv) markReady(d store.ManagedDB) {
	e.t.Helper()
	ns := Namespace(d.ID)
	sts, err := e.kube.AppsV1().StatefulSets(ns).Get(context.Background(), WorkloadName, metav1.GetOptions{})
	if err != nil {
		e.t.Fatal(err)
	}
	sts.Status.ReadyReplicas, sts.Status.UpdatedReplicas = 1, 1
	if _, err := e.kube.AppsV1().StatefulSets(ns).UpdateStatus(context.Background(), sts, metav1.UpdateOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

func TestDatabases_CreazioneFinoAReady(t *testing.T) {
	e := newCtlEnv(t, nil)
	d := e.create("alice", "shop", 17)
	ctx := context.Background()
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	ns := Namespace(d.ID)

	// cosign PRIMA dello StatefulSet (DB5).
	if len(e.order) < 2 || e.order[0] != "verify" || e.order[1] != "create statefulsets" {
		t.Fatalf("ordine: %v", e.order)
	}
	got := e.reload(d.ID)
	if got.Status != store.DBCreating || got.ImageVerifiedAt == nil || got.ImageDigest == nil || *got.ImageDigest != testImage.Digest {
		t.Fatalf("stato %s, verificata=%v digest=%v", got.Status, got.ImageVerifiedAt, got.ImageDigest)
	}
	for _, name := range []string{SecretAuth, SecretTLS, SecretConfig} {
		if _, err := e.kube.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{}); err != nil {
			t.Fatalf("Secret %s: %v", name, err)
		}
	}
	if _, err := e.kube.CoreV1().Services(ns).Get(ctx, WorkloadName, metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.kube.NetworkingV1().NetworkPolicies(ns).Get(ctx, NetworkPolicyName, metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if e.sql.index("CREATE ROLE") >= 0 {
		t.Fatal("i ruoli non si creano prima che il pod sia pronto")
	}

	e.markReady(d)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	got = e.reload(d.ID)
	if got.Status != store.DBRunning {
		t.Fatalf("stato %s (%v), voluto running", got.Status, got.StatusReason)
	}
	all := strings.Join(e.sql.stmts, "\n")
	for _, must := range []string{`CREATE ROLE "shop_owner" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE`, `CREATE ROLE "shop_ro" NOLOGIN`, `CREATE DATABASE "shop" OWNER "shop_owner"`, "pg_read_all_data"} {
		if !strings.Contains(all, must) {
			t.Errorf("manca %q in:\n%s", must, all)
		}
	}
	// Si crea una sola volta: una seconda riconciliazione non rifa i ruoli.
	before := len(e.sql.stmts)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	if after := strings.Join(e.sql.stmts[before:], "\n"); strings.Contains(after, "CREATE ROLE") {
		t.Fatalf("ruoli ricreati: %s", after)
	}
	// La password dell'owner e' nel Secret, mai nel database di core.
	sec, _ := e.kube.CoreV1().Secrets(ns).Get(ctx, SecretAuth, metav1.GetOptions{})
	if string(sec.Data["owner-password"]) != "owner-pw-shop" {
		t.Fatalf("password nel Secret: %q", sec.Data["owner-password"])
	}
}

func TestDatabases_CosignFallitoNonCreaLoStatefulSet(t *testing.T) {
	e := newCtlEnv(t, nil)
	e.ver.err = errors.New("firma non valida")
	d := e.create("alice", "shop", 17)
	e.ctl.Reconcile(context.Background(), e.reload(d.ID))
	got := e.reload(d.ID)
	if got.Status != store.DBFailed || got.StatusReason == nil || !strings.Contains(*got.StatusReason, "firma non valida") {
		t.Fatalf("stato %s motivo %v", got.Status, got.StatusReason)
	}
	if _, err := e.kube.AppsV1().StatefulSets(Namespace(d.ID)).Get(context.Background(), WorkloadName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("lo StatefulSet non doveva esistere: %v", err)
	}
	// Senza verificatore configurato la creazione fallisce in modo esplicito (mai senza verifica).
	e2 := newCtlEnv(t, func(c *Config) { c.Verifier = nil })
	d2 := e2.create("alice", "shop", 17)
	e2.ctl.Reconcile(context.Background(), e2.reload(d2.ID))
	if g := e2.reload(d2.ID); g.Status != store.DBFailed || !strings.Contains(*g.StatusReason, "cosign") {
		t.Fatalf("senza verificatore: %s %v", g.Status, g.StatusReason)
	}
}

func TestDatabases_StatoErrore(t *testing.T) {
	e := newCtlEnv(t, func(c *Config) { c.Images = map[int]Image{17: testImage} })
	d := e.create("alice", "old", 16)
	e.ctl.Reconcile(context.Background(), e.reload(d.ID))
	got := e.reload(d.ID)
	if got.Status != store.DBFailed || got.StatusReason == nil || !strings.Contains(*got.StatusReason, "16") {
		t.Fatalf("stato %s motivo %v", got.Status, got.StatusReason)
	}
}

// DB4: il controller crea RoleBinding solo nei namespace dei database con la label; nessun ClusterRoleBinding;
// e rifiuta di legare il ruolo modello altrove.
func TestDatabases_RoleBindingSoloNeiNamespaceDeiDatabase(t *testing.T) {
	e := newCtlEnv(t, nil)
	ctx := context.Background()
	other, _ := e.kube.CoreV1().Namespaces().Create(ctx, plainNamespace("default"), metav1.CreateOptions{})
	_ = other
	for _, n := range []string{"shop", "blog"} {
		d := e.create("alice", n, 17)
		e.ctl.Reconcile(ctx, e.reload(d.ID))
	}
	rbs, err := e.kube.RbacV1().RoleBindings(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil || len(rbs.Items) != 2 {
		t.Fatalf("RoleBinding: %d (%v)", len(rbs.Items), err)
	}
	for _, rb := range rbs.Items {
		ns, err := e.kube.CoreV1().Namespaces().Get(ctx, rb.Namespace, metav1.GetOptions{})
		if err != nil || !strings.HasPrefix(rb.Namespace, "gitstack-db-") || ns.Labels["gitstack.io/managed-db"] != "true" {
			t.Fatalf("RoleBinding fuori da un namespace dei database: %s (%v)", rb.Namespace, err)
		}
		if rb.RoleRef.Kind != "ClusterRole" || rb.RoleRef.Name != "gitstack-db-workload" || len(rb.Subjects) != 1 || rb.Subjects[0].Name != "gitstack-core" {
			t.Fatalf("roleRef/soggetti: %+v", rb)
		}
	}
	if crb, _ := e.kube.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{}); len(crb.Items) != 0 {
		t.Fatalf("il controller non crea ClusterRoleBinding: %d", len(crb.Items))
	}
	// Il test fallisce se il controller creasse un binding fuori da quei namespace: qui ci si prova.
	if err := e.ctl.ensureRoleBinding(ctx, "default"); err == nil {
		t.Fatal("RoleBinding fuori dai namespace dei database accettato")
	}
	if all, _ := e.kube.RbacV1().RoleBindings("default").List(ctx, metav1.ListOptions{}); len(all.Items) != 0 {
		t.Fatal("RoleBinding creato in default")
	}
	// Un namespace gitstack-db-* senza la label non e' dei database.
	bare := plainNamespace("gitstack-db-zzz")
	_, _ = e.kube.CoreV1().Namespaces().Create(ctx, bare, metav1.CreateOptions{})
	if err := e.ctl.ensureRoleBinding(ctx, "gitstack-db-zzz"); err == nil {
		t.Fatal("RoleBinding in un namespace senza la label accettato")
	}
}

// DB19: l'eliminazione toglie il namespace (con PVC e Secret dentro) e libera il nome; un database di un
// altro owner non si tocca mai.
func TestDatabases_Eliminazione(t *testing.T) {
	e := newCtlEnv(t, nil)
	ctx := context.Background()
	mine := e.create("alice", "shop", 17)
	theirs := e.create("bob", "shop", 17)
	e.ctl.Reconcile(ctx, e.reload(mine.ID))
	e.ctl.Reconcile(ctx, e.reload(theirs.ID))
	actor := &store.AppActor{ID: uuid.New(), Username: "alice", Type: "human"}
	if err := e.ctl.Delete(ctx, e.reload(mine.ID), actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.kube.CoreV1().Namespaces().Get(ctx, Namespace(mine.ID), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("namespace ancora presente: %v", err)
	}
	if _, err := e.kube.CoreV1().Namespaces().Get(ctx, Namespace(theirs.ID), metav1.GetOptions{}); err != nil {
		t.Fatalf("il namespace di un altro owner non si tocca: %v", err)
	}
	if _, err := e.kube.CoreV1().Secrets(Namespace(theirs.ID)).Get(ctx, SecretAuth, metav1.GetOptions{}); err != nil {
		t.Fatalf("i Secret di un altro owner non si toccano: %v", err)
	}
	if _, err := e.st.GetManagedDB(ctx, "alice", "shop"); !errors.Is(err, store.ErrManagedDBNotFound) {
		t.Fatalf("la riga deve sparire e il nome liberarsi: %v", err)
	}
	if other, err := e.st.GetManagedDB(ctx, "bob", "shop"); err != nil || other.ID != theirs.ID {
		t.Fatalf("il database di bob: %v", err)
	}
	// Il nome si libera subito.
	again := e.create("alice", "shop", 17)
	if again.ID == mine.ID {
		t.Fatal("serve un id nuovo")
	}
	// Audit ed evento database.deleted.
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE name = 'database.deleted'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("eventi database.deleted: %d (%v)", n, err)
	}
	audit, _ := e.st.ManagedDBAudit(ctx, mine.ID)
	var actions []string
	for _, a := range audit {
		actions = append(actions, a.Action)
	}
	if strings.Join(actions, ",") != "created,delete_requested,deleted" {
		t.Fatalf("audit: %v", actions)
	}
}

// Un namespace con lo stesso nome ma label di un altro database non si elimina (e la riga resta `deleting`).
func TestDatabases_EliminazioneRifiutaNamespaceAltrui(t *testing.T) {
	e := newCtlEnv(t, nil)
	ctx := context.Background()
	d := e.create("alice", "shop", 17)
	ns, _ := e.kube.CoreV1().Namespaces().Get(ctx, Namespace(d.ID), metav1.GetOptions{})
	ns.Labels[LabelDBID] = uuid.New().String()
	_, _ = e.kube.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{})
	if err := e.ctl.Delete(ctx, e.reload(d.ID), nil); err == nil {
		t.Fatal("l'eliminazione doveva rifiutare il namespace altrui")
	}
	if _, err := e.kube.CoreV1().Namespaces().Get(ctx, Namespace(d.ID), metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace toccato: %v", err)
	}
	if got := e.reload(d.ID); got.Status != store.DBDeleting {
		t.Fatalf("stato %s", got.Status)
	}
}

// DB3: il ridimensionamento cambia la dimensione dichiarata e non espande il PVC; avvisi all'80% e al 100%.
func TestDatabases_RidimensionamentoEAvvisiDiDisco(t *testing.T) {
	e := newCtlEnv(t, nil)
	ctx := context.Background()
	d := e.create("alice", "shop", 17)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	e.markReady(d)
	e.ctl.Reconcile(ctx, e.reload(d.ID))

	big := int64(4 << 30)
	mem := int64(512 << 20)
	updated, err := e.st.UpdateManagedDB(ctx, d.ID, store.ManagedDBChange{SizeBytes: &big, MemoryBytes: &mem}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if updated.SizeBytes != big || updated.Status != store.DBUpdating {
		t.Fatalf("dopo il ridimensionamento: %d %s", updated.SizeBytes, updated.Status)
	}
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	sts, _ := e.kube.AppsV1().StatefulSets(Namespace(d.ID)).Get(ctx, WorkloadName, metav1.GetOptions{})
	if sts.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().Value() != mem {
		t.Fatal("la nuova RAM deve arrivare nello StatefulSet")
	}
	if sz := sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests.Storage().Value(); sz != 1<<30 {
		t.Fatalf("il PVC non si espande: %d", sz)
	}

	// Avvisi: 85% di 4 GiB -> warning, poi 100% -> full; ogni stato avvisa una volta sola.
	events := func() int {
		var n int
		_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE name = 'database.quota_warning'`).Scan(&n)
		return n
	}
	e.markReady(d)
	for i, step := range []struct {
		size  int64
		state string
		evs   int
	}{{big * 85 / 100, "warning", 1}, {big * 86 / 100, "warning", 1}, {big, "full", 2}, {big / 2, "none", 2}} {
		e.sql.size = step.size
		e.ctl.Reconcile(ctx, e.reload(d.ID))
		got := e.reload(d.ID)
		if got.DiskState != step.state || got.UsageBytes == nil || *got.UsageBytes != step.size || events() != step.evs {
			t.Fatalf("passo %d: stato disco %s (voluto %s), uso %v, avvisi %d (voluti %d)", i, got.DiskState, step.state, got.UsageBytes, events(), step.evs)
		}
	}
}

func TestDatabases_ErroreTransitorioNonSpostaLoStato(t *testing.T) {
	e := newCtlEnv(t, nil)
	d := e.create("alice", "shop", 17)
	e.kube.PrependReactor("create", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden transitorio")
	})
	e.ctl.Reconcile(context.Background(), e.reload(d.ID))
	if got := e.reload(d.ID); got.Status != store.DBCreating {
		t.Fatalf("stato %s", got.Status)
	}
}

// plainNamespace e' un namespace senza la label dei database.
func plainNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

//go:build integration

package databases

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Prove su un cluster k3d vero (internal/databases/k3d-test.sh): PostgreSQL alpine con l'immagine del chart,
// non root, TLS dalla CA intermedia, NetworkPolicy deny di default. Senza GITSTACK_TEST_KUBECONFIG si saltano.
//
// Il verificatore cosign e' finto: le firme delle immagini di Docker Hub non sono verificabili con la chiave
// dell'istanza. La verifica vera e' provata dai test del controller (ordine verifica -> StatefulSet).

const (
	envKubeconfig = "GITSTACK_TEST_KUBECONFIG"
	// Digest dell'indice multi-architettura di postgres:17-alpine, quello di deploy/gitstack/values.yaml.
	k3dImageDigest = "sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b"
)

type k3dEnv struct {
	t    *testing.T
	kube *kubernetes.Clientset
	ctl  *Controller
	st   *store.Store
	exec *SPDYExecutor
	ctx  context.Context
}

func newK3dEnv(t *testing.T) *k3dEnv {
	t.Helper()
	kc := os.Getenv(envKubeconfig)
	if kc == "" {
		t.Skip(envKubeconfig + " non impostata: serve un cluster k3d (internal/databases/k3d-test.sh)")
	}
	rc, err := clientcmd.BuildConfigFromFlags("", kc)
	if err != nil {
		t.Fatal(err)
	}
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	// Il chart crea la PriorityClass (ADR 0002): su un k3d vuoto i pod senza non nascono.
	if _, err := kube.SchedulingV1().PriorityClasses().Get(context.Background(), PriorityClass, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		pc := priorityClass()
		if _, err := kube.SchedulingV1().PriorityClasses().Create(context.Background(), pc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	pool, _ := dbtest.NewPool(t)
	_, caDir := writeTestCA(t)
	exec := &SPDYExecutor{Config: rc, Client: kube}
	e := &k3dEnv{t: t, kube: kube, st: store.New(pool), exec: exec}
	e.ctl = New(e.st, kube, Config{
		Images: map[int]Image{17: {Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: k3dImageDigest}},
		CADir:  caDir, Exec: exec, Verifier: verifierFunc(func(context.Context, string) error { return nil }),
		LoopInterval: time.Second, UsageEvery: 5 * time.Second, ProgressTimeout: 4 * time.Minute, Log: nil,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.ctx = ctx
	go e.ctl.Run(ctx)
	return e
}

func (e *k3dEnv) create(owner, name string) (store.ManagedDB, string) {
	e.t.Helper()
	d, err := e.st.CreateManagedDB(context.Background(), store.NewManagedDB{ID: uuid.New(), OwnerName: owner, OwnerType: "user", OwnerID: uuid.New(),
		Name: name, PgMajor: 17, SizeBytes: 1 << 30, CPUMillis: 100, MemoryBytes: 256 << 20}, time.Now().UTC())
	if err != nil {
		e.t.Fatal(err)
	}
	pw, _ := NewPassword()
	pw, err = e.ctl.Provision(context.Background(), d, pw)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() {
		_ = e.kube.CoreV1().Namespaces().Delete(context.Background(), Namespace(d.ID), metav1.DeleteOptions{})
	})
	return d, pw
}

func (e *k3dEnv) waitStatus(d store.ManagedDB, want string) store.ManagedDB {
	e.t.Helper()
	deadline := time.Now().Add(6 * time.Minute)
	for {
		got, err := e.st.GetManagedDBByID(context.Background(), d.ID)
		if err != nil {
			e.t.Fatal(err)
		}
		if got.Status == want {
			return got
		}
		if got.Status == store.DBFailed || time.Now().After(deadline) {
			reason := ""
			if got.StatusReason != nil {
				reason = *got.StatusReason
			}
			e.t.Fatalf("database in stato %s (%s), voluto %s", got.Status, reason, want)
		}
		time.Sleep(2 * time.Second)
	}
}

// sh esegue un comando nel pod del database.
func (e *k3dEnv) sh(d store.ManagedDB, script string) (string, error) {
	return e.exec.Exec(context.Background(), Namespace(d.ID), PodName, WorkloadName, []string{"sh", "-c", script}, nil)
}

// DB4/DB5: il pod gira non root con il contesto di sicurezza dell'ADR e il namespace ha Pod Security restricted.
func TestDatabases_ContestoDiSicurezzaSuK3d(t *testing.T) {
	e := newK3dEnv(t)
	d, _ := e.create("alice", "shop")
	e.waitStatus(d, store.DBRunning)
	ns, _ := e.kube.CoreV1().Namespaces().Get(context.Background(), Namespace(d.ID), metav1.GetOptions{})
	if ns.Labels["pod-security.kubernetes.io/enforce"] != "restricted" || ns.Labels[LabelManagedDB] != "true" {
		t.Fatalf("label del namespace: %v", ns.Labels)
	}
	out, err := e.sh(d, "id -u; touch /etc/non-scrivibile 2>&1 || echo ro; echo $PGDATA; ls -d /var/lib/postgresql/data/pgdata")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(out)
	if len(lines) < 3 || lines[0] != "70" || !strings.Contains(out, "Read-only file system") && !strings.Contains(out, "ro") {
		t.Fatalf("uid atteso 70 e root fs in sola lettura: %q", out)
	}
	if !strings.Contains(out, "/var/lib/postgresql/data/pgdata") {
		t.Fatalf("PGDATA: %q", out)
	}
	pod, _ := e.kube.CoreV1().Pods(Namespace(d.ID)).Get(context.Background(), PodName, metav1.GetOptions{})
	if pod.Spec.SecurityContext.RunAsNonRoot == nil || !*pod.Spec.SecurityContext.RunAsNonRoot || *pod.Spec.SecurityContext.RunAsUser != 70 {
		t.Fatalf("contesto di sicurezza del pod: %+v", pod.Spec.SecurityContext)
	}
	// Ruoli (DB7): gitstack_admin superuser, owner no.
	sql := func(q string) string {
		o, err := e.sh(d, fmt.Sprintf(`psql -X -t -A -h /var/run/postgresql -U gitstack_admin -d postgres -c "%s"`, q))
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return strings.TrimSpace(o)
	}
	if got := sql(`SELECT rolsuper FROM pg_roles WHERE rolname = 'gitstack_admin'`); got != "t" {
		t.Fatalf("gitstack_admin superuser: %q", got)
	}
	if got := sql(`SELECT rolsuper OR rolcreaterole OR rolcreatedb FROM pg_roles WHERE rolname = 'shop_owner'`); got != "f" {
		t.Fatalf("shop_owner non deve essere superuser: %q", got)
	}
	if got := sql(`SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'shop'`); got != "shop_owner" {
		t.Fatalf("proprietario del database: %q", got)
	}
}

// DB4/DB11: NetworkPolicy deny di default: un pod di un namespace non collegato non raggiunge la 5432, uno di
// un namespace con la label db-link-<id> si'.
func TestDatabases_IsolamentoSuK3d(t *testing.T) {
	e := newK3dEnv(t)
	d, _ := e.create("alice", "net")
	e.waitStatus(d, store.DBRunning)
	np, err := e.kube.NetworkingV1().NetworkPolicies(Namespace(d.ID)).Get(context.Background(), NetworkPolicyName, metav1.GetOptions{})
	if err != nil || len(np.Spec.Egress) != 0 || len(np.Spec.PolicyTypes) != 2 {
		t.Fatalf("NetworkPolicy deny di default: %+v %v", np, err)
	}
	probe := func(label string, linked bool) bool {
		ns := "gitstack-dbtest-" + label + "-" + d.ID.String()[:8]
		labels := map[string]string{}
		if linked {
			labels[LabelDBLinkPrefix+d.ID.String()] = "true"
		}
		if _, err := e.kube.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns, Labels: labels}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })
		reach := func() bool {
			name := fmt.Sprintf("probe-%d", time.Now().UnixNano())
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{Name: "p", Image: "docker.io/library/postgres@" + k3dImageDigest,
					Command: []string{"pg_isready", "-h", ServiceHost(d.ID), "-p", "5432", "-t", "8"}}}}}
			if _, err := e.kube.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Minute)
			for time.Now().Before(deadline) {
				p, _ := e.kube.CoreV1().Pods(ns).Get(context.Background(), name, metav1.GetOptions{})
				if p.Status.Phase == corev1.PodSucceeded {
					return true
				}
				if p.Status.Phase == corev1.PodFailed {
					return false
				}
				time.Sleep(2 * time.Second)
			}
			t.Fatal("il pod di prova non e' terminato")
			return false
		}
		if !linked {
			return reach()
		}
		// Un pod appena creato non e' ammesso per qualche secondo (ADR 0003): si ritenta.
		for i := 0; i < 6; i++ {
			if reach() {
				return true
			}
		}
		return false
	}
	if probe("altro", false) {
		t.Fatal("un namespace non collegato ha raggiunto la 5432")
	}
	if !probe("collegato", true) {
		t.Fatal("un namespace con la label db-link non ha raggiunto la 5432")
	}
}

// DB19: l'eliminazione toglie namespace, PVC e Secret; il database di un altro owner non si tocca.
func TestDatabases_EliminazioneSuK3d(t *testing.T) {
	e := newK3dEnv(t)
	mine, _ := e.create("alice", "del")
	theirs, _ := e.create("bob", "del")
	e.waitStatus(mine, store.DBRunning)
	e.waitStatus(theirs, store.DBRunning)
	ns := Namespace(mine.ID)
	if pvcs, err := e.kube.CoreV1().PersistentVolumeClaims(ns).List(context.Background(), metav1.ListOptions{}); err != nil || len(pvcs.Items) != 1 || *pvcs.Items[0].Spec.StorageClassName != "local-path" {
		t.Fatalf("PVC local-path: %v %v", pvcs, err)
	}
	if err := e.ctl.Delete(context.Background(), mine, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		_, err := e.kube.CoreV1().Namespaces().Get(context.Background(), ns, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("namespace ancora presente: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
	if pvcs, _ := e.kube.CoreV1().PersistentVolumeClaims(ns).List(context.Background(), metav1.ListOptions{}); pvcs != nil && len(pvcs.Items) != 0 {
		t.Fatal("PVC rimasti")
	}
	if secrets, _ := e.kube.CoreV1().Secrets(ns).List(context.Background(), metav1.ListOptions{}); secrets != nil && len(secrets.Items) != 0 {
		t.Fatal("Secret rimasti")
	}
	if _, err := e.kube.CoreV1().Secrets(Namespace(theirs.ID)).Get(context.Background(), SecretAuth, metav1.GetOptions{}); err != nil {
		t.Fatalf("il database di un altro owner non si tocca: %v", err)
	}
	if got := e.waitStatus(theirs, store.DBRunning); got.ID != theirs.ID {
		t.Fatal("database di bob alterato")
	}
}

func priorityClass() *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{ObjectMeta: metav1.ObjectMeta{Name: PriorityClass}, Value: 100000}
}

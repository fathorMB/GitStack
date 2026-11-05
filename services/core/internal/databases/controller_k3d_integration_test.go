//go:build integration

package databases

import (
	"context"
	"crypto/x509"
	"encoding/json"
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
	// k3dLANPortFrom e k3dLANPortTo: le porte che k3d-test.sh mappa dall'host al load balancer.
	k3dLANPortFrom = 15432
	k3dLANPortTo   = 15439

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
	// root e' la radice di prova: i client LAN si fidano solo di lei (GIT-554).
	root *x509.Certificate
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
	root, caDir := writeTestCA(t)
	exec := &SPDYExecutor{Config: rc, Client: kube}
	e := &k3dEnv{t: t, kube: kube, st: store.New(pool), exec: exec, root: root}
	e.ctl = New(e.st, kube, Config{
		Images: map[int]Image{17: {Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: k3dImageDigest}},
		CADir:  caDir, Exec: exec, Verifier: verifierFunc(func(context.Context, string) error { return nil }),
		LoopInterval: time.Second, UsageEvery: 5 * time.Second, ProgressTimeout: 4 * time.Minute, Log: nil,
		// Le porte mappate sul load balancer da k3d-test.sh.
		LANPortFrom: k3dLANPortFrom, LANPortTo: k3dLANPortTo,
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
	host := ServiceHost(d.ID)
	// Un solo pod per sonda: i tentativi stanno dentro il container. Un pod appena creato non e' ammesso per
	// qualche secondo (ADR 0003, kube-router aggiorna gli ipset in ritardo), quindi ritentare con pod nuovi
	// non vedrebbe mai la policy applicata.
	//
	// pg_isready -t 2 e' un timeout MASSIMO: un rifiuto immediato puo' terminare subito. La durata minima della
	// finestra viene quindi dalla pausa obbligatoria dopo ogni esito negativo, non da -t.
	//
	// Collegato: 32 giri con sleep 2 dopo ogni esito negativo = almeno 64 s (>= 60 s) anche se pg_isready fallisce
	// subito; al massimo circa 128 s se ogni giro consuma il timeout.
	linkedScript := fmt.Sprintf(`i=0; while [ $i -lt 32 ]; do
  if pg_isready -h %s -p 5432 -t 2; then echo RAGGIUNTO giro=$i; exit 0; fi
  i=$((i+1)); sleep 2
done; echo NON-RAGGIUNTO; exit 1`, host)
	// Non collegato: 30 giri con sleep 1 dopo ogni esito negativo = almeno 30 s dall'avvio del pod, anche se
	// pg_isready fallisce subito (piu' dei secondi di ammissione di kube-router). Esce 1 subito alla prima
	// riuscita, 0 solo dopo tutta la finestra.
	isolatedScript := fmt.Sprintf(`i=0; while [ $i -lt 30 ]; do
  if pg_isready -h %s -p 5432 -t 2; then echo RAGGIUNTO giro=$i; exit 1; fi
  i=$((i+1)); sleep 1
done; echo ISOLATO; exit 0`, host)

	// dump stampa i log del pod di prova e lo spec della NetworkPolicy, per leggere un rosso dal job.
	dump := func(ns, name string) {
		logs, err := e.kube.CoreV1().Pods(ns).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(context.Background())
		t.Logf("log del pod %s/%s (err=%v):\n%s", ns, name, err, logs)
		if np, err := e.kube.NetworkingV1().NetworkPolicies(Namespace(d.ID)).Get(context.Background(), NetworkPolicyName, metav1.GetOptions{}); err == nil {
			spec, _ := json.MarshalIndent(np.Spec, "", "  ")
			t.Logf("NetworkPolicy %s/%s spec:\n%s", np.Namespace, np.Name, spec)
		} else {
			t.Logf("NetworkPolicy %s non leggibile: %v", NetworkPolicyName, err)
		}
	}
	// probe crea il namespace e un solo pod; torna true se il container termina con successo.
	probe := func(label string, linked bool, script string) bool {
		ns := "gitstack-dbtest-" + label + "-" + d.ID.String()[:8]
		labels := map[string]string{}
		if linked {
			labels[LabelDBLinkPrefix+d.ID.String()] = "true"
		}
		if _, err := e.kube.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns, Labels: labels}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })
		name := "probe-" + label
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{Name: "p", Image: "docker.io/library/postgres@" + k3dImageDigest,
				Command: []string{"sh", "-c", script}}}}}
		if _, err := e.kube.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		// Tetto di attesa: pull dell'immagine + al piu' circa 100 s di sonda.
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			p, _ := e.kube.CoreV1().Pods(ns).Get(context.Background(), name, metav1.GetOptions{})
			switch p.Status.Phase {
			case corev1.PodSucceeded:
				return true
			case corev1.PodFailed:
				dump(ns, name)
				return false
			}
			time.Sleep(2 * time.Second)
		}
		dump(ns, name)
		t.Fatal("il pod di prova non e' terminato")
		return false
	}
	if !probe("altro", false, isolatedScript) {
		t.Fatal("un namespace non collegato ha raggiunto la 5432 (o la sonda e' fallita): vedi log del pod e NetworkPolicy sopra")
	}
	if !probe("collegato", true, linkedScript) {
		t.Fatal("un namespace con la label db-link non ha raggiunto la 5432 in 32 giri (almeno 64 s): vedi log del pod e NetworkPolicy sopra")
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

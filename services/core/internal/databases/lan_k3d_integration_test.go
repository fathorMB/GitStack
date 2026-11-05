//go:build integration

package databases

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// lanHost e' un nome dell'istanza nelle name constraints della CA di prova (testca_test.go): il client lo
// verifica come libpq con sslmode=verify-full, ma la connessione TCP va al load balancer di k3d su 127.0.0.1
// (come `hostaddr` di psql).
const lanHost = "gitstack.test"

// lanConnect si connette come farebbe psql dal PC: verify-full contro `root` (vuoto = CA di sistema).
func (e *k3dEnv) lanConnect(port int, host, sslmode, rootPEM, user, password, dbname string) error {
	e.t.Helper()
	conninfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=5", host, port, user, password, dbname, sslmode)
	if rootPEM != "" {
		path := filepath.Join(e.t.TempDir(), "ca.crt")
		if err := os.WriteFile(path, []byte(rootPEM), 0o600); err != nil {
			e.t.Fatal(err)
		}
		conninfo += " sslrootcert=" + path
	}
	cfg, err := pgconn.ParseConfig(conninfo)
	if err != nil {
		e.t.Fatal(err)
	}
	// Il nome (verificato nel certificato) non e' nel DNS: si risolve al load balancer di k3d, come hostaddr di psql.
	cfg.LookupFunc = func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }
	cfg.DialFunc = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", port))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	res := conn.ExecParams(ctx, "SELECT current_user", nil, nil, nil, nil).Read()
	if res.Err != nil {
		return res.Err
	}
	if got := string(res.Rows[0][0]); got != user {
		return fmt.Errorf("current_user = %q, voluto %q", got, user)
	}
	return nil
}

// lanPsql e' la prova con libpq vero, come dal PC: psql locale se c'e', altrimenti psql dell'immagine postgres
// con --network host (runner Linux di CI). host= e' il nome verificato, hostaddr= l'indirizzo a cui si arriva
// (il load balancer di k3d sul loopback). Ritorna l'output combinato e l'errore.
func (e *k3dEnv) lanPsql(port int, host, sslmode, rootPEM, user, password, dbname string) (string, error) {
	e.t.Helper()
	dir := e.t.TempDir()
	conn := fmt.Sprintf("host=%s hostaddr=127.0.0.1 port=%d dbname=%s user=%s sslmode=%s connect_timeout=5", host, port, dbname, user, sslmode)
	local := true
	if rootPEM != "" {
		if err := os.WriteFile(filepath.Join(dir, "ca.crt"), []byte(rootPEM), 0o600); err != nil {
			e.t.Fatal(err)
		}
		conn += " sslrootcert=" + filepath.Join(dir, "ca.crt")
	} else {
		conn += " sslrootcert=system"
	}
	var cmd *exec.Cmd
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if _, err := exec.LookPath("psql"); err == nil {
		cmd = exec.CommandContext(ctx, "psql", conn, "-Atc", "select current_user")
	} else {
		local = false
		conn = strings.ReplaceAll(conn, dir, "/ca")
		cmd = exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "host", "-e", "PGPASSWORD", "-v", dir+":/ca:ro",
			"docker.io/library/postgres@"+k3dImageDigest, "psql", conn, "-Atc", "select current_user")
	}
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
	out, err := cmd.CombinedOutput()
	e.t.Logf("psql (%s, sslmode=%s, host=%s, user=%s): err=%v out=%q", map[bool]string{true: "locale", false: "docker"}[local], sslmode, host, user, err, strings.TrimSpace(string(out)))
	return strings.TrimSpace(string(out)), err
}

func rootPEM(root *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw}))
}

// eventually ritenta f fino al tetto; ritorna l'ultimo errore.
func eventually(timeout time.Duration, f func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := f()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(2 * time.Second)
	}
}

// DB16/DB17 (criteri di GIT-554): con l'interruttore acceso psql-come-il-PC si connette con verify-full e la
// CA interna; senza TLS, senza la CA, con un nome sbagliato, con la password sbagliata e come gitstack_admin e'
// respinto; con l'interruttore spento la porta non risponde; la porta e' la stessa dopo un «riavvio» di core.
func TestDatabases_AccessoLANSuK3d(t *testing.T) {
	e := newK3dEnv(t)
	ctx := context.Background()
	d, pw := e.create("alice", "lan")
	d = e.waitStatus(d, store.DBRunning)
	owner := OwnerRole(d.Name)
	root := rootPEM(e.root)

	// Spenta di default: nessun Service LoadBalancer, nessuna db-lan.
	if _, err := e.kube.CoreV1().Services(Namespace(d.ID)).Get(ctx, LANServiceName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Service LAN con la LAN spenta: %v", err)
	}

	on, err := e.ctl.SetLAN(ctx, d, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if on.LANPort == nil || *on.LANPort < k3dLANPortFrom || *on.LANPort > k3dLANPortTo {
		t.Fatalf("porta assegnata: %+v", on.LANPort)
	}
	port := *on.LANPort
	t.Logf("porta LAN assegnata: %d", port)

	// ServiceLB e kube-router ci mettono qualche decina di secondi: si ritenta fino a 5 minuti.
	if err := eventually(5*time.Minute, func() error { return e.lanConnect(port, lanHost, "verify-full", root, owner, pw, d.Name) }); err != nil {
		e.dumpLAN(d)
		t.Fatalf("verify-full con la CA interna non si connette: %v", err)
	}

	// Rifiuti: ognuno con il suo motivo, non un errore qualunque.
	for _, c := range []struct {
		name, host, mode, root, user, pw string
		want                             []string
	}{
		{"senza TLS (sslmode=disable)", lanHost, "disable", "", owner, pw, []string{"rejects connection", "no encryption"}},
		{"verify-full senza la CA privata", lanHost, "verify-full", "", owner, pw, []string{"certificate", "unknown authority"}},
		{"verify-full con un nome sbagliato", "altro.gitstack.test", "verify-full", root, owner, pw, []string{"certificate is valid for", "does not match", "x509"}},
		{"password sbagliata", lanHost, "verify-full", root, owner, "sbagliata", []string{"password authentication failed", "28P01"}},
		{"gitstack_admin dalla rete (DB7)", lanHost, "verify-full", root, AdminRole, "", []string{"rejects connection", "no pg_hba.conf entry"}},
	} {
		err := e.lanConnect(port, c.host, c.mode, c.root, c.user, c.pw, d.Name)
		if err == nil {
			t.Errorf("%s: la connessione e' stata ammessa", c.name)
			continue
		}
		ok := false
		for _, w := range c.want {
			if strings.Contains(err.Error(), w) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s: respinta per un motivo inatteso: %v (attesi: %v)", c.name, err, c.want)
		}
	}

	// Stessa prova con libpq/psql (criterio 1 e 2): verify-full con la CA interna si connette, il resto e' respinto.
	if out, err := e.lanPsql(port, lanHost, "verify-full", root, owner, pw, d.Name); err != nil || out != owner {
		t.Fatalf("psql verify-full con la CA interna: out=%q err=%v", out, err)
	}
	for _, c := range []struct {
		name, host, mode, root, user, pw, want string
	}{
		{"psql sslmode=disable", lanHost, "disable", root, owner, pw, "rejects connection"},
		{"psql senza la CA privata", lanHost, "verify-full", "", owner, pw, "certificate verify failed"},
		{"psql con un nome sbagliato", "altro.gitstack.test", "verify-full", root, owner, pw, "does not match host name"},
		{"psql con password sbagliata", lanHost, "verify-full", root, owner, "sbagliata", "password authentication failed"},
	} {
		out, err := e.lanPsql(port, c.host, c.mode, c.root, c.user, c.pw, d.Name)
		if err == nil || !strings.Contains(out, c.want) {
			t.Errorf("%s: voluto un rifiuto con %q, ottenuto err=%v out=%q", c.name, c.want, err, out)
		}
	}

	// Un pod di un namespace NON collegato non raggiunge indirizzo e porta della LAN del nodo: ServiceLB puo'
	// riscrivere la sorgente, quindi l'esclusione del PodCIDR si prova qui con traffico vero, non si presume.
	e.probeLANFromUnlinkedPod(d, port)

	// «Riavvio» di core: un controller nuovo sullo stesso database lascia la porta com'e' e la LAN aperta.
	restarted := New(e.st, e.kube, e.ctl.cfg)
	restarted.Reconcile(ctx, mustGet(t, e.st, d.ID))
	if again := mustGet(t, e.st, d.ID); again.LANPort == nil || *again.LANPort != port {
		t.Fatalf("porta dopo il riavvio: %v", again.LANPort)
	}
	if err := e.lanConnect(port, lanHost, "verify-full", root, owner, pw, d.Name); err != nil {
		t.Fatalf("dopo il riavvio di core la LAN non risponde: %v", err)
	}

	// Spenta: Service e policy spariscono prima della risposta, e la porta smette di rispondere.
	off, err := e.ctl.SetLAN(ctx, mustGet(t, e.st, d.ID), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if off.LANEnabled || off.LANPort != nil {
		t.Fatalf("modello dopo lo spegnimento: %+v", off)
	}
	if _, err := e.kube.CoreV1().Services(Namespace(d.ID)).Get(ctx, LANServiceName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Service LAN dopo lo spegnimento: %v", err)
	}
	if _, err := e.kube.NetworkingV1().NetworkPolicies(Namespace(d.ID)).Get(ctx, LANNetworkPolicyName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("db-lan dopo lo spegnimento: %v", err)
	}
	// La porta non risponde (rifiuto o chiusura, mai una sessione) e continua a non rispondere.
	if err := eventually(2*time.Minute, func() error {
		if e.lanConnect(port, lanHost, "verify-full", root, owner, pw, d.Name) == nil {
			return fmt.Errorf("la porta %d risponde ancora", port)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		time.Sleep(time.Second)
		if e.lanConnect(port, lanHost, "verify-full", root, owner, pw, d.Name) == nil {
			t.Fatalf("la porta %d ha ripreso a rispondere con la LAN spenta", port)
		}
	}
	if out, err := e.lanPsql(port, lanHost, "verify-full", root, owner, pw, d.Name); err == nil {
		t.Fatalf("psql: la porta %d risponde con la LAN spenta: %q", port, out)
	}
	// Anche il traffico interno (le app collegate) e' intatto: db-isolation e il Service interno restano.
	if _, err := e.kube.NetworkingV1().NetworkPolicies(Namespace(d.ID)).Get(ctx, NetworkPolicyName, metav1.GetOptions{}); err != nil {
		t.Fatalf("db-isolation: %v", err)
	}
}

// probeLANFromUnlinkedPod lancia un pod in un namespace senza la label db-link-<id> che prova a raggiungere
// <IP del nodo>:<porta LAN> per tutta la finestra: pg_isready riuscito anche una sola volta e' un buco.
func (e *k3dEnv) probeLANFromUnlinkedPod(d store.ManagedDB, port int) {
	e.t.Helper()
	nodes, err := e.kube.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil || len(nodes.Items) == 0 {
		e.t.Fatalf("nodi: %v", err)
	}
	nodeIP := ""
	for _, a := range nodes.Items[0].Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			nodeIP = a.Address
		}
	}
	if nodeIP == "" {
		e.t.Fatal("il nodo non ha un InternalIP")
	}
	ns := "gitstack-dbtest-lan-" + d.ID.String()[:8]
	if _, err := e.kube.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = e.kube.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })
	// 30 giri con sleep 2 dopo ogni esito negativo = almeno 60 s di finestra; esce 1 alla prima riuscita.
	script := fmt.Sprintf(`i=0; while [ $i -lt 30 ]; do
  if pg_isready -h %s -p %d -t 2; then echo RAGGIUNTO giro=$i; exit 1; fi
  i=$((i+1)); sleep 2
done; echo ISOLATO; exit 0`, nodeIP, port)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "probe-lan", Namespace: ns}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{{Name: "p", Image: "docker.io/library/postgres@" + k3dImageDigest, Command: []string{"sh", "-c", script}}}}}
	if _, err := e.kube.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		e.t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		p, _ := e.kube.CoreV1().Pods(ns).Get(context.Background(), "probe-lan", metav1.GetOptions{})
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			e.t.Logf("pod non collegato verso %s:%d: ISOLATO", nodeIP, port)
			return
		case corev1.PodFailed:
			logs, _ := e.kube.CoreV1().Pods(ns).GetLogs("probe-lan", &corev1.PodLogOptions{}).DoRaw(context.Background())
			e.dumpLAN(d)
			e.t.Fatalf("un pod di un namespace non collegato ha raggiunto %s:%d (la LAN passa dalla rete delle app): %s", nodeIP, port, logs)
		}
		time.Sleep(2 * time.Second)
	}
	e.t.Fatal("il pod di prova non e' terminato")
}

func mustGet(t *testing.T, st *store.Store, id uuid.UUID) store.ManagedDB {
	t.Helper()
	d, err := st.GetManagedDBByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// dumpLAN stampa Service e policy per leggere un rosso dal job.
func (e *k3dEnv) dumpLAN(d store.ManagedDB) {
	ns := Namespace(d.ID)
	if svc, err := e.kube.CoreV1().Services(ns).Get(context.Background(), LANServiceName, metav1.GetOptions{}); err == nil {
		e.t.Logf("Service %s: type=%s ports=%+v status=%+v", svc.Name, svc.Spec.Type, svc.Spec.Ports, svc.Status.LoadBalancer)
	} else {
		e.t.Logf("Service LAN non leggibile: %v", err)
	}
	if np, err := e.kube.NetworkingV1().NetworkPolicies(ns).Get(context.Background(), LANNetworkPolicyName, metav1.GetOptions{}); err == nil {
		e.t.Logf("NetworkPolicy %s: %+v", np.Name, np.Spec)
	} else {
		e.t.Logf("db-lan non leggibile: %v", err)
	}
}

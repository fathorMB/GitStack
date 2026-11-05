//go:build integration

package databases

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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
	// Anche il traffico interno (le app collegate) e' intatto: db-isolation e il Service interno restano.
	if _, err := e.kube.NetworkingV1().NetworkPolicies(Namespace(d.ID)).Get(ctx, NetworkPolicyName, metav1.GetOptions{}); err != nil {
		t.Fatalf("db-isolation: %v", err)
	}
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

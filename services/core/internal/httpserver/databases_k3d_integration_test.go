//go:build integration

package httpserver_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

type okVerifier struct{}

func (okVerifier) Verify(context.Context, string) error { return nil }

// k3dCA scrive una radice e un'intermedia con name constraints (come l'installer) e ritorna cartella e radice PEM.
func k3dCA(t *testing.T) (string, []byte) {
	t.Helper()
	rk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rt := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rder, err := x509.CreateCertificate(rand.Reader, rt, rt, &rk.PublicKey, rk)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := x509.ParseCertificate(rder)
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	it := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Test Database CA"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(5 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign, PermittedDNSDomainsCritical: true, PermittedDNSDomains: []string{"svc.cluster.local"}}
	der, err := x509.CreateCertificate(rand.Reader, it, root, &k.PublicKey, rk)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalPKCS8PrivateKey(k)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rder})
}

// Criterio 1 (DB8): un database creato DA API arriva a `running` su k3d e accetta una connessione TLS con le
// credenziali generate (la password della risposta di creazione); sslmode=disable e la password errata sono
// respinti; gitstack_admin non entra dalla rete. Poi l'eliminazione da API toglie namespace, PVC e Secret.
// Serve un cluster (GITSTACK_TEST_KUBECONFIG, internal/databases/k3d-test.sh); il verificatore cosign e'
// finto perche' le firme di Docker Hub non sono verificabili con la chiave dell'istanza.
func TestDatabases_ConnessioneTlsSuK3d(t *testing.T) {
	kc := os.Getenv("GITSTACK_TEST_KUBECONFIG")
	if kc == "" {
		t.Skip("GITSTACK_TEST_KUBECONFIG non impostata: serve un cluster k3d (internal/databases/k3d-test.sh)")
	}
	rc, err := clientcmd.BuildConfigFromFlags("", kc)
	if err != nil {
		t.Fatal(err)
	}
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := kube.SchedulingV1().PriorityClasses().Create(ctx, &schedulingv1.PriorityClass{ObjectMeta: metav1.ObjectMeta{Name: databases.PriorityClass}, Value: 100000}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	caDir, rootPEM := k3dCA(t)
	exec := &databases.SPDYExecutor{Config: rc, Client: kube}
	proxy := &lateController{}
	e := newAppsEnv(t, httpserver.WithDatabases(httpserver.DatabasesConfig{Controller: proxy}))
	img := map[int]databases.Image{17: {Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: "sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b"}}
	ctl := databases.New(store.New(e.pool), kube, databases.Config{Images: img, CADir: caDir, Exec: exec, Verifier: okVerifier{},
		LoopInterval: time.Second, UsageEvery: 5 * time.Second, ProgressTimeout: 5 * time.Minute})
	proxy.set(ctl)
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go ctl.Run(runCtx)

	de := &dbEnv{appsEnv: e}
	created := de.create("alice", "alice", `{"name":"tls","pgMajor":17}`, http.StatusAccepted)
	id := uuid.UUID(created.Database.Id)
	ns := databases.Namespace(id)
	t.Cleanup(func() { _ = kube.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}) })
	deadline := time.Now().Add(6 * time.Minute)
	for {
		rec := e.do(http.MethodGet, "/databases/alice/tls", "alice", "")
		var d openapi.Database
		_ = json.Unmarshal(rec.Body.Bytes(), &d)
		if d.Status == "running" {
			break
		}
		if d.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("database in stato %s: %s", d.Status, rec.Body.String())
		}
		time.Sleep(2 * time.Second)
	}
	// La radice di prova fa da ancora: la catena inviata dal server (foglia + intermedia) deve verificare contro di lei.
	sh := func(script string, in string) (string, error) {
		return exec.Exec(ctx, ns, databases.PodName, databases.WorkloadName, []string{"sh", "-c", script}, strings.NewReader(in))
	}
	if _, err := sh("cat > /tmp/root.crt", string(rootPEM)); err != nil {
		t.Fatal(err)
	}
	host := databases.ServiceHost(id)
	conn := func(mode, user, password string) (string, error) {
		return sh(fmt.Sprintf(`PGPASSWORD=%q psql -X -t -A "host=%s hostaddr=127.0.0.1 port=5432 dbname=tls user=%s sslmode=%s sslrootcert=/tmp/root.crt" -c "select current_user"`, password, host, user, mode), "")
	}
	if out, err := conn("verify-full", created.Credentials.Username, created.Credentials.Password); err != nil || strings.TrimSpace(out) != "tls_owner" {
		t.Fatalf("verify-full con le credenziali della risposta di creazione: %q %v", out, err)
	}
	if out, err := conn("disable", created.Credentials.Username, created.Credentials.Password); err == nil {
		t.Fatalf("sslmode=disable doveva essere respinto: %q", out)
	}
	if out, err := conn("verify-full", created.Credentials.Username, "sbagliata"); err == nil {
		t.Fatalf("password sbagliata accettata: %q", out)
	}
	if out, err := conn("verify-full", "gitstack_admin", ""); err == nil {
		t.Fatalf("gitstack_admin non deve entrare dalla rete: %q", out)
	}

	// Eliminazione da API: namespace (con PVC e Secret) spariti.
	e.want(e.do(http.MethodDelete, "/databases/alice/tls?confirm=alice/tls", "alice", ""), http.StatusNoContent, "")
	for {
		_, err := kube.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			break
		}
		if time.Now().After(deadline.Add(4 * time.Minute)) {
			t.Fatalf("namespace ancora presente: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
	if pvcs, _ := kube.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{}); pvcs != nil && len(pvcs.Items) != 0 {
		t.Fatal("PVC rimasti")
	}
}

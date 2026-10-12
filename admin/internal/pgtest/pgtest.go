// Package pgtest avvia Postgres veri (15, 16, 17, e 14 per i rifiuti) in
// container Docker per i test d'integrazione dell'installazione con database
// esterno (N8). Usa la CLI docker: nessuna dipendenza da testcontainers nel
// modulo admin.
//
// Un test che chiama Start senza Docker viene saltato, tranne con
// GITSTACK_REQUIRE_DOCKER=1 (CI): lì fallisce.
package pgtest

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// SuperPassword è la password dell'utente postgres del container.
const SuperPassword = "super-secret-test"

// Server è un Postgres in un container.
type Server struct {
	Container string
	Port      int
	Major     int
}

// Start avvia postgres:<major>-alpine e aspetta che accetti connessioni TCP.
func Start(t testing.TB, major int) *Server {
	t.Helper()
	return start(t, major, nil)
}

// StartTLS come Start, con TLS attivo: certificato per 127.0.0.1 e
// localhost firmato da una CA generata al volo. Ritorna anche la CA in PEM.
func StartTLS(t testing.TB, major int) (*Server, []byte) {
	t.Helper()
	caPEM, certPEM, keyPEM := makeCerts(t)
	return start(t, major, []string{"GS_TEST_CERT=" + string(certPEM), "GS_TEST_KEY=" + string(keyPEM)}), caPEM
}

func makeCerts(t testing.TB) (caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gitstack-test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	pemOf := func(typ string, der []byte) []byte { return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}) }
	return pemOf("CERTIFICATE", caDER), pemOf("CERTIFICATE", srvDER), pemOf("EC PRIVATE KEY", keyDER)
}

func start(t testing.TB, major int, tlsEnv []string) *Server {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		skipOrFail(t, "docker non trovato nel PATH")
	}
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) == "" {
		skipOrFail(t, "il demone docker non risponde")
	}
	image := fmt.Sprintf("postgres:%d-alpine", major)
	args := []string{"run", "-d", "--rm", "-p", "127.0.0.1::5432", "-e", "POSTGRES_PASSWORD=" + SuperPassword}
	if tlsEnv != nil {
		for _, e := range tlsEnv {
			args = append(args, "-e", e)
		}
		// Il certificato arriva da variabili d'ambiente: lo script lo scrive
		// con i permessi che Postgres richiede e avvia il server con TLS.
		args = append(args, "--entrypoint", "sh", image, "-c",
			`printf '%s
' "$GS_TEST_CERT" > /tmp/server.crt && printf '%s
' "$GS_TEST_KEY" > /tmp/server.key && chown postgres /tmp/server.crt /tmp/server.key && chmod 600 /tmp/server.key && exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key`)
	} else {
		args = append(args, image)
	}
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s: %v: %s", image, err, out)
	}
	s := &Server{Container: strings.TrimSpace(string(out)), Major: major}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", s.Container).Run() })

	pout, err := exec.Command("docker", "port", s.Container, "5432/tcp").CombinedOutput()
	if err != nil {
		t.Fatalf("docker port: %v: %s", err, pout)
	}
	line := strings.Split(strings.TrimSpace(string(pout)), "\n")[0]
	s.Port, err = strconv.Atoi(line[strings.LastIndex(line, ":")+1:])
	if err != nil {
		t.Fatalf("porta non leggibile da %q", line)
	}
	// Il container avvia un server temporaneo solo su socket unix per
	// l'init, poi quello definitivo: la prova via TCP dentro il container
	// ripetuta due volte distingue i due.
	deadline := time.Now().Add(90 * time.Second)
	okCount := 0
	for time.Now().Before(deadline) {
		c := exec.Command("docker", "exec", "-e", "PGPASSWORD="+SuperPassword, s.Container,
			"psql", "-h", "127.0.0.1", "-U", "postgres", "-tA", "-c", "SELECT 1")
		if b, e := c.CombinedOutput(); e == nil && strings.TrimSpace(string(b)) == "1" {
			okCount++
			if okCount >= 2 {
				return s
			}
		} else {
			okCount = 0
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("postgres %d non pronto in 90 s", major)
	return nil
}

func skipOrFail(t testing.TB, why string) {
	t.Helper()
	if os.Getenv("GITSTACK_REQUIRE_DOCKER") == "1" {
		t.Fatalf("Docker richiesto (GITSTACK_REQUIRE_DOCKER=1): %s", why)
	}
	t.Skipf("test d'integrazione saltato: %s", why)
}

// URL è l'URL con cui l'host raggiunge il server (senza password).
func (s *Server) URL(user, db, sslmode string) string {
	return fmt.Sprintf("postgres://%s@127.0.0.1:%d/%s?sslmode=%s", user, s.Port, db, sslmode)
}

// Exec esegue args nel container, con env (PGUSER, PGPASSWORD...) e stdin;
// è la stessa forma di kubectl exec nel pod client.
func (s *Server) Exec(ctx context.Context, env []string, stdin io.Reader, stdout io.Writer, args ...string) error {
	full := []string{"exec", "-i"}
	for _, e := range env {
		full = append(full, "-e", e)
	}
	full = append(full, s.Container)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker exec %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ClientEnv è l'ambiente del "pod client": psql e pg_dump dentro il
// container parlano a 127.0.0.1 come l'utente indicato, con $POSTGRES_USER e
// $POSTGRES_DB come nel chart (templates/postgres/client-deployment.yaml).
func ClientEnv(user, password, db string) []string {
	return []string{
		"PGHOST=127.0.0.1", "PGUSER=" + user, "PGPASSWORD=" + password, "PGDATABASE=" + db,
		"POSTGRES_USER=" + user, "POSTGRES_DB=" + db,
	}
}

// SQL esegue uno script SQL (da stdin) come user e ritorna l'output.
func (s *Server) SQL(t testing.TB, user, password, db string, extraEnv []string, script string, vars ...string) string {
	t.Helper()
	var out bytes.Buffer
	args := []string{"psql", "-q", "-tA", "-v", "ON_ERROR_STOP=1"}
	for _, v := range vars {
		args = append(args, "-v", v)
	}
	args = append(args, "-f", "-")
	env := append(ClientEnv(user, password, db), extraEnv...)
	if err := s.Exec(context.Background(), env, strings.NewReader(script), &out, args...); err != nil {
		t.Fatalf("sql come %s: %v", user, err)
	}
	return strings.TrimSpace(out.String())
}

// SQLErr come SQL ma ritorna l'errore invece di far fallire il test.
func (s *Server) SQLErr(user, password, db, script string) (string, error) {
	var out bytes.Buffer
	env := ClientEnv(user, password, db)
	err := s.Exec(context.Background(), env, strings.NewReader(script), &out, "psql", "-q", "-tA", "-v", "ON_ERROR_STOP=1", "-f", "-")
	return strings.TrimSpace(out.String()), err
}

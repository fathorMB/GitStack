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

// Label è l'etichetta dei container avviati da pgtest: serve a ritrovarli e
// rimuoverli se il processo di test è stato ucciso prima dei t.Cleanup (vedi
// scripts/cleanup-test-containers.sh e .ps1).
const Label = "gitstack.test=pgtest"

// staleAfter è l'età oltre cui un container con Label è considerato orfano.
const staleAfter = time.Hour

func pidLabel() string { return fmt.Sprintf("gitstack.test.pid=%d", os.Getpid()) }

// removeStale rimuove i container con Label creati più di staleAfter fa
// (rimasti da processi uccisi). Gli errori sono ignorati: è solo pulizia.
func removeStale() {
	out, err := exec.Command("docker", "ps", "-a", "--filter", "label="+Label,
		"--format", "{{.ID}} {{.CreatedAt}}").Output()
	if err != nil {
		return
	}
	for _, id := range staleIDs(string(out), time.Now()) {
		_ = exec.Command("docker", "rm", "-f", id).Run()
	}
}

// staleIDs estrae dall'output "<id> <CreatedAt>" di docker ps gli id più
// vecchi di staleAfter. CreatedAt ha la forma "2006-01-02 15:04:05 -0700 MST".
func staleIDs(out string, now time.Time) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		id, rest, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 3 {
			continue
		}
		created, err := time.Parse("2006-01-02 15:04:05 -0700", strings.Join(fields[:3], " "))
		if err != nil {
			continue
		}
		if now.Sub(created) > staleAfter {
			ids = append(ids, id)
		}
	}
	return ids
}

// SuperPassword è la password dell'utente postgres del container.
const SuperPassword = "super-secret-test"

// Server è un Postgres in un container.
type Server struct {
	Container string
	Port      int
	Major     int
	// Client, se non vuoto, è il container da cui girano psql e pg_dump:
	// vedi WithClient.
	Client string
}

// WithClient avvia un container client postgres:<major>-alpine nello stesso
// spazio di rete del server: è la forma del pod client del chart
// (templates/postgres/client-deployment.yaml, stessa versione principale
// del server). Da qui Exec esegue i comandi nel client.
func (s *Server) WithClient(t testing.TB, major int) {
	t.Helper()
	image := fmt.Sprintf("postgres:%d-alpine", major)
	out, err := exec.Command("docker", "run", "-d", "--rm", "--label", Label, "--label", pidLabel(), "--network", "container:"+s.Container,
		"--entrypoint", "sleep", image, "3600").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run client %s: %v: %s", image, err, out)
	}
	s.Client = containerID(out)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", s.Client).Run() })
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
	removeStale()
	args := []string{"run", "-d", "--rm", "--label", Label, "--label", pidLabel(), "-p", "127.0.0.1::5432", "-e", "POSTGRES_PASSWORD=" + SuperPassword}
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
	s := &Server{Container: containerID(out), Major: major}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", s.Container).Run() })

	s.Port = publishedPort(t, s.Container)
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

// containerID estrae l'id del container dall'output di `docker run -d`. Se
// l'immagine non è in locale, docker scrive prima le righe del pull
// ("Unable to find image ... locally", "Pulling from library/postgres"...)
// e l'id è solo l'ultima riga: sul runner di CI questo faceva diventare il
// "nome" del container un testo con barre, e `docker port` rispondeva 404
// «page not found».
func containerID(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// publishedPort legge la porta host pubblicata per 5432/tcp con docker
// inspect, riprovando finché non c'è; se non arriva, stampa docker version e
// docker logs del container.
func publishedPort(t testing.TB, container string) int {
	t.Helper()
	const format = `{{with index .NetworkSettings.Ports "5432/tcp"}}{{(index . 0).HostPort}}{{end}}`
	var last string
	for i := 0; i < 20; i++ {
		out, err := exec.Command("docker", "inspect", "--format", format, container).CombinedOutput()
		last = strings.TrimSpace(string(out))
		if err == nil {
			if p, perr := strconv.Atoi(last); perr == nil && p > 0 {
				return p
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	ver, _ := exec.Command("docker", "version").CombinedOutput()
	logs, _ := exec.Command("docker", "logs", "--tail", "50", container).CombinedOutput()
	t.Fatalf("porta 5432/tcp non pubblicata per il container %q (ultimo output %q)\ndocker version:\n%s\ndocker logs:\n%s",
		container, last, ver, logs)
	return 0
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
	target := s.Container
	if s.Client != "" {
		target = s.Client
	}
	full = append(full, target)
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

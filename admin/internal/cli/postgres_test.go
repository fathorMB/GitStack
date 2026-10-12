package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dbRunner risponde con l'elenco dei workload a `get` e con la versione a `exec`.
type dbRunner struct{ version string }

func (d dbRunner) Run(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
	for _, a := range args {
		if a == "exec" {
			return []byte(d.version + "\n"), nil
		}
	}
	return []byte(kubectlJSON(1)), nil
}

func writeExternalConfig(t *testing.T, host, version string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "version: 1\nhost: " + host + "\nrelease: gitstack\npostgres:\n  external: true\n  host: db.example.com\n" +
		"  port: 5432\n  database: gitstack\n  user: gitstack\n  ssl_mode: require\n  version: \"" + version + "\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStatusMostraIlDatabaseEsterno(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	a, out, errb := newApp(nil, srv)
	a.Runner = dbRunner{version: "16.4"}
	cfg := writeExternalConfig(t, hostOf(srv), "15.3")
	if code := a.Run(context.Background(), []string{"status", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errb)
	}
	for _, want := range []string{"Database", "Postgres ESTERNO", "db.example.com:5432/gitstack", "utente gitstack", "sslmode require", "Versione PostgreSQL          16.4"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("manca %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), "rilevata all'installazione") {
		t.Errorf("la versione doveva essere letta dal cluster:\n%s", out)
	}
}

func TestStatusEsternoNonRaggiungibileNonESano(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	a, out, _ := newApp(nil, srv)
	a.Runner = dbRunner{version: ""}
	cfg := writeExternalConfig(t, hostOf(srv), "15.3")
	if code := a.Run(context.Background(), []string{"status", "--config", cfg}); code != ExitUnhealthy {
		t.Fatalf("exit %d, atteso %d\n%s", code, ExitUnhealthy, out)
	}
	for _, want := range []string{"ATTENZIONE", "15.3 (rilevata all'installazione)", "Stato: NON sano"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("manca %q in:\n%s", want, out)
		}
	}
}

func TestStatusBundleDiceInterno(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	a, out, _ := newApp(nil, srv)
	a.Runner = dbRunner{version: "16.2"}
	cfg := writeConfig(t, hostOf(srv), t.TempDir())
	if code := a.Run(context.Background(), []string{"status", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out.String(), "Postgres interno (bundle)") || !strings.Contains(out.String(), "16.2") {
		t.Errorf("output:\n%s", out)
	}
}

func TestPostgresCheckUsoESenzaUrl(t *testing.T) {
	a, _, errb := newApp(nil, nil)
	if code := a.Run(context.Background(), []string{"postgres"}); code != ExitUsage {
		t.Errorf("senza sottocomando: exit %d", code)
	}
	errb.Reset()
	if code := a.Run(context.Background(), []string{"postgres", "check"}); code != ExitPostgres {
		t.Errorf("senza --url: exit %d", code)
	}
	if !strings.Contains(errb.String(), "manca l'URL del database") {
		t.Errorf("stderr: %s", errb)
	}
}

func TestPostgresCheckNonStampaLaPassword(t *testing.T) {
	a, out, errb := newApp(nil, nil)
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte("segretissima.x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Porta chiusa: la connessione fallisce, e nessun messaggio contiene la password.
	code := a.Run(context.Background(), []string{"postgres", "check", "--url", "postgres://gs@127.0.0.1:1/gitstack?sslmode=disable", "--password-file", pw})
	if code != ExitPostgres {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out.String()+errb.String(), "segretissima") {
		t.Errorf("la password è nell'output: %s%s", out, errb)
	}
	if !strings.Contains(errb.String(), "Postgres esterno:") {
		t.Errorf("stderr: %s", errb)
	}
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backupstate"
)

type fakeRunner struct {
	out  string
	err  error
	args []string
	env  []string
}

func (f *fakeRunner) Run(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	f.args = append([]string{name}, args...)
	f.env = env
	return []byte(f.out), f.err
}

func kubectlJSON(readyGateway int) string {
	return `{"items":[
 {"kind":"Deployment","metadata":{"name":"gitstack-gateway"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"ghcr.io/x/gateway:sha-abc123"}]}}},"status":{"readyReplicas":` + string(rune('0'+readyGateway)) + `}},
 {"kind":"StatefulSet","metadata":{"name":"gitstack-postgres"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"postgres:16"}]}}},"status":{"readyReplicas":1}}
]}`
}

func writeConfig(t *testing.T, host, backupDir string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "version: 1\nhost: " + host + "\nbackup:\n  destination: " + filepath.ToSlash(backupDir) + "\n"
	if runtime.GOOS == "windows" {
		body = "version: 1\nhost: " + host + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newApp(r *fakeRunner, srv *httptest.Server) (*App, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	a := &App{
		Version:  "v-test",
		Stdout:   &out,
		Stderr:   &errb,
		Getenv:   func(string) string { return "" },
		Geteuid:  func() int { return 0 },
		Runner:   r,
		LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil },
	}
	if srv != nil {
		a.HTTP = srv.Client()
	}
	return a, &out, &errb
}

func healthServer(code int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(code)
	}))
}

func hostOf(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

func TestStatusHealthy(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	r := &fakeRunner{out: kubectlJSON(1)}
	a, out, errb := newApp(r, srv)
	cfg := writeConfig(t, hostOf(srv), t.TempDir())

	code := a.Run(context.Background(), []string{"status", "--config", cfg})
	if code != ExitOK {
		t.Fatalf("exit %d, stdout:\n%s\nstderr:\n%s", code, out, errb)
	}
	for _, want := range []string{"Versione server: sha-abc123", "Versione gitstack: v-test", "Host: " + hostOf(srv), "gitstack-gateway", "1/1 pronti", "Ultimo backup: nessuno", "Stato: sano"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("manca %q in:\n%s", want, out)
		}
	}
	if len(r.env) != 1 || !strings.HasPrefix(r.env[0], "KUBECONFIG=") {
		t.Errorf("KUBECONFIG non passato: %v", r.env)
	}
	if !strings.Contains(strings.Join(r.args, " "), "-n default") {
		t.Errorf("argomenti kubectl inattesi: %v", r.args)
	}
}

func TestStatusUnhealthyService(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	a, out, _ := newApp(&fakeRunner{out: kubectlJSON(0)}, srv)
	code := a.Run(context.Background(), []string{"status", "--config", writeConfig(t, hostOf(srv), t.TempDir())})
	if code != ExitUnhealthy {
		t.Fatalf("exit %d, atteso %d", code, ExitUnhealthy)
	}
	if !strings.Contains(out.String(), "NON PRONTO") || !strings.Contains(out.String(), "Stato: NON sano") {
		t.Errorf("output inatteso:\n%s", out)
	}
}

func TestStatusAPIDown(t *testing.T) {
	srv := healthServer(503)
	defer srv.Close()
	a, out, _ := newApp(&fakeRunner{out: kubectlJSON(1)}, srv)
	code := a.Run(context.Background(), []string{"status", "--config", writeConfig(t, hostOf(srv), t.TempDir())})
	if code != ExitUnhealthy || !strings.Contains(out.String(), "NON RAGGIUNGIBILE") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestStatusClusterError(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	a, out, _ := newApp(&fakeRunner{err: errors.New("connection refused")}, srv)
	code := a.Run(context.Background(), []string{"status", "--config", writeConfig(t, hostOf(srv), t.TempDir())})
	if code != ExitCluster {
		t.Fatalf("exit %d, atteso %d", code, ExitCluster)
	}
	if !strings.Contains(out.String(), "cluster non interrogabile") {
		t.Errorf("output inatteso:\n%s", out)
	}
}

func TestStatusNoKubectl(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	a, _, _ := newApp(&fakeRunner{}, srv)
	a.LookPath = func(string) (string, error) { return "", errors.New("assente") }
	if code := a.Run(context.Background(), []string{"status", "--config", writeConfig(t, hostOf(srv), t.TempDir())}); code != ExitCluster {
		t.Fatalf("exit %d, atteso %d", code, ExitCluster)
	}
}

func TestStatusLastBackup(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()

	// Caso 1: backup riuscito con --config
	t.Run("success", func(t *testing.T) {
		configDir := t.TempDir()
		cfgPath := writeConfig(t, hostOf(srv), configDir)

		// Scrivi lo stato di un backup riuscito
		state := "backupstate.State{Success: true, Path: \"/var/backups/gitstack/backup-20261006T120000Z.tar.gz\", At: time.Now()}"
		if err := backupstate.Write(configDir, backupstate.State{
			Success: true,
			Path:    "/var/backups/gitstack/backup-20261006T120000Z.tar.gz",
			At:      time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}

		a, out, _ := newApp(&fakeRunner{out: kubectlJSON(1)}, srv)
		code := a.Run(context.Background(), []string{"status", "--config", cfgPath})
		if code != ExitOK {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(out.String(), "ultimo successo:") || !strings.Contains(out.String(), "/var/backups/gitstack/") {
			t.Errorf("output inatteso:\n%s", out)
		}
	})

	// Caso 2: backup fallito (ultimo errore)
	t.Run("failure", func(t *testing.T) {
		configDir := t.TempDir()
		cfgPath := writeConfig(t, hostOf(srv), configDir)

		// Scrivi lo stato di un backup fallito
		if err := backupstate.Write(configDir, backupstate.State{
			Success: false,
			Error:   "cluster non raggiungibile",
			At:      time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}

		a, out, _ := newApp(&fakeRunner{out: kubectlJSON(1)}, srv)
		code := a.Run(context.Background(), []string{"status", "--config", cfgPath})
		if code != ExitOK {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(out.String(), "ultimo errore:") || !strings.Contains(out.String(), "cluster non raggiungibile") {
			t.Errorf("output inatteso:\n%s", out)
		}
	})

	// Caso 3: SENZA --config, usa variabile d'ambiente
	t.Run("env-config", func(t *testing.T) {
		configDir := t.TempDir()
		cfgPath := writeConfig(t, hostOf(srv), configDir)

		// Scrivi lo stato di un backup riuscito
		if err := backupstate.Write(configDir, backupstate.State{
			Success: true,
			Path:    "/var/backups/gitstack/backup-env-20261006T120000Z.tar.gz",
			At:      time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}

		a, out, _ := newApp(&fakeRunner{out: kubectlJSON(1)}, srv)
		a.Getenv = func(k string) string {
			if k == envConfigPath {
				return cfgPath
			}
			return ""
		}
		code := a.Run(context.Background(), []string{"status"})
		if code != ExitOK {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(out.String(), "ultimo successo:") || !strings.Contains(out.String(), "backup-env-") {
			t.Errorf("output inatteso:\n%s", out)
		}
	})
}

func TestStatusJSON(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	a, out, _ := newApp(&fakeRunner{out: kubectlJSON(1)}, srv)
	code := a.Run(context.Background(), []string{"status", "--json", "--config", writeConfig(t, hostOf(srv), t.TempDir())})
	if code != ExitOK || !strings.Contains(out.String(), `"server_version": "sha-abc123"`) {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestStatusConfigMissing(t *testing.T) {
	a, _, errb := newApp(&fakeRunner{}, nil)
	code := a.Run(context.Background(), []string{"status", "--config", filepath.Join(t.TempDir(), "x.yaml")})
	if code != ExitConfig || !strings.Contains(errb.String(), "ERRORE:") {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

func TestConfigFromEnv(t *testing.T) {
	a, _, errb := newApp(&fakeRunner{}, nil)
	missing := filepath.Join(t.TempDir(), "y.yaml")
	a.Getenv = func(k string) string {
		if k == "GITSTACK_CONFIG" {
			return missing
		}
		return ""
	}
	code := a.Run(context.Background(), []string{"status"})
	if code != ExitConfig || !strings.Contains(errb.String(), missing) {
		t.Fatalf("exit %d: %s", code, errb)
	}
}

func TestUsageErrors(t *testing.T) {
	a, _, errb := newApp(&fakeRunner{}, nil)
	for _, args := range [][]string{nil, {"boh"}, {"status", "--nope"}, {"status", "extra"}} {
		if code := a.Run(context.Background(), args); code != ExitUsage {
			t.Errorf("%v: exit %d, atteso %d", args, code, ExitUsage)
		}
	}
	if !strings.Contains(errb.String(), "comando sconosciuto: boh") {
		t.Errorf("messaggio: %s", errb)
	}
}

func TestHelpAndVersion(t *testing.T) {
	a, out, _ := newApp(&fakeRunner{}, nil)
	if code := a.Run(context.Background(), []string{"--help"}); code != ExitOK || !strings.Contains(out.String(), "status") {
		t.Fatalf("help: exit %d\n%s", code, out)
	}
	out.Reset()
	if code := a.Run(context.Background(), []string{"version"}); code != ExitOK || out.String() != "gitstack v-test\n" {
		t.Fatalf("version: exit %d %q", code, out)
	}
}

func TestNeedsRoot(t *testing.T) {
	ran := false
	cmds = append(cmds, command{name: "mutate", needsRoot: true, run: func(context.Context, *App, []string) int { ran = true; return 0 }})
	defer func() { cmds = cmds[:len(cmds)-1] }()

	a, _, errb := newApp(&fakeRunner{}, nil)
	a.Geteuid = func() int { return 1000 }
	if code := a.Run(context.Background(), []string{"mutate"}); code != ExitNeedsRoot || ran {
		t.Fatalf("exit %d, ran=%v", code, ran)
	}
	if !strings.Contains(errb.String(), "sudo gitstack mutate") {
		t.Errorf("messaggio: %s", errb)
	}
	a.Geteuid = func() int { return 0 }
	if code := a.Run(context.Background(), []string{"mutate"}); code != 0 || !ran {
		t.Fatalf("exit %d, ran=%v", code, ran)
	}
}

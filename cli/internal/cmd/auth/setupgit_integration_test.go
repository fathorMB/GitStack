//go:build integration

package auth_test

import (
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Prova end-to-end di `gs auth setup-git`: il binario gs vero è il credential
// helper del git vero, che clona in HTTPS da un server (git http-backend) che
// pretende il token in Basic auth, come il servizio git di GitStack. Si lancia
// con: go test -tags=integration ./internal/cmd/auth (serve git nel PATH).

const itToken = "gst_integrazione_0123456789"

func gitEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		// Le variabili GIT_* della sessione (hook, indici) non devono arrivare ai git di prova.
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "GS_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func runIn(t *testing.T, dir string, env []string, stdin string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustRun(t *testing.T, dir string, env []string, stdin, name string, args ...string) string {
	t.Helper()
	out, err := runIn(t, dir, env, stdin, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// gitServer serve repo.git con git http-backend dietro Basic auth (password =
// itToken) e risponde a GET /api/v1/auth/session come il gateway.
func gitServer(t *testing.T, root string) *httptest.Server {
	t.Helper()
	execPath := strings.TrimSpace(mustRun(t, "", gitEnv(), "", "git", "--exec-path"))
	backend := filepath.Join(execPath, "git-http-backend")
	if runtime.GOOS == "windows" {
		backend += ".exe"
	}
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("git-http-backend non trovato: %v", err)
	}
	cgiH := &cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pw, ok := r.BasicAuth()
		bearer := r.Header.Get("Authorization") == "Bearer "+itToken
		if r.URL.Path == "/api/v1/auth/session" {
			if !bearer {
				w.WriteHeader(401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"authMethod":"token","mustChangePassword":false,"scopes":["read:resource"],"user":{"username":"alice","displayName":"A","kind":"user"}}`)
			return
		}
		if !ok || pw != itToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitStack"`)
			w.WriteHeader(401)
			return
		}
		cgiH.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSetupGitCloneHTTPSConCredenzialiDiGs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non è nel PATH")
	}
	tmp := t.TempDir()
	gs := filepath.Join(tmp, "gs")
	if runtime.GOOS == "windows" {
		gs += ".exe"
	}
	mustRun(t, "", gitEnv(), "", "go", "build", "-o", gs, "github.com/fathorMB/GitStack/cli/cmd/gs")

	// Repo bare con un commit.
	seed := filepath.Join(tmp, "seed")
	env0 := gitEnv("GIT_CONFIG_GLOBAL="+filepath.Join(tmp, "seed.gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
	mustRun(t, "", env0, "", "git", "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("ciao\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, seed, env0, "", "git", "add", ".")
	mustRun(t, seed, env0, "", "git", "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+os.DevNull, "commit", "-q", "-m", "init")
	root := filepath.Join(tmp, "srv")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "", env0, "", "git", "clone", "-q", "--bare", seed, filepath.Join(root, "repo.git"))

	srv := gitServer(t, root)
	cloneURL := srv.URL + "/repo.git"

	newEnv := func(name string, extra ...string) []string {
		return gitEnv(append([]string{
			"GIT_CONFIG_GLOBAL=" + filepath.Join(tmp, name+".gitconfig"),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0",
			"GCM_INTERACTIVE=never",
			"GS_CONFIG_DIR=" + filepath.Join(tmp, name+"-gs"),
			"GS_NO_KEYRING=1", // il portachiavi vero dell'host non si tocca
		}, extra...)...)
	}

	t.Run("dopo_login", func(t *testing.T) {
		env := newEnv("login")
		// Senza credenziali il clone fallisce, senza chiedere niente.
		if out, err := runIn(t, tmp, env, "", "git", "clone", "-q", cloneURL, filepath.Join(tmp, "no")); err == nil {
			t.Fatalf("clone anonimo riuscito:\n%s", out)
		}
		out := mustRun(t, "", env, itToken+"\n", gs, "auth", "login", "--hostname", srv.URL, "--with-token")
		if strings.Contains(out, itToken) {
			t.Fatal("login ha stampato il token")
		}
		mustRun(t, "", env, "", gs, "auth", "setup-git")
		cfg := mustRun(t, "", env, "", "git", "config", "--global", "--get-all", "credential."+srv.URL+".helper")
		if !strings.Contains(cfg, "auth git-credential") {
			t.Fatalf("helper non configurato: %q", cfg)
		}
		dest := filepath.Join(tmp, "clone-login")
		if out, err := runIn(t, tmp, env, "", "git", "clone", "-q", cloneURL, dest); err != nil {
			t.Fatalf("clone con le credenziali di gs: %v\n%s", err, out)
		} else if strings.Contains(out, itToken) {
			t.Error("il token è comparso nell'output di git")
		}
		if b, err := os.ReadFile(filepath.Join(dest, "README.md")); err != nil || string(b) != "ciao\n" {
			t.Errorf("README clonato: %q %v", b, err)
		}
		// Dopo il logout il token non c'è più e il clone fallisce.
		mustRun(t, "", env, "", gs, "auth", "logout", "--hostname", srv.URL)
		if out, err := runIn(t, tmp, env, "", "git", "clone", "-q", cloneURL, filepath.Join(tmp, "dopo-logout")); err == nil {
			t.Fatalf("clone riuscito dopo il logout:\n%s", out)
		}
	})

	t.Run("GS_TOKEN_senza_configurazione", func(t *testing.T) {
		env := newEnv("env", "GS_HOST="+srv.URL, "GS_TOKEN="+itToken)
		mustRun(t, "", env, "", gs, "auth", "setup-git")
		dest := filepath.Join(tmp, "clone-env")
		if out, err := runIn(t, tmp, env, "", "git", "clone", "-q", cloneURL, dest); err != nil {
			t.Fatalf("clone con GS_TOKEN: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(tmp, "env-gs", "hosts")); err == nil {
			t.Error("creata configurazione degli host")
		}
		// Senza GS_TOKEN lo stesso helper non risponde: il clone fallisce.
		noTok := newEnv("env")
		if out, err := runIn(t, tmp, noTok, "", "git", "clone", "-q", cloneURL, filepath.Join(tmp, "senza-token")); err == nil {
			t.Fatalf("clone senza token riuscito:\n%s", out)
		}
	})
}

//go:build integration

package stackitest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/services/core/internal/issuelinks"
)

// M-07/L (GIT-173): il ciclo completo di un agente con il binario gs vero sullo
// stack completo (gateway, identity e git veri, core in-process, Postgres):
// login con token, repo create, clone, issue create, commit con `fixes #n` e
// push, issue chiusa, notifica letta con gs, `gs api` su un endpoint admin.
// Le regole G1–G8 si leggono in docs/rules-coverage.md.

// gsSession è un «terminale» di una persona: una sua cartella di configurazione
// di gs (dopo `gs auth login` il token sta lì, non in GS_TOKEN) e un gitconfig
// il cui credential helper risponde col suo token (clone e push HTTPS).
type gsSession struct {
	g      *gsEnv
	user   string
	token  string
	cfgDir string
	home   string
}

func (g *gsEnv) session2(user, token string) *gsSession {
	home := g.t.TempDir()
	helper := fmt.Sprintf("[credential]\n\thelper = \"!f() { echo username=%s; echo password=%s; }; f\"\n", user, token)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[init]\n\tdefaultBranch = main\n[core]\n\tautocrlf = false\n"+helper), 0o600); err != nil {
		g.t.Fatal(err)
	}
	return &gsSession{g: g, user: user, token: token, cfgDir: g.t.TempDir(), home: home}
}

// run lancia gs con la configurazione della sessione, senza GS_TOKEN.
func (s *gsSession) run(stdin string, args ...string) gsRun {
	s.g.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.g.bin, append([]string{"--hostname", s.g.proxy}, args...)...)
	cmd.Dir = s.g.t.TempDir()
	gitconfig := filepath.Join(s.home, ".gitconfig")
	cmd.Env = append(cleanEnv(), "GS_CONFIG_DIR="+s.cfgDir, "GS_NO_KEYRING=1", "HOME="+s.home, "USERPROFILE="+s.home,
		"GIT_CONFIG_GLOBAL="+gitconfig, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=")
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			s.g.t.Fatalf("gs %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return gsRun{out.String(), errOut.String(), code}
}

func (s *gsSession) ok(stdin string, args ...string) gsRun {
	s.g.t.Helper()
	r := s.run(stdin, args...)
	if r.code != 0 {
		s.g.t.Fatalf("gs %v come %s: exit %d: %s", args, s.user, r.code, r.errOut)
	}
	return r
}

func TestGsE2E(t *testing.T) {
	g := newGsEnv(t)
	alice := g.session2("alice", g.tokens["alice"])
	bob := g.session2("bob", g.tokens["bob"])

	// G2: login con token da stdin; il token non compare nell'output e resta nella config.
	t.Run("auth_login_e_status", func(t *testing.T) {
		if r := alice.run("gst_inventato0000000000000000000000000000\n", "auth", "login", "--with-token"); r.code == 0 {
			t.Fatalf("login con token inventato riuscito: %+v", r)
		}
		for _, s := range []*gsSession{alice, bob} {
			r := s.ok(s.token+"\n", "auth", "login", "--with-token")
			if strings.Contains(r.out+r.errOut, s.token) {
				t.Fatalf("login ha stampato il token di %s", s.user)
			}
			st := s.ok("", "auth", "status")
			if !strings.Contains(st.out+st.errOut, s.user) || strings.Contains(st.out+st.errOut, s.token) {
				t.Errorf("auth status di %s: %+v", s.user, st)
			}
		}
	})

	var repoID string
	t.Run("repo_create", func(t *testing.T) {
		r := alice.ok("", "repo", "create", "ciclo-gs", "--json", "id,fullName,visibility")
		m := jsonMap(t, r.out)
		if m["fullName"] != "alice/ciclo-gs" || m["visibility"] != "private" {
			t.Fatalf("repo creato: %v", m)
		}
		repoID, _ = m["id"].(string)
		// bob scrive nel repo e potrà essere assegnatario.
		g.must("alice", "POST", "/resources/"+repoID+"/grants", map[string]any{"subjectType": "user", "subjectId": g.userID["bob"], "role": "write"}, 201)
	})

	work := filepath.Join(t.TempDir(), "w")
	t.Run("clone", func(t *testing.T) {
		alice.ok("", "repo", "clone", "alice/ciclo-gs", work)
		if _, err := os.Stat(filepath.Join(work, ".git")); err != nil {
			t.Fatalf("clone: %v", err)
		}
		if u := strings.TrimSpace(g.mustGit(work, "", "remote", "get-url", "origin")); u != g.gitHTTP+"/alice/ciclo-gs.git" {
			t.Errorf("origin = %s", u)
		}
	})

	var n int
	t.Run("issue_create", func(t *testing.T) {
		r := alice.ok("", "issue", "create", "-R", "alice/ciclo-gs", "-t", "Manca il README", "-b", "da aggiungere", "-a", "bob", "--json", "number")
		ns := gsNumbers(t, "["+strings.TrimSpace(r.out)+"]")
		if len(ns) != 1 || ns[0] != 1 {
			t.Fatalf("issue creata: %s", r.out)
		}
		n = ns[0]
	})

	t.Run("commit_fixes_e_push", func(t *testing.T) {
		g.commitFile(work, "README.md", "# ciclo gs\n", fmt.Sprintf("Aggiunge il README\n\nfixes #%d", n))
		g.mustGit(work, "", "remote", "set-url", "origin", g.httpsURL("alice", g.tokens["alice"], "/alice/ciclo-gs.git"))
		g.mustGit(work, "", "push", "-q", "origin", "main")
		// core consuma git.push (qui lo fa il linker, come nel processo vero).
		id, ok := g.notifier.Identity.(issuelinks.Identity)
		if !ok {
			t.Fatal("l'identity dello stack non ha HasRole")
		}
		linker := &issuelinks.Linker{Pool: g.notifier.Pool, Identity: id, Notifier: g.notifier}
		for _, p := range g.pushEvents("alice/ciclo-gs", 1) {
			if err := linker.HandlePush(context.Background(), pkgevents.Envelope{}, p); err != nil {
				t.Fatalf("HandlePush: %v", err)
			}
		}
	})

	t.Run("issue_chiusa", func(t *testing.T) {
		r := alice.ok("", "issue", "view", fmt.Sprint(n), "-R", "alice/ciclo-gs", "--json", "state")
		if m := jsonMap(t, r.out); m["state"] != "closed" {
			t.Fatalf("issue dopo il push con fixes: %v", m)
		}
	})

	t.Run("notifica_letta_con_gs", func(t *testing.T) {
		g.process()
		if all := g.notesVia(bob); len(all) == 0 {
			t.Fatal("bob senza notifiche non lette")
		}
		bob.ok("", "notification", "read", "--all")
		if rest := g.notesVia(bob); len(rest) != 0 {
			t.Errorf("restano non lette: %+v", rest)
		}
	})

	// G4: `gs api` su un endpoint di amministrazione; chi non è admin è rifiutato.
	t.Run("gs_api_admin", func(t *testing.T) {
		admin := g.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": newPassword}, nil, nil)
		want(t, admin, 200, "")
		exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
		at := g.gw("POST", "/user/tokens", map[string]any{"name": "admin-cli", "scopes": []string{"read:user", "write:user"}, "expiresAt": exp}, nil, g.session(admin))
		want(t, at, 201, "")
		adminTok, _ := at.json()["token"].(string)
		sa := g.session2("admin", adminTok)
		sa.ok(adminTok+"\n", "auth", "login", "--with-token")
		r := sa.ok("", "api", "-X", "POST", "/users/botty/tokens", "-f", "name=gs-e2e",
			"-F", "scopes[]=read:resource", "-f", "expiresAt="+exp, "--jq", ".token")
		if !strings.HasPrefix(strings.TrimSpace(r.out), "gst_") {
			t.Fatalf("token dell'agente: %+v", r)
		}
		if r := alice.run("", "api", "-X", "POST", "/users/botty/tokens", "-f", "name=no", "-F", "scopes[]=read:resource", "-f", "expiresAt="+exp); r.code == 0 {
			t.Errorf("alice ha creato un token per un altro utente: %+v", r)
		}
	})
}

// notesVia legge le notifiche non lette della sessione con gs.
func (g *gsEnv) notesVia(s *gsSession) []gsNote {
	g.t.Helper()
	r := s.ok("", "notification", "list", "--json", "id,reason,read,url,repository,issue")
	var ns []gsNote
	if err := json.Unmarshal([]byte(r.out), &ns); err != nil {
		g.t.Fatalf("JSON non valido: %v: %s", err, r.out)
	}
	return ns
}

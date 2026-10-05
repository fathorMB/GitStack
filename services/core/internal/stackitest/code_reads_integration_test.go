//go:build integration

package stackitest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
)

// scratch è il ramo del repo di lavoro: i repo veri del test si riempiono con
// un fetch dal repo di lavoro, senza push.
const scratch = "item/GIT-84"

type fullReply struct {
	status int
	header http.Header
	body   []byte
}

func rawGet(t *testing.T, url string, hdr map[string]string) fullReply {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return fullReply{status: resp.StatusCode, header: resp.Header, body: b}
}

func runGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func authorEnv(name, email string) []string {
	return []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email, "GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email}
}

// TestCodeReads prova le letture pubbliche del codice (GIT-84) sullo stack
// vero: gateway e identity e git sono i binari, core è il router in-process
// con i client veri. Per ogni lettura: proprietario 200, utente senza
// permesso su repo privato 404, senza credenziali 401, altro utente su repo
// interno 200; i 404 non portano dati del repo.
func TestCodeReads(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non installato")
	}
	pool, dsn := dbtest.NewPool(t)
	_ = pool
	identityBin := build(t, "../../../identity", "identity")
	gatewayBin := build(t, "../../../gateway", "gateway")
	gitBin := build(t, "../../../git", "git")

	identityAddr, gatewayAddr, gitAddr := freeAddr(t), freeAddr(t), freeAddr(t)
	dataDir := t.TempDir()
	coreSrv := httptest.NewServer(httpserver.NewRouter(pool, events.NoopPublisher{}, serviceSecret,
		httpserver.WithRepoIdentity(identityclient.New(mustURL(t, "http://"+identityAddr), serviceSecret, 5*time.Second)),
		httpserver.WithGit(gitclient.New(mustURL(t, "http://"+gitAddr), serviceSecret, 30*time.Second)),
	))
	t.Cleanup(coreSrv.Close)

	start(t, "identity", identityBin, identityAddr,
		"GITSTACK_IDENTITY_ADDR="+identityAddr,
		"GITSTACK_IDENTITY_DB_URL="+dsn,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
		"GITSTACK_IDENTITY_ADMIN_USERNAME=admin",
		"GITSTACK_IDENTITY_ADMIN_PASSWORD="+adminPassword,
	)
	start(t, "git", gitBin, gitAddr,
		"GITSTACK_GIT_ADDR="+gitAddr,
		"GITSTACK_GIT_DATA_DIR="+dataDir,
		"GITSTACK_GIT_SSH_ADDR=off",
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
	)
	start(t, "gateway", gatewayBin, gatewayAddr,
		"GITSTACK_GATEWAY_ADDR="+gatewayAddr,
		"GITSTACK_CORE_URL="+coreSrv.URL,
		"GITSTACK_IDENTITY_URL=http://"+identityAddr,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
		"GITSTACK_GATEWAY_AUTH_CACHE_TTL=1s",
		"GITSTACK_GATEWAY_AUTH_CACHE_NEGATIVE_TTL=1s",
	)
	s := &stack{t: t, gateway: "http://" + gatewayAddr, core: coreSrv.URL}

	// --- utenti e token ------------------------------------------------------
	login := s.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": adminPassword}, nil, nil)
	want(t, login, 200, "")
	var adminCookie *http.Cookie
	for _, c := range login.cookies {
		if c.Name == "gst_session" {
			adminCookie = c
		}
	}
	if adminCookie == nil {
		t.Fatalf("login admin: %s", login.body)
	}
	want(t, s.gw("PUT", "/users/admin/password", map[string]any{"currentPassword": adminPassword, "newPassword": newPassword}, nil, adminCookie), 204, "")

	const userPassword = "password-di-prova-molto-lunga-1"
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	mkUser := func(name string) {
		want(t, s.gw("POST", "/users", map[string]any{"username": name, "email": name + "@example.com", "password": userPassword}, nil, adminCookie), 201, "")
	}
	mkToken := func(name string, scopes ...string) map[string]string {
		l := s.gw("POST", "/auth/login", map[string]any{"username": name, "password": userPassword}, nil, nil)
		want(t, l, 200, "")
		var ck *http.Cookie
		for _, c := range l.cookies {
			if c.Name == "gst_session" {
				ck = c
			}
		}
		tok := s.gw("POST", "/user/tokens", map[string]any{"name": "t-" + strings.Join(scopes, "-"), "scopes": scopes, "expiresAt": exp}, nil, ck)
		want(t, tok, 201, "")
		token, _ := tok.json()["token"].(string)
		return map[string]string{"Authorization": "Bearer " + token}
	}
	mkUser("alice")
	mkUser("bob")
	// Un agente: stessa email usata come autore di un commit.
	want(t, s.gw("POST", "/users", map[string]any{"username": "botty", "kind": "agent", "email": "botty@agents.example.com"}, nil, adminCookie), 201, "")
	alice := mkToken("alice", "read:resource", "write:resource")
	bob := mkToken("bob", "read:resource")
	aliceNoRead := mkToken("alice", "read:user")

	// --- repo: uno privato e uno interno, di alice ------------------------------
	repoID := map[string]string{}
	for name, vis := range map[string]string{"segreto-privato": "private", "aperto-interno": "internal"} {
		r := s.gw("POST", "/repos", map[string]any{"owner": "alice", "name": name, "visibility": vis, "readme": true}, alice, nil)
		want(t, r, 201, "")
		id, _ := r.json()["id"].(string)
		repoID[name] = id
	}

	// --- contenuto: HTML, SVG con script, branch con '/', tag, autore agente ----
	work := t.TempDir()
	runGit(t, work, nil, "init", "-q", "-b", scratch)
	write := func(p, c string) {
		full := filepath.Join(work, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# Contenuto di prova\n")
	write("index.html", "<html><script>alert(1)</script></html>\n")
	write("logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`+"\n")
	write("dir/note.txt", "ciao\n")
	runGit(t, work, authorEnv("Alice", "alice@example.com"), "add", "-A")
	runGit(t, work, authorEnv("Alice", "alice@example.com"), "commit", "-q", "-m", "primo commit")
	firstSHA := runGit(t, work, nil, "rev-parse", "HEAD")
	write("dir/note.txt", "ciao dal bot\n")
	runGit(t, work, authorEnv("Botty", "Botty@Agents.Example.com"), "commit", "-q", "-a", "-m", "modifica del bot")
	botSHA := runGit(t, work, nil, "rev-parse", "HEAD")
	for _, id := range repoID {
		bare := filepath.Join(dataDir, "repos", id[:2], id+".git")
		runGit(t, bare, nil, "fetch", "-q", "--force", work, scratch+":refs/heads/main", scratch+":refs/heads/feat/x", firstSHA+":refs/tags/v1")
	}

	// --- le letture --------------------------------------------------------------
	type read struct {
		name string
		path string // sotto /repos/<owner>/<repo>
	}
	reads := []read{
		{"tree", "/tree"},
		{"contents", "/contents?path=index.html"},
		{"readme", "/readme"},
		{"raw", "/raw?path=index.html"},
		{"raw_per_indirizzo", "/raw/main/dir/note.txt"},
		{"raw_ref_con_slash", "/raw/feat/x/dir/note.txt"},
		{"branches", "/branches"},
		{"tags", "/tags"},
		{"commits", "/commits"},
		{"commit", "/commits/" + botSHA},
		{"blame", "/blame?path=dir/note.txt"},
		{"archive_zip", "/archive?ref=v1"},
		{"archive_targz", "/archive?ref=main&format=tar.gz"},
		{"diff", "/commits/" + botSHA + "/patch?format=diff"},
		{"patch", "/commits/" + botSHA + "/patch?format=patch"},
	}
	leak := []string{"segreto-privato", repoID["segreto-privato"], "index.html", "note.txt", "Contenuto di prova", "primo commit", "alice@example.com"}
	for _, rd := range reads {
		t.Run(rd.name, func(t *testing.T) {
			priv := "/v1/repos/alice/segreto-privato" + rd.path
			intl := "/v1/repos/alice/aperto-interno" + rd.path
			base := "http://" + gatewayAddr
			if r := rawGet(t, base+priv, alice); r.status != 200 {
				t.Fatalf("proprietario: %d %s", r.status, r.body)
			}
			if r := rawGet(t, base+priv, bob); r.status != 404 {
				t.Fatalf("senza permesso: %d, atteso 404", r.status)
			} else {
				for _, l := range leak {
					if bytes.Contains(r.body, []byte(l)) {
						t.Errorf("il 404 contiene %q: %s", l, r.body)
					}
				}
			}
			// Un repo inesistente dà lo stesso 404 (stesso corpo a parte il nome).
			missing := rawGet(t, base+strings.Replace(priv, "segreto-privato", "non-esiste", 1), bob)
			denied := rawGet(t, base+priv, bob)
			if missing.status != 404 || strings.ReplaceAll(string(denied.body), "segreto-privato", "") != strings.ReplaceAll(string(missing.body), "non-esiste", "") {
				t.Errorf("404 distinguibili: %d %s / %d %s", missing.status, missing.body, denied.status, denied.body)
			}
			if r := rawGet(t, base+priv, nil); r.status != 401 {
				t.Fatalf("senza credenziali: %d, atteso 401", r.status)
			} else {
				for _, l := range leak {
					if bytes.Contains(r.body, []byte(l)) {
						t.Errorf("il 401 contiene %q: %s", l, r.body)
					}
				}
			}
			if r := rawGet(t, base+priv, aliceNoRead); r.status != 403 {
				t.Errorf("token senza read:resource: %d, atteso 403", r.status)
			}
			if r := rawGet(t, base+intl, bob); r.status != 200 {
				t.Fatalf("repo interno da un altro utente: %d %s", r.status, r.body)
			}
		})
	}

	t.Run("raw_header_di_sicurezza", func(t *testing.T) {
		base := "http://" + gatewayAddr + "/v1/repos/alice/aperto-interno"
		for _, p := range []string{"/raw?path=index.html", "/raw/main/index.html", "/raw?path=logo.svg", "/raw/main/logo.svg"} {
			r := rawGet(t, base+p, bob)
			if r.status != 200 {
				t.Fatalf("%s: %d", p, r.status)
			}
			ct := strings.ToLower(r.header.Get("Content-Type"))
			if strings.Contains(ct, "html") || strings.Contains(ct, "svg") {
				t.Errorf("%s: Content-Type %q eseguibile", p, ct)
			}
			if r.header.Get("X-Content-Type-Options") != "nosniff" || r.header.Get("Content-Security-Policy") != "sandbox" {
				t.Errorf("%s: header di sicurezza mancanti: %v", p, r.header)
			}
			if strings.HasSuffix(p, ".svg") && !strings.HasPrefix(r.header.Get("Content-Disposition"), "attachment") {
				t.Errorf("%s: svg senza attachment", p)
			}
		}
		for _, p := range []string{"/archive?ref=main", "/archive?ref=main&format=tar.gz", "/commits/" + botSHA + "/patch"} {
			r := rawGet(t, base+p, bob)
			if r.header.Get("X-Content-Type-Options") != "nosniff" || r.header.Get("Content-Security-Policy") != "sandbox" ||
				!strings.HasPrefix(r.header.Get("Content-Disposition"), "attachment") {
				t.Errorf("%s: header: %v", p, r.header)
			}
		}
	})

	t.Run("autori_collegati_agli_utenti", func(t *testing.T) {
		r := rawGet(t, "http://"+gatewayAddr+"/v1/repos/alice/aperto-interno/commits", bob)
		var list struct {
			Items []struct {
				Subject string `json:"subject"`
				Author  struct {
					User *struct {
						Username string `json:"username"`
						Kind     string `json:"kind"`
					} `json:"user"`
				} `json:"author"`
			} `json:"items"`
		}
		if err := json.Unmarshal(r.body, &list); err != nil {
			t.Fatal(err, string(r.body))
		}
		got := map[string]string{}
		for _, c := range list.Items {
			if c.Author.User != nil {
				got[c.Subject] = c.Author.User.Username + "/" + c.Author.User.Kind
			}
		}
		if got["modifica del bot"] != "botty/agent" {
			t.Errorf("autore agente: %v", got)
		}
		if got["primo commit"] != "alice/human" {
			t.Errorf("autore umano: %v", got)
		}
	})

	t.Run("tag_con_indirizzi_di_download", func(t *testing.T) {
		r := rawGet(t, "http://"+gatewayAddr+"/v1/repos/alice/aperto-interno/tags", bob)
		if !strings.Contains(string(r.body), url.QueryEscape("v1")) || !strings.Contains(string(r.body), "zipUrl") {
			t.Errorf("tag: %s", r.body)
		}
	})
}

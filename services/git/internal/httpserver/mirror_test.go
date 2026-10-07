package httpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/mirrorpush"
)

// GIT-179: l'endpoint interno del mirror in push. Senza firma 401, input non
// valido 400, repo inesistente 404; e il token non esce né dalla risposta né dai log.

func mirrorEnv(t *testing.T, logs *bytes.Buffer) *env {
	t.Helper()
	e := setup(t)
	runner, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.h = NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Logger: log,
		Mirror: &mirrorpush.Service{Run: runner, Dir: e.store.Dir, Timeout: 20 * time.Second}})
	return e
}

func TestMirrorPush_AutorizzazioneEInput(t *testing.T) {
	var logs bytes.Buffer
	e := mirrorEnv(t, &logs)
	path := "/internal/git/repos/" + rid + "/mirror-push"
	ok := `{"url":"https://mirror.example.test/a.git","username":"u","token":"t","ip":"93.184.216.34","defaultBranch":"main"}`
	e.expect(e.do("POST", path, ok, false), 401)
	// Repo inesistente.
	e.expect(e.do("POST", path, ok, true), 404)
	e.expect(e.do("POST", "/internal/git/repos", `{"repoId":"`+rid+`","name":"app","readme":true,"author":{"name":"A","email":"a@example.com"}}`, true), 201)
	for name, body := range map[string]string{
		"corpo non JSON":   `non json`,
		"http":             `{"url":"http://mirror.example.test/a.git","username":"u","token":"t","ip":"93.184.216.34","defaultBranch":"main"}`,
		"userinfo":         `{"url":"https://u:p@mirror.example.test/a.git","username":"u","token":"t","ip":"93.184.216.34","defaultBranch":"main"}`,
		"senza ip":         `{"url":"https://mirror.example.test/a.git","username":"u","token":"t","defaultBranch":"main"}`,
		"senza token":      `{"url":"https://mirror.example.test/a.git","username":"u","ip":"93.184.216.34","defaultBranch":"main"}`,
		"branch con più":   `{"url":"https://mirror.example.test/a.git","username":"u","token":"t","ip":"93.184.216.34","defaultBranch":"+main"}`,
		"branch opzione":   `{"url":"https://mirror.example.test/a.git","username":"u","token":"t","ip":"93.184.216.34","defaultBranch":"--force"}`,
		"url come opzione": `{"url":"--receive-pack=x","username":"u","token":"t","ip":"93.184.216.34","defaultBranch":"main"}`,
	} {
		if rec := e.do("POST", path, body, true); rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestMirrorPush_IlTokenNonEsceMai(t *testing.T) {
	var logs bytes.Buffer
	e := mirrorEnv(t, &logs)
	e.expect(e.do("POST", "/internal/git/repos", `{"repoId":"`+rid+`","name":"app","readme":true,"author":{"name":"A","email":"a@example.com"}}`, true), 201)
	// Una porta locale chiusa: il push fallisce subito con «connection refused».
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	const token = "ghp_TOKEN_CHE_NON_DEVE_USCIRE_42"
	body, _ := json.Marshal(map[string]string{
		"url": "https://mirror.example.test:" + itoa(port) + "/a.git", "username": "bot", "token": token, "ip": "127.0.0.1", "defaultBranch": "main",
	})
	rec := e.do("POST", "/internal/git/repos/"+rid+"/mirror-push", string(body), true)
	e.expect(rec, 200)
	var res mirrorpush.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Diverged || res.Error == "" {
		t.Fatalf("atteso un errore di rete: %+v", res)
	}
	cred := base64.StdEncoding.EncodeToString([]byte("bot:" + token))
	for _, bad := range []string{token, cred} {
		if strings.Contains(rec.Body.String(), bad) || strings.Contains(logs.String(), bad) {
			t.Errorf("%q presente nella risposta o nei log:\n%s\n%s", bad, rec.Body, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "mirror push eseguito") {
		t.Errorf("manca il log dell'esecuzione: %s", logs.String())
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

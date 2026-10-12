package httpserver

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/mirrorpull"
)

// GIT-196: l'endpoint interno del mirror in pull. Senza firma 401, input non
// valido 400, repo inesistente 404, destinazione vietata 403; il token non esce
// né dalla risposta né dai log.

func pullEnv(t *testing.T, logs *bytes.Buffer) *env {
	t.Helper()
	e := setup(t)
	runner, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.h = NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Logger: log,
		Pull: &mirrorpull.Service{Run: runner, Dir: e.store.Dir, Timeout: 20 * time.Second}})
	return e
}

const pullPusher = `"pusher":{"id":"11111111-2222-4333-8444-555555555555","username":"mirror"}`

func TestMirrorPull_AutorizzazioneEInput(t *testing.T) {
	var logs bytes.Buffer
	e := pullEnv(t, &logs)
	path := "/internal/git/repos/" + rid + "/mirror-pull"
	ok := `{"url":"https://mirror.example.test/a.git","username":"u","token":"t","ip":"93.184.216.34","defaultBranch":"main","owner":"o","name":"n",` + pullPusher + `}`
	e.expect(e.do("POST", path, ok, false), 401)
	e.expect(e.do("POST", path, ok, true), 404)
	e.expect(e.do("POST", "/internal/git/repos", `{"repoId":"`+rid+`","name":"app","readme":true,"author":{"name":"A","email":"a@example.com"}}`, true), 201)
	for name, body := range map[string]string{
		"corpo non JSON": `non json`,
		"http":           `{"url":"http://mirror.example.test/a.git","ip":"93.184.216.34","owner":"o","name":"n",` + pullPusher + `}`,
		"userinfo":       `{"url":"https://u:p@mirror.example.test/a.git","ip":"93.184.216.34","owner":"o","name":"n",` + pullPusher + `}`,
		"senza ip":       `{"url":"https://mirror.example.test/a.git","owner":"o","name":"n",` + pullPusher + `}`,
		"solo token":     `{"url":"https://mirror.example.test/a.git","token":"t","ip":"93.184.216.34","owner":"o","name":"n",` + pullPusher + `}`,
		"senza pusher":   `{"url":"https://mirror.example.test/a.git","ip":"93.184.216.34","owner":"o","name":"n"}`,
		"url opzione":    `{"url":"--upload-pack=x","ip":"93.184.216.34","owner":"o","name":"n",` + pullPusher + `}`,
	} {
		if rec := e.do("POST", path, body, true); rec.Code != 400 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// Loopback: vietato prima di ogni connessione.
	loop := `{"url":"https://mirror.example.test/a.git","username":"u","token":"t","ip":"127.0.0.1","owner":"o","name":"n",` + pullPusher + `}`
	if rec := e.do("POST", path, loop, true); rec.Code != 403 {
		t.Errorf("loopback: %d %s", rec.Code, rec.Body)
	}
}

func TestMirrorPull_IlTokenNonEsceMai(t *testing.T) {
	var logs bytes.Buffer
	e := pullEnv(t, &logs)
	// La difesa di default vieta il loopback: per una porta chiusa in locale la
	// si allenta solo nel test.
	runner, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.h = NewRouter(Deps{Store: e.store, Content: fakeContent{}, Secret: secret, Logger: log,
		Pull: &mirrorpull.Service{Run: runner, Dir: e.store.Dir, Timeout: 20 * time.Second,
			CheckIP: func(netip.Addr) error { return nil }}})
	e.expect(e.do("POST", "/internal/git/repos", `{"repoId":"`+rid+`","name":"app","readme":true,"author":{"name":"A","email":"a@example.com"}}`, true), 201)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	const token = "ghp_TOKEN_PULL_CHE_NON_DEVE_USCIRE_42"
	body, _ := json.Marshal(map[string]any{
		"url": "https://mirror.example.test:" + itoa(port) + "/a.git", "username": "bot", "token": token, "ip": "127.0.0.1",
		"defaultBranch": "main", "owner": "o", "name": "n",
		"pusher": map[string]string{"id": "11111111-2222-4333-8444-555555555555", "username": "mirror"},
	})
	rec := e.do("POST", "/internal/git/repos/"+rid+"/mirror-pull", string(body), true)
	e.expect(rec, 200)
	var res mirrorpull.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Diverged || res.Error == "" {
		t.Fatalf("atteso un errore di rete: %+v", res)
	}
	for _, bad := range []string{token, "Authorization"} {
		if strings.Contains(rec.Body.String(), bad) || strings.Contains(logs.String(), bad) {
			t.Errorf("%q presente nella risposta o nei log", bad)
		}
	}
	if !strings.Contains(logs.String(), "mirror pull eseguito") {
		t.Errorf("manca il log dell'esecuzione: %s", logs.String())
	}
}

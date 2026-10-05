package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// R7: porta 2222 di default, indirizzo completo; la forma corta vale solo con 22.
func TestCloneConfig_R7(t *testing.T) {
	cases := []struct {
		name      string
		cfg       CloneConfig
		https     string
		ssh       string
		sshShort  string // vuoto = assente
		shortNull bool
	}{
		{"default_2222", CloneConfig{PublicURL: "https://git.example.com"},
			"https://git.example.com/alice/app.git", "ssh://git@git.example.com:2222/alice/app.git", "", true},
		{"porta_2222_esplicita", CloneConfig{PublicURL: "https://git.example.com/", SSHPort: 2222},
			"https://git.example.com/alice/app.git", "ssh://git@git.example.com:2222/alice/app.git", "", true},
		{"porta_22_forma_corta", CloneConfig{PublicURL: "https://git.example.com", SSHPort: 22},
			"https://git.example.com/alice/app.git", "ssh://git@git.example.com:22/alice/app.git", "git@git.example.com:alice/app.git", false},
		{"host_ssh_distinto", CloneConfig{PublicURL: "https://git.example.com:8443", SSHHost: "ssh.example.com", SSHPort: 2200},
			"https://git.example.com:8443/alice/app.git", "ssh://git@ssh.example.com:2200/alice/app.git", "", true},
		{"host_dal_public_url_senza_porta_https", CloneConfig{PublicURL: "https://git.example.com:8443", SSHPort: 22},
			"https://git.example.com:8443/alice/app.git", "ssh://git@git.example.com:22/alice/app.git", "git@git.example.com:alice/app.git", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := c.cfg.urls(nil, "alice", "app")
			if u.Https != c.https || u.Ssh != c.ssh {
				t.Fatalf("https=%q ssh=%q, voluti %q %q", u.Https, u.Ssh, c.https, c.ssh)
			}
			if c.shortNull != (u.SshShort == nil) || (u.SshShort != nil && *u.SshShort != c.sshShort) {
				t.Fatalf("sshShort = %v, voluto %q", u.SshShort, c.sshShort)
			}
		})
	}
}

// Senza identity e git configurati ogni operazione sui repo risponde 503
// prima di toccare il database (pool nil): mai un repo senza permessi.
func TestRepos_NonConfigurato503(t *testing.T) {
	router := NewRouter(nil, events.NoopPublisher{}, "segreto")
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/repos", `{"owner":"alice","name":"x"}`},
		{http.MethodGet, "/repos", ""},
		{http.MethodGet, "/repos/alice/x", ""},
		{http.MethodPatch, "/repos/alice/x", `{"archived":true}`},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		trust.Sign(req.Header, "segreto", trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice"}, time.Now())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s = %d %s, voluto 503", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// Senza PublicURL gli indirizzi si compongono dalla richiesta: prima
// X-Forwarded-Proto/Host (scritti dal gateway), altrimenti r.Host con http.
func TestCloneConfig_SenzaPublicURL(t *testing.T) {
	req := func(host string, hdr map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/repos", nil)
		r.Host = host
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name, https, ssh string
		r                *http.Request
		cfg              CloneConfig
	}{
		{"forwarded", "https://git.example.com/alice/app.git", "ssh://git@git.example.com:2222/alice/app.git",
			req("core:8080", map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "git.example.com"}), CloneConfig{}},
		{"forwarded_con_porta", "https://git.example.com:8443/alice/app.git", "ssh://git@git.example.com:2222/alice/app.git",
			req("core:8080", map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "git.example.com:8443"}), CloneConfig{}},
		{"forwarded_catena", "https://git.example.com/alice/app.git", "ssh://git@git.example.com:2222/alice/app.git",
			req("core:8080", map[string]string{"X-Forwarded-Proto": "https, http", "X-Forwarded-Host": "git.example.com, proxy"}), CloneConfig{}},
		{"solo_host", "http://core.local:8080/alice/app.git", "ssh://git@core.local:2222/alice/app.git",
			req("core.local:8080", nil), CloneConfig{}},
		{"proto_non_valido_ignorato", "http://core.local/alice/app.git", "ssh://git@core.local:2222/alice/app.git",
			req("core.local", map[string]string{"X-Forwarded-Proto": "javascript"}), CloneConfig{}},
		{"ssh_host_esplicito", "https://git.example.com/alice/app.git", "ssh://git@ssh.example.com:2222/alice/app.git",
			req("core", map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "git.example.com"}), CloneConfig{SSHHost: "ssh.example.com"}},
		// Con PublicURL impostata gli header della richiesta non contano.
		{"public_url_vince", "https://git.example.com/alice/app.git", "ssh://git@git.example.com:2222/alice/app.git",
			req("evil.example", map[string]string{"X-Forwarded-Host": "evil.example"}), CloneConfig{PublicURL: "https://git.example.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := c.cfg.urls(c.r, "alice", "app")
			if u.Https != c.https || u.Ssh != c.ssh {
				t.Fatalf("https=%q ssh=%q, voluti %q %q", u.Https, u.Ssh, c.https, c.ssh)
			}
		})
	}
}

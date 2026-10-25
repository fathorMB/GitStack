//go:build integration

package stackitest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
)

// GIT-361 (v2-b): pacchetti del registry sullo stack completo. L'immagine si pubblica nel
// registry del servizio git (binario vero) col protocollo OCI, poi `gs package list --json`
// passa da gateway e core (che chiama l'API interna di git) e mostra nome, tag, tagDetails e
// pushedBy; `gs package delete` elimina un tag e poi l'immagine, e sparisce dall'elenco.
// Il driver su disco di distribution non gira su Windows (rinomina di file aperti): il test
// si prova in CI Linux.

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// ociPush pubblica un'immagine OCI minima (config e un layer) come user/token e ritorna il
// digest del manifest.
func ociPush(t *testing.T, base, repoPath, tag, user, token string, layer []byte) string {
	t.Helper()
	do := func(method, target string, body []byte, ctype string, want ...int) *http.Response {
		req, err := http.NewRequest(method, target, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth(user, token)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		for _, w := range want {
			if resp.StatusCode == w {
				resp.Body = io.NopCloser(bytes.NewReader(b))
				return resp
			}
		}
		t.Fatalf("%s %s: %d %s", method, target, resp.StatusCode, b)
		return nil
	}
	upload := func(blob []byte) {
		resp := do(http.MethodPost, base+"/v2/"+repoPath+"/blobs/uploads/", nil, "", http.StatusAccepted)
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := url.Parse(base)
		u := b.ResolveReference(loc)
		q := u.Query()
		q.Set("digest", sha(blob))
		u.RawQuery = q.Encode()
		do(http.MethodPut, u.String(), blob, "application/octet-stream", http.StatusCreated)
	}
	config := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	upload(config)
	upload(layer)
	manifest, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": sha(config), "size": len(config)},
		"layers": []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar", "digest": sha(layer), "size": len(layer)}},
	})
	do(http.MethodPut, base+"/v2/"+repoPath+"/manifests/"+tag, manifest, "application/vnd.oci.image.manifest.v1+json", http.StatusCreated)
	return sha(manifest)
}

// newGsPkgEnv è newGsEnv senza i repo e gli utenti di newIssuesE2E, che qui non servono: solo
// lo stack (alice, bob, carol), il binario gs e il proxy /api/v1 → /v1.
func newGsPkgEnv(t *testing.T) *gsEnv {
	t.Helper()
	e := &issuesE2E{gitEnv: newGitEnv(t), userID: map[string]string{}}
	bin := build(t, "../../../../cli/cmd/gs", "gs")
	gw, err := url.Parse(e.gateway)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(gw)
	orig := rp.Director
	rp.Director = func(r *http.Request) {
		orig(r)
		r.URL.Path = "/v1" + strings.TrimPrefix(r.URL.Path, "/api/v1")
		r.Host = gw.Host
	}
	srv := httptest.NewServer(rp)
	t.Cleanup(srv.Close)
	return &gsEnv{issuesE2E: e, bin: bin, proxy: srv.URL}
}

func TestGsPackage_PubblicaElencaEliminaSulloStack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("il driver su disco del registry non gira su Windows: si prova in CI Linux")
	}
	g := newGsPkgEnv(t)
	g.createRepo("alice", "pkgs", "private")

	// Token con lo scope del registry (la password di docker login).
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	tk := g.gw("POST", "/user/tokens", map[string]any{"name": "registry", "scopes": []string{"write:registry"}, "expiresAt": exp}, nil, g.cookies["alice"])
	want(t, tk, 201, "")
	regToken, _ := tk.json()["token"].(string)

	layer := []byte(strings.Repeat("strato", 200))
	dg := ociPush(t, g.gitHTTP, "alice/pkgs/core", "v1", "alice", regToken, layer)
	ociPush(t, g.gitHTTP, "alice/pkgs/core", "dev", "alice", regToken, append(layer, '!'))
	ociPush(t, g.gitHTTP, "alice/pkgs/web", "latest", "alice", regToken, []byte("altro strato"))

	type pusher struct{ Kind, Login string }
	type pkg struct {
		Name       string   `json:"name"`
		Tags       []string `json:"tags"`
		Digest     string   `json:"digest"`
		SizeBytes  int64    `json:"sizeBytes"`
		Signature  string   `json:"signature"`
		HasSbom    bool     `json:"hasSbom"`
		Kind       string   `json:"kind"`
		PushedBy   *pusher  `json:"pushedBy"`
		TagDetails []struct {
			Name      string  `json:"name"`
			Digest    string  `json:"digest"`
			SizeBytes int64   `json:"sizeBytes"`
			PushedAt  string  `json:"pushedAt"`
			Signature string  `json:"signature"`
			HasSbom   bool    `json:"hasSbom"`
			Protected bool    `json:"protected"`
			PushedBy  *pusher `json:"pushedBy"`
		} `json:"tagDetails"`
	}
	list := func(user string) []pkg {
		t.Helper()
		r := g.ok(user, "pkgs", "", "pkg", "list", "--json", "name,tags,digest,sizeBytes,signature,hasSbom,kind,pushedBy,tagDetails")
		var out []pkg
		if err := json.Unmarshal([]byte(r.out), &out); err != nil {
			t.Fatalf("JSON non valido: %v: %s", err, r.out)
		}
		return out
	}

	t.Run("elenco_con_tagDetails_e_pushedBy", func(t *testing.T) {
		items := list("alice")
		byName := map[string]pkg{}
		for _, p := range items {
			byName[p.Name] = p
		}
		core, ok := byName["alice/pkgs/core"]
		if !ok || len(items) != 2 || byName["alice/pkgs/web"].Name == "" {
			t.Fatalf("immagini: %+v", items)
		}
		if len(core.Tags) != 2 || len(core.TagDetails) != 2 || core.SizeBytes < int64(len(layer)) || core.Kind != "image" {
			t.Fatalf("core: %+v", core)
		}
		if core.Signature != "unverified" || core.HasSbom {
			t.Fatalf("un'immagine pubblicata a mano non è firmata: %+v", core)
		}
		if core.PushedBy == nil || core.PushedBy.Kind != "user" || core.PushedBy.Login != "alice" {
			t.Fatalf("pushedBy dell'immagine: %+v", core.PushedBy)
		}
		var v1 = core.TagDetails[0]
		for _, d := range core.TagDetails {
			if d.Name == "v1" {
				v1 = d
			}
		}
		if v1.Name != "v1" || v1.Digest != dg || v1.SizeBytes < int64(len(layer)) || v1.PushedAt == "" || v1.Protected ||
			v1.PushedBy == nil || v1.PushedBy.Kind != "user" || v1.PushedBy.Login != "alice" {
			t.Fatalf("tag v1: %+v", v1)
		}
	})

	t.Run("permessi", func(t *testing.T) {
		// bob non ha accesso al repo privato: 404 anche per il registry; senza token 401.
		if r := g.gs("bob", "pkgs", "", "pkg", "list"); r.code == 0 {
			t.Fatalf("bob legge i pacchetti di un repo privato: %s", r.out)
		}
		if r := g.gs("", "pkgs", "", "pkg", "list"); r.code == 0 {
			t.Fatalf("token inventato: %s", r.out)
		}
		want(t, g.gw("GET", "/repos/alice/pkgs/packages", nil, nil, nil), 401, "")
		want(t, g.api("bob", "GET", "/repos/alice/pkgs/packages", nil), 404, "")
		want(t, g.api("bob", "DELETE", "/repos/alice/pkgs/packages?name=alice/pkgs/core", nil), 404, "")
	})

	t.Run("elimina_tag_poi_immagine", func(t *testing.T) {
		g.ok("alice", "pkgs", "", "pkg", "delete", "alice/pkgs/core", "--tag", "dev", "-y")
		items := list("alice")
		for _, p := range items {
			if p.Name == "alice/pkgs/core" && (len(p.Tags) != 1 || p.Tags[0] != "v1") {
				t.Fatalf("dopo l'eliminazione del tag: %+v", p)
			}
		}
		// Tag inesistente: 404 → exit diverso da 0.
		if r := g.gs("alice", "pkgs", "", "pkg", "delete", "alice/pkgs/core", "--tag", "dev", "-y"); r.code == 0 {
			t.Fatalf("eliminato due volte: %s", r.out)
		}
		g.ok("alice", "pkgs", "", "pkg", "delete", "alice/pkgs/core", "-y")
		items = list("alice")
		if len(items) != 1 || items[0].Name != "alice/pkgs/web" {
			t.Fatalf("dopo l'eliminazione dell'immagine: %+v", items)
		}
		// Anche il registry non serve più il manifest.
		req, _ := http.NewRequest(http.MethodGet, g.gitHTTP+"/v2/alice/pkgs/core/manifests/v1", nil)
		req.SetBasicAuth("alice", regToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("manifest eliminato: %d, voluto 404", resp.StatusCode)
		}
	})

	t.Run("tag_protetto_409", func(t *testing.T) {
		want(t, g.api("alice", "PATCH", "/repos/alice/pkgs/packages/settings", map[string]any{"protectedTagPatterns": []string{"latest"}, "includeInBackup": false}), 200, "")
		// Il registry tiene le impostazioni di core in cache per 10 s: si aspetta che scada.
		time.Sleep(11 * time.Second)
		want(t, g.api("alice", "DELETE", "/repos/alice/pkgs/packages?name="+url.QueryEscape("alice/pkgs/web")+"&tag=latest", nil), 409, "")
		if items := list("alice"); len(items) != 1 {
			t.Fatalf("il tag protetto doveva restare: %+v", items)
		}
	})
}

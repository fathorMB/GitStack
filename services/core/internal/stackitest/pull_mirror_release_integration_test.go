//go:build integration

package stackitest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/mirrors"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
)

// relRaw manda un corpo grezzo (upload e download degli asset) attraverso il gateway.
func (e *gitEnv) relRaw(method, path string, hdr map[string]string, data []byte) reply {
	e.t.Helper()
	var rd io.Reader
	if data != nil {
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, e.gateway+"/v1"+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: b}
}

// jobTokenFor crea un'esecuzione e un job in corso del repo, con contents: write
// e un token valido (gsj_), come li lascerebbe il motore CI.
func (e *gitEnv) jobTokenFor(repoName, token string) {
	e.t.Helper()
	ctx := context.Background()
	var repoID uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT resource_id FROM core.repositories WHERE name=$1`, repoName).Scan(&repoID); err != nil {
		e.t.Fatal(err)
	}
	run, job := uuid.New(), uuid.New()
	sum := sha256.Sum256([]byte(token))
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, event, ref, sha, group_key, status)
			VALUES ($1, $2, 'user', $3, '.github/workflows/release.yml', 'push', 'refs/tags/v2.0.0', repeat('a', 40), $4, 'in_progress')`, []any{run, repoID, uuid.New(), "g-" + run.String()}},
		{`INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name, status, permissions) VALUES ($1, $2, 0, 'release', 'release', 'in_progress', '{"contents":"write","packages":"read","actions":"read"}'::jsonb)`, []any{job, run}},
		{`INSERT INTO core.ci_job_tokens (job_id, token_hash, issued_at, expires_at) VALUES ($1, $2, now(), $3)`, []any{job, sum[:], time.Now().Add(time.Hour)}},
	} {
		if _, err := e.pool.Exec(ctx, q.sql, q.args...); err != nil {
			e.t.Fatalf("%s: %v", q.sql, err)
		}
	}
}

// TestPullMirrorRelease_TagSincronizzatoConAllegati prova GIT-529 sullo stack
// vero: su un repo in mirror in pull un tag arrivato dalla sincronizzazione
// ammette la release e gli allegati (token del job e token utente), con
// download e SHA-256; un tag non ancora sincronizzato è rifiutato con
// tag_not_found e il mirror resta in sola lettura per il codice.
func TestPullMirrorRelease_TagSincronizzatoConAllegati(t *testing.T) {
	requireTools(t)
	keys, err := webhooks.NewKeyring("k1", mirrorSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	eg, err := mirrors.NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ip := hostIPv4(t, eg)
	eg.Resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == mirrorDestHost {
			return []netip.Addr{ip}, nil
		}
		return nil, net.UnknownNetworkError("host sconosciuto: " + host)
	}
	origin := newMirrorDest(t)
	t.Setenv("GITSTACK_GIT_MIRROR_CA_FILE", origin.ca)

	e := newGitEnv(t,
		httpserver.WithMirrors(httpserver.MirrorConfig{Keys: keys, Egress: eg}),
		httpserver.WithAttachments(httpserver.AttachmentsConfig{Disk: &attachments.Disk{Dir: t.TempDir()}, ReleaseAssets: &attachments.Disk{Dir: t.TempDir()}, MaxBytes: 1 << 20}))
	eng := &mirrors.PullEngine{
		Pool: e.pool, Keys: keys, Egress: eg,
		Git: gitclient.New(mustURL(t, e.gitHTTP), coreSecret, 60*time.Second),
	}
	process := func() {
		t.Helper()
		if n, err := eng.ProcessDue(context.Background()); err != nil || n != 1 {
			t.Fatalf("ProcessDue: n=%d err=%v, atteso 1 aggiornamento", n, err)
		}
	}
	alice := e.bearer("alice")
	bob := e.bearer("bob")
	const name = "rel-mirror"
	repoURL := e.httpsURL("alice", e.tokens["alice"], "/alice/"+name+".git")

	// Origine: main con un commit e il tag v1.0.0 (già presente alla prima copia).
	src := e.initWork(origin.bare, map[string]string{"uno.txt": "1\n"})
	e.mustGit(src, "", "tag", "v1.0.0")
	e.mustGit(src, "", "push", "-q", "origin", "main", "v1.0.0")
	want(t, e.gw("POST", "/repos/import", map[string]any{
		"owner": "alice", "name": name, "visibility": "private", "url": origin.url(), "mode": "mirror",
		"username": mirrorDestUser, "token": mirrorDestToken,
	}, alice, nil), 201, "")
	process()

	relPath := "/repos/alice/" + name + "/releases"
	// Il repo è un mirror: la release su v1.0.0 (tag arrivato con la prima copia) funziona.
	if m, _ := e.gw("GET", "/repos/alice/"+name, nil, alice, nil).json()["mirror"].(map[string]any); m == nil {
		t.Fatal("il repo non risulta un mirror")
	}

	// --- negativo: v2.0.0 esiste solo nell'origine, la sincronizzazione non è passata ---
	e.commitFile(src, "due.txt", "2\n", "secondo")
	e.mustGit(src, "", "tag", "v2.0.0")
	e.mustGit(src, "", "push", "-q", "origin", "main", "v2.0.0")
	e.jobTokenFor(name, "gsj_mirror_scrive")
	job := map[string]string{"Authorization": "Bearer gsj_mirror_scrive"}
	r := e.gw("POST", relPath, map[string]any{"tagName": "v2.0.0", "name": "Prima del sync"}, alice, nil)
	want(t, r, 422, "tag_not_found")
	if !strings.Contains(string(r.body), "tag") {
		t.Fatalf("errore poco chiaro: %s", r.body)
	}
	want(t, e.gw("POST", relPath, map[string]any{"tagName": "v2.0.0", "name": "Prima del sync"}, job, nil), 422, "tag_not_found")

	// --- sincronizzazione: il tag arriva ---
	want(t, e.gw("POST", "/repos/alice/"+name+"/pull-mirror/sync", nil, alice, nil), 202, "")
	process()
	if f := strings.Fields(e.mustGit(e.home, "", "ls-remote", repoURL, "refs/tags/v2.0.0")); len(f) == 0 {
		t.Fatal("v2.0.0 non è arrivato con la sincronizzazione")
	}

	// --- release con il token del job (tag v2.0.0) e due allegati ---
	create := e.gw("POST", relPath, map[string]any{"tagName": "v2.0.0", "name": "Versione 2"}, job, nil)
	want(t, create, 201, "")
	jobRel, _ := create.json()["id"].(string)
	files := map[string][]byte{
		"installer.msi": bytes.Repeat([]byte("installer-bytes-"), 1000),
		"latest.json":   []byte(`{"version":"2.0.0"}` + "\n"),
	}
	check := func(relID, label string, hdr map[string]string, want0 map[string][]byte) {
		t.Helper()
		for fn, data := range want0 {
			up := e.relRaw("POST", relPath+"/"+relID+"/assets?name="+url.QueryEscape(fn), hdr, data)
			if up.status != 201 {
				t.Fatalf("%s: upload di %s: %d %s", label, fn, up.status, up.body)
			}
			sum := sha256.Sum256(data)
			if up.json()["sha256"] != hex.EncodeToString(sum[:]) {
				t.Fatalf("%s: sha256 di %s = %v", label, fn, up.json()["sha256"])
			}
		}
		// Elenco e download con un utente che legge (bob non ha il repo privato: lo legge alice).
		list := e.gw("GET", relPath+"/"+relID, nil, alice, nil)
		want(t, list, 200, "")
		assets, _ := list.json()["assets"].([]any)
		if len(assets) != len(want0) {
			t.Fatalf("%s: allegati elencati %d, attesi %d: %s", label, len(assets), len(want0), list.body)
		}
		for _, a := range assets {
			am := a.(map[string]any)
			data, ok := want0[am["name"].(string)]
			if !ok {
				t.Fatalf("%s: allegato inatteso %v", label, am)
			}
			dl := e.relRaw("GET", relPath+"/"+relID+"/assets/"+am["id"].(string)+"/download", alice, nil)
			sum := sha256.Sum256(data)
			got := sha256.Sum256(dl.body)
			if dl.status != 200 || got != sum || am["sha256"] != hex.EncodeToString(sum[:]) {
				t.Fatalf("%s: download di %v: %d, sha256 %x, atteso %x", label, am["name"], dl.status, got, sum)
			}
		}
	}
	check(jobRel, "token del job", job, files)

	// --- release con il token utente su un altro tag (v1.0.0) ---
	create = e.gw("POST", relPath, map[string]any{"tagName": "v1.0.0", "name": "Versione 1"}, alice, nil)
	want(t, create, 201, "")
	userRel, _ := create.json()["id"].(string)
	check(userRel, "token utente", alice, map[string][]byte{
		"app.dmg":     bytes.Repeat([]byte("dmg"), 500),
		"app.dmg.sig": []byte("firma\n"),
	})
	list := e.gw("GET", relPath, nil, alice, nil)
	want(t, list, 200, "")
	if items, _ := list.json()["items"].([]any); len(items) != 2 {
		t.Fatalf("release elencate: %s", list.body)
	}
	// Chi non ha accesso al repo privato non vede né release né allegati.
	if r := e.gw("GET", relPath, nil, bob, nil); r.status == 200 {
		t.Fatalf("bob legge le release di un repo privato: %s", r.body)
	}

	// --- il mirror resta in sola lettura per il codice, anche con le release ---
	work := e.clone(repoURL, "")
	e.commitFile(work, "locale.txt", "l\n", "tentativo locale")
	before := e.remoteHead(repoURL, "")
	out, err := e.git(work, "", "push", "origin", "HEAD:main")
	if err == nil || !strings.Contains(out, "mirror_read_only") {
		t.Fatalf("il push sul mirror doveva essere rifiutato (mirror_read_only), err=%v:\n%s", err, out)
	}
	if out, err := e.git(work, "", "push", "origin", "HEAD:refs/tags/v9.9.9"); err == nil || !strings.Contains(out, "mirror_read_only") {
		t.Fatalf("anche un tag spinto sul mirror va rifiutato, err=%v:\n%s", err, out)
	}
	if got := e.remoteHead(repoURL, ""); got != before {
		t.Fatalf("main sul mirror è cambiato: %q, era %q", got, before)
	}
	// Il tag rifiutato non esiste: nemmeno la release lo accetta.
	want(t, e.gw("POST", relPath, map[string]any{"tagName": "v9.9.9", "name": "Nato dal push"}, alice, nil), 422, "tag_not_found")
}

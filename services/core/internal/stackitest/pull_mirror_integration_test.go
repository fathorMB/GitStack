//go:build integration

package stackitest

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/mirrors"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// TestPullMirrorGiroCompleto prova sullo stack vero (gateway, identity, git
// come binari, core in-process) il mirror in pull (M-09/A7, GIT-201): un
// secondo server git (repo bare dietro TLS con autenticazione Basic) fa da
// origine di prova. Si importa come mirror, si aggiorna, si riscrive la storia
// nell'origine (force-push: ref `diverged`, niente sovrascritto, gli altri ref
// avanzano), il push sul repo mirror è rifiutato (sola lettura), «Interrompi»
// lo rende scrivibile e l'import una tantum («Copia una volta sola») produce
// un repo normale senza legami con l'origine. Regole MP1-MP8.
func TestPullMirrorGiroCompleto(t *testing.T) {
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
	// Il servizio git (binario vero) si fida della CA dell'origine di prova
	// (come per il push). Su Windows git usa schannel e non legge questo file:
	// il test gira in CI (Linux).
	t.Setenv("GITSTACK_GIT_MIRROR_CA_FILE", origin.ca)

	e := newGitEnv(t, httpserver.WithMirrors(httpserver.MirrorConfig{Keys: keys, Egress: eg}))
	eng := &mirrors.PullEngine{
		Pool: e.pool, Keys: keys, Egress: eg,
		Git: gitclient.New(mustURL(t, e.gitHTTP), serviceSecret, 60*time.Second),
	}
	ctx := context.Background()
	process := func(wantRuns int) {
		t.Helper()
		n, err := eng.ProcessDue(ctx)
		if err != nil {
			t.Fatalf("ProcessDue: %v", err)
		}
		if n != wantRuns {
			t.Fatalf("aggiornamenti eseguiti: %d, attesi %d", n, wantRuns)
		}
	}
	alice := e.bearer("alice")
	repoURL := func(name string) string { return e.httpsURL("alice", e.tokens["alice"], "/alice/"+name+".git") }

	// --- origine di prova: main con due commit, il tag v1 e un branch ----------
	src := e.initWork(origin.bare, map[string]string{"uno.txt": "1\n"})
	e.commitFile(src, "due.txt", "2\n", "secondo")
	e.mustGit(src, "", "tag", "v1")
	e.mustGit(src, "", "push", "-q", "origin", "main", "v1")
	e.mustGit(src, "", "checkout", "-q", "-b", "feature")
	e.commitFile(src, "feature.txt", "f\n", "su feature")
	e.mustGit(src, "", "push", "-q", "origin", "feature")
	e.mustGit(src, "", "checkout", "-q", "main")
	srcHead := func() string { return strings.TrimSpace(e.mustGit(src, "", "rev-parse", "HEAD")) }
	oldMain := srcHead()
	oldFeature := e.destRef(origin, "refs/heads/feature")

	// --- 1. import «mantieni sincronizzato» (MP3, MP7): la prima copia parte da sola
	created := e.gw("POST", "/repos/import", map[string]any{
		"owner": "alice", "name": "giro-copia", "visibility": "private", "url": origin.url(), "mode": "mirror",
		"username": mirrorDestUser, "token": mirrorDestToken,
	}, alice, nil)
	want(t, created, 201, "")
	if strings.Contains(string(created.body), mirrorDestToken) {
		t.Fatalf("il token torna nella risposta: %s", created.body)
	}
	pullPath := "/repos/alice/giro-copia/pull-mirror"
	pull := func() map[string]any {
		t.Helper()
		r := e.gw("GET", pullPath, nil, alice, nil)
		want(t, r, 200, "")
		if strings.Contains(string(r.body), mirrorDestToken) {
			t.Fatalf("il token torna nel mirror: %s", r.body)
		}
		return r.json()
	}
	if s := pull()["state"]; s != "pending" {
		t.Fatalf("stato prima della copia: %v", s)
	}
	process(1)
	if s := pull()["state"]; s != "in_sync" {
		t.Fatalf("stato dopo la prima copia: %v; mirror: %v", s, pull())
	}
	// Tutti i branch e i tag, con gli stessi SHA (MP1).
	if got := e.remoteHead(repoURL("giro-copia"), ""); got != oldMain {
		t.Fatalf("main sul mirror %q, nell'origine %q", got, oldMain)
	}
	lsRef := func(ref string) string {
		t.Helper()
		f := strings.Fields(e.mustGit(e.home, "", "ls-remote", repoURL("giro-copia"), ref))
		if len(f) == 0 {
			return ""
		}
		return f[0]
	}
	if got := lsRef("refs/tags/v1"); got != strings.TrimSpace(e.mustGit(src, "", "rev-parse", "refs/tags/v1")) {
		t.Fatalf("tag v1 sul mirror: %q", got)
	}
	if got := lsRef("refs/heads/feature"); got != oldFeature {
		t.Fatalf("branch feature sul mirror %q, nell'origine %q", got, oldFeature)
	}
	// Il repo porta l'etichetta Mirror con l'origine (MP2).
	repo := e.gw("GET", "/repos/alice/giro-copia", nil, alice, nil)
	want(t, repo, 200, "")
	if m, _ := repo.json()["mirror"].(map[string]any); m == nil || !strings.Contains(m["origin"].(string), "/dest.git") {
		t.Fatalf("campo mirror del repo: %s", repo.body)
	}
	// Non si aggiorna da solo prima dell'intervallo (MP4: default 10 minuti).
	process(0)

	// --- 2. l'origine avanza: «Sincronizza ora» allinea main (MP4) -----------
	e.commitFile(src, "tre.txt", "3\n", "terzo")
	e.mustGit(src, "", "push", "-q", "origin", "main")
	want(t, e.gw("POST", pullPath+"/sync", nil, alice, nil), 202, "")
	process(1)
	if got, w := e.remoteHead(repoURL("giro-copia"), ""), srcHead(); got != w {
		t.Fatalf("dopo Sincronizza ora main sul mirror %q, nell'origine %q", got, w)
	}
	synced := srcHead()

	// --- 3. force-push nell'origine: main diverged, niente sovrascritto (MP1) ---
	e.mustGit(src, "", "reset", "-q", "--hard", oldMain)
	e.commitFile(src, "riscritto.txt", "r\n", "storia riscritta")
	e.mustGit(src, "", "push", "-q", "--force", "origin", "main")
	// Un altro ref avanza nello stesso giro: gli altri ref si aggiornano comunque.
	e.mustGit(src, "", "checkout", "-q", "feature")
	e.commitFile(src, "feature2.txt", "f2\n", "ancora su feature")
	e.mustGit(src, "", "push", "-q", "origin", "feature")
	newFeature := strings.TrimSpace(e.mustGit(src, "", "rev-parse", "HEAD"))
	want(t, e.gw("POST", pullPath+"/sync", nil, alice, nil), 202, "")
	process(1)
	got := pull()
	if got["state"] != "in_sync" {
		t.Fatalf("con un ref divergente il mirror resta in_sync: %v", got)
	}
	diverged, _ := got["divergedRefs"].([]any)
	if len(diverged) != 1 {
		t.Fatalf("ref divergenti: %v", got["divergedRefs"])
	}
	d, _ := diverged[0].(map[string]any)
	if d["ref"] != "refs/heads/main" || d["kind"] != "rewritten" || d["localSha"] != synced {
		t.Fatalf("ref divergente: %v", d)
	}
	if got := e.remoteHead(repoURL("giro-copia"), ""); got != synced {
		t.Fatalf("main sul mirror è stato sovrascritto: %q, atteso %q", got, synced)
	}
	if got := lsRef("refs/heads/feature"); got != newFeature {
		t.Fatalf("feature doveva avanzare: %q, atteso %q", got, newFeature)
	}
	runs := e.gw("GET", pullPath+"/runs", nil, alice, nil)
	want(t, runs, 200, "")
	if !strings.Contains(string(runs.body), `"diverged"`) || strings.Contains(string(runs.body), mirrorDestToken) {
		t.Fatalf("registro delle esecuzioni: %s", runs.body)
	}

	// --- 4. il repo mirror è in sola lettura: push rifiutato (MP2) ----------
	work := e.clone(repoURL("giro-copia"), "")
	e.commitFile(work, "locale.txt", "l\n", "tentativo locale")
	out, err := e.git(work, "", "push", "origin", "HEAD:main")
	if err == nil {
		t.Fatalf("il push su un mirror in pull doveva essere rifiutato:\n%s", out)
	}
	if !strings.Contains(out, "mirror_read_only") {
		t.Fatalf("manca il codice del rifiuto:\n%s", out)
	}
	if got := e.remoteHead(repoURL("giro-copia"), ""); got != synced {
		t.Fatalf("il push rifiutato ha cambiato main: %q", got)
	}

	// --- 5. Interrompi (MP6): la conferma è il nome; il repo torna scrivibile --
	want(t, e.gw("POST", pullPath+"/stop", map[string]any{"confirm": "altro"}, alice, nil), 422, "confirmation_mismatch")
	stop := e.gw("POST", pullPath+"/stop", map[string]any{"confirm": "giro-copia"}, alice, nil)
	want(t, stop, 200, "")
	if s := pull(); s["state"] != "stopped" || s["hasToken"] != false {
		t.Fatalf("mirror interrotto: %v", s)
	}
	e.mustGit(work, "", "push", "-q", "origin", "HEAD:main")
	if got, w := e.remoteHead(repoURL("giro-copia"), ""), strings.TrimSpace(e.mustGit(work, "", "rev-parse", "HEAD")); got != w {
		t.Fatalf("dopo Interrompi il push non è arrivato: %q, atteso %q", got, w)
	}
	process(0) // interrotto: niente più aggiornamenti

	// --- 6. import una tantum «Copia una volta sola» (MP8) ------------------
	once := e.gw("POST", "/repos/import", map[string]any{
		"owner": "alice", "name": "giro-once", "visibility": "private", "url": origin.url(), "mode": "once",
		"username": mirrorDestUser, "token": mirrorDestToken,
	}, alice, nil)
	want(t, once, 201, "")
	process(1)
	onceURL := repoURL("giro-once")
	if got, w := e.remoteHead(onceURL, ""), e.destRef(origin, "refs/heads/main"); got != w {
		t.Fatalf("copia una tantum: main %q, nell'origine %q", got, w)
	}
	st := e.gw("GET", "/repos/alice/giro-once/pull-mirror", nil, alice, nil)
	want(t, st, 200, "")
	if j := st.json(); j["state"] != "stopped" || j["mode"] != "once" || j["hasToken"] != false {
		t.Fatalf("mirror dopo la copia una tantum: %s", st.body)
	}
	repoOnce := e.gw("GET", "/repos/alice/giro-once", nil, alice, nil)
	want(t, repoOnce, 200, "")
	if m := repoOnce.json()["mirror"]; m != nil {
		t.Fatalf("dopo la copia il repo è normale, senza mirror: %s", repoOnce.body)
	}
	w2 := e.clone(onceURL, "")
	e.commitFile(w2, "mio.txt", "m\n", "lavoro mio")
	e.mustGit(w2, "", "push", "-q", "origin", "HEAD:main")
	// L'origine non è più toccata: nessun aggiornamento in coda.
	process(0)
}

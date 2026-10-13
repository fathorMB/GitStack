//go:build integration

package stackitest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// M-09/B, regola V3 (GIT-220): test di fumo con utenti e agenti contemporanei.
// Cinque persone (clone e push alternati HTTPS e SSH, chiamate API dirette) e
// tre agenti (binario gs vero con GS_TOKEN, HTTPS) lavorano insieme sullo
// stesso repo per un tempo configurabile: clone, push su un ramo proprio e
// sul ramo condiviso (main, con pull --rebase e ripetizione se il push è
// rifiutato perché non fast-forward), fetch, lettura del repo, creazione e
// commento di issue.
//
// Non ci sono obiettivi numerici: l'esito è
//   - zero risposte 5xx e zero 429 (gateway e servizio git, osservate da proxy
//     contatori: lo stack usa i limiti di default del gateway, che il carico
//     reale non deve far scattare),
//   - nessun errore delle operazioni (a parte il non-fast-forward atteso),
//   - nessun push perso: ogni commit spinto con successo è nel ramo condiviso
//     e il numero di commit del ramo è esattamente seed + push riusciti,
//   - nessuna corsa sui ref: il ramo di ogni lavoratore sta allo sha
//     dell'ultimo suo push, il repo passa `git fsck`, i numeri delle issue
//     sono tutti diversi e il totale combacia.
//
// Il riepilogo delle latenze va nel log (con -v) ed è solo informativo.
//
// Il test gira solo con GITSTACK_SMOKE_DURATION (per esempio 30s, 3m): così
// non allunga il job «go» di ci.yml. Comando locale in docs/operations.md,
// job di CI in .github/workflows/smoke.yml.

const (
	smokeRepo   = "carico"
	smokeUsers  = 5
	smokeAgents = 3
)

type smokeStats struct {
	mu       sync.Mutex
	lat      map[string][]time.Duration
	errs     []string
	conflict int
	fiveXX   []string
	limited  []string          // risposte 429 (rate limit) ricevute dai lavoratori
	gaveUp   int               // giri in cui le ripetizioni sono finite senza push: il commit resta locale e parte col giro dopo
	pushed   []string          // sha spinti con successo sul ramo condiviso
	ownTip   map[string]string // worker -> sha dell'ultimo push sul suo ramo
	issues   []int             // numeri delle issue create
	comments map[int]int       // issue -> commenti scritti
}

func (s *smokeStats) rec(op string, d time.Duration) {
	s.mu.Lock()
	s.lat[op] = append(s.lat[op], d)
	s.mu.Unlock()
}

func (s *smokeStats) fail(worker, op string, err error) {
	s.mu.Lock()
	if len(s.errs) < 50 {
		msg := err.Error()
		if len(msg) > 400 {
			msg = msg[:400]
		}
		s.errs = append(s.errs, fmt.Sprintf("%s %s: %s", worker, op, msg))
	}
	s.mu.Unlock()
}

// countingProxy inoltra a target contando le risposte 5xx e 429; con v1 mappa
// /api/v1 su /v1 come fa il web.
func countingProxy(t *testing.T, st *smokeStats, target string, v1 bool) *httptest.Server {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.FlushInterval = -1
	orig := rp.Director
	rp.Director = func(r *http.Request) {
		orig(r)
		if v1 {
			r.URL.Path = "/v1" + strings.TrimPrefix(r.URL.Path, "/api/v1")
		}
		r.Host = u.Host
	}
	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode >= 500 {
			st.mu.Lock()
			if len(st.fiveXX) < 50 {
				st.fiveXX = append(st.fiveXX, fmt.Sprintf("%s %s -> %d", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode))
			} else {
				st.fiveXX = append(st.fiveXX, "")
			}
			st.mu.Unlock()
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			st.mu.Lock()
			if len(st.limited) < 50 {
				st.limited = append(st.limited, fmt.Sprintf("%s %s -> 429", resp.Request.Method, resp.Request.URL.Path))
			} else {
				st.limited = append(st.limited, "")
			}
			st.mu.Unlock()
		}
		return nil
	}
	srv := httptest.NewServer(rp)
	t.Cleanup(srv.Close)
	return srv
}

type smokeWorker struct {
	name   string
	agent  bool
	token  string
	key    string // chiave SSH (solo persone)
	work   string // cartella di scratch del lavoratore
	clone  string
	nIssue int
}

var smokeNumberRe = regexp.MustCompile(`"number"\s*:\s*(\d+)`)

func TestCaricoFumo(t *testing.T) {
	dur := os.Getenv("GITSTACK_SMOKE_DURATION")
	if dur == "" {
		t.Skip("GITSTACK_SMOKE_DURATION non impostata (es. 30s): test di fumo V3 non richiesto")
	}
	total, err := time.ParseDuration(dur)
	if err != nil || total <= 0 {
		t.Fatalf("GITSTACK_SMOKE_DURATION %q non valida: %v", dur, err)
	}
	g := newGsEnv(t)
	st := &smokeStats{lat: map[string][]time.Duration{}, ownTip: map[string]string{}, comments: map[int]int{}}
	apiSrv := countingProxy(t, st, g.gateway, true)
	gitSrv := countingProxy(t, st, g.gitHTTP, false)

	// --- preparazione: utenti, agenti, repo con grant di scrittura ---
	login := g.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": newPassword}, nil, nil)
	want(t, login, 200, "")
	admin := g.session(login)
	const userPassword = "password-di-prova-molto-lunga-1"
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	keyDir := t.TempDir()
	var workers []*smokeWorker
	ids := map[string]string{}
	for i := 1; i <= smokeUsers+smokeAgents; i++ {
		w := &smokeWorker{work: t.TempDir()}
		if i <= smokeUsers {
			w.name = fmt.Sprintf("umano%d", i)
			r := g.gw("POST", "/users", map[string]any{"username": w.name, "email": w.name + "@example.com", "password": userPassword}, nil, admin)
			want(t, r, 201, "")
			ids[w.name], _ = r.json()["id"].(string)
			l := g.gw("POST", "/auth/login", map[string]any{"username": w.name, "password": userPassword}, nil, nil)
			want(t, l, 200, "")
			ck := g.session(l)
			tok := g.gw("POST", "/user/tokens", map[string]any{"name": "rw", "scopes": []string{"read:resource", "write:resource"}, "expiresAt": exp}, nil, ck)
			want(t, tok, 201, "")
			w.token, _ = tok.json()["token"].(string)
			key := filepath.Join(keyDir, "id_"+w.name)
			if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", w.name+"@test", "-f", key).CombinedOutput(); err != nil {
				t.Fatalf("ssh-keygen: %v\n%s", err, out)
			}
			w.key = filepath.ToSlash(key)
			pub, err := os.ReadFile(key + ".pub")
			if err != nil {
				t.Fatal(err)
			}
			want(t, g.gw("POST", "/user/ssh-keys", map[string]any{"title": "smoke", "publicKey": strings.TrimSpace(string(pub))}, nil, ck), 201, "")
		} else {
			w.name = fmt.Sprintf("agente%d", i-smokeUsers)
			w.agent = true
			r := g.gw("POST", "/users", map[string]any{"username": w.name, "kind": "agent", "email": w.name + "@agents.example.com"}, nil, admin)
			want(t, r, 201, "")
			ids[w.name], _ = r.json()["id"].(string)
			tok := g.gw("POST", "/users/"+w.name+"/tokens", map[string]any{"name": "rw", "scopes": []string{"read:resource", "write:resource"}, "expiresAt": exp}, nil, admin)
			want(t, tok, 201, "")
			w.token, _ = tok.json()["token"].(string)
		}
		if !strings.HasPrefix(w.token, "gst_") {
			t.Fatalf("token di %s non valido", w.name)
		}
		workers = append(workers, w)
	}
	repoID := g.createRepo("alice", smokeRepo, "private")
	for _, w := range workers {
		want(t, g.gw("POST", "/resources/"+repoID+"/grants", map[string]any{"subjectType": "user", "subjectId": ids[w.name], "role": "write"}, g.bearer("alice"), nil), 201, "")
	}
	// URL del repo attraverso il proxy contatore del servizio git.
	httpsURL := func(user, token string) string {
		return strings.Replace(gitSrv.URL, "http://", "http://"+user+":"+token+"@", 1) + "/alice/" + smokeRepo + ".git"
	}
	sshURL := g.sshURL("/alice/" + smokeRepo + ".git")
	seed := g.initWork(httpsURL("alice", g.tokens["alice"]), map[string]string{"README.md": "# carico\n"})
	g.mustGit(seed, "", "push", "-q", "origin", "main")

	// --- il carico ---
	deadline := time.Now().Add(total)
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			smokeRun(g, st, w, deadline, httpsURL(w.name, w.token), sshURL, apiSrv.URL)
		}()
	}
	wg.Wait()

	// --- verifica ---
	st.mu.Lock()
	errs := append([]string(nil), st.errs...)
	st.mu.Unlock()
	for _, e := range errs {
		t.Errorf("errore: %s", e)
	}
	st.mu.Lock()
	fiveXX := append([]string(nil), st.fiveXX...)
	st.mu.Unlock()
	for _, f := range fiveXX {
		if f != "" {
			t.Errorf("risposta 5xx: %s", f)
		}
	}
	if n := len(fiveXX); n > 0 {
		t.Errorf("%d risposte 5xx", n)
	}
	st.mu.Lock()
	limited := append([]string(nil), st.limited...)
	st.mu.Unlock()
	for _, f := range limited {
		if f != "" {
			t.Errorf("rate limit durante il carico: %s", f)
		}
	}
	if n := len(limited); n > 0 {
		t.Errorf("%d risposte 429 (rate limit) ai lavoratori: i limiti di default del gateway non devono scattare con il carico del fumo", n)
	}

	ver := g.clone(httpsURL("alice", g.tokens["alice"]), "")
	out := g.mustGit(ver, "", "rev-list", "origin/main")
	inMain := map[string]bool{}
	for _, l := range strings.Fields(out) {
		inMain[l] = true
	}
	for _, sha := range st.pushed {
		if !inMain[sha] {
			t.Errorf("push perso: il commit %s non è su main", sha)
		}
	}
	if want := 1 + len(st.pushed); len(inMain) != want {
		t.Errorf("commit su main: %d, attesi %d (seed + %d push riusciti)", len(inMain), want, len(st.pushed))
	}
	if out, err := g.git(ver, "", "fsck", "--strict"); err != nil {
		t.Errorf("git fsck: %v\n%s", err, out)
	}
	refs := g.mustGit(ver, "", "ls-remote", "origin", "refs/heads/w/*")
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(refs), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 {
			got[strings.TrimPrefix(f[1], "refs/heads/w/")] = f[0]
		}
	}
	for name, sha := range st.ownTip {
		if got[name] != sha {
			t.Errorf("ref w/%s = %q, atteso %q (ultimo push riuscito)", name, got[name], sha)
		}
	}
	if len(got) != len(st.ownTip) {
		t.Errorf("rami w/*: %d sul server, %d spinti", len(got), len(st.ownTip))
	}

	seen := map[int]bool{}
	for _, n := range st.issues {
		if seen[n] {
			t.Errorf("numero di issue duplicato: #%d", n)
		}
		seen[n] = true
	}
	list := g.gwPolite(t, "GET", "/repos/alice/"+smokeRepo+"/issues", g.bearer("alice"))
	want(t, list, 200, "")
	if tot, ok := list.json()["total"].(float64); ok && int(tot) != len(st.issues) {
		t.Errorf("issue sul server: %d, create %d", int(tot), len(st.issues))
	}
	for n, c := range st.comments {
		r := g.gwPolite(t, "GET", fmt.Sprintf("/repos/alice/%s/issues/%d/comments", smokeRepo, n), g.bearer("alice"))
		want(t, r, 200, "")
		if tot, ok := r.json()["total"].(float64); ok && int(tot) != c {
			t.Errorf("commenti della issue #%d: %d, scritti %d", n, int(tot), c)
		}
	}

	// --- riepilogo informativo ---
	ops := make([]string, 0, len(st.lat))
	for op := range st.lat {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n=== V3 fumo: %d persone + %d agenti per %s ===\n", smokeUsers, smokeAgents, total)
	fmt.Fprintf(&sb, "commit entrati su main: %d (rifiuti non-ff ritentati: %d; rinunce, giri senza push col commit portato al giro dopo: %d), issue: %d, 5xx: %d, 429: %d, errori: %d\n",
		len(st.pushed), st.conflict, st.gaveUp, len(st.issues), len(fiveXX), len(limited), len(errs))
	fmt.Fprintf(&sb, "%-18s %6s %10s %10s %10s\n", "operazione", "n", "p50", "p95", "max")
	for _, op := range ops {
		d := append([]time.Duration(nil), st.lat[op]...)
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		pc := func(p float64) time.Duration { return d[int(float64(len(d)-1)*p)] }
		fmt.Fprintf(&sb, "%-18s %6d %10s %10s %10s\n", op, len(d),
			pc(0.5).Round(time.Millisecond), pc(0.95).Round(time.Millisecond), d[len(d)-1].Round(time.Millisecond))
	}
	sb.WriteString("(latenze solo informative: nessun obiettivo numerico, regola V3)\n")
	t.Log(sb.String())
}

const (
	politeMaxTries = 30
	politeMaxWait  = 90 * time.Second // tetto di attesa totale per una lettura
)

// gwPolite è una lettura da client educato: su 429 aspetta Retry-After (in
// secondi, 1 se assente o illeggibile) e riprova, con un tetto di tentativi e
// di tempo. Serve alle letture di verifica dopo il carico, fatte in sequenza
// con un solo token a centinaia di richieste al secondo: il gateway ha i
// limiti di default (per credenziale 20 rps, burst 200) e il fumo non li
// alza. Qualunque altra risposta, o l'esaurirsi del tetto, torna al chiamante.
func (g *gsEnv) gwPolite(t *testing.T, method, path string, hdr map[string]string) reply {
	t.Helper()
	start := time.Now()
	for try := 1; ; try++ {
		r := g.gw(method, path, nil, hdr, nil)
		if r.status != http.StatusTooManyRequests || try >= politeMaxTries {
			return r
		}
		wait := time.Second
		if n, err := strconv.Atoi(strings.TrimSpace(r.retryAfter)); err == nil && n > 0 {
			wait = time.Duration(n) * time.Second
		}
		if time.Since(start)+wait > politeMaxWait {
			return r
		}
		time.Sleep(wait)
	}
}

// smokeRun è il ciclo di un lavoratore: nessuna chiamata a t.Fatal, gli
// errori vanno in st.
func smokeRun(g *gsEnv, st *smokeStats, w *smokeWorker, deadline time.Time, httpsURL, sshURL, apiURL string) {
	timed := func(op string, f func() error) bool {
		start := time.Now()
		err := f()
		st.rec(op, time.Since(start))
		if err != nil {
			st.fail(w.name, op, err)
			return false
		}
		return true
	}
	git := func(dir string, args ...string) (string, error) {
		out, err := g.git(dir, w.key, args...)
		if err != nil {
			return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
		}
		return out, nil
	}
	// Persone: HTTPS e SSH alternati a ogni giro; agenti: solo HTTPS.
	remote := func(i int) (string, string) {
		if !w.agent && i%2 == 1 {
			return sshURL, "ssh"
		}
		return httpsURL, "https"
	}

	w.clone = filepath.Join(w.work, "w")
	url0, proto := remote(0)
	if !timed("clone-"+proto, func() error { _, err := git(w.work, "clone", "-q", url0, w.clone); return err }) {
		return
	}
	for _, kv := range [][2]string{{"user.name", w.name}, {"user.email", w.name + "@example.com"}} {
		if _, err := git(w.clone, "config", kv[0], kv[1]); err != nil {
			st.fail(w.name, "config", err)
			return
		}
	}

	for i := 0; time.Now().Before(deadline); i++ {
		url, proto := remote(i)
		if _, err := git(w.clone, "remote", "set-url", "origin", url); err != nil {
			st.fail(w.name, "remote", err)
			return
		}
		// Un commit con un file solo suo, spinto su main con ripetizione.
		file := fmt.Sprintf("%s-%04d.txt", w.name, i)
		if err := os.WriteFile(filepath.Join(w.clone, file), []byte(fmt.Sprintf("%s %d %s\n", w.name, i, time.Now().Format(time.RFC3339Nano))), 0o600); err != nil {
			st.fail(w.name, "scrittura", err)
			return
		}
		if _, err := git(w.clone, "add", "-A"); err != nil {
			st.fail(w.name, "add", err)
			return
		}
		if _, err := git(w.clone, "commit", "-q", "-m", "carico "+file); err != nil {
			st.fail(w.name, "commit", err)
			return
		}
		pushed := false
		var batch []string // commit locali che entrano con il push (anche quelli di giri senza push)
		for try := 0; try < 20 && !pushed; try++ {
			if !timed("pull-rebase-"+proto, func() error { _, err := git(w.clone, "pull", "-q", "--rebase", "origin", "main"); return err }) {
				break
			}
			batch = batch[:0]
			if lst, err := git(w.clone, "rev-list", "origin/main..HEAD"); err != nil {
				st.fail(w.name, "rev-list", err)
				break
			} else {
				batch = append(batch, strings.Fields(lst)...)
			}
			start := time.Now()
			out, err := g.git(w.clone, w.key, "push", "-q", "origin", "HEAD:refs/heads/main")
			if err == nil {
				st.rec("push-main-"+proto, time.Since(start))
				pushed = true
				break
			}
			if strings.Contains(out, "rejected") || strings.Contains(out, "fetch first") || strings.Contains(out, "non-fast-forward") {
				st.mu.Lock()
				st.conflict++
				st.mu.Unlock()
				continue // atteso: un altro ha spinto prima
			}
			st.fail(w.name, "push-main", fmt.Errorf("%w: %s", err, out))
			break
		}
		if !pushed {
			st.mu.Lock()
			st.gaveUp++
			st.mu.Unlock()
			continue
		}
		sha, err := git(w.clone, "rev-parse", "HEAD")
		if err != nil {
			st.fail(w.name, "rev-parse", err)
			return
		}
		sha = strings.TrimSpace(sha)
		st.mu.Lock()
		st.pushed = append(st.pushed, batch...)
		st.mu.Unlock()
		if timed("push-ramo-"+proto, func() error {
			_, err := git(w.clone, "push", "-q", "origin", "HEAD:refs/heads/w/"+w.name)
			return err
		}) {
			st.mu.Lock()
			st.ownTip[w.name] = sha
			st.mu.Unlock()
		}
		timed("fetch-"+proto, func() error { _, err := git(w.clone, "fetch", "-q", "origin"); return err })

		// API: lettura, issue, commento.
		var num int
		if w.agent {
			num = smokeAgentAPI(g, st, w, apiURL, timed)
		} else {
			num = smokeUserAPI(st, w, apiURL, i, timed)
		}
		if num > 0 {
			st.mu.Lock()
			st.issues = append(st.issues, num)
			st.comments[num]++
			st.mu.Unlock()
		}
	}
}

func smokeUserAPI(st *smokeStats, w *smokeWorker, apiURL string, i int, timed func(string, func() error) bool) int {
	call := func(method, path string, body any, wantStatus int) (map[string]any, error) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, method, apiURL+"/api/v1"+path, rd)
		req.Header.Set("Authorization", "Bearer "+w.token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != wantStatus {
			return nil, fmt.Errorf("%s %s: %d, atteso %d: %s", method, path, resp.StatusCode, wantStatus, b)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m, nil
	}
	base := "/repos/alice/" + smokeRepo
	timed("api-repo", func() error { _, err := call("GET", base, nil, 200); return err })
	timed("api-commits", func() error { _, err := call("GET", base+"/commits?ref=main", nil, 200); return err })
	timed("api-issues-lista", func() error { _, err := call("GET", base+"/issues", nil, 200); return err })
	var num int
	timed("api-issue-crea", func() error {
		m, err := call("POST", base+"/issues", map[string]any{"title": fmt.Sprintf("%s: giro %d", w.name, i), "body": "issue di prova del carico"}, 201)
		if err != nil {
			return err
		}
		n, _ := m["number"].(float64)
		num = int(n)
		return nil
	})
	if num == 0 {
		return 0
	}
	if !timed("api-commento", func() error {
		_, err := call("POST", fmt.Sprintf("%s/issues/%d/comments", base, num), map[string]any{"body": "commento di " + w.name}, 201)
		return err
	}) {
		// issue creata ma commento fallito: l'errore è già registrato.
		st.mu.Lock()
		st.issues = append(st.issues, num)
		st.mu.Unlock()
		return 0
	}
	return num
}

func smokeAgentAPI(g *gsEnv, st *smokeStats, w *smokeWorker, apiURL string, timed func(string, func() error) bool) int {
	gs := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		full := append([]string{"-R", "alice/" + smokeRepo, "--hostname", apiURL}, args...)
		cmd := exec.CommandContext(ctx, g.bin, full...)
		cmd.Dir = w.work
		cmd.Env = append(cleanEnv(), "GS_CONFIG_DIR="+filepath.Join(w.work, "gs-config"), "GS_NO_KEYRING=1", "GS_HOST="+apiURL, "GS_TOKEN="+w.token,
			"HOME="+w.work, "USERPROFILE="+w.work)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			return out.String(), fmt.Errorf("gs %s: %w: %s", strings.Join(args, " "), err, errOut.String())
		}
		return out.String(), nil
	}
	timed("gs-issue-lista", func() error { _, err := gs("issue", "list", "--json", "number"); return err })
	timed("gs-repo-view", func() error { _, err := gs("api", "/repos/alice/"+smokeRepo); return err })
	var num int
	timed("gs-issue-crea", func() error {
		out, err := gs("issue", "create", "-t", fmt.Sprintf("%s: segnalazione %d", w.name, w.nIssue), "-b", "issue di prova del carico", "--json", "number")
		if err != nil {
			return err
		}
		m := smokeNumberRe.FindStringSubmatch(out)
		if m == nil {
			return fmt.Errorf("numero della issue non trovato: %q", out)
		}
		_, _ = fmt.Sscanf(m[1], "%d", &num)
		w.nIssue++
		return nil
	})
	if num == 0 {
		return 0
	}
	if !timed("gs-commento", func() error {
		_, err := gs("issue", "comment", fmt.Sprint(num), "-b", "commento di "+w.name)
		return err
	}) {
		st.mu.Lock()
		st.issues = append(st.issues, num)
		st.mu.Unlock()
		return 0
	}
	return num
}

package app_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/cli/internal/cmd/app"
)

// GIT-354 (H13, mockup H10 data-screen="cli"): ogni sottocomando di gs app contro un gateway finto, con --json,
// codici di uscita G3 e conferme G8.

const (
	appsBase = "/repos/acme/web/apps"
	img      = `{"reference":"homehub.local/acme/web/web:sha-91be20d","source":"registry","verified":true,"signedBy":"acme/web CI"}`
	res      = `{"guaranteed":{"cpuMillis":250,"memoryBytes":134217728},"limit":{"cpuMillis":500,"memoryBytes":268435456}}`
)

func appJSON(name, status string, version, ready, copies int, verified bool) string {
	v := "false"
	if verified {
		v = "true"
	}
	return fmt.Sprintf(`{"id":"00000000-0000-0000-0000-0000000000b1","name":%q,"owner":"acme","repo":"web","access":"login","activeVersion":%d,`+
		`"copies":%d,"readyCopies":%d,"status":%q,"image":{"reference":"homehub.local/acme/web/%s:sha-91be20d","source":"registry","verified":%s},`+
		`"resources":%s,"createdAt":"2026-10-26T10:00:00Z","updatedAt":"2026-10-26T10:00:00Z"}`, name, version, copies, ready, status, name, v, res)
}

func versionJSON(n int, state, reason string) string {
	r := ""
	if reason != "" {
		r = fmt.Sprintf(`,"stateReason":%q`, reason)
	}
	return fmt.Sprintf(`{"number":%d,"state":%q,"trigger":"manual","image":%s,"imageAvailable":true,"inBackup":false,`+
		`"createdAt":"2026-10-26T10:00:00Z","createdBy":{"id":"00000000-0000-0000-0000-0000000000c1","username":"deploybot","type":"agent"},`+
		`"config":{"copies":2,"resources":%s,"secretNames":[],"variables":{}}%s}`, n, state, img, res, r)
}

// newSeqFake risponde con la funzione fn, che riceve il numero della chiamata a quella rotta (da 1): serve a
// far cambiare lo stato di una versione fra un sondaggio e l'altro.
func newSeqFake(t *testing.T, fn func(method, path string, n int) reply) *fake {
	t.Helper()
	fk := &fake{}
	counts := map[string]int{}
	var mu sync.Mutex
	fk.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		key := r.Method + " " + path
		mu.Lock()
		counts[key]++
		n := counts[key]
		mu.Unlock()
		fk.mu.Lock()
		fk.calls = append(fk.calls, call{r.Method, path, r.URL.RawQuery, body})
		fk.mu.Unlock()
		rep := fn(r.Method, path, n)
		if r.Header.Get("Authorization") != "Bearer tok" {
			rep = reply{401, `{"error":{"code":"unauthenticated","message":"serve autenticazione"}}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(fk.srv.Close)
	return fk
}

func notFound() reply {
	return reply{404, `{"error":{"code":"not_found","message":"non trovato"}}`}
}

// Elenco in JSON: gli stessi campi e valori del mockup.
func TestAppList_JSONComeNelMockup(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET " + appsBase: {200, `{"items":[` +
		appJSON("web", "running", 45, 2, 2, true) + `,` + appJSON("search-worker", "running", 19, 1, 1, true) + `,` + appJSON("wiki", "running", 7, 1, 1, false) + `]}`}})
	r := run(t, fk, "app", "list", "--json", "name,status,version,verified")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	var got, want []map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, r.out)
	}
	_ = json.Unmarshal([]byte(`[{"name":"web","status":"running","version":45,"verified":true},
 {"name":"search-worker","status":"running","version":19,"verified":true},
 {"name":"wiki","status":"running","version":7,"verified":false}]`), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	r = run(t, fk, "app", "list")
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if r.code != 0 || len(lines) != 3 || !strings.HasPrefix(lines[0], "web\trunning\tv45\t2/2\tverificata") || !strings.Contains(lines[2], "non verificata") {
		t.Fatalf("tabella: %+v", r)
	}
	if r := run(t, fk, "app", "list", "--json", "nope"); r.code != 2 {
		t.Fatalf("campo ignoto deve uscire 2: %+v", r)
	}
}

func TestAppStatus(t *testing.T) {
	a := strings.Replace(appJSON("web", "unhealthy", 45, 1, 2, true), `"updatedAt"`, `"lastDeploy":{"version":46,"state":"rolled_back","reason":"GET /healthz ha risposto 500 per 2 min","startedAt":"2026-10-26T10:00:00Z"},"updatedAt"`, 1)
	fk := newFake(t, map[string]reply{"GET " + appsBase + "/web": {200, a}})
	r := run(t, fk, "app", "status", "web")
	if r.code != 0 || !strings.Contains(r.out, "web · unhealthy · v45 · 1/2 copie pronte") || !strings.Contains(r.out, "ultimo deploy: v46 rolled_back · GET /healthz") {
		t.Fatalf("status: %+v", r)
	}
	r = run(t, fk, "app", "status", "web", "--json", "status,readyCopies,lastDeploy")
	if r.code != 0 || !strings.Contains(r.out, `"readyCopies":1`) || !strings.Contains(r.out, `"rolled_back"`) {
		t.Fatalf("status json: %+v", r)
	}
	if r := run(t, fk, "app", "status", "altra"); r.code != 6 {
		t.Fatalf("404 deve uscire 6: %+v", r)
	}
}

// Il giro del mockup: deploy --wait fallito (exit 1, ritorno alla versione precedente) e poi riuscito.
func TestAppDeploy_WaitCostruitoSulMockup(t *testing.T) {
	defer app.SetPollInterval(time.Millisecond)()
	// v43 fallisce con ritorno automatico alla v42.
	fk := newSeqFake(t, func(m, p string, n int) reply {
		switch m + " " + p {
		case "POST " + appsBase + "/web/deploys":
			return reply{202, versionJSON(43, "in_progress", "")}
		case "GET " + appsBase + "/web/versions/43":
			if n < 2 {
				return reply{200, versionJSON(43, "in_progress", "")}
			}
			return reply{200, versionJSON(43, "rolled_back", "health check failed for 2m")}
		case "GET " + appsBase + "/web":
			return reply{200, appJSON("web", "running", 42, 2, 2, true)}
		}
		return notFound()
	})
	r := run(t, fk, "app", "deploy", "web", "--image", "sha-91be20d", "--wait")
	want := "deploying v43 to web (2 copies) · image signed by acme/web CI\n✗ health check failed for 2m · rolled back to v42\n"
	if r.code != 1 || r.out != want {
		t.Fatalf("exit %d\n%q\nvoglio\n%q\nerr: %s", r.code, r.out, want, r.errOut)
	}
	if c := fk.find("POST", appsBase+"/web/deploys"); len(c) != 1 || c[0].Body["image"] != "sha-91be20d" {
		t.Fatalf("corpo del deploy: %+v", c)
	}

	// v45 sana.
	fk = newSeqFake(t, func(m, p string, n int) reply {
		switch m + " " + p {
		case "POST " + appsBase + "/web/deploys":
			return reply{202, versionJSON(45, "in_progress", "")}
		case "GET " + appsBase + "/web/versions/45":
			return reply{200, versionJSON(45, "serving", "")}
		case "GET " + appsBase + "/web":
			return reply{200, appJSON("web", "running", 45, 2, 2, true)}
		}
		return notFound()
	})
	r = run(t, fk, "app", "deploy", "web", "--image", "sha-91be20d", "--wait")
	if r.code != 0 || !strings.HasSuffix(r.out, "✓ v45 healthy · 2/2 copies\n") {
		t.Fatalf("exit %d: %q %s", r.code, r.out, r.errOut)
	}
	// Con --json solo il JSON della versione finale, niente righe decorative.
	r = run(t, fk, "app", "deploy", "web", "--image", "sha-91be20d", "--wait", "--json", "number,state,createdBy")
	var got map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || r.code != 0 || got["state"] != "serving" || got["number"] != float64(45) {
		t.Fatalf("json: %+v (%v)", r, err)
	}
	// L'identità dell'agente arriva fino all'output.
	if cb, _ := got["createdBy"].(map[string]any); cb["username"] != "deploybot" || cb["type"] != "agent" {
		t.Fatalf("createdBy: %v", got["createdBy"])
	}
}

func TestAppDeploy_SenzaWaitENoImmagine(t *testing.T) {
	fk := newSeqFake(t, func(m, p string, n int) reply {
		if m+" "+p == "POST "+appsBase+"/web/deploys" {
			return reply{202, versionJSON(43, "in_progress", "")}
		}
		return notFound()
	})
	r := run(t, fk, "app", "deploy", "web", "--image", "sha-1")
	if r.code != 0 || r.out != "deploying v43 to web (2 copies) · image signed by acme/web CI\n" {
		t.Fatalf("%+v", r)
	}
	if len(fk.find("GET", appsBase+"/web/versions/43")) != 0 {
		t.Fatal("senza --wait non si deve sondare")
	}
	if r := run(t, fk, "app", "deploy", "web"); r.code != 2 {
		t.Fatalf("--image mancante deve uscire 2: %+v", r)
	}
	if r := run(t, fk, "app", "deploy", "web", "--image", "x", "--wait", "--timeout", "1ms"); r.code != 1 || !strings.Contains(r.errOut, "timeout") {
		t.Fatalf("timeout: %+v", r)
	}
}

func TestAppDeploy_ErroriDellApi(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		exit   int
	}{
		{401, `{"error":{"code":"unauthenticated","message":"no"}}`, 4},
		{403, `{"error":{"code":"forbidden","message":"Serve il permesso write sul repo."}}`, 5},
		{404, `{"error":{"code":"not_found","message":"non trovato"}}`, 6},
		{409, `{"error":{"code":"deploy_in_progress","message":"Un deploy è già in corso."}}`, 1},
		{422, `{"error":{"code":"image_not_allowed","message":"Solo immagini firmate."}}`, 1},
	} {
		fk := newFake(t, map[string]reply{"POST " + appsBase + "/web/deploys": {c.status, c.body}})
		if r := run(t, fk, "app", "deploy", "web", "--image", "x"); r.code != c.exit {
			t.Errorf("HTTP %d: exit %d, atteso %d (%s)", c.status, r.code, c.exit, r.errOut)
		}
	}
	// In modalità JSON l'errore è su stderr in JSON (G3).
	fk := newFake(t, map[string]reply{"POST " + appsBase + "/web/deploys": {403, `{"error":{"code":"forbidden","message":"Serve write."}}`}})
	r := run(t, fk, "app", "deploy", "web", "--image", "x", "--json", "number")
	if r.code != 5 || r.out != "" || !strings.Contains(r.errOut, `"forbidden"`) {
		t.Fatalf("%+v", r)
	}
}

func TestAppLogs(t *testing.T) {
	nd := `{"time":"2026-10-26T18:02:24Z","copy":"web-3","source":"app","message":"panic: search index \"handbook-v2\" not found"}` + "\n" +
		`{"time":"2026-10-26T18:02:30Z","source":"platform","message":"riavvio di web-3"}` + "\n"
	fk := newFake(t, map[string]reply{"GET " + appsBase + "/web/logs": {200, nd}})
	r := run(t, fk, "app", "logs", "web", "--since", "5m", "--grep", "panic")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], `web-3  panic: search index "handbook-v2" not found`) || !strings.Contains(lines[1], " platform  riavvio di web-3") {
		t.Fatalf("righe: %q", r.out)
	}
	if len(lines[0]) < 8 || lines[0][2] != ':' || lines[0][5] != ':' {
		t.Fatalf("manca l'ora HH:MM:SS: %q", lines[0])
	}
	r = run(t, fk, "app", "logs", "web", "--json", "copy,message")
	jl := strings.Split(strings.TrimSpace(r.out), "\n")
	if r.code != 0 || len(jl) != 2 || !strings.HasPrefix(jl[0], `{"copy":"web-3","message":"panic`) {
		t.Fatalf("json: %+v", r)
	}
	if r := run(t, fk, "app", "logs", "web", "--source", "boh"); r.code != 2 {
		t.Fatalf("source non valido deve uscire 2: %+v", r)
	}
	fk = newFake(t, map[string]reply{"GET " + appsBase + "/web/logs": {403, forbidden}})
	if r := run(t, fk, "app", "logs", "web"); r.code != 5 {
		t.Fatalf("read non legge i log: %+v", r)
	}
}

func TestAppRestartRollbackScale(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"POST " + appsBase + "/web/restart":  {202, appJSON("web", "restarting", 45, 1, 2, true)},
		"POST " + appsBase + "/web/rollback": {202, versionJSON(46, "in_progress", "")},
		"PUT " + appsBase + "/web/scale":     {200, appJSON("web", "running", 45, 3, 3, true)},
	})
	if r := run(t, fk, "app", "restart", "web"); r.code != 0 || r.out != "restarting web (2 copies)\n" {
		t.Fatalf("restart: %+v", r)
	}
	r := run(t, fk, "app", "rollback", "web", "--to", "42", "--pause-auto-deploy")
	if r.code != 0 || r.out != "rolling web back to v42 as v46\n" {
		t.Fatalf("rollback: %+v", r)
	}
	if c := fk.find("POST", appsBase+"/web/rollback"); len(c) != 1 || c[0].Body["version"] != float64(42) || c[0].Body["pauseAutoDeploy"] != true {
		t.Fatalf("corpo del rollback: %+v", c)
	}
	if r := run(t, fk, "app", "rollback", "web"); r.code != 2 {
		t.Fatalf("--to mancante deve uscire 2: %+v", r)
	}
	if r := run(t, fk, "app", "scale", "web", "--copies", "3"); r.code != 0 || r.out != "web: 3 copies\n" {
		t.Fatalf("scale: %+v", r)
	}
	if c := fk.find("PUT", appsBase+"/web/scale"); len(c) != 1 || c[0].Body["copies"] != float64(3) {
		t.Fatalf("corpo dello scale: %+v", c)
	}
	if r := run(t, fk, "app", "scale", "web", "--copies", "2", "--json", "name,copies"); r.code != 0 || !strings.Contains(r.out, `"name":"web"`) {
		t.Fatalf("scale json: %+v", r)
	}
	if r := run(t, fk, "app", "scale", "web"); r.code != 2 {
		t.Fatalf("--copies mancante deve uscire 2: %+v", r)
	}
}

// Quota piena: l'errore API esce 1 con la riga «! …» del mockup.
func TestAppScale_QuotaPiena(t *testing.T) {
	fk := newFake(t, map[string]reply{"PUT " + appsBase + "/web/scale": {409, `{"error":{"code":"insufficient_capacity","message":"acme quota: 2.5 of 3 vCPU guaranteed in use · scale needs 0.5 more"}}`}})
	r := run(t, fk, "app", "scale", "web", "--copies", "3")
	if r.code != 1 || !strings.Contains(r.errOut, "! acme quota: 2.5 of 3 vCPU guaranteed in use · scale needs 0.5 more") {
		t.Fatalf("%+v", r)
	}
}

// G8: fermare l'app, togliere variabili e segreti chiedono conferma; senza terminale né --yes è un uso errato
// (exit 2) e nessuna chiamata parte.
func TestAppConfermeG8(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"PUT " + appsBase + "/web/scale":                  {200, appJSON("web", "stopped", 45, 0, 0, true)},
		"DELETE " + appsBase + "/web/variables/OLD":       {204, ``},
		"DELETE " + appsBase + "/web/secrets/DB_PASSWORD": {204, ``},
	})
	for _, args := range [][]string{
		{"app", "scale", "web", "--copies", "0"},
		{"app", "env", "unset", "web", "OLD"},
		{"app", "secret", "rm", "web", "DB_PASSWORD"},
	} {
		if r := run(t, fk, args...); r.code != 2 || !strings.Contains(r.errOut, "--yes") {
			t.Errorf("%v senza --yes: %+v", args, r)
		}
	}
	if n := api(fk); len(n) != 0 {
		t.Fatalf("nessuna chiamata prima della conferma: %+v", n)
	}
	// Con terminale: risposta sbagliata annulla (exit 1), quella giusta procede.
	if r := runWith(t, fk, true, "no\n", "app", "scale", "web", "--copies", "0"); r.code != 1 || len(api(fk)) != 0 {
		t.Fatalf("annullata: %+v", r)
	}
	if r := runWith(t, fk, true, "web\n", "app", "scale", "web", "--copies", "0"); r.code != 0 || len(fk.find("PUT", appsBase+"/web/scale")) != 1 {
		t.Fatalf("confermata: %+v", r)
	}
	if r := run(t, fk, "app", "env", "unset", "web", "OLD", "--yes"); r.code != 0 || r.out != "unset OLD\n" {
		t.Fatalf("unset: %+v", r)
	}
	if r := run(t, fk, "app", "secret", "rm", "web", "DB_PASSWORD", "-y"); r.code != 0 || r.out != "removed secret DB_PASSWORD\n" {
		t.Fatalf("secret rm: %+v", r)
	}
}

func TestAppEnvESecret(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"GET " + appsBase + "/web/variables":              {200, `{"items":[{"name":"SEARCH_INDEX","value":"handbook-v2","managed":false,"updatedAt":"2026-10-26T10:00:00Z"},{"name":"GITSTACK_USER_HEADER","value":"X-GitStack-User","managed":true,"updatedAt":"2026-10-26T10:00:00Z"}]}`},
		"PUT " + appsBase + "/web/variables/SEARCH_INDEX": {200, `{"name":"SEARCH_INDEX","value":"handbook-v2","managed":false,"updatedAt":"2026-10-26T10:00:00Z","version":44}`},
		"GET " + appsBase + "/web/secrets":                {200, `{"items":[{"name":"DB_PASSWORD","updatedAt":"2026-10-26T10:00:00Z"}]}`},
		"PUT " + appsBase + "/web/secrets/DB_PASSWORD":    {200, `{"name":"DB_PASSWORD","updatedAt":"2026-10-26T10:00:00Z","version":45}`},
	})
	if r := run(t, fk, "app", "vars", "set", "web", "SEARCH_INDEX=handbook-v2"); r.code != 0 || r.out != "set SEARCH_INDEX (v44)\n" {
		t.Fatalf("set (alias vars come nel mockup): %+v", r)
	}
	if c := fk.find("PUT", appsBase+"/web/variables/SEARCH_INDEX"); len(c) != 1 || c[0].Body["value"] != "handbook-v2" {
		t.Fatalf("corpo: %+v", c)
	}
	if r := run(t, fk, "app", "env", "set", "web", "senzauguale"); r.code != 2 {
		t.Fatalf("forma errata: %+v", r)
	}
	r := run(t, fk, "app", "env", "list", "web")
	if r.code != 0 || !strings.Contains(r.out, "SEARCH_INDEX=handbook-v2\n") || !strings.Contains(r.out, "(gestita da GitStack)") {
		t.Fatalf("list: %+v", r)
	}
	if r := run(t, fk, "app", "env", "list", "web", "--json", "name,managed"); r.code != 0 || !strings.Contains(r.out, `"managed":true`) {
		t.Fatalf("list json: %+v", r)
	}
	// Il valore del segreto parte dallo stdin e non compare mai in uscita.
	r = runWith(t, fk, false, "s3cr3t-valore\n", "app", "secret", "set", "web", "DB_PASSWORD")
	if r.code != 0 || r.out != "set secret DB_PASSWORD\n" || strings.Contains(r.out+r.errOut, "s3cr3t") {
		t.Fatalf("secret set: %+v", r)
	}
	if c := fk.find("PUT", appsBase+"/web/secrets/DB_PASSWORD"); len(c) != 1 || c[0].Body["value"] != "s3cr3t-valore" {
		t.Fatalf("corpo del segreto: %+v", c)
	}
	if r := runWith(t, fk, true, "", "app", "secret", "set", "web", "DB_PASSWORD"); r.code != 2 {
		t.Fatalf("senza valore e con terminale: %+v", r)
	}
	r = run(t, fk, "app", "secret", "list", "web", "--json", "name")
	if r.code != 0 || strings.TrimSpace(r.out) != `[{"name":"DB_PASSWORD"}]` {
		t.Fatalf("secret list: %+v", r)
	}
}

func TestAppCreate(t *testing.T) {
	created := `{"app":` + appJSON("web", "starting", 1, 0, 2, true) + `,"version":` + versionJSON(1, "in_progress", "") + `}`
	fk := newFake(t, map[string]reply{"POST " + appsBase: {202, created}})
	r := run(t, fk, "app", "create", "web", "--image", "sha-91be20d", "--port", "8080", "--copies", "2", "--health-path", "/healthz")
	if r.code != 0 || r.out != "creating web v1 (2 copies) · image signed by acme/web CI\n" {
		t.Fatalf("%+v", r)
	}
	c := fk.find("POST", appsBase)
	if len(c) != 1 || c[0].Body["name"] != "web" || c[0].Body["port"] != float64(8080) || c[0].Body["copies"] != float64(2) {
		t.Fatalf("corpo: %+v", c)
	}
	if hc, _ := c[0].Body["healthCheck"].(map[string]any); hc["path"] != "/healthz" {
		t.Fatalf("healthCheck: %+v", c[0].Body)
	}
	if r := run(t, fk, "app", "create", "web"); r.code != 2 {
		t.Fatalf("senza --image: %+v", r)
	}
	fk = newFake(t, map[string]reply{"POST " + appsBase: {403, forbidden}})
	if r := run(t, fk, "app", "create", "web", "--image", "x"); r.code != 5 {
		t.Fatalf("write non crea: %+v", r)
	}
	fk = newFake(t, map[string]reply{"POST " + appsBase: {409, `{"error":{"code":"name_taken","message":"nome già usato"}}`}})
	if r := run(t, fk, "app", "create", "web", "--image", "x"); r.code != 1 || !strings.Contains(r.errOut, "name_taken") {
		t.Fatalf("name_taken: %+v", r)
	}
}

// api sono le chiamate arrivate al gateway senza il controllo di versione di /meta (G6).
func api(fk *fake) []call {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	var out []call
	for _, c := range fk.calls {
		if c.Path != "/meta" {
			out = append(out, c)
		}
	}
	return out
}

func TestAppDelete_ConfermaEsplicita(t *testing.T) {
	fk := newFake(t, map[string]reply{"DELETE " + appsBase + "/web": {204, ""}})
	// Senza terminale e senza --yes non parte nessuna richiesta.
	if r := run(t, fk, "app", "delete", "web"); r.code != 2 || len(fk.find("DELETE", appsBase+"/web")) != 0 {
		t.Fatalf("senza conferma deve uscire 2 senza rete: %+v", r)
	}
	// Con il terminale, un nome sbagliato annulla.
	if r := runWith(t, fk, true, "altra\n", "app", "delete", "web"); r.code == 0 || len(fk.find("DELETE", appsBase+"/web")) != 0 {
		t.Fatalf("nome sbagliato deve annullare: %+v", r)
	}
	// Nome riscritto: la richiesta porta confirm=<app>.
	if r := runWith(t, fk, true, "web\n", "app", "delete", "web"); r.code != 0 || r.out != "deleted web\n" {
		t.Fatalf("conferma col nome: %+v", r)
	}
	if c := fk.find("DELETE", appsBase+"/web"); len(c) != 1 || c[0].Query != "confirm=web" {
		t.Fatalf("query: %+v", c)
	}
	if r := run(t, fk, "app", "delete", "web", "--yes"); r.code != 0 {
		t.Fatalf("--yes: %+v", r)
	}
	fk = newFake(t, map[string]reply{"DELETE " + appsBase + "/web": {403, forbidden}})
	if r := run(t, fk, "app", "delete", "web", "--yes"); r.code != 5 {
		t.Fatalf("write non elimina: %+v", r)
	}
}

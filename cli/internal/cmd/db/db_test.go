package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmd/db"
	"github.com/fathorMB/GitStack/cli/internal/cmd/root"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

// GIT-556 (v3-d): gs db contro un gateway finto, con --json, codici di uscita G3 e conferme G8.

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

type call struct {
	Method, Path string
	Query        string
	Body         map[string]any
}

type reply struct {
	status int
	body   string
}

type fake struct {
	mu     sync.Mutex
	routes map[string]reply
	calls  []call
	srv    *httptest.Server
}

func newFake(t *testing.T, routes map[string]reply) *fake {
	t.Helper()
	fk := &fake{routes: routes}
	fk.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		fk.mu.Lock()
		fk.calls = append(fk.calls, call{r.Method, path, r.URL.RawQuery, body})
		fk.mu.Unlock()
		rep, ok := fk.routes[r.Method+" "+path]
		if !ok {
			rep = reply{404, `{"error":{"code":"not_found","message":"non trovato"}}`}
		}
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

func (fk *fake) find(method, path string) []call {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	var out []call
	for _, c := range fk.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

type result struct {
	code        int
	out, errOut string
}

func runWith(t *testing.T, fk *fake, tty bool, stdin string, args ...string) result {
	t.Helper()
	io_, in, out, errOut := cmdutil.Test()
	in.WriteString(stdin)
	io_.InTTY = tty
	f := cmdutil.New("test", io_)
	env := map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok", "GS_HOST": fk.srv.URL}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	full := append([]string{"--hostname", fk.srv.URL, "-R", "acme/web"}, args...)
	code := root.Run(context.Background(), f, full)
	return result{code, out.String(), errOut.String()}
}

func run(t *testing.T, fk *fake, args ...string) result { return runWith(t, fk, false, "", args...) }

const (
	dbBase    = "/databases/acme/shop"
	idDb      = "00000000-0000-0000-0000-0000000000d1"
	idLink    = "00000000-0000-0000-0000-0000000000e1"
	idBackup  = "00000000-0000-0000-0000-0000000000f1"
	forbidden = `{"error":{"code":"forbidden","message":"Serve il permesso admin sull'owner."}}`
	dbJSON    = `{"id":"` + idDb + `","owner":"acme","name":"shop","pgMajor":17,"status":"running","sizeBytes":1073741824,"cpuMillis":500,"memoryBytes":268435456,` +
		`"lanEnabled":false,"linkCount":1,"usage":{"databaseBytes":104857600,"diskState":"none"},"operations":{"minorUpdateAvailable":false,"lastBackupAt":"2026-11-05T02:00:00Z"},` +
		`"createdAt":"2026-11-01T10:00:00Z","updatedAt":"2026-11-05T10:00:00Z"}`
	credsJSON = `{"username":"shop_owner","password":"S3gr3ta","database":"shop","sslMode":"verify-full"}`
	linkJSON  = `{"id":"` + idLink + `","app":{"owner":"acme","repo":"web","name":"api"},"mode":"rw","state":"active","createdAt":"2026-11-02T10:00:00Z"}`
	backupJS  = `{"id":"` + idBackup + `","kind":"on_demand","status":"completed","sizeBytes":5242880,"createdAt":"2026-11-04T10:00:00Z","completedAt":"2026-11-04T10:01:00Z"}`
	lanOn     = `{"enabled":true,"port":15432,"host":"home.lan","caCertificate":"-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n","ufwCommand":"ufw allow from 192.168.1.0/24 to any port 15432 proto tcp","connections":2}`
	lanOff    = `{"enabled":false,"port":null,"host":null,"caCertificate":null,"ufwCommand":null}`
)

func TestDbList(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET /databases/acme": {200, `{"items":[` + dbJSON + `]}`}})
	// L'owner viene da -R acme/web; con un argomento si può nominare un altro owner.
	r := run(t, fk, "db", "list")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	// Senza TTY le tabelle non hanno intestazione: una riga a tab per database.
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "shop\trunning\t17\t1 GB\t500m\t256 MB\toff\t1") {
		t.Fatalf("righe: %q", r.out)
	}
	r = run(t, fk, "db", "list", "acme", "--json", "name,status,sizeBytes")
	if r.code != 0 || !strings.Contains(r.out, `"shop"`) || !strings.Contains(r.out, `1073741824`) || strings.Contains(r.out, `"pgMajor"`) {
		t.Fatalf("json: %+v", r)
	}
	if r := run(t, fk, "db", "list", "--json", "nope"); r.code != 2 {
		t.Fatalf("campo ignoto deve uscire 2: %+v", r)
	}
	fk = newFake(t, map[string]reply{"GET /databases/acme": {422, `{"error":{"code":"databases_disabled","message":"I database sono spenti."}}`}})
	if r := run(t, fk, "db", "list"); r.code == 0 || !strings.Contains(r.errOut, "databases_disabled") {
		t.Fatalf("funzione spenta: %+v", r)
	}
}

func TestDbView(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET " + dbBase: {200, dbJSON}})
	r := run(t, fk, "db", "view", "acme/shop")
	if r.code != 0 || !strings.Contains(r.out, "acme/shop · running · PostgreSQL 17") || !strings.Contains(r.out, "uso: 100 MB (none)") || !strings.Contains(r.out, "ultimo backup:") {
		t.Fatalf("view: %+v", r)
	}
	r = run(t, fk, "db", "view", "shop", "--json", "status,linkCount")
	if r.code != 0 || !strings.Contains(r.out, `"status":"running"`) || !strings.Contains(r.out, `"linkCount":1`) {
		t.Fatalf("json: %+v", r)
	}
	fk = newFake(t, nil)
	if r := run(t, fk, "db", "view", "acme/shop"); r.code != 6 {
		t.Fatalf("404 deve uscire 6: %+v", r)
	}
}

func TestDbCreate(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST /databases/acme": {202, `{"database":` + dbJSON + `,"credentials":` + credsJSON + `}`}})
	r := run(t, fk, "db", "create", "acme/shop", "--pg", "17", "--size", "2GB", "--cpu", "0.5", "--memory", "512MB")
	if r.code != 0 || !strings.Contains(r.out, "creato acme/shop") || !strings.Contains(r.out, "password: S3gr3ta") {
		t.Fatalf("create: %+v", r)
	}
	c := fk.find("POST", "/databases/acme")
	if len(c) != 1 || c[0].Body["name"] != "shop" || c[0].Body["pgMajor"] != float64(17) || c[0].Body["sizeBytes"] != float64(2<<30) ||
		c[0].Body["cpuMillis"] != float64(500) || c[0].Body["memoryBytes"] != float64(512<<20) {
		t.Fatalf("corpo: %+v", c)
	}
	r = run(t, fk, "db", "create", "shop", "--pg", "16", "--json", "credentials")
	if r.code != 0 || !strings.Contains(r.out, `"password":"S3gr3ta"`) {
		t.Fatalf("json: %+v", r)
	}
	// Senza flag di risorse il corpo non li porta: valgono i default dell'API.
	if c = fk.find("POST", "/databases/acme"); len(c) != 2 || c[1].Body["sizeBytes"] != nil || c[1].Body["cpuMillis"] != nil {
		t.Fatalf("default: %+v", c)
	}
}

func TestDbCreate_Usi(t *testing.T) {
	fk := newFake(t, nil)
	for _, args := range [][]string{
		{"db", "create", "shop"},
		{"db", "create", "shop", "--pg", "15"},
		{"db", "create", "shop", "--pg", "17", "--size", "tanto"},
		{"db", "create", "shop", "--pg", "17", "--cpu", "-1"},
		{"db", "create", "a/b/c", "--pg", "17"},
	} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2 (%s)", args, r.code, r.errOut)
		}
	}
	if n := len(fk.find("POST", "/databases/acme")); n != 0 {
		t.Fatalf("un uso errato non deve creare database: %d", n)
	}
	fk = newFake(t, map[string]reply{"POST /databases/acme": {409, `{"error":{"code":"insufficient_capacity","message":"Manca disco nella quota di acme."}}`}})
	if r := run(t, fk, "db", "create", "shop", "--pg", "17", "--size", "500GB"); r.code == 0 || !strings.Contains(r.errOut, "disco") {
		t.Fatalf("capacità: %+v", r)
	}
}

func TestDbResize(t *testing.T) {
	fk := newFake(t, map[string]reply{"PATCH " + dbBase: {200, dbJSON}})
	r := run(t, fk, "db", "resize", "acme/shop", "--size", "10GB", "--cpu", "1")
	if r.code != 0 || !strings.Contains(r.out, "ridimensionato acme/shop") {
		t.Fatalf("resize: %+v", r)
	}
	c := fk.find("PATCH", dbBase)
	if len(c) != 1 || c[0].Body["sizeBytes"] != float64(10<<30) || c[0].Body["cpuMillis"] != float64(1000) || c[0].Body["memoryBytes"] != nil {
		t.Fatalf("corpo: %+v", c)
	}
	if r := run(t, fk, "db", "resize", "acme/shop"); r.code != 2 {
		t.Fatalf("senza flag: exit %d", r.code)
	}
	if r := run(t, fk, "db", "resize", "acme/shop", "--json", "sizeBytes", "--memory", "1GB"); r.code != 0 || !strings.Contains(r.out, `"sizeBytes"`) {
		t.Fatalf("json: %+v", r)
	}
}

func TestDbDelete_ConfermaG8(t *testing.T) {
	fk := newFake(t, map[string]reply{"DELETE " + dbBase: {204, ""}})
	if r := run(t, fk, "db", "delete", "acme/shop"); r.code != 2 || len(fk.find("DELETE", dbBase)) != 0 {
		t.Fatalf("senza conferma deve uscire 2 senza rete: %+v", r)
	}
	if r := runWith(t, fk, true, "shop\n", "db", "delete", "acme/shop"); r.code == 0 || len(fk.find("DELETE", dbBase)) != 0 {
		t.Fatalf("nome parziale deve annullare: %+v", r)
	}
	if r := runWith(t, fk, true, "acme/shop\n", "db", "delete", "acme/shop"); r.code != 0 || r.out != "deleted acme/shop\n" {
		t.Fatalf("conferma col nome: %+v", r)
	}
	if c := fk.find("DELETE", dbBase); len(c) != 1 || c[0].Query != "confirm=acme%2Fshop" {
		t.Fatalf("query: %+v", c)
	}
	if r := run(t, fk, "db", "delete", "shop", "--yes"); r.code != 0 {
		t.Fatalf("--yes: %+v", r)
	}
	fk = newFake(t, map[string]reply{"DELETE " + dbBase: {403, forbidden}})
	if r := run(t, fk, "db", "rm", "acme/shop", "-y"); r.code != 5 {
		t.Fatalf("non admin: %+v", r)
	}
}

func TestDbLinkEUnlink(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"POST " + dbBase + "/links":             {201, `{"link":` + linkJSON + `,"credentials":{"username":"l_e1","password":"LinkPw","host":"pg.gitstack-db-d1.svc.cluster.local","port":5432,"database":"shop","sslMode":"verify-full","sslRootcert":"/etc/ca.pem"}}`},
		"GET " + dbBase + "/links":              {200, `{"items":[` + linkJSON + `]}`},
		"DELETE " + dbBase + "/links/" + idLink: {202, strings.Replace(linkJSON, `"active"`, `"unlinking"`, 1)},
	})
	r := run(t, fk, "db", "link", "acme/shop", "--app", "acme/web/api", "--mode", "ro")
	if r.code != 0 || !strings.Contains(r.out, "collegato acme/web/api a acme/shop") || !strings.Contains(r.out, "password: LinkPw") {
		t.Fatalf("link: %+v", r)
	}
	c := fk.find("POST", dbBase+"/links")
	app, _ := c[0].Body["app"].(map[string]any)
	if len(c) != 1 || c[0].Body["mode"] != "ro" || app["owner"] != "acme" || app["repo"] != "web" || app["name"] != "api" {
		t.Fatalf("corpo: %+v", c)
	}
	r = run(t, fk, "db", "link", "acme/shop", "--list")
	if r.code != 0 || !strings.HasPrefix(r.out, "acme/web/api\trw\tactive\t") {
		t.Fatalf("list: %+v", r)
	}
	r = run(t, fk, "db", "link", "acme/shop", "--list", "--json", "app,mode")
	if r.code != 0 || !strings.Contains(r.out, `"repo":"web"`) {
		t.Fatalf("list json: %+v", r)
	}
	for _, args := range [][]string{
		{"db", "link", "acme/shop"},
		{"db", "link", "acme/shop", "--app", "web/api"},
		{"db", "link", "acme/shop", "--app", "acme/web/api", "--mode", "x"},
	} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2", args, r.code)
		}
	}
	// unlink: senza --yes e senza terminale non parte niente; con terminale serve riscrivere l'app.
	if r := run(t, fk, "db", "unlink", "acme/shop", "--app", "acme/web/api"); r.code != 2 || len(fk.find("DELETE", dbBase+"/links/"+idLink)) != 0 {
		t.Fatalf("unlink senza conferma: %+v", r)
	}
	if r := runWith(t, fk, true, "acme/web/api\n", "db", "unlink", "acme/shop", "--app", "acme/web/api"); r.code != 0 || !strings.Contains(r.out, "scollegamento di acme/web/api") {
		t.Fatalf("unlink: %+v", r)
	}
	if r := run(t, fk, "db", "unlink", "acme/shop", "--app", "acme/web/altra", "--yes"); r.code != 6 {
		t.Fatalf("app non collegata deve uscire 6: %+v", r)
	}
	if n := len(fk.find("DELETE", dbBase+"/links/"+idLink)); n != 1 {
		t.Fatalf("DELETE: %d", n)
	}
	fk = newFake(t, map[string]reply{"POST " + dbBase + "/links": {409, `{"error":{"code":"variable_conflict","message":"L'app ha già PGHOST."}}`}})
	if r := run(t, fk, "db", "link", "acme/shop", "--app", "acme/web/api"); r.code != 1 || !strings.Contains(r.errOut, "variable_conflict") {
		t.Fatalf("conflitto: %+v", r)
	}
}

func TestDbBackup(t *testing.T) {
	fk := newFake(t, map[string]reply{
		"GET " + dbBase + "/backups":  {200, `{"items":[` + backupJS + `]}`},
		"POST " + dbBase + "/backups": {202, strings.Replace(strings.Replace(backupJS, `"completed"`, `"in_progress"`, 1), `"sizeBytes":5242880,`, "", 1)},
	})
	r := run(t, fk, "db", "backup", "list", "acme/shop")
	if r.code != 0 || !strings.HasPrefix(r.out, idBackup+"\ton_demand\tcompleted\t5 MB\t") {
		t.Fatalf("list: %+v", r)
	}
	if r := run(t, fk, "db", "backup", "list", "acme/shop", "--json", "id,status"); r.code != 0 || !strings.Contains(r.out, `"completed"`) {
		t.Fatalf("list json: %+v", r)
	}
	r = run(t, fk, "db", "backup", "run", "acme/shop")
	if r.code != 0 || !strings.Contains(r.out, "backup "+idBackup+" avviato (in_progress)") {
		t.Fatalf("run: %+v", r)
	}
	fk2 := newFake(t, map[string]reply{"POST " + dbBase + "/backups": {409, `{"error":{"code":"backup_in_progress","message":"Un dump è già in corso."}}`}})
	if r := run(t, fk2, "db", "backup", "run", "acme/shop"); r.code != 1 || !strings.Contains(r.errOut, "backup_in_progress") {
		t.Fatalf("in corso: %+v", r)
	}
}

func TestDbBackupRestore_Conferma(t *testing.T) {
	path := dbBase + "/backups/" + idBackup + "/restore"
	fk := newFake(t, map[string]reply{"POST " + path: {202, dbJSON}})
	// Senza terminale e senza --yes: exit 2 e nessuna chiamata.
	if r := run(t, fk, "db", "backup", "restore", "acme/shop", idBackup); r.code != 2 || len(fk.find("POST", path)) != 0 {
		t.Fatalf("senza conferma: %+v", r)
	}
	// Sostituzione: serve riscrivere owner/nome.
	if r := runWith(t, fk, true, "shop\n", "db", "backup", "restore", "acme/shop", idBackup); r.code == 0 || len(fk.find("POST", path)) != 0 {
		t.Fatalf("nome sbagliato: %+v", r)
	}
	if r := runWith(t, fk, true, "acme/shop\n", "db", "backup", "restore", "acme/shop", idBackup); r.code != 0 || !strings.Contains(r.out, "ripristino avviato in acme/shop") {
		t.Fatalf("conferma: %+v", r)
	}
	c := fk.find("POST", path)
	if len(c) != 1 || c[0].Body["target"] != "same" || c[0].Body["confirm"] != "acme/shop" {
		t.Fatalf("corpo same: %+v", c)
	}
	// --new in un database con un altro nome: --yes, nessuna `confirm`.
	if r := run(t, fk, "db", "backup", "restore", "acme/shop", idBackup, "--new", "--name", "shop-copia", "--yes"); r.code != 0 {
		t.Fatalf("new: %+v", r)
	}
	if c = fk.find("POST", path); len(c) != 2 || c[1].Body["target"] != "new" || c[1].Body["name"] != "shop-copia" || c[1].Body["confirm"] != nil {
		t.Fatalf("corpo new: %+v", c)
	}
	// --yes anche per la sostituzione.
	if r := run(t, fk, "db", "backup", "restore", "acme/shop", idBackup, "-y", "--json", "status"); r.code != 0 || !strings.Contains(r.out, `"running"`) {
		t.Fatalf("--yes: %+v", r)
	}
	for _, args := range [][]string{
		{"db", "backup", "restore", "acme/shop", "non-un-id", "--yes"},
		{"db", "backup", "restore", "acme/shop", idBackup, "--name", "x", "--yes"},
	} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2", args, r.code)
		}
	}
}

func TestDbAccessLan(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET " + dbBase + "/lan": {200, lanOn}, "PUT " + dbBase + "/lan": {200, lanOn}})
	r := run(t, fk, "db", "access", "lan", "acme/shop")
	if r.code != 0 || !strings.Contains(r.out, "lan: on") || !strings.Contains(r.out, "home.lan:15432") || !strings.Contains(r.out, "ufw allow") {
		t.Fatalf("stato: %+v", r)
	}
	r = run(t, fk, "db", "access", "lan", "acme/shop", "on")
	if r.code != 0 || !strings.Contains(r.out, "home.lan:15432") {
		t.Fatalf("on: %+v", r)
	}
	if c := fk.find("PUT", dbBase+"/lan"); len(c) != 1 || c[0].Body["enabled"] != true {
		t.Fatalf("corpo on: %+v", c)
	}
	fk = newFake(t, map[string]reply{"PUT " + dbBase + "/lan": {200, lanOff}})
	if r := run(t, fk, "db", "access", "lan", "acme/shop", "off"); r.code != 0 || !strings.Contains(r.out, "lan: off") {
		t.Fatalf("off: %+v", r)
	}
	if c := fk.find("PUT", dbBase+"/lan"); len(c) != 1 || c[0].Body["enabled"] != false {
		t.Fatalf("corpo off: %+v", c)
	}
	if r := run(t, fk, "db", "access", "lan", "acme/shop", "off", "--json", "enabled,port"); r.code != 0 || !strings.Contains(r.out, `"enabled":false`) {
		t.Fatalf("json: %+v", r)
	}
	if r := run(t, fk, "db", "access", "lan", "acme/shop", "forse"); r.code != 2 {
		t.Fatalf("valore errato: %+v", r)
	}
}

func TestDbAccessLan_ChiusuraPendente(t *testing.T) {
	fk := newFake(t, map[string]reply{"PUT " + dbBase + "/lan": {503, `{"error":{"code":"lan_close_failed","message":"Kubernetes non ha tolto il Service."}}`}})
	r := run(t, fk, "db", "access", "lan", "acme/shop", "off")
	if r.code != 1 || !strings.Contains(r.errOut, "chiusura è pendente") || !strings.Contains(r.errOut, "porta resta riservata") || !strings.Contains(r.errOut, "Ripeti") {
		t.Fatalf("503: %+v", r)
	}
	r = run(t, fk, "db", "access", "lan", "acme/shop", "off", "--json", "enabled")
	if r.code != 1 || !strings.Contains(r.errOut, `"lan_close_failed"`) {
		t.Fatalf("503 json: %+v", r)
	}
	fk = newFake(t, map[string]reply{"PUT " + dbBase + "/lan": {409, `{"error":{"code":"port_unavailable","message":"Porta già usata."}}`}})
	if r := run(t, fk, "db", "access", "lan", "acme/shop", "on"); r.code != 1 || !strings.Contains(r.errOut, "port_unavailable") {
		t.Fatalf("porta: %+v", r)
	}
}

func TestDbCredentialsRotate(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST " + dbBase + "/credentials/rotate": {200, `{"database":` + dbJSON + `,"credentials":` + credsJSON + `}`}})
	r := run(t, fk, "db", "credentials", "rotate", "acme/shop")
	if r.code != 0 || !strings.Contains(r.out, "ruotata la password di acme/shop") || !strings.Contains(r.out, "password: S3gr3ta") || !strings.Contains(r.errOut, "non si rilegge") {
		t.Fatalf("rotate: %+v", r)
	}
	if r := run(t, fk, "db", "credentials", "rotate", "shop", "--json", "credentials"); r.code != 0 || !strings.Contains(r.out, `"username":"shop_owner"`) {
		t.Fatalf("json: %+v", r)
	}
	fk = newFake(t, map[string]reply{"POST " + dbBase + "/credentials/rotate": {403, forbidden}})
	if r := run(t, fk, "db", "credentials", "rotate", "acme/shop"); r.code != 5 {
		t.Fatalf("non admin: %+v", r)
	}
}

func TestDbConnect(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET " + dbBase + "/lan": {200, lanOn}})
	var gotPath string
	var gotArgs []string
	var caContent string
	restore := db.SetPsql(
		func(string) (string, error) { return "/usr/bin/psql", nil },
		func(path string, args, _ []string, _ *cmdutil.Factory) error {
			gotPath, gotArgs = path, args
			for _, kv := range strings.Fields(args[0]) {
				if p, ok := strings.CutPrefix(kv, "sslrootcert="); ok {
					b, err := os.ReadFile(p)
					if err != nil {
						t.Errorf("CA non leggibile durante psql: %v", err)
					}
					caContent = string(b)
				}
			}
			return nil
		})
	defer restore()
	r := run(t, fk, "db", "connect", "acme/shop", "--", "-c", "select 1")
	if r.code != 0 {
		t.Fatalf("connect: %+v", r)
	}
	conn := gotArgs[0]
	for _, want := range []string{"host=home.lan", "port=15432", "dbname=shop", "user=shop_owner", "sslmode=verify-full"} {
		if !strings.Contains(conn, want) {
			t.Errorf("manca %q in %q", want, conn)
		}
	}
	if gotPath != "/usr/bin/psql" || strings.Join(gotArgs[1:], " ") != "-c select 1" || !strings.Contains(caContent, "BEGIN CERTIFICATE") {
		t.Fatalf("path %q args %q ca %q", gotPath, gotArgs, caContent)
	}
	if strings.Contains(conn, "password") {
		t.Errorf("la password non deve comparire: %q", conn)
	}
	for _, kv := range strings.Fields(conn) {
		if p, ok := strings.CutPrefix(kv, "sslrootcert="); ok {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("la CA temporanea doveva sparire: %s", p)
			}
		}
	}
}

func TestDbConnect_SenzaPsqlOLanSpenta(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET " + dbBase + "/lan": {200, lanOn}})
	restore := db.SetPsql(func(string) (string, error) { return "", errors.New("not found") }, nil)
	r := run(t, fk, "db", "connect", "acme/shop")
	restore()
	if r.code != 1 || !strings.Contains(r.errOut, "psql non è nel PATH") || !strings.Contains(r.errOut, "home.lan:15432") {
		t.Fatalf("senza psql: %+v", r)
	}
	fk = newFake(t, map[string]reply{"GET " + dbBase + "/lan": {200, lanOff}})
	called := false
	restore = db.SetPsql(func(string) (string, error) { return "/psql", nil }, func(string, []string, []string, *cmdutil.Factory) error { called = true; return nil })
	defer restore()
	if r := run(t, fk, "db", "connect", "acme/shop"); r.code != 1 || !strings.Contains(r.errOut, "access lan acme/shop on") || called {
		t.Fatalf("lan spenta: %+v called=%v", r, called)
	}
	if r := run(t, fk, "db", "connect", "acme/shop", "-c", "x"); r.code != 2 {
		t.Fatalf("argomenti senza --: %+v", r)
	}
}

func TestDbSenzaOwner(t *testing.T) {
	// Senza -R, senza --owner e con un nome nudo non si sa di chi sia il database.
	fk := newFake(t, nil)
	io_, _, out, errOut := cmdutil.Test()
	f := cmdutil.New("test", io_)
	env := map[string]string{config.EnvConfigDir: t.TempDir(), config.EnvToken: "tok"}
	f.Getenv = func(k string) string { return env[k] }
	f.Git = noGit{}
	code := root.Run(context.Background(), f, []string{"--hostname", fk.srv.URL, "db", "view", "shop"})
	if code != 2 || !strings.Contains(errOut.String(), "owner") {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errOut.String())
	}
}

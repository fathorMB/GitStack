//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/appenv"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// newAppEnvWithKeys è newAppsEnv con la chiave dei segreti (GITSTACK_WEBHOOK_SECRET_KEY), come in
// produzione: senza, i segreti delle app rispondono 503.
func newAppEnvWithKeys(t *testing.T) *appsEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	e := &issuesEnv{t: t, pool: pool, id: id}
	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	git := newFakeGit()
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now),
		httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys}))
	return &appsEnv{issuesEnv: e, clock: clock, git: git}
}

// assertNoSecret: il valore del segreto non sta in nessuna risposta.
func assertNoSecret(t *testing.T, where, body, secret string) {
	t.Helper()
	if strings.Contains(body, secret) {
		t.Fatalf("%s riporta il valore del segreto: %s", where, body)
	}
}

// GIT-348 (H8): write legge le variabili ma non i segreti; un segreto non torna mai da nessuna GET.
func TestAppEnv_WriteLeggeVariabiliNonSegreti(t *testing.T) {
	e := newAppEnvWithKeys(t)
	repoID := e.repo("handbook", true) // internal: carol legge
	e.id.grantWrite(repoID, "bob")
	e.repo("riservato", false)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	base := "/repos/alice/handbook/apps/web"
	const secret = "s3cr3t-sentinella" // valore sentinella: non deve comparire in nessuna risposta

	// admin imposta una variabile e un segreto: ognuno crea una versione.
	e.want(e.do(http.MethodPut, base+"/variables/SEARCH_TIMEOUT", "alice", `{"value":"30"}`), http.StatusOK, "")
	rec := e.do(http.MethodPut, base+"/secrets/DB_PASSWORD", "alice", `{"value":"`+secret+`"}`)
	e.want(rec, http.StatusOK, "")
	assertNoSecret(t, "PUT del segreto", rec.Body.String(), secret)
	if !strings.Contains(rec.Body.String(), `"name":"DB_PASSWORD"`) || strings.Contains(rec.Body.String(), `"value"`) {
		t.Fatalf("PUT del segreto: solo metadati attesi: %s", rec.Body.String())
	}

	// write: variabili sì (con il valore), segreti e scritture no.
	rec = e.do(http.MethodGet, base+"/variables", "bob", "")
	e.want(rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"value":"30"`) {
		t.Fatalf("write deve leggere le variabili: %s", rec.Body.String())
	}
	e.want(e.do(http.MethodGet, base+"/secrets", "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, base+"/secrets/DB_PASSWORD", "bob", `{"value":"x"}`), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodDelete, base+"/secrets/DB_PASSWORD", "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, base+"/variables/SEARCH_TIMEOUT", "bob", `{"value":"1"}`), http.StatusForbidden, "forbidden")
	// read non legge le variabili (serve write); un repo che non si legge è 404.
	e.want(e.do(http.MethodGet, base+"/variables", "carol", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, "/repos/alice/riservato/apps/web/variables", "carol", ""), http.StatusNotFound, "")

	// Nessuna GET riporta il valore del segreto, né come admin né come write: dettaglio dell'app,
	// versioni, variabili ed elenco dei segreti (write riceve 403 lì, ma il corpo non deve averlo).
	for _, user := range []string{"alice", "bob"} {
		for _, p := range []string{"", "/versions", "/versions/1", "/versions/2", "/versions/3", "/variables", "/secrets"} {
			rec = e.do(http.MethodGet, base+p, user, "")
			if rec.Code != http.StatusOK && rec.Code != http.StatusForbidden {
				t.Fatalf("GET %s come %s: %d %s", p, user, rec.Code, rec.Body.String())
			}
			assertNoSecret(t, "GET "+p+" come "+user, rec.Body.String(), secret)
		}
	}
	// Anche il corpo delle versioni con il segreto (secretNames) non riporta il valore.
	rec = e.do(http.MethodGet, base+"/versions/3", "alice", "")
	e.want(rec, http.StatusOK, "")
	assertNoSecret(t, "GET versione 3", rec.Body.String(), secret)
	rec = e.do(http.MethodGet, base+"/versions/3", "bob", "")
	if !strings.Contains(rec.Body.String(), `"secretNames":["DB_PASSWORD"]`) {
		t.Fatalf("la versione deve dire il nome del segreto, non il valore: %s", rec.Body.String())
	}
	rec = e.do(http.MethodGet, base+"/secrets", "alice", "")
	e.want(rec, http.StatusOK, "")
	assertNoSecret(t, "GET /secrets", rec.Body.String(), secret)

	// Nel database il valore è cifrato.
	var ct []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT ciphertext FROM core.app_secrets WHERE name = 'DB_PASSWORD'`).Scan(&ct); err != nil || len(ct) == 0 || strings.Contains(string(ct), secret) {
		t.Fatalf("segreto nel database: %v", err)
	}

	// Un nome non può essere variabile e segreto insieme; eliminazione e 404 dopo.
	e.want(e.do(http.MethodPut, base+"/variables/DB_PASSWORD", "alice", `{"value":"x"}`), http.StatusConflict, "name_in_use")
	e.want(e.do(http.MethodPut, base+"/secrets/SEARCH_TIMEOUT", "alice", `{"value":"x"}`), http.StatusConflict, "name_in_use")
	e.want(e.do(http.MethodPut, base+"/secrets/DB_PASSWORD", "alice", `{"value":""}`), http.StatusUnprocessableEntity, "")
	e.want(e.do(http.MethodPut, base+"/variables/1X", "alice", `{"value":"x"}`), http.StatusUnprocessableEntity, "")
	e.want(e.do(http.MethodDelete, base+"/variables/SEARCH_TIMEOUT", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, base+"/variables/SEARCH_TIMEOUT", "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodDelete, base+"/secrets/DB_PASSWORD", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, base+"/secrets/DB_PASSWORD", "alice", ""), http.StatusNotFound, "not_found")
}

// GIT-348: i segreti della CI non sono visibili all'app e viceversa, anche con lo stesso nome; il
// valore di un segreto dell'app compare mascherato nei log.
func TestAppEnv_SegretiCIESegretiAppSeparati(t *testing.T) {
	e := newAppEnvWithKeys(t)
	repoID := e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	base := "/repos/alice/handbook/apps/web"
	const ciValue, appValue = "valore-ci-111", "valore-app-222"

	e.want(e.do(http.MethodPut, "/repos/alice/handbook/actions/secrets", "alice", `{"name":"DEPLOY_KEY","value":"`+ciValue+`"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPut, base+"/secrets/DEPLOY_KEY", "alice", `{"value":"`+appValue+`"}`), http.StatusOK, "")

	// Ogni elenco vede solo i suoi: due righe, una per spazio.
	var ciList openapi.ActionsSecretList
	rec := e.do(http.MethodGet, "/repos/alice/handbook/actions/secrets", "alice", "")
	e.want(rec, http.StatusOK, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &ciList); err != nil || len(ciList.Items) != 1 {
		t.Fatalf("segreti CI: %v %s", err, rec.Body.String())
	}
	assertNoSecret(t, "GET segreti CI", rec.Body.String(), appValue)
	var appList openapi.AppSecretList
	rec = e.do(http.MethodGet, base+"/secrets", "alice", "")
	e.want(rec, http.StatusOK, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &appList); err != nil || len(appList.Items) != 1 {
		t.Fatalf("segreti app: %v %s", err, rec.Body.String())
	}
	assertNoSecret(t, "GET segreti app", rec.Body.String(), appValue)

	// Il job della CI riceve solo il suo segreto, in chiaro; l'app solo il suo.
	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	env, err := ci.LoadJobEnv(context.Background(), e.pool, keys, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if env.Secrets["DEPLOY_KEY"] != ciValue || len(env.Secrets) != 1 {
		t.Fatalf("il job della CI deve avere solo il segreto CI: %v", env.Secrets)
	}
	var appID uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM core.apps WHERE name = 'web'`).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	appSecrets, err := store.New(e.pool).AppSecretValues(context.Background(), keys, appID)
	if err != nil {
		t.Fatal(err)
	}
	if len(appSecrets) != 1 || appSecrets["DEPLOY_KEY"] != appValue {
		t.Fatalf("l'app deve avere solo il suo segreto: %v", appSecrets)
	}

	// Il valore dell'app, stampato, esce mascherato; quello della CI non è coinvolto.
	got := appenv.Masker(appSecrets).Mask("deploy con " + appValue)
	if strings.Contains(got, appValue) || !strings.Contains(got, ci.MaskedPlaceholder) {
		t.Fatalf("log non mascherato: %q", got)
	}

	// Cancellare il segreto CI non tocca quello dell'app, e viceversa.
	e.want(e.do(http.MethodDelete, "/repos/alice/handbook/actions/secrets/DEPLOY_KEY", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodGet, base+"/secrets", "alice", ""), http.StatusOK, "")
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_secrets`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("segreto dell'app dopo la cancellazione di quello CI: %d %v", n, err)
	}
}

// GIT-348 (H8): ogni versione copia variabili e nomi dei segreti; una modifica di porta non li
// cancella dalla storia.
func TestAppEnv_VersioniCopianoVariabiliESegreti(t *testing.T) {
	e := newAppEnvWithKeys(t)
	e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	base := "/repos/alice/handbook/apps/web"
	e.want(e.do(http.MethodPut, base+"/variables/REGION", "alice", `{"value":"eu"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPut, base+"/secrets/TOKEN", "alice", `{"value":"abc-token-999"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPatch, base, "alice", `{"port":9090}`), http.StatusOK, "")

	// versione 4: la modifica della porta, con variabili e nomi dei segreti copiati.
	v := e.versionOf(base+"/versions/4", "alice")
	if v.Config == nil || v.Config.Variables["REGION"] != "eu" || len(v.Config.SecretNames) != 1 || v.Config.SecretNames[0] != "TOKEN" {
		t.Fatalf("la versione 4 perde variabili o segreti: %+v", v.Config)
	}
	rec := e.do(http.MethodGet, base+"/versions/4", "alice", "")
	assertNoSecret(t, "versione 4", rec.Body.String(), "abc-token-999")
}

// GIT-348: senza la chiave dei segreti (GITSTACK_WEBHOOK_SECRET_KEY) un segreto risponde 503, mentre
// le variabili funzionano lo stesso.
func TestAppEnv_SenzaChiaveSegretiNon503SuVariabili(t *testing.T) {
	e := newAppsEnv(t)
	e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	base := "/repos/alice/handbook/apps/web"
	e.want(e.do(http.MethodPut, base+"/secrets/TOKEN", "alice", `{"value":"x"}`), http.StatusServiceUnavailable, "app_secrets_unavailable")
	e.want(e.do(http.MethodPut, base+"/variables/REGION", "alice", `{"value":"eu"}`), http.StatusOK, "")
	e.want(e.do(http.MethodGet, base+"/secrets", "alice", ""), http.StatusOK, "")
}

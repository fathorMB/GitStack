//go:build integration

package httpserver_test

import (
	"context"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-356 (v3-b, H3): volumi persistenti, quota disco, copia unica. Senza cluster: il controller è
// un finto che non fa nulla, i deploy restano «in corso» finché il test non li chiude.

const gib = int64(1 << 30)

func (e *appsEnv) finishDeploys() {
	e.t.Helper()
	e.sql(`UPDATE core.app_versions SET state = 'superseded' WHERE state = 'serving'`)
	e.sql(`UPDATE core.app_versions SET state = 'serving' WHERE state = 'in_progress'`)
	e.sql(`UPDATE core.app_deploys SET state = 'serving', finished_at = now() WHERE state = 'in_progress'`)
	e.sql(`UPDATE core.apps a SET active_version = (SELECT max(number) FROM core.app_versions v WHERE v.app_id = a.id AND v.state = 'serving')`)
}

func (e *appsEnv) volumeChange(rec interface{ Bytes() []byte }) openapi.AppVolumeChange {
	e.t.Helper()
	var c openapi.AppVolumeChange
	if err := json.Unmarshal(rec.Bytes(), &c); err != nil {
		e.t.Fatalf("esito non valido: %v: %s", err, rec.Bytes())
	}
	return c
}

func volumeBody(name, mount string, size int64) string {
	b, _ := json.Marshal(map[string]any{"name": name, "mountPath": mount, "sizeBytes": size})
	return string(b)
}

func TestAppVolumes_PermessiValidazioneEVersione(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(commitNoopController{}))
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	base := "/repos/alice/handbook/apps/web"
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.finishDeploys()

	// write e read non aggiungono; read elenca.
	e.want(e.do(http.MethodPost, base+"/volumes", "bob", volumeBody("data", "/data", gib)), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPost, base+"/volumes", "carol", volumeBody("data", "/data", gib)), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodDelete, base+"/volumes/data", "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, base+"/volumes", "carol", ""), http.StatusOK, "")

	// Validazione: nome, percorso, dimensione.
	for _, bad := range []string{
		volumeBody("Data", "/data", gib), volumeBody("data", "data", gib), volumeBody("data", "/", gib), volumeBody("data", "/a/../b", gib),
		volumeBody("data", "/proc/x", gib), volumeBody("data", "/data/", gib), volumeBody("data", "/data", 10), volumeBody("data", "/dev", gib),
	} {
		e.want(e.do(http.MethodPost, base+"/volumes", "alice", bad), http.StatusUnprocessableEntity, "")
	}

	rec := e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("data", "/var/lib/data", 2*gib))
	e.want(rec, http.StatusAccepted, "")
	ch := e.volumeChange(rec.Body)
	if ch.Volume.Name != "data" || ch.Volume.SizeBytes != 2*gib || ch.Version.Number != 2 || ch.Version.Trigger != openapi.AppVersionTriggerConfig ||
		ch.Version.Summary == nil || *ch.Version.Summary != "volume data aggiunto" || ch.Version.Config == nil || ch.Version.Config.Volumes == nil ||
		len(*ch.Version.Config.Volumes) != 1 || (*ch.Version.Config.Volumes)[0].MountPath != "/var/lib/data" {
		t.Fatalf("esito: %s", rec.Body.String())
	}
	if ch.Quota.Used.DiskBytes != 2*gib || ch.Quota.Warning {
		t.Fatalf("quota: %+v", ch.Quota)
	}
	// Il deploy della versione 2 è in corso (ogni modifica ai volumi crea una versione).
	a := e.app(e.do(http.MethodGet, base, "carol", ""))
	if a.LastDeploy == nil || a.LastDeploy.Version != 2 || a.Volumes == nil || len(*a.Volumes) != 1 || (*a.Volumes)[0].Name != "data" {
		t.Fatalf("app: %+v", a)
	}

	// Nome e percorso già usati.
	e.want(e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("data", "/altro", gib)), http.StatusConflict, "volume_exists")
	e.want(e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("altro", "/var/lib/data", gib)), http.StatusConflict, "mount_path_in_use")
	// Volume su app che non c'è.
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps/nessuna/volumes", "alice", volumeBody("x", "/x", gib)), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, base+"/volumes/non-esiste", "alice", ""), http.StatusNotFound, "")

	// Elenco con quota.
	rec = e.do(http.MethodGet, base+"/volumes", "carol", "")
	var l openapi.AppVolumeList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Items) != 1 || l.Quota.Used.DiskBytes != 2*gib || l.Quota.Limits.DiskBytes != 20*gib {
		t.Fatalf("elenco: %v %s", err, rec.Body.String())
	}

	// Elimina: nuova versione senza il volume, quota liberata.
	e.finishDeploys()
	rec = e.do(http.MethodDelete, base+"/volumes/data", "alice", "")
	e.want(rec, http.StatusAccepted, "")
	ch = e.volumeChange(rec.Body)
	if ch.Version.Number != 3 || ch.Version.Summary == nil || *ch.Version.Summary != "volume data rimosso" || ch.Quota.Used.DiskBytes != 0 {
		t.Fatalf("eliminazione: %s", rec.Body.String())
	}
	if ch.Version.Config != nil && ch.Version.Config.Volumes != nil && len(*ch.Version.Config.Volumes) != 0 {
		t.Fatalf("la versione 3 non ha volumi: %s", rec.Body.String())
	}
}

func TestAppVolumes_QuotaDiscoEAvvisoAll80(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(commitNoopController{}))
	e.id.sysAdmin[users["carol"]] = true
	e.repo("handbook", true)
	base := "/repos/alice/handbook/apps/web"
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.finishDeploys()

	// Quota di default: 20 GiB. 10 GiB: 50%, nessun avviso.
	ch := e.volumeChange(mustBody(t, e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("a", "/a", 10*gib)), http.StatusAccepted, e))
	if ch.Quota.Warning {
		t.Fatalf("a metà quota non c'è avviso: %+v", ch.Quota)
	}
	e.finishDeploys()
	// 17 GiB: 85%, avviso.
	ch = e.volumeChange(mustBody(t, e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("b", "/b", 7*gib)), http.StatusAccepted, e))
	if !ch.Quota.Warning || ch.Quota.Used.DiskBytes != 17*gib {
		t.Fatalf("oltre l'80%% serve l'avviso: %+v", ch.Quota)
	}
	e.finishDeploys()
	// Il quarto di GiB oltre il limite: rifiutato con quanto manca, niente versione né volume.
	rec := e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("c", "/c", 4*gib))
	e.want(rec, http.StatusConflict, "insufficient_capacity")
	if !strings.Contains(rec.Body.String(), `"diskBytes":`+itoa64(gib)) || !strings.Contains(rec.Body.String(), "quota disco") {
		t.Fatalf("il rifiuto dice quanto manca (1 GiB): %s", rec.Body.String())
	}
	if n := e.app(e.do(http.MethodGet, base, "alice", "")); n.LastDeploy == nil || n.LastDeploy.Version != 3 || n.Volumes == nil || len(*n.Volumes) != 2 {
		t.Fatalf("il rifiuto non deve scrivere nulla: %+v", n)
	}
	// Con esattamente il resto della quota entra (3 GiB → 100%).
	e.want(e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("c", "/c", 3*gib)), http.StatusAccepted, "")

	// L'admin non può portare la quota sotto l'uso dei volumi.
	e.want(e.do(http.MethodPut, "/admin/apps/quotas/alice", "carol", `{"cpuMillis":4000,"memoryBytes":8589934592,"diskBytes":1073741824}`), http.StatusUnprocessableEntity, "quota_below_usage")
	// Il volume tolto libera la quota.
	e.finishDeploys()
	e.want(e.do(http.MethodDelete, base+"/volumes/a", "alice", ""), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("d", "/d", 4*gib)), http.StatusAccepted, "")
}

func TestAppVolumes_CopiaUnica(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(commitNoopController{}))
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	base := "/repos/alice/handbook/apps/web"
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.finishDeploys()

	// Con due copie un volume non si aggiunge: prima si scende a una.
	e.want(e.do(http.MethodPut, base+"/scale", "bob", `{"copies":2}`), http.StatusOK, "")
	rec := e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("data", "/data", gib))
	e.want(rec, http.StatusConflict, "single_copy")
	e.want(e.do(http.MethodPut, base+"/scale", "bob", `{"copies":1}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("data", "/data", gib)), http.StatusAccepted, "")
	e.finishDeploys()

	// Con un volume la scala oltre 1 è rifiutata con un messaggio chiaro; 0 e 1 restano ammessi.
	rec = e.do(http.MethodPut, base+"/scale", "bob", `{"copies":2}`)
	e.want(rec, http.StatusConflict, "single_copy")
	if !strings.Contains(rec.Body.String(), "una sola copia") {
		t.Fatalf("messaggio poco chiaro: %s", rec.Body.String())
	}
	if a := e.app(e.do(http.MethodGet, base, "alice", "")); a.Copies != 1 {
		t.Fatalf("copie dopo il rifiuto: %d", a.Copies)
	}
	e.want(e.do(http.MethodPut, base+"/scale", "bob", `{"copies":0}`), http.StatusOK, "")
	e.want(e.do(http.MethodPut, base+"/scale", "bob", `{"copies":1}`), http.StatusOK, "")
	// Tolto il volume, la scala a 2 torna possibile.
	e.want(e.do(http.MethodDelete, base+"/volumes/data", "alice", ""), http.StatusAccepted, "")
	e.want(e.do(http.MethodPut, base+"/scale", "bob", `{"copies":2}`), http.StatusOK, "")
}

// I volumi sono dati: deploy e rollback li portano con sé, senza toglierli né rimetterli.
func TestAppVolumes_DeployERollbackConservano(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(commitNoopController{}))
	e.repo("handbook", true)
	base := "/repos/alice/handbook/apps/web"
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.finishDeploys()
	e.want(e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("data", "/data", gib)), http.StatusAccepted, "")
	e.finishDeploys()

	e.want(e.do(http.MethodPost, base+"/deploys", "alice", `{"image":"sha-2"}`), http.StatusAccepted, "")
	e.finishDeploys()
	v := e.versionOf(base+"/versions/3", "alice")
	if v.Config.Volumes == nil || len(*v.Config.Volumes) != 1 {
		t.Fatalf("il deploy porta i volumi: %+v", v.Config)
	}
	// Rollback alla versione 1 (nata senza volumi): i volumi restano.
	e.want(e.do(http.MethodPost, base+"/rollback", "alice", `{"version":1}`), http.StatusAccepted, "")
	v = e.versionOf(base+"/versions/4", "alice")
	if v.Config.Volumes == nil || len(*v.Config.Volumes) != 1 || (*v.Config.Volumes)[0].Name != "data" {
		t.Fatalf("il rollback non toglie i volumi: %+v", v.Config)
	}
	// Anche una modifica di porta li copia nella versione.
	e.finishDeploys()
	e.want(e.do(http.MethodPatch, base, "alice", `{"port":9090}`), http.StatusOK, "")
	v = e.versionOf(base+"/versions/5", "alice")
	if v.Config.Volumes == nil || len(*v.Config.Volumes) != 1 {
		t.Fatalf("la versione di configurazione porta i volumi: %+v", v.Config)
	}
}

func mustBody(t *testing.T, rec *httptest.ResponseRecorder, status int, e *appsEnv) *bytes.Buffer {
	t.Helper()
	e.want(rec, status, "")
	return rec.Body
}

// GIT-358 (H5, v3-b): eliminare un'app con volumi elimina anche i dati e richiede la conferma
// esplicita col nome dell'app; senza volumi la conferma non serve.
func TestAppDelete_ConVolumiRichiedeConferma(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(commitNoopController{}))
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	base := "/repos/alice/handbook/apps/web"
	// Senza volumi basta la richiesta.
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", strings.Replace(createWeb, `"web"`, `"light"`, 1)), http.StatusAccepted, "")
	e.finishDeploys()
	e.want(e.do(http.MethodDelete, "/repos/alice/handbook/apps/light", "alice", ""), http.StatusNoContent, "")

	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.finishDeploys()

	e.want(e.do(http.MethodPost, base+"/volumes", "alice", volumeBody("data", "/var/lib/data", gib)), http.StatusAccepted, "")
	e.finishDeploys()

	// Permesso: write non elimina, con o senza conferma.
	e.want(e.do(http.MethodDelete, base+"?confirm=web", "bob", ""), http.StatusForbidden, "forbidden")
	// Conferma mancante, vuota o sbagliata: 422 e non si tocca niente.
	e.want(e.do(http.MethodDelete, base, "alice", ""), http.StatusUnprocessableEntity, "confirmation_required")
	e.want(e.do(http.MethodDelete, base+"?confirm=", "alice", ""), http.StatusUnprocessableEntity, "confirmation_required")
	e.want(e.do(http.MethodDelete, base+"?confirm=altra", "alice", ""), http.StatusUnprocessableEntity, "confirmation_mismatch")
	e.want(e.do(http.MethodGet, base+"/volumes", "alice", ""), http.StatusOK, "")
	if rec := e.do(http.MethodGet, base+"/volumes", "alice", ""); !strings.Contains(rec.Body.String(), `"data"`) {
		t.Fatalf("il volume deve esserci ancora: %s", rec.Body.String())
	}
	// Conferma col nome: l'app e i volumi spariscono, la quota disco torna a zero.
	e.want(e.do(http.MethodDelete, base+"?confirm=web", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodGet, base, "alice", ""), http.StatusNotFound, "")
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_volumes`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("volumi rimasti: %d (%v)", n, err)
	}
}

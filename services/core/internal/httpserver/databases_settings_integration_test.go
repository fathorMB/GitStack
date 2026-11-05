//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Impostazioni d'istanza dei database e console Admin (GIT-557, DB23): senza 501, solo l'amministratore, i
// valori della console valgono subito e ogni cambiamento e' nell'audit.

const dbSettingsBody = `{"enabled":true,"allowedMajors":[16,17],"maxPerDatabase":{"sizeBytes":5368709120,"cpuMillis":500,"memoryBytes":1073741824},` +
	`"lanPortRange":{"from":20000,"to":20010},"backupRetention":{"scheduled":3,"onDemand":2}}`

func (e *dbEnv) dbSettings(user string, want int) openapi.DatabasesInstanceSettings {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/admin/databases/settings", user, "")
	e.want(rec, want, "")
	var out openapi.DatabasesInstanceSettings
	if want == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			e.t.Fatalf("%v: %s", err, rec.Body.String())
		}
	}
	return out
}

func TestDatabasesSettings_SoloAmministratoreESenza501(t *testing.T) {
	e := newDBEnv(t)
	e.want(e.do(http.MethodGet, "/admin/databases/settings", "alice", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPatch, "/admin/databases/settings", "alice", dbSettingsBody), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, "/admin/databases", "alice", ""), http.StatusForbidden, "forbidden")
	e.id.sysAdmin[users["carol"]] = true
	got := e.dbSettings("carol", http.StatusOK)
	if !got.Enabled || len(got.AllowedMajors) != 2 || got.LanPortRange.From != 15432 || got.LanPortRange.To != 15531 ||
		got.BackupRetention.Scheduled != 7 || got.BackupRetention.OnDemand != 5 || got.MaxPerDatabase.CpuMillis < 1 {
		t.Fatalf("predefiniti: %+v", got)
	}
}

func TestDatabasesSettings_Modifica(t *testing.T) {
	e := newDBEnv(t)
	e.id.sysAdmin[users["carol"]] = true
	rec := e.do(http.MethodPatch, "/admin/databases/settings", "carol", dbSettingsBody)
	e.want(rec, http.StatusOK, "")
	got := e.dbSettings("carol", http.StatusOK)
	if got.LanPortRange.From != 20000 || got.LanPortRange.To != 20010 || got.BackupRetention.Scheduled != 3 || got.BackupRetention.OnDemand != 2 ||
		got.MaxPerDatabase.CpuMillis != 500 || got.MaxPerDatabase.SizeBytes != 5368709120 {
		t.Fatalf("dopo il PATCH: %+v", got)
	}
	// Il controller le applica subito.
	if from, to := e.ctl.EffectiveLANRange(); from != 20000 || to != 20010 {
		t.Errorf("intervallo LAN nel controller: %d-%d", from, to)
	}
	if s, o := e.ctl.EffectiveKeep(); s != 3 || o != 2 {
		t.Errorf("conservazione nel controller: %d %d", s, o)
	}
	// Ogni cambiamento e' nell'audit (DB20), con chi l'ha fatto.
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.managed_db_audit WHERE action = 'settings_update' AND actor->>'username' = 'carol'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit delle impostazioni: %d %v", n, err)
	}
	// Il massimo per database vale per creare e ridimensionare.
	e.want(e.do(http.MethodPost, "/databases/alice", "alice", `{"name":"grosso","pgMajor":17,"cpuMillis":501}`), http.StatusUnprocessableEntity, "")
	e.want(e.do(http.MethodPost, "/databases/alice", "alice", `{"name":"grosso","pgMajor":17,"memoryBytes":2147483648}`), http.StatusUnprocessableEntity, "")
	e.create("alice", "alice", `{"name":"shop","pgMajor":17,"cpuMillis":500}`, http.StatusAccepted)
	e.want(e.do(http.MethodPatch, "/databases/alice/shop", "alice", `{"sizeBytes":6442450944}`), http.StatusUnprocessableEntity, "")
	e.want(e.do(http.MethodPatch, "/databases/alice/shop", "alice", `{"sizeBytes":2147483648}`), http.StatusOK, "")
}

func TestDatabasesSettings_MajorAmmesse(t *testing.T) {
	e := newDBEnv(t)
	e.id.sysAdmin[users["carol"]] = true
	solo17 := strings.Replace(dbSettingsBody, `"allowedMajors":[16,17]`, `"allowedMajors":[17]`, 1)
	e.want(e.do(http.MethodPatch, "/admin/databases/settings", "carol", solo17), http.StatusOK, "")
	// Una major tolta non si puo' piu' creare; quella rimasta si'.
	e.want(e.do(http.MethodPost, "/databases/alice", "alice", `{"name":"vecchio","pgMajor":16}`), http.StatusUnprocessableEntity, "pg_major_not_allowed")
	e.create("alice", "alice", `{"name":"nuovo","pgMajor":17}`, http.StatusAccepted)
	// Una major che un database usa non si toglie (DB6).
	e.want(e.do(http.MethodPatch, "/admin/databases/settings", "carol", strings.Replace(dbSettingsBody, `"allowedMajors":[16,17]`, `"allowedMajors":[16]`, 1)),
		http.StatusUnprocessableEntity, "major_in_use")
	// Major senza immagine nella tabella, ripetute o vuote.
	for _, bad := range []string{`[15]`, `[17,17]`, `[]`} {
		e.want(e.do(http.MethodPatch, "/admin/databases/settings", "carol", strings.Replace(dbSettingsBody, `"allowedMajors":[16,17]`, `"allowedMajors":`+bad, 1)), http.StatusUnprocessableEntity, "")
	}
}

func TestDatabasesSettings_ValidazioneEInterruttore(t *testing.T) {
	e := newDBEnv(t)
	e.id.sysAdmin[users["carol"]] = true
	// L'interruttore non si spegne da qui (toglie i permessi Kubernetes): serve il chart.
	e.want(e.do(http.MethodPatch, "/admin/databases/settings", "carol", strings.Replace(dbSettingsBody, `"enabled":true`, `"enabled":false`, 1)), http.StatusUnprocessableEntity, "databases_switch_cli")
	for name, body := range map[string]string{
		"intervallo capovolto": strings.Replace(dbSettingsBody, `"from":20000,"to":20010`, `"from":20010,"to":20000`, 1),
		"porta riservata":      strings.Replace(dbSettingsBody, `"from":20000,"to":20010`, `"from":80,"to":90`, 1),
		"limite a zero":        strings.Replace(dbSettingsBody, `"cpuMillis":500`, `"cpuMillis":0`, 1),
		"dump a zero":          strings.Replace(dbSettingsBody, `"scheduled":3`, `"scheduled":0`, 1),
		"campo sconosciuto":    strings.Replace(dbSettingsBody, `"enabled":true`, `"enabled":true,"extra":1`, 1),
	} {
		if rec := e.do(http.MethodPatch, "/admin/databases/settings", "carol", body); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("%s: risposta %d, voluto 400 o 422", name, rec.Code)
		}
	}
	// Niente e' cambiato dopo i rifiuti.
	if got := e.dbSettings("carol", http.StatusOK); got.LanPortRange.From != 15432 {
		t.Errorf("un rifiuto ha cambiato le impostazioni: %+v", got)
	}
}

func TestDatabasesSettings_ElencoDiTuttaLIstanza(t *testing.T) {
	e := newDBEnv(t)
	e.id.sysAdmin[users["carol"]] = true
	e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	e.create("alice", "acme", `{"name":"erp","pgMajor":16}`, http.StatusAccepted)
	rec := e.do(http.MethodGet, "/admin/databases", "carol", "")
	e.want(rec, http.StatusOK, "")
	var list openapi.DatabaseList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	owners := map[string]bool{}
	for _, d := range list.Items {
		owners[d.Owner+"/"+string(d.Name)] = true
	}
	if len(list.Items) != 2 || !owners["alice/shop"] || !owners["acme/erp"] {
		t.Fatalf("elenco dell'istanza: %+v", list)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "password") {
		t.Errorf("l'elenco non rivela password: %s", rec.Body.String())
	}
}

// Il limite della LAN: una porta gia' assegnata non puo' restare fuori dal nuovo intervallo.
func TestDatabasesSettings_PortaFuoriIntervallo(t *testing.T) {
	e := newDBEnv(t)
	e.id.sysAdmin[users["carol"]] = true
	e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.managed_dbs SET lan_enabled = true, lan_port = 15440 WHERE name = 'shop'`); err != nil {
		t.Fatal(err)
	}
	e.want(e.do(http.MethodPatch, "/admin/databases/settings", "carol", dbSettingsBody), http.StatusUnprocessableEntity, "lan_port_outside_range")
	inside := strings.Replace(dbSettingsBody, `"from":20000,"to":20010`, `"from":15432,"to":15531`, 1)
	e.want(e.do(http.MethodPatch, "/admin/databases/settings", "carol", inside), http.StatusOK, "")
}

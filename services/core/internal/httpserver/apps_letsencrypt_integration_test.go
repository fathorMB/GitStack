//go:build integration

package httpserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-459 (v3-c): stato di Let's Encrypt e porta 80 nelle impostazioni d'istanza delle app.

// acmeCtl è un controller finto con la parte ACME del controller vero.
type acmeCtl struct {
	routeCtl
	cfg apps.ACMEConfig
	p80 apps.Port80Status
}

func (c *acmeCtl) ACMEStatus() (apps.ACMEConfig, apps.Port80Status) { return c.cfg, c.p80 }

func withSettingsFields(base, extra string) string {
	return strings.TrimSuffix(base, "}") + "," + extra + "}"
}

func (e *appsEnv) settings(user string) openapi.AppsInstanceSettings {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/admin/apps/settings", user, "")
	e.want(rec, http.StatusOK, "")
	var s openapi.AppsInstanceSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		e.t.Fatalf("impostazioni non valide: %v: %s", err, rec.Body.String())
	}
	return s
}

func TestAdminAppsSettings_LetsEncryptEPorta80(t *testing.T) {
	checked := time.Now().UTC().Truncate(time.Second)
	no := false
	ctl := &acmeCtl{cfg: apps.ACMEConfig{Resolver: "letsencrypt", Email: "ops@example.com"},
		p80: apps.Port80Status{Reachable: &no, CheckedAt: &checked, Error: "la porta 80 non risponde"}}
	e := newAppsEnv(t, httpserver.WithAppController(ctl))
	e.id.sysAdmin[users["carol"]] = true

	// Spento l'interruttore: il blocco c'è, ma Let's Encrypt non è attivo; email e porta 80 dell'installazione.
	le := e.settings("carol").LetsEncrypt
	if le == nil || le.Enabled || le.AcmeEmail == nil || string(*le.AcmeEmail) != "ops@example.com" {
		t.Fatalf("blocco letsEncrypt inatteso: %+v", le)
	}
	if le.Port80Reachable == nil || *le.Port80Reachable || le.Port80Error == nil || le.Port80CheckedAt == nil {
		t.Fatalf("porta 80: %+v", le)
	}
	if le.PublicIps == nil || len(*le.PublicIps) != 0 {
		t.Fatalf("publicIps vuoti attesi: %+v", le.PublicIps)
	}

	// IP privati o non validi: 422 sul campo publicIps.
	for _, bad := range []string{`["10.0.0.5"]`, `["not-an-ip"]`, `["127.0.0.1"]`} {
		e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", withSettingsFields(publicSettings(true), `"publicIps":`+bad)), http.StatusUnprocessableEntity, "validation_failed")
	}
	// Un'email diversa da quella dell'installazione: la config ACME è una sola e non si cambia da qui.
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", withSettingsFields(publicSettings(true), `"acmeEmail":"altra@example.com"`)), http.StatusUnprocessableEntity, "validation_failed")

	// IP pubblici validi e la stessa email: salvati; con interruttore acceso Let's Encrypt è attivo.
	body := withSettingsFields(publicSettings(true), `"publicIps":["203.0.113.7","203.0.113.7","2001:db8::1"],"acmeEmail":"ops@example.com"`)
	rec := e.do(http.MethodPut, "/admin/apps/settings", "carol", body)
	e.want(rec, http.StatusOK, "")
	var put openapi.AppsInstanceSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &put); err != nil {
		t.Fatal(err)
	}
	if put.LetsEncrypt == nil || !put.LetsEncrypt.Enabled || put.LetsEncrypt.PublicIps == nil || len(*put.LetsEncrypt.PublicIps) != 2 {
		t.Fatalf("dopo il PUT: %+v", put.LetsEncrypt)
	}
	got := e.settings("carol").LetsEncrypt
	if got == nil || !got.Enabled || got.PublicIps == nil || (*got.PublicIps)[0] != "203.0.113.7" {
		t.Fatalf("dopo il GET: %+v", got)
	}

	// Senza publicIps nel corpo restano invariati; [] li toglie.
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")
	if g := e.settings("carol").LetsEncrypt; g == nil || g.PublicIps == nil || len(*g.PublicIps) != 2 {
		t.Fatalf("assente = invariati: %+v", g)
	}
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", withSettingsFields(publicSettings(true), `"publicIps":[]`)), http.StatusOK, "")
	if g := e.settings("carol").LetsEncrypt; g == nil || g.PublicIps == nil || len(*g.PublicIps) != 0 {
		t.Fatalf("[] li toglie: %+v", g)
	}
}

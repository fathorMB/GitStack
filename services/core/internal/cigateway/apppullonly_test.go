package cigateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/apppull"
	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
	"github.com/google/uuid"
)

// Con la CI spenta (GIT-449) il ci-gateway serve solo registry-access per il token di pull delle
// app: i token dei job e ogni altro percorso sono rifiutati senza toccare il database (Pool nil).
func TestAppPullOnly_RifiutaJobEAltriPercorsi(t *testing.T) {
	keys := []string{"chiave-gateway"}
	h := cigateway.New(cigateway.Config{AppPullKeys: keys, AppPullOnly: true})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	appTok, err := apppull.Mint(keys, uuid.New(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, _ := apppull.Mint([]string{"altra"}, uuid.New(), time.Now())

	get := func(method, path, token string) int {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	q := cigateway.RegistryAccessPath + "?owner=alice&repo=uno"
	for name, c := range map[string]struct {
		method, path, tok string
		want              int
	}{
		"token del job":          {"GET", q, "gsj_qualunque", http.StatusUnauthorized},
		"senza token":            {"GET", q, "", http.StatusUnauthorized},
		"app con chiave diversa": {"GET", q, wrongKey, http.StatusUnauthorized},
		"archivio":               {"GET", cigateway.ArchivePath, appTok, http.StatusNotFound},
		"runner v1":              {"POST", "/runner.v1.RunnerService/FetchTask", "gsj_x", http.StatusNotFound},
		"artefatti":              {"GET", cigateway.ArtifactsPath, appTok, http.StatusNotFound},
		"metodo sbagliato":       {"POST", q, appTok, http.StatusNotFound},
	} {
		if got := get(c.method, c.path, c.tok); got != c.want {
			t.Errorf("%s: %d, atteso %d", name, got, c.want)
		}
	}
}

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"gopkg.in/yaml.v3"
)

// anonymous esegue una richiesta senza header di identità dal router completo.
func anonymous(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestRouter_InstanceCaPubblicaSenzaIdentita(t *testing.T) {
	dir := t.TempDir()
	writeTestCA(t, dir)
	for name, opts := range map[string][]Option{
		"senza CA configurata": nil,
		"con CA configurata":   {WithInstanceCA(CAConfig{Dir: dir})},
	} {
		h := NewRouter(nil, events.NoopPublisher{}, "segreto", opts...)
		if rec := anonymous(t, h, http.MethodGet, "/instance/ca"); rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, corpo %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestRouter_MetaEProbeNonRispondono401(t *testing.T) {
	h := NewRouter(nil, events.NoopPublisher{}, "segreto")
	for _, p := range []string{"/meta", "/healthz"} {
		if rec := anonymous(t, h, http.MethodGet, p); rec.Code == http.StatusUnauthorized {
			t.Errorf("GET %s anonima: 401", p)
		}
	}
}

func TestRouter_RottaNonPubblicaSenzaIdentita401(t *testing.T) {
	h := NewRouter(nil, events.NoopPublisher{}, "segreto")
	for _, p := range []string{"/resources", "/user", "/repos/alice/demo", "/instance/ca/altro", "/meta/x"} {
		if rec := anonymous(t, h, http.MethodGet, p); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s anonima: status = %d, voluto 401", p, rec.Code)
		}
	}
}

// Ogni operazione `security: []` del contratto che il mux di core registra
// (le altre, come /auth/*, sono di identity e il mux di core non le conosce)
// deve essere esentata da publicPath, altrimenti il gateway la lascia passare
// e core risponde 401.
func TestPublicPathMatchesContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	// Il mux di core con le sole rotte generate dal contratto.
	mux := http.NewServeMux()
	openapi.HandlerWithOptions(&apiServer{}, openapi.StdHTTPServerOptions{BaseRouter: mux})

	public, served := 0, 0
	for path, ops := range doc.Paths {
		for method, node := range ops {
			// Le chiavi che non sono metodi HTTP (parameters, summary...) non sono operazioni.
			switch strings.ToLower(method) {
			case "get", "put", "post", "delete", "patch", "head", "options":
			default:
				continue
			}
			var op struct {
				Security *[]any `yaml:"security"`
			}
			if err := node.Decode(&op); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			if op.Security == nil || len(*op.Security) != 0 {
				continue
			}
			public++
			concrete := path
			for strings.Contains(concrete, "{") {
				i, j := strings.Index(concrete, "{"), strings.Index(concrete, "}")
				concrete = concrete[:i] + "x" + concrete[j+1:]
			}
			req := httptest.NewRequest(strings.ToUpper(method), concrete, nil)
			if _, pattern := mux.Handler(req); pattern == "" {
				continue // non servita da core
			}
			served++
			if !publicPath(req) {
				t.Errorf("%s %s è `security: []` nel contratto e servita da core, ma publicPath non la esenta", strings.ToUpper(method), path)
			}
		}
	}
	if public == 0 || served == 0 {
		t.Fatalf("contratto letto male: %d operazioni pubbliche, %d servite da core", public, served)
	}
}

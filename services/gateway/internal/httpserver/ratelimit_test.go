package httpserver

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/gateway/internal/config"
)

func rateLimitedRouter(t *testing.T, ipBurst int) http.Handler {
	t.Helper()
	cfg := newTestConfig(t, "http://127.0.0.1:1")
	_, trusted, err := net.ParseCIDR("10.42.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	cfg.TrustedProxies = []*net.IPNet{trusted}
	// Rifornimento quasi nullo: nel corso del test nessun token si rigenera.
	cfg.RateLimit = config.RateLimitConfig{IPRate: 0.001, IPBurst: ipBurst}
	return NewRouter(cfg, discardLogger())
}

func metaFrom(h http.Handler, remote, xff string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/meta", nil)
	req.RemoteAddr = remote
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRouter_RateLimit_OltreIlBurstDa429(t *testing.T) {
	h := rateLimitedRouter(t, 2)
	for i := 0; i < 2; i++ {
		if rec := metaFrom(h, "192.0.2.10:5000", ""); rec.Code != http.StatusOK {
			t.Fatalf("richiesta %d: %d, voluto 200", i, rec.Code)
		}
	}
	rec := metaFrom(h, "192.0.2.10:5000", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("oltre il burst: %d, voluto 429", rec.Code)
	}
	if n, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || n < 1 {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), `"code":"rate_limited"`) {
		t.Errorf("corpo = %s, manca il codice rate_limited", rec.Body.String())
	}
}

func TestRouter_RateLimit_XForwardedForSoloDaProxyFidati(t *testing.T) {
	h := rateLimitedRouter(t, 1)

	// Proxy fidato (10.42.0.7): due client diversi in XFF hanno bucket separati.
	if rec := metaFrom(h, "10.42.0.7:4000", "203.0.113.1"); rec.Code != http.StatusOK {
		t.Fatalf("client 1: %d", rec.Code)
	}
	if rec := metaFrom(h, "10.42.0.7:4000", "203.0.113.2"); rec.Code != http.StatusOK {
		t.Fatalf("client 2 dietro lo stesso proxy non deve essere limitato: %d", rec.Code)
	}
	if rec := metaFrom(h, "10.42.0.7:4000", "203.0.113.1"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("client 1 al secondo giro: %d, voluto 429", rec.Code)
	}

	// Peer non fidato: XFF ignorato, cambiarlo non aggira il limite.
	if rec := metaFrom(h, "198.51.100.9:4000", "1.1.1.1"); rec.Code != http.StatusOK {
		t.Fatalf("peer non fidato, primo: %d", rec.Code)
	}
	if rec := metaFrom(h, "198.51.100.9:4000", "2.2.2.2"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("XFF falsificato da peer non fidato: %d, voluto 429", rec.Code)
	}
}

func TestRouter_RateLimit_HealthzMaiLimitato(t *testing.T) {
	h := rateLimitedRouter(t, 1)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "192.0.2.10:5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/healthz richiesta %d: %d, voluto 200", i, rec.Code)
		}
	}
	// Le chiamate a /healthz non consumano il bucket dell'API.
	if rec := metaFrom(h, "192.0.2.10:5000", ""); rec.Code != http.StatusOK {
		t.Errorf("/v1/meta dopo /healthz: %d, voluto 200", rec.Code)
	}
}

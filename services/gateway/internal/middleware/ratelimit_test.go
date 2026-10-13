package middleware

import (
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func do(h http.Handler, remote, token, xff string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/resources", nil)
	req.RemoteAddr = remote
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

func TestRateLimit_PerTokenAndPerIP(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	var logs bytes.Buffer
	rl := NewRateLimiter(RateLimitConfig{
		CredentialRate: 1, CredentialBurst: 3,
		IPRate: 1, IPBurst: 10,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Now:    clock.now,
	})
	h := rl.Middleware(okHandler())

	// Per credenziale: burst 3, poi 429 con Retry-After; un altro token non è
	// toccato (stesso IP, sotto la soglia IP).
	for i := 0; i < 3; i++ {
		if rec := do(h, "10.0.0.1:1000", "gst_aaaa", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("richiesta %d: %d", i, rec.Code)
		}
	}
	rec := do(h, "10.0.0.1:1000", "gst_aaaa", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("oltre la soglia: %d, voluto 429", rec.Code)
	}
	ra, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || ra < 1 {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if rec := do(h, "10.0.0.1:1000", "gst_bbbb", ""); rec.Code != http.StatusNoContent {
		t.Errorf("un altro token non deve essere influenzato: %d", rec.Code)
	}
	// Dopo l'attesa il token torna a passare.
	clock.advance(time.Duration(ra) * time.Second)
	if rec := do(h, "10.0.0.1:1000", "gst_aaaa", ""); rec.Code != http.StatusNoContent {
		t.Errorf("dopo Retry-After: %d", rec.Code)
	}

	// Contatore e log: chiave offuscata, mai il token.
	if got := rl.Rejected(ScopeCredential); got != 1 {
		t.Errorf("Rejected(credential) = %d, voluto 1", got)
	}
	if !strings.Contains(logs.String(), "rate limit exceeded") || strings.Contains(logs.String(), "gst_aaaa") {
		t.Errorf("log inatteso: %q", logs.String())
	}

	// Per IP: senza credenziale, burst 10, poi 429; un altro IP non è toccato.
	// (il bucket di 10.0.0.1 ha già consumato 6 richieste sopra).
	n := 0
	for ; n < 20; n++ {
		if do(h, "10.0.0.1:1000", "", "").Code == http.StatusTooManyRequests {
			break
		}
	}
	if n >= 20 || n == 0 {
		t.Fatalf("limite per IP non scattato correttamente (n=%d)", n)
	}
	if rec := do(h, "10.0.0.2:1000", "", ""); rec.Code != http.StatusNoContent {
		t.Errorf("un altro IP non deve essere influenzato: %d", rec.Code)
	}
	if rl.Rejected(ScopeIP) == 0 {
		t.Error("Rejected(ip) = 0")
	}
}

func TestRateLimit_XForwardedForSoloDaProxyFidati(t *testing.T) {
	_, trusted, _ := net.ParseCIDR("10.42.0.0/16")
	clientIP := func(r *http.Request) string {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip := net.ParseIP(host); ip != nil && trusted.Contains(ip) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				return strings.TrimSpace(strings.Split(xff, ",")[0])
			}
		}
		return host
	}
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	h := NewRateLimiter(RateLimitConfig{IPRate: 1, IPBurst: 1, ClientIP: clientIP, Now: clock.now}).Middleware(okHandler())

	// Dal proxy fidato: client diversi (XFF) non si influenzano.
	if do(h, "10.42.0.5:1", "", "203.0.113.1").Code != http.StatusNoContent {
		t.Fatal("primo client")
	}
	if do(h, "10.42.0.5:1", "", "203.0.113.2").Code != http.StatusNoContent {
		t.Error("secondo client dietro lo stesso proxy non deve essere limitato")
	}
	if do(h, "10.42.0.5:1", "", "203.0.113.1").Code != http.StatusTooManyRequests {
		t.Error("stesso client: atteso 429")
	}
	// Da un peer non fidato XFF è ignorato: cambiarlo non aggira il limite.
	if do(h, "198.51.100.9:1", "", "1.1.1.1").Code != http.StatusNoContent {
		t.Fatal("primo peer non fidato")
	}
	if do(h, "198.51.100.9:1", "", "2.2.2.2").Code != http.StatusTooManyRequests {
		t.Error("XFF falsificato da peer non fidato non deve aggirare il limite")
	}
}

func TestRateLimit_Disattivato(t *testing.T) {
	h := NewRateLimiter(RateLimitConfig{}).Middleware(okHandler())
	for i := 0; i < 50; i++ {
		if do(h, "10.0.0.1:1", "gst_x", "").Code != http.StatusNoContent {
			t.Fatal("con rate 0 non si limita nulla")
		}
	}
}

func TestRateLimit_Concorrenza(t *testing.T) {
	h := NewRateLimiter(RateLimitConfig{CredentialRate: 100, CredentialBurst: 100, IPRate: 100, IPBurst: 100}).Middleware(okHandler())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				do(h, "10.0.0.1:1", "gst_"+strconv.Itoa(g), "")
			}
		}()
	}
	wg.Wait()
}

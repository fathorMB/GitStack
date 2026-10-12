package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

// TestSecurityHeaders_AutenticatoJSON verifica che la risposta JSON di una
// richiesta autenticata abbia Cache-Control: no-store e
// X-Content-Type-Options: nosniff. Il next è un handler finto per il caso
// semplice senza proxy ReverseProxy.
func TestSecurityHeaders_AutenticatoJSON(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[]}`))
	})

	mw := SecurityHeaders(next)
	req := httptest.NewRequest(http.MethodGet, "/v1/resources?type=repo", nil)
	id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice"}
	req = req.WithContext(trust.WithIdentity(req.Context(), id))

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, voluto nosniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, voluto no-store", got)
	}
}

// TestSecurityHeaders_AutenticatoRaw verifica che la risposta raw di una
// richiesta autenticata abbia Cache-Control: no-store e
// X-Content-Type-Options: nosniff.
func TestSecurityHeaders_AutenticatoRaw(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("contenuto"))
	})

	mw := SecurityHeaders(next)
	req := httptest.NewRequest(http.MethodGet, "/v1/repos/a/b/raw/HEAD/f", nil)
	id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "bob"}
	req = req.WithContext(trust.WithIdentity(req.Context(), id))

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, voluto nosniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, voluto no-store", got)
	}
}

func newProxy(t *testing.T, upstream *httptest.Server) http.Handler {
	t.Helper()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	return httputil.NewSingleHostReverseProxy(u)
}

// TestSecurityHeaders_ServiceCacheControlNonSovrascritto verifica che un
// Cache-Control già impostato dal servizio non venga sovrascritto. Usa un
// proxy reale (ReverseProxy + httptest.Server) per simulare il flusso reale.
func TestSecurityHeaders_ServiceCacheControlNonSovrascritto(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":"test"}`))
	}))
	defer upstream.Close()

	proxy := newProxy(t, upstream)
	mw := SecurityHeaders(proxy)

	req := httptest.NewRequest(http.MethodGet, "/v1/resources", nil)
	id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice"}
	req = req.WithContext(trust.WithIdentity(req.Context(), id))

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	cc := rec.Header().Values("Cache-Control")
	if len(cc) != 1 || cc[0] != "private, max-age=60" {
		t.Errorf("Cache-Control = %v, voluto [\"private, max-age=60\"]", cc)
	}
	nosniff := rec.Header().Values("X-Content-Type-Options")
	if len(nosniff) != 1 || nosniff[0] != "nosniff" {
		t.Errorf("X-Content-Type-Options = %v, voluto [\"nosniff\"]", nosniff)
	}
}

// TestSecurityHeaders_Allegato_PrivateNoStore verifica che un Cache-Control
// "private, no-store" (usato per allegati) venga rispettato e non si duplichi
// nosniff.
func TestSecurityHeaders_Allegato_PrivateNoStore(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("binario"))
	}))
	defer upstream.Close()

	proxy := newProxy(t, upstream)
	mw := SecurityHeaders(proxy)

	req := httptest.NewRequest(http.MethodGet, "/v1/repos/a/b/raw/HEAD/file.bin", nil)
	id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "carol"}
	req = req.WithContext(trust.WithIdentity(req.Context(), id))

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	cc := rec.Header().Values("Cache-Control")
	if len(cc) != 1 || cc[0] != "private, no-store" {
		t.Errorf("Cache-Control = %v, voluto [\"private, no-store\"]", cc)
	}
	nosniff := rec.Header().Values("X-Content-Type-Options")
	if len(nosniff) != 1 || nosniff[0] != "nosniff" {
		t.Errorf("X-Content-Type-Options = %v, voluto [\"nosniff\"]", nosniff)
	}
}

// TestSecurityHeaders_NonAutenticato verifica che le risposte non autenticate
// non ricevano le intestazioni di sicurezza.
func TestSecurityHeaders_NonAutenticato(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := SecurityHeaders(next)
	req := httptest.NewRequest(http.MethodGet, "/v1/resources", nil)

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if !called {
		t.Error("il gestore successivo non è stato chiamato")
	}
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control su richiesta non autenticata = %q, voluto vuoto", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "" {
		t.Errorf("X-Content-Type-Options su richiesta non autenticata = %q, voluto vuoto", got)
	}
}

// TestAPIResponses_NoStoreNosniff è il test principale per il criterio F4:
// verifica con un proxy reale (ReverseProxy) che:
// - JSON senza Cache-Control riceva esattamente ["no-store"] e ["nosniff"]
// - raw text/plain dal servizio con nosniff già messo riceva un solo "nosniff"
// - servizio con "private, no-store" riceva solo ["private, no-store"]
func TestAPIResponses_NoStoreNosniff(t *testing.T) {
	t.Run("JSON senza Cache-Control dal servizio", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"items":[]}`))
		}))
		defer upstream.Close()

		proxy := newProxy(t, upstream)
		mw := SecurityHeaders(proxy)

		req := httptest.NewRequest(http.MethodGet, "/v1/resources?type=repo", nil)
		id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice"}
		req = req.WithContext(trust.WithIdentity(req.Context(), id))

		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, voluto 200", rec.Code)
		}
		cc := rec.Header().Values("Cache-Control")
		if len(cc) != 1 || cc[0] != "no-store" {
			t.Errorf("Cache-Control = %v, voluto [\"no-store\"]", cc)
		}
		nt := rec.Header().Values("X-Content-Type-Options")
		if len(nt) != 1 || nt[0] != "nosniff" {
			t.Errorf("X-Content-Type-Options = %v, voluto [\"nosniff\"]", nt)
		}
	})

	t.Run("Raw text/plain con nosniff dal servizio", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("contenuto"))
		}))
		defer upstream.Close()

		proxy := newProxy(t, upstream)
		mw := SecurityHeaders(proxy)

		req := httptest.NewRequest(http.MethodGet, "/v1/repos/a/b/raw/HEAD/f", nil)
		id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "bob"}
		req = req.WithContext(trust.WithIdentity(req.Context(), id))

		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, voluto 200", rec.Code)
		}
		nt := rec.Header().Values("X-Content-Type-Options")
		if len(nt) != 1 || nt[0] != "nosniff" {
			t.Errorf("X-Content-Type-Options = %v, voluto [\"nosniff\"] (senza duplicati)", nt)
		}
		cc := rec.Header().Values("Cache-Control")
		if len(cc) != 1 || cc[0] != "no-store" {
			t.Errorf("Cache-Control = %v, voluto [\"no-store\"]", cc)
		}
	})

	t.Run("Servizio con private,no-store", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Cache-Control", "private, no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("allegato"))
		}))
		defer upstream.Close()

		proxy := newProxy(t, upstream)
		mw := SecurityHeaders(proxy)

		req := httptest.NewRequest(http.MethodGet, "/v1/repos/a/b/raw/HEAD/allegato.pdf", nil)
		id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "carol"}
		req = req.WithContext(trust.WithIdentity(req.Context(), id))

		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, voluto 200", rec.Code)
		}
		cc := rec.Header().Values("Cache-Control")
		if len(cc) != 1 || cc[0] != "private, no-store" {
			t.Errorf("Cache-Control = %v, voluto [\"private, no-store\"]", cc)
		}
		nt := rec.Header().Values("X-Content-Type-Options")
		if len(nt) != 1 || nt[0] != "nosniff" {
			t.Errorf("X-Content-Type-Options = %v, voluto [\"nosniff\"]", nt)
		}
	})
}

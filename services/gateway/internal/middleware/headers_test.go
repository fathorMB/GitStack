package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

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

func TestSecurityHeaders_ServiceCacheControlNonSovrascritto(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":"test"}`))
	})

	mw := SecurityHeaders(next)
	req := httptest.NewRequest(http.MethodGet, "/v1/resources", nil)
	id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice"}
	req = req.WithContext(trust.WithIdentity(req.Context(), id))

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=60" {
		t.Errorf("Cache-Control = %q, voluto private, max-age=60 (non sovrascritto)", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, voluto nosniff", got)
	}
}

func TestSecurityHeaders_Allegato_PrivateNoStore(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="file.bin"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("dati"))
	})

	mw := SecurityHeaders(next)
	req := httptest.NewRequest(http.MethodGet, "/v1/resources", nil)
	id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice"}
	req = req.WithContext(trust.WithIdentity(req.Context(), id))

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, voluto 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, voluto private, no-store (non sovrascritto)", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, voluto nosniff", got)
	}
}

func TestSecurityHeaders_NonAutenticato(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := SecurityHeaders(next)
	req := httptest.NewRequest(http.MethodGet, "/v1/resources", nil)
	// Nessuna identità nel contesto.

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
// verifica che le risposte autenticate del gateway abbiano Cache-Control:
// no-store e X-Content-Type-Options: nosniff, e che un Cache-Control già
// impostato dal servizio non venga sovrascritto.
func TestAPIResponses_NoStoreNosniff(t *testing.T) {
	t.Run("JSON autenticato ha no-store e nosniff", func(t *testing.T) {
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
	})

	t.Run("Raw autenticato ha no-store e nosniff", func(t *testing.T) {
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
	})

	t.Run("Cache-Control del servizio non viene sovrascritto", func(t *testing.T) {
		t.Run("private,max-age", func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "private, max-age=60")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"data":"test"}`))
			})

			mw := SecurityHeaders(next)
			req := httptest.NewRequest(http.MethodGet, "/v1/resources", nil)
			id := trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice"}
			req = req.WithContext(trust.WithIdentity(req.Context(), id))

			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, voluto 200", rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != "private, max-age=60" {
				t.Errorf("Cache-Control = %q, voluto private, max-age=60 (non sovrascritto)", got)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, voluto nosniff", got)
			}
		})
	})
}

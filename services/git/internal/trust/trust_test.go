package trust

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var t0 = time.Unix(1700000000, 0)

func signed(id Identity) http.Header {
	h := http.Header{}
	Sign(h, "segreto", id, t0)
	return h
}

// Vettore noto, uguale a quello di services/gateway/internal/trust: se la
// firma cambia da una parte sola, uno dei due test si rompe.
func TestSign_VettoreDiProva(t *testing.T) {
	h := signed(Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice", Scopes: []string{"read:user", "write:org"}})
	const want = "bd36809e19293e4b25d04bf84b6e31cf9862b44893a707744ca4611ede58a363"
	if got := h.Get(HeaderSignature); got != want {
		t.Fatalf("firma = %s, voluta %s", got, want)
	}
}

func TestSign_VettoreDiProvaToken(t *testing.T) {
	h := signed(Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice", Scopes: []string{"read:user", "write:org"},
		TokenID: "22222222-2222-2222-2222-222222222222", TokenName: "ci-runner"})
	if h.Get(HeaderTokenID) != "22222222-2222-2222-2222-222222222222" || h.Get(HeaderTokenName) != "ci-runner" {
		t.Fatalf("header = %v", h)
	}
	const want = "2ad8821a340ec69859107a68d17cddb013c3613e5f55d52789beecca74585125"
	if got := h.Get(HeaderSignature); got != want {
		t.Fatalf("firma = %s, voluta %s", got, want)
	}
}

func TestVerify_Token(t *testing.T) {
	id, ok := Verify(signed(Identity{UserID: "u1", Username: "alice", Scopes: []string{"read:user"}, TokenID: "t1", TokenName: "ci-runner"}), "segreto", t0)
	if !ok || id.TokenID != "t1" || id.TokenName != "ci-runner" {
		t.Fatalf("Verify = %+v %v", id, ok)
	}
	id, ok = Verify(signed(Identity{UserID: "u1", Username: "bob"}), "segreto", t0)
	if !ok || id.TokenID != "" || id.TokenName != "" {
		t.Fatalf("una sessione non ha token: %+v %v", id, ok)
	}
}

func TestVerify_RifiutiToken(t *testing.T) {
	cases := []struct {
		name   string
		base   Identity
		mutate func(http.Header)
	}{
		{"id del token cambiato", Identity{UserID: "u1", Username: "a", TokenID: "t1", TokenName: "n"}, func(h http.Header) { h.Set(HeaderTokenID, "t2") }},
		{"nome del token cambiato", Identity{UserID: "u1", Username: "a", TokenID: "t1", TokenName: "n"}, func(h http.Header) { h.Set(HeaderTokenName, "root") }},
		{"token aggiunto a una sessione", Identity{UserID: "u1", Username: "a"}, func(h http.Header) { h.Set(HeaderTokenID, "t1"); h.Set(HeaderTokenName, "n") }},
		{"nome aggiunto a una sessione", Identity{UserID: "u1", Username: "a"}, func(h http.Header) { h.Set(HeaderTokenName, "n") }},
		{"token tolto", Identity{UserID: "u1", Username: "a", TokenID: "t1", TokenName: "n"}, func(h http.Header) { h.Set(HeaderTokenID, ""); h.Set(HeaderTokenName, "") }},
		{"manca l'id del token", Identity{UserID: "u1", Username: "a"}, func(h http.Header) { h.Del(HeaderTokenID) }},
		{"manca il nome del token", Identity{UserID: "u1", Username: "a"}, func(h http.Header) { h.Del(HeaderTokenName) }},
		{"nome del token ripetuto", Identity{UserID: "u1", Username: "a", TokenID: "t1", TokenName: "n"}, func(h http.Header) { h.Add(HeaderTokenName, "x") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := signed(c.base)
			c.mutate(h)
			if _, ok := Verify(h, "segreto", t0); ok {
				t.Error("identità accettata")
			}
		})
	}
}

func TestVerify_Valida(t *testing.T) {
	id, ok := Verify(signed(Identity{UserID: "u1", Username: "alice", Scopes: []string{"read:user", "write:org"}}), "segreto", t0.Add(30*time.Second))
	if !ok || id.UserID != "u1" || id.Username != "alice" || len(id.Scopes) != 2 || id.Scopes[1] != "write:org" {
		t.Fatalf("Verify = %+v %v", id, ok)
	}
	id, ok = Verify(signed(Identity{UserID: "u1", Username: "bob"}), "segreto", t0)
	if !ok || len(id.Scopes) != 0 {
		t.Fatalf("una sessione ha scope vuoti: %+v %v", id, ok)
	}
}

func TestVerify_Rifiuti(t *testing.T) {
	base := Identity{UserID: "u1", Username: "alice", Scopes: []string{"read:user"}}
	cases := []struct {
		name   string
		mutate func(http.Header)
		secret string
		now    time.Time
	}{
		{"nessun header", func(h http.Header) {
			for k := range h {
				delete(h, k)
			}
		}, "segreto", t0},
		{"manca la firma", func(h http.Header) { h.Del(HeaderSignature) }, "segreto", t0},
		{"manca il timestamp", func(h http.Header) { h.Del(HeaderTimestamp) }, "segreto", t0},
		{"manca lo username", func(h http.Header) { h.Del(HeaderUsername) }, "segreto", t0},
		{"manca l'header degli scope", func(h http.Header) { h.Del(HeaderScopes) }, "segreto", t0},
		{"utente cambiato", func(h http.Header) { h.Set(HeaderUserID, "u2") }, "segreto", t0},
		{"username cambiato", func(h http.Header) { h.Set(HeaderUsername, "root") }, "segreto", t0},
		{"scope ampliati", func(h http.Header) { h.Set(HeaderScopes, "read:user,admin:org") }, "segreto", t0},
		{"scope aggiunti a una sessione", func(h http.Header) { h.Set(HeaderScopes, "admin:org") }, "segreto", t0},
		{"firma non esadecimale", func(h http.Header) { h.Set(HeaderSignature, "zz") }, "segreto", t0},
		{"firma vuota", func(h http.Header) { h.Set(HeaderSignature, "") }, "segreto", t0},
		{"timestamp non numerico", func(h http.Header) { h.Set(HeaderTimestamp, "ieri") }, "segreto", t0},
		{"header ripetuto", func(h http.Header) { h.Add(HeaderUsername, "root") }, "segreto", t0},
		{"segreto diverso", func(h http.Header) {}, "altro", t0},
		{"segreto vuoto", func(h http.Header) {}, "", t0},
		{"firma scaduta", func(h http.Header) {}, "segreto", t0.Add(MaxSkew + time.Second)},
		{"firma dal futuro", func(h http.Header) {}, "segreto", t0.Add(-MaxSkew - time.Second)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := signed(base)
			c.mutate(h)
			if _, ok := Verify(h, c.secret, c.now); ok {
				t.Error("identità accettata")
			}
		})
	}
}

func TestRequire(t *testing.T) {
	var gotID Identity
	var reached bool
	h := Require("segreto", func(r *http.Request) bool { return r.URL.Path == "/health" }, func() time.Time { return t0 })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached = true
			gotID, _ = FromContext(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}))

	do := func(path string, hdr http.Header) *httptest.ResponseRecorder {
		reached = false
		req := httptest.NewRequest("GET", path, nil)
		for k, v := range hdr {
			req.Header[k] = v
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do("/resources", nil); rec.Code != 401 || reached || !strings.Contains(rec.Body.String(), `"unauthenticated"`) {
		t.Errorf("senza identità: %d %s", rec.Code, rec.Body.String())
	}
	// Un client che manda direttamente gli header, senza firma valida.
	forged := http.Header{HeaderUserID: {"u1"}, HeaderUsername: {"root"}, HeaderScopes: {"admin:org"}}
	if rec := do("/resources", forged); rec.Code != 401 || reached {
		t.Errorf("identità falsificata: %d", rec.Code)
	}
	forged.Set(HeaderTimestamp, "1700000000")
	forged.Set(HeaderSignature, "00")
	if rec := do("/resources", forged); rec.Code != 401 || reached {
		t.Errorf("firma inventata: %d", rec.Code)
	}
	if rec := do("/resources", signed(Identity{UserID: "u1", Username: "alice"})); rec.Code != 204 || !reached || gotID.Username != "alice" {
		t.Errorf("identità firmata: %d, id %+v", rec.Code, gotID)
	}
	// Rotta pubblica: passa senza identità.
	if rec := do("/health", nil); rec.Code != 204 || !reached {
		t.Errorf("/health: %d", rec.Code)
	}
}

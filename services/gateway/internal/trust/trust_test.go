package trust

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestSign_VettoreDiProva(t *testing.T) {
	// Lo stesso vettore è in services/core/internal/trust: se cambia la firma
	// da una parte sola, uno dei due test si rompe.
	h := http.Header{}
	Sign(h, "segreto", Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice", Scopes: []string{"read:user", "write:org"}}, time.Unix(1700000000, 0))
	if h.Get(HeaderUserID) != "11111111-1111-1111-1111-111111111111" || h.Get(HeaderUsername) != "alice" ||
		h.Get(HeaderScopes) != "read:user,write:org" || h.Get(HeaderTimestamp) != "1700000000" {
		t.Fatalf("header = %v", h)
	}
	const want = "bd36809e19293e4b25d04bf84b6e31cf9862b44893a707744ca4611ede58a363"
	if got := h.Get(HeaderSignature); got != want {
		t.Fatalf("firma = %s, voluta %s", got, want)
	}
}

func TestSign_VettoreDiProvaToken(t *testing.T) {
	h := http.Header{}
	Sign(h, "segreto", Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice", Scopes: []string{"read:user", "write:org"},
		TokenID: "22222222-2222-2222-2222-222222222222", TokenName: "ci-runner"}, time.Unix(1700000000, 0))
	if h.Get(HeaderTokenID) != "22222222-2222-2222-2222-222222222222" || h.Get(HeaderTokenName) != "ci-runner" {
		t.Fatalf("header = %v", h)
	}
	const want = "2ad8821a340ec69859107a68d17cddb013c3613e5f55d52789beecca74585125"
	if got := h.Get(HeaderSignature); got != want {
		t.Fatalf("firma = %s, voluta %s", got, want)
	}
}

func TestSign_SessioneSenzaToken(t *testing.T) {
	h := http.Header{}
	Sign(h, "s", Identity{UserID: "u", Username: "bob"}, time.Unix(1, 0))
	for _, name := range []string{HeaderTokenID, HeaderTokenName} {
		if v, ok := h[name]; !ok || v[0] != "" {
			t.Errorf("%s di una sessione deve essere presente e vuoto: %v", name, h)
		}
	}
}

// Un client non può farsi riconoscere come token: gli header X-Gitstack-Token-*
// che manda vengono tolti prima che il gateway scriva i propri.
func TestStripClientHeaders_Token(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderTokenID, "22222222-2222-2222-2222-222222222222")
	h["x-gitstack-token-name"] = []string{"root"}
	StripClientHeaders(h)
	if len(h) != 0 {
		t.Errorf("header rimasti: %v", h)
	}
}

func TestSign_SessioneSenzaScope(t *testing.T) {
	h := http.Header{}
	Sign(h, "s", Identity{UserID: "u", Username: "bob"}, time.Unix(1, 0))
	if _, ok := h[HeaderScopes]; !ok || h.Get(HeaderScopes) != "" {
		t.Errorf("gli scope di una sessione devono essere presenti e vuoti: %v", h)
	}
}

func TestStripClientHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Gitstack-User-Id", "x")
	h["x-gitstack-username"] = []string{"y"}
	h.Set("X-Gitstack-Anything", "z")
	h.Set("X-Request-Id", "keep")
	StripClientHeaders(h)
	if len(h) != 1 || h.Get("X-Request-Id") != "keep" {
		t.Errorf("header rimasti: %v", h)
	}
}

func TestContext(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Error("contesto vuoto")
	}
	id := Identity{UserID: "u", Username: "n"}
	got, ok := FromContext(WithIdentity(context.Background(), id))
	if !ok || got.Username != "n" {
		t.Errorf("FromContext = %v %v", got, ok)
	}
}

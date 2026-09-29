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
	const want = "1f1fa6bc1164c65db6b0cc006ba0c317dcfcb6fe650ac53f93212229967ab7b8"
	if got := h.Get(HeaderSignature); got != want {
		t.Fatalf("firma = %s, voluta %s", got, want)
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

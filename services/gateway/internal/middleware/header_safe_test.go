package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

// Round-trip su rete vera: il nome firmato dal gateway deve arrivare a valle
// identico, anche se il server HTTP di Go toglie gli spazi ai bordi dei valori
// degli header (identity accetta nomi come " ci ").
func TestHeaderSafe_RoundTripHTTP(t *testing.T) {
	const secret = "segreto"
	now := time.Unix(1700000000, 0)
	for _, raw := range []string{" ci ", "città", "ci-runner", "ci x", "a\nb", "\tx\t"} {
		t.Run(raw, func(t *testing.T) {
			name := headerSafe(raw)
			var recomputed, got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get(trust.HeaderTokenName)
				// A valle il MAC si ricalcola sui valori ricevuti.
				h := http.Header{}
				trust.Sign(h, secret, trust.Identity{
					UserID: r.Header.Get(trust.HeaderUserID), Username: r.Header.Get(trust.HeaderUsername),
					TokenID: r.Header.Get(trust.HeaderTokenID), TokenName: got,
				}, now)
				recomputed = h.Get(trust.HeaderSignature)
			}))
			defer srv.Close()

			req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			trust.Sign(req.Header, secret, trust.Identity{UserID: "u1", Username: "bot", TokenID: "t1", TokenName: name}, now)
			sent := req.Header.Get(trust.HeaderSignature)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if got != name {
				t.Errorf("nome arrivato %q, firmato %q", got, name)
			}
			if recomputed != sent {
				t.Errorf("la firma ricalcolata a valle non torna per %q", raw)
			}
		})
	}
}

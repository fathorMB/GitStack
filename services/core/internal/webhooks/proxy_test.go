package webhooks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fathorMB/GitStack/pkg/egress"
)

// N9: le consegne escono dal proxy aziendale, ma la destinazione vera si
// giudica prima: una destinazione vietata o interna non contatta il proxy.
func TestNewEgressWithProxy_WebhookAmmessoPassaDalProxy(t *testing.T) {
	var hits atomic.Int64
	var last atomic.Value
	px := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		last.Store(r.Method + " " + r.URL.String())
		w.WriteHeader(http.StatusAccepted)
	}))
	defer px.Close()
	pc, err := egress.NewProxyConfig(px.URL, px.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := NewEgressWithProxy(nil, []string{"203.0.113.0/24"}, nil, pc)
	if err != nil {
		t.Fatal(err)
	}
	client := d.(*http.Client)

	// IP pubblico letterale (nessun DNS nel test): ammesso, passa dal proxy.
	resp, err := client.Post("http://93.184.216.34/hook", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || hits.Load() != 1 || last.Load() != "POST http://93.184.216.34/hook" {
		t.Fatalf("status %d, richieste al proxy %d (%v)", resp.StatusCode, hits.Load(), last.Load())
	}

	before := hits.Load()
	for _, bad := range []string{
		"http://127.0.0.1:9/x",        // loopback
		"http://[::1]/x",              // loopback IPv6
		"http://169.254.169.254/meta", // link-local
		"http://10.43.0.10/x",         // cluster
		"http://203.0.113.7/x",        // deny dell'amministratore
	} {
		resp, err := client.Post(bad, "application/json", strings.NewReader("{}"))
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("%s: doveva essere rifiutato", bad)
		}
		if !egress.IsBlocked(err) {
			t.Errorf("%s: errore %v, atteso un blocco di policy", bad, err)
		}
	}
	if hits.Load() != before {
		t.Fatalf("il proxy è stato contattato per destinazioni vietate (%d richieste)", hits.Load()-before)
	}
}

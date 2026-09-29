package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func mustCIDRs(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestClientIP(t *testing.T) {
	traefik := mustCIDRs(t, "10.42.0.0/16")
	two := mustCIDRs(t, "10.42.0.0/16", "192.168.0.0/24")
	tests := []struct {
		name    string
		trusted []*net.IPNet
		remote  string
		xff     []string
		want    string
	}{
		{"nessun proxy fidato: XFF ignorato", nil, "10.42.1.5:1000", []string{"203.0.113.7"}, "10.42.1.5"},
		{"peer fidato con XFF", traefik, "10.42.1.5:1000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"peer non fidato con XFF: ignorato", traefik, "192.0.2.9:1000", []string{"203.0.113.7"}, "192.0.2.9"},
		{"peer fidato senza XFF", traefik, "10.42.1.5:1000", nil, "10.42.1.5"},
		{"XFF malformato", traefik, "10.42.1.5:1000", []string{"non-un-ip"}, "10.42.1.5"},
		{"XFF con una voce malformata tra le valide", traefik, "10.42.1.5:1000", []string{"203.0.113.7, boh"}, "10.42.1.5"},
		{"più hop: ultimo non fidato da destra", two, "10.42.1.5:1000", []string{"6.6.6.6, 203.0.113.7, 192.168.0.4"}, "203.0.113.7"},
		{"il client falsifica la testa di XFF", traefik, "10.42.1.5:1000", []string{"1.1.1.1, 203.0.113.7"}, "203.0.113.7"},
		{"più righe XFF", traefik, "10.42.1.5:1000", []string{"1.1.1.1", "203.0.113.7"}, "203.0.113.7"},
		{"tutti gli hop fidati: il più a sinistra", traefik, "10.42.1.5:1000", []string{"10.42.9.9, 10.42.8.8"}, "10.42.9.9"},
		{"IPv6 nell'XFF", traefik, "10.42.1.5:1000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"RemoteAddr non valido", traefik, "boh", []string{"203.0.113.7"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tc.trusted); got != tc.want {
				t.Errorf("ClientIP = %q, voluto %q", got, tc.want)
			}
		})
	}
}

func TestToCore_ClientIPDaXFFSoloSeProxyFidato(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trusted []*net.IPNet
		remote  string
		want    string
	}{
		{"proxy fidato", mustCIDRs(t, "10.42.0.0/16"), "10.42.1.5:1", "203.0.113.7"},
		{"peer non fidato", mustCIDRs(t, "10.42.0.0/16"), "198.51.100.9:1", "198.51.100.9"},
		{"nessuna configurazione", nil, "10.42.1.5:1", "10.42.1.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get(ClientIPHeader)
			}))
			defer core.Close()
			u, _ := url.Parse(core.URL)
			h := ToCore(u, time.Second, discardLogger(), WithTrustedProxies(tc.trusted))
			req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", "203.0.113.7")
			req.Header.Set(ClientIPHeader, "6.6.6.6")
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Errorf("%s a valle = %q, voluto %q", ClientIPHeader, got, tc.want)
			}
		})
	}
}

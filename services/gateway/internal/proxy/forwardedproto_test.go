package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestToCore_XForwardedProto(t *testing.T) {
	_, trustedNet, _ := net.ParseCIDR("192.0.2.0/24")
	cases := []struct {
		name    string
		remote  string
		inProto string
		trusted bool
		want    string
	}{
		{"peer fidato con https", "192.0.2.1:1234", "https", true, "https"},
		{"peer fidato con http", "192.0.2.1:1234", "http", true, "http"},
		{"peer fidato senza header", "192.0.2.1:1234", "", true, "http"},
		{"peer fidato con valore non valido", "192.0.2.1:1234", "javascript", true, "http"},
		{"peer non fidato con https", "198.51.100.9:1234", "https", true, "http"},
		{"nessun proxy fidato configurato", "192.0.2.1:1234", "https", false, "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("X-Forwarded-Proto")
			}))
			defer core.Close()
			coreURL, _ := url.Parse(core.URL)
			var opts []Option
			if tc.trusted {
				opts = append(opts, WithTrustedProxies([]*net.IPNet{trustedNet}))
			}
			h := ToCore(coreURL, time.Second, discardLogger(), opts...)
			req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
			req.RemoteAddr = tc.remote
			if tc.inProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.inProto)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Errorf("X-Forwarded-Proto visto a valle = %q, voluto %q", got, tc.want)
			}
		})
	}
}

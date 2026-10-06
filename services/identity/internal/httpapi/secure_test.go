package httpapi

import (
	"crypto/tls"
	"net"
	"net/http/httptest"
	"testing"
)

func TestIsSecure(t *testing.T) {
	_, gw, _ := net.ParseCIDR("10.42.0.0/16")
	tests := []struct {
		name    string
		trusted []*net.IPNet
		remote  string
		proto   string
		tls     bool
		want    bool
	}{
		{"http semplice", []*net.IPNet{gw}, "10.42.1.5:1000", "", false, false},
		{"peer fidato con https", []*net.IPNet{gw}, "10.42.1.5:1000", "https", false, true},
		{"peer fidato con HTTPS maiuscolo", []*net.IPNet{gw}, "10.42.1.5:1000", "HTTPS", false, true},
		{"peer fidato con http", []*net.IPNet{gw}, "10.42.1.5:1000", "http", false, false},
		{"peer non fidato con https", []*net.IPNet{gw}, "192.0.2.9:1000", "https", false, false},
		{"nessun proxy fidato con https", nil, "10.42.1.5:1000", "https", false, false},
		{"TLS diretto", nil, "192.0.2.9:1000", "", true, true},
		{"RemoteAddr non valido", []*net.IPNet{gw}, "boh", "https", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &server{trustedProxies: tc.trusted}
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := s.isSecure(r); got != tc.want {
				t.Errorf("isSecure = %v, voluto %v", got, tc.want)
			}
		})
	}
}

package httpapi

import (
	"net"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	_, gw, _ := net.ParseCIDR("10.42.0.0/16")
	tests := []struct {
		name    string
		trusted []*net.IPNet
		remote  string
		header  string
		want    string
	}{
		{"nessun proxy fidato: header ignorato", nil, "10.42.1.5:1000", "203.0.113.7", "10.42.1.5"},
		{"proxy fidato: si usa l'header", []*net.IPNet{gw}, "10.42.1.5:1000", "203.0.113.7", "203.0.113.7"},
		{"peer non fidato: header ignorato", []*net.IPNet{gw}, "192.0.2.9:1000", "203.0.113.7", "192.0.2.9"},
		{"proxy fidato senza header", []*net.IPNet{gw}, "10.42.1.5:1000", "", "10.42.1.5"},
		{"proxy fidato, header non valido", []*net.IPNet{gw}, "10.42.1.5:1000", "non-un-ip", "10.42.1.5"},
		{"proxy fidato, IPv6 nell'header", []*net.IPNet{gw}, "10.42.1.5:1000", "2001:db8::1", "2001:db8::1"},
		{"RemoteAddr non valido", []*net.IPNet{gw}, "boh", "203.0.113.7", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &server{trustedProxies: tc.trusted}
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			if tc.header != "" {
				r.Header.Set(ClientIPHeader, tc.header)
			}
			if got := s.clientIP(r); got != tc.want {
				t.Errorf("clientIP = %q, voluto %q", got, tc.want)
			}
		})
	}
}

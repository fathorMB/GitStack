package main

import "testing"

func TestCIExternalListener(t *testing.T) {
	cases := []struct {
		name                string
		enabled             bool
		addr                string
		internalUp          bool
		wantStart, wantWarn bool
	}{
		{"CI spenta con indirizzo: niente listener e Warn", false, ":8082", true, false, true},
		{"CI spenta senza indirizzo", false, "", true, false, false},
		{"CI accesa con indirizzo e porta interna", true, ":8082", true, true, false},
		{"CI accesa senza porta interna", true, ":8082", false, false, false},
		{"CI accesa senza indirizzo", true, "", true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, warn := ciExternalListener(c.enabled, c.addr, c.internalUp)
			if start != c.wantStart || warn != c.wantWarn {
				t.Fatalf("start=%v warn=%v, attesi %v %v", start, warn, c.wantStart, c.wantWarn)
			}
		})
	}
}

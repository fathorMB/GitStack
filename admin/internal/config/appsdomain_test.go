package config

import (
	"strings"
	"testing"
)

func TestValidateAppsDomain(t *testing.T) {
	for _, d := range []string{"apps.azienda.it", "apps.gitstack.internal", "a.b", "apps.git.azienda.it", "x-1.example.com"} {
		if err := ValidateAppsDomain(d); err != nil {
			t.Errorf("%q: %v", d, err)
		}
	}
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63)
	for _, d := range []string{"", "apps", "Apps.azienda.it", "apps..azienda.it", "-a.azienda.it", "a-.azienda.it", "10.0.0.5", "a_b.azienda.it", "apps.azienda.it.", long} {
		if err := ValidateAppsDomain(d); err == nil {
			t.Errorf("%q accettato", d)
		}
	}
}

func TestParseAppsDomain(t *testing.T) {
	base := "version: 1\nhost: git.azienda.it\n"
	c, err := Parse([]byte(base), "t")
	if err != nil || c.AppsDomain != "" {
		t.Fatalf("v2 senza dominio: %v %+v", err, c)
	}
	c, err = Parse([]byte(base+"apps_domain: apps.azienda.it\n"), "t")
	if err != nil || c.AppsDomain != "apps.azienda.it" {
		t.Fatalf("con dominio: %v %+v", err, c)
	}
	if _, err = Parse([]byte(base+"apps_domain: Apps\n"), "t"); err == nil {
		t.Error("dominio non valido accettato")
	}
}

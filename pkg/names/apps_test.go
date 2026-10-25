package names

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAppName(t *testing.T) {
	tests := []struct {
		in   string
		want error
	}{
		{"web", nil},
		{"a", nil},
		{"sito-2", nil},
		{strings.Repeat("a", 40), nil},
		{"", ErrAppNameEmpty},
		{strings.Repeat("a", 41), ErrAppNameTooLong},
		{"Web", ErrAppNameChar},
		{"my_app", ErrAppNameChar},
		{"a.b", ErrAppNameChar},
		{"-web", ErrAppNameDash},
		{"web-", ErrAppNameDash},
	}
	for _, tt := range tests {
		if got := ValidateAppName(tt.in); !errors.Is(got, tt.want) {
			t.Errorf("ValidateAppName(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestValidateAppsDomain(t *testing.T) {
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63)
	ok := []string{"apps.azienda.it", "apps.gitstack.internal", "a.b", "x-1.example.com", "apps.git.azienda.it"}
	bad := []string{"", "apps", "Apps.azienda.it", "apps..azienda.it", "-apps.azienda.it", "apps-.azienda.it",
		"apps.azienda.it.", "192.168.1.5", "app_s.azienda.it", "apps.azienda.it/x", long}
	for _, d := range ok {
		if err := ValidateAppsDomain(d); err != nil {
			t.Errorf("ValidateAppsDomain(%q) = %v, want nil", d, err)
		}
	}
	for _, d := range bad {
		if err := ValidateAppsDomain(d); err == nil {
			t.Errorf("ValidateAppsDomain(%q) = nil, want error", d)
		}
	}
}

func TestAppAddress(t *testing.T) {
	tests := []struct{ owner, repo, app, want string }{
		{"acme", "handbook", "web", "web-handbook-acme.apps.acme.it"},
		{"acme", "My_Repo.v2", "api", "api-my-repo-v2-acme.apps.acme.it"},
		{"Acme_Corp", "a--b__c", "x", "x-a-b-c-acme-corp.apps.acme.it"},
	}
	for _, tt := range tests {
		got, err := AppAddress(tt.owner, tt.repo, tt.app, "apps.acme.it")
		if err != nil || got != tt.want {
			t.Errorf("AppAddress(%q,%q,%q) = %q, %v; want %q", tt.owner, tt.repo, tt.app, got, err, tt.want)
		}
	}
	// Lo stesso repo e la stessa app con owner diversi: indirizzi leggibili e diversi.
	a, _ := AppAddress("acme", "handbook", "web", "apps.acme.it")
	b, _ := AppAddress("globex", "handbook", "web", "apps.acme.it")
	if a == b || b != "web-handbook-globex.apps.acme.it" {
		t.Errorf("owners must give different addresses: %q %q", a, b)
	}
	for _, tc := range []struct{ owner, repo, app, domain string }{
		{"acme", "handbook", "Web", "apps.acme.it"},
		{"acme", "handbook", "web", "acme"},
		{"acme", "x.git", "web", "apps.acme.it"},
	} {
		if _, err := AppAddress(tc.owner, tc.repo, tc.app, tc.domain); err == nil {
			t.Errorf("%+v must be rejected", tc)
		}
	}
	for _, tc := range []struct{ owner, repo string }{{"acme", "___"}, {"___", "handbook"}, {"", "handbook"}} {
		if _, err := AppAddress(tc.owner, tc.repo, "web", "apps.acme.it"); !errors.Is(err, ErrAddressInvalid) {
			t.Errorf("%+v: err = %v, want ErrAddressInvalid", tc, err)
		}
	}
}

// Nessun taglio e nessun hash: 63 caratteri passano, 64 no.
func TestAppAddressTooLong(t *testing.T) {
	// "web-" (4) + repo + "-acme" (5) = 63 con repo di 54 caratteri.
	ok, err := AppAddress("acme", strings.Repeat("r", 54), "web", "apps.acme.it")
	if err != nil {
		t.Fatalf("63 caratteri: %v", err)
	}
	if label := strings.TrimSuffix(ok, ".apps.acme.it"); len(label) != MaxDNSLabelLen {
		t.Errorf("label %q has %d characters", label, len(label))
	}
	got, err := AppAddress("acme", strings.Repeat("r", 55), "web", "apps.acme.it")
	if !errors.Is(err, ErrAddressTooLong) || got != "" {
		t.Errorf("64 caratteri: %q, %v; want ErrAddressTooLong", got, err)
	}
	if _, err := AppAddress("acme", strings.Repeat("r", 100), strings.Repeat("a", 40), "apps.acme.it"); !errors.Is(err, ErrAddressTooLong) {
		t.Errorf("err = %v, want ErrAddressTooLong", err)
	}
}

// «a-b» in «c» e «a» in «b-c» (stesso owner) danno la stessa etichetta: il
// secondo è un conflitto, senza alcun suffisso automatico.
func TestCheckAppAddressConflict(t *testing.T) {
	const domain = "apps.acme.it"
	used := map[string]bool{}
	taken := func(a string) bool { return used[a] }
	first, err := CheckAppAddress("acme", "c", "a-b", domain, taken)
	if err != nil || first != "a-b-c-acme.apps.acme.it" {
		t.Fatalf("first = %q, %v", first, err)
	}
	used[first] = true
	got, err := CheckAppAddress("acme", "b-c", "a", domain, taken)
	if !errors.Is(err, ErrAddressConflict) || got != "" {
		t.Errorf("second = %q, %v; want ErrAddressConflict", got, err)
	}
	// Anche '_' e maiuscole portano alla stessa etichetta.
	if _, err := CheckAppAddress("acme", "B_C", "a", domain, taken); !errors.Is(err, ErrAddressConflict) {
		t.Errorf("B_C: err = %v, want ErrAddressConflict", err)
	}
	// Un altro owner non confligge: l'owner è nell'etichetta.
	other, err := CheckAppAddress("globex", "b-c", "a", domain, taken)
	if err != nil || other != "a-b-c-globex.apps.acme.it" {
		t.Errorf("other owner = %q, %v", other, err)
	}
	// Senza taken non c'è controllo; gli errori di AppAddress passano.
	if _, err := CheckAppAddress("acme", "c", "a-b", domain, nil); err != nil {
		t.Errorf("nil taken: %v", err)
	}
	if _, err := CheckAppAddress("acme", "c", "Web", domain, taken); err == nil {
		t.Error("invalid app name must fail")
	}
	if _, err := CheckAppAddress("acme", strings.Repeat("r", 60), "web", domain, taken); !errors.Is(err, ErrAddressTooLong) {
		t.Errorf("too long: err = %v", err)
	}
}

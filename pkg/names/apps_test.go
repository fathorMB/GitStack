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
		{"acme", "sito", "web", "web-sito.apps.azienda.it"},
		{"acme", "My_Repo.v2", "api", "api-my-repo-v2.apps.azienda.it"},
		{"acme", "a--b__c", "x", "x-a-b-c.apps.azienda.it"},
		{"acme", "___", "x", "x-repo.apps.azienda.it"},
	}
	for _, tt := range tests {
		got, err := AppAddress(tt.owner, tt.repo, tt.app, "apps.azienda.it")
		if err != nil || got != tt.want {
			t.Errorf("AppAddress(%q,%q,%q) = %q, %v; want %q", tt.owner, tt.repo, tt.app, got, err, tt.want)
		}
	}
	if _, err := AppAddress("acme", "sito", "Web", "apps.azienda.it"); err == nil {
		t.Error("app name with uppercase must be rejected")
	}
	if _, err := AppAddress("acme", "sito", "web", "azienda"); err == nil {
		t.Error("domain with one label must be rejected")
	}
	if _, err := AppAddress("acme", "x.git", "web", "apps.azienda.it"); err == nil {
		t.Error("invalid repo name must be rejected")
	}
}

func TestAppAddressLabelLimit(t *testing.T) {
	repo := strings.Repeat("r", 100)
	repo = repo[:99] + "q"
	app := strings.Repeat("a", 40)
	got, err := AppAddress("acme", repo, app, "apps.azienda.it")
	if err != nil {
		t.Fatal(err)
	}
	label := strings.TrimSuffix(got, ".apps.azienda.it")
	if len(label) > MaxDNSLabelLen || strings.HasSuffix(label, "-") || strings.Contains(label, "--") {
		t.Errorf("label %q (len %d) breaks DNS rules", label, len(label))
	}
	// Due repo che differiscono solo dopo il punto di taglio non coincidono.
	other, err := AppAddress("acme", repo[:99]+"x", app, "apps.azienda.it")
	if err != nil {
		t.Fatal(err)
	}
	if other == got {
		t.Errorf("truncated labels collide: %q", got)
	}
}

// Il caso classico: «a-b» in «c» e «a» in «b-c» danno entrambe a-b-c.
func TestUniqueAppAddressCollisions(t *testing.T) {
	const domain = "apps.azienda.it"
	used := map[string]string{} // indirizzo -> chiave dell'app
	taken := func(a string) bool { _, ok := used[a]; return ok }
	type app struct{ owner, repo, name string }
	apps := []app{
		{"acme", "c", "a-b"},
		{"acme", "b-c", "a"},         // stessa etichetta a-b-c
		{"acme", "B_C", "a"},         // '_' e maiuscole: ancora a-b-c
		{"other", "b.c", "a"},        // owner diverso, stessa etichetta
		{"acme", "sito", "web"},      // nessuna collisione
		{"other", "sito", "web"},     // stesso repo e app, owner diverso
		{"acme", "sito", "web-sito"}, // web-sito-sito: non collide
	}
	got := map[app]string{}
	for _, a := range apps {
		addr, err := UniqueAppAddress(a.owner, a.repo, a.name, domain, taken)
		if err != nil {
			t.Fatalf("%v: %v", a, err)
		}
		if prev, dup := used[addr]; dup {
			t.Fatalf("%v: address %q already used by %s", a, addr, prev)
		}
		if !strings.HasSuffix(addr, "."+domain) {
			t.Errorf("%v: %q outside %s", a, addr, domain)
		}
		label := strings.TrimSuffix(addr, "."+domain)
		if len(label) > MaxDNSLabelLen || strings.Contains(label, ".") {
			t.Errorf("%v: label %q invalid", a, label)
		}
		used[addr] = a.owner + "/" + a.repo + "/" + a.name
		got[a] = addr
	}
	if got[apps[0]] != "a-b-c.apps.azienda.it" {
		t.Errorf("first app keeps the plain address, got %q", got[apps[0]])
	}
	if got[apps[4]] != "web-sito.apps.azienda.it" {
		t.Errorf("app without collision keeps the plain address, got %q", got[apps[4]])
	}
	if len(used) != len(apps) {
		t.Errorf("%d addresses for %d apps", len(used), len(apps))
	}

	// Un indirizzo occupato non si restituisce di nuovo.
	again, err := UniqueAppAddress("acme", "b-c", "a", domain, taken)
	if err != nil {
		t.Fatal(err)
	}
	if again == got[apps[1]] {
		t.Errorf("an occupied address cannot be returned again: %q", again)
	}
	// Stabilità: liberato il suo, l'app riprende lo stesso suffisso.
	delete(used, got[apps[1]])
	same, err := UniqueAppAddress("acme", "b-c", "a", domain, taken)
	if err != nil {
		t.Fatal(err)
	}
	if same != got[apps[1]] {
		t.Errorf("suffix not stable: %q vs %q", same, got[apps[1]])
	}
}

func TestUniqueAppAddressExhausted(t *testing.T) {
	_, err := UniqueAppAddress("acme", "sito", "web", "apps.azienda.it", func(string) bool { return true })
	if !errors.Is(err, ErrNoFreeAddress) {
		t.Errorf("err = %v, want ErrNoFreeAddress", err)
	}
	if _, err := UniqueAppAddress("acme", "sito", "Web", "apps.azienda.it", nil); err == nil {
		t.Error("invalid app name must fail even with nil taken")
	}
}

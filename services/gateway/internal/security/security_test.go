package security

import (
	"reflect"
	"testing"
)

func TestGrants(t *testing.T) {
	cases := []struct {
		have     []string
		required string
		want     bool
	}{
		{[]string{"read:user"}, "read:user", true},
		{[]string{"write:user"}, "read:user", true},
		{[]string{"read:user"}, "write:user", false},
		{[]string{"admin:org"}, "write:org", true},
		{[]string{"admin:org"}, "read:org", true},
		{[]string{"admin:org"}, "admin:org", true},
		{[]string{"write:org"}, "admin:org", false},
		{[]string{"write:org"}, "read:org", true},
		// ambiti diversi non si includono
		{[]string{"write:user"}, "read:org", false},
		{[]string{"admin:org"}, "read:user", false},
		{[]string{"write:resource"}, "read:resource", true},
		{[]string{"read:resource"}, "write:resource", false},
		{nil, "read:user", false},
		{[]string{"read:user", "write:org"}, "read:org", true},
		{[]string{"junk"}, "read:user", false},
	}
	for _, c := range cases {
		if got := Grants(c.have, c.required); got != c.want {
			t.Errorf("Grants(%v, %q) = %v, voluto %v", c.have, c.required, got, c.want)
		}
	}
}

func TestMissing(t *testing.T) {
	got := Missing([]string{"read:user"}, []string{"read:user", "write:org"})
	if !reflect.DeepEqual(got, []string{"write:org"}) {
		t.Errorf("Missing = %v", got)
	}
	if got := Missing(nil, nil); len(got) != 0 {
		t.Errorf("Missing senza requisiti = %v", got)
	}
}

func TestNewTable_Errori(t *testing.T) {
	dup := []Route{
		{Method: "GET", Path: "/a", Public: true},
		{Method: "GET", Path: "/a", Public: true},
	}
	if _, err := NewTable(dup); err == nil {
		t.Error("una rotta dichiarata due volte deve dare errore")
	}
	vuota := []Route{{Method: "GET", Path: "/a"}}
	if _, err := NewTable(vuota); err == nil {
		t.Error("una rotta né pubblica né con credenziali deve dare errore")
	}
}

func TestLookup(t *testing.T) {
	table, err := NewTable(Routes)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := table.Lookup("PUT", "/users/alice/password")
	if !ok || m.Route.OperationID != "changePassword" || m.Params["username"] != "alice" || m.Route.PasswordChangeExempt != ExemptSelf {
		t.Fatalf("Lookup = %+v %v", m, ok)
	}
	// un segmento letterale vince su un parametro
	if m, ok := table.Lookup("GET", "/auth/oidc/providers"); !ok || m.Route.OperationID != "listOidcProviders" || !m.Route.Public {
		t.Fatalf("Lookup providers = %+v %v", m, ok)
	}
	for _, c := range [][2]string{
		{"GET", "/nope"}, {"PATCH", "/users"}, {"GET", "/users/"}, {"GET", "/users/a/b/c"},
		{"POST", "/internal/verify"}, {"GET", "/resources/x/y"},
	} {
		if _, ok := table.Lookup(c[0], c[1]); ok {
			t.Errorf("Lookup(%s %s) ha trovato una dichiarazione", c[0], c[1])
		}
	}
}

// Le eccezioni alla regola della password da cambiare sono esattamente le tre
// del contratto (CurrentSession.mustChangePassword).
func TestRoutes_EccezioniPasswordDaCambiare(t *testing.T) {
	got := map[string]Exemption{}
	for _, r := range Routes {
		if r.PasswordChangeExempt != ExemptNever {
			got[r.OperationID] = r.PasswordChangeExempt
		}
	}
	want := map[string]Exemption{"getCurrentSession": ExemptAlways, "logout": ExemptAlways, "changePassword": ExemptSelf}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("eccezioni = %v, volute %v", got, want)
	}
}

func TestRoutes_NienteInternal(t *testing.T) {
	for _, r := range Routes {
		if len(r.Path) >= 9 && r.Path[:9] == "/internal" {
			t.Errorf("%s %s: /internal non deve essere nella tabella del gateway", r.Method, r.Path)
		}
	}
}

package scopes

import "testing"

func TestGrants(t *testing.T) {
	tests := []struct {
		name string
		have []string
		need string
		want bool
	}{
		{"esatto", []string{"read:resource"}, "read:resource", true},
		{"write implica read", []string{"write:resource"}, "read:resource", true},
		{"read non implica write", []string{"read:resource"}, "write:resource", false},
		{"admin org implica write", []string{"admin:org"}, "write:org", true},
		{"admin org implica read", []string{"admin:org"}, "read:org", true},
		{"write non implica admin", []string{"write:org"}, "admin:org", false},
		{"ambito diverso", []string{"write:user"}, "read:resource", false},
		{"vuoto", nil, "read:resource", false},
		{"malformato", []string{"resource"}, "read:resource", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Grants(tt.have, tt.need); got != tt.want {
				t.Errorf("Grants(%v, %q) = %v, want %v", tt.have, tt.need, got, tt.want)
			}
		})
	}
}

// Il catalogo e la regola generica non devono divergere.
func TestImplicationsCoerentiConGrants(t *testing.T) {
	for s, imps := range Implications {
		for _, imp := range imps {
			if !Grants([]string{s}, imp) {
				t.Errorf("Grants([%q], %q) = false, ma il catalogo lo dichiara", s, imp)
			}
		}
	}
}

func TestMissing(t *testing.T) {
	got := Missing([]string{"write:resource"}, []string{"read:resource", "read:user"})
	if len(got) != 1 || got[0] != "read:user" {
		t.Errorf("Missing = %v", got)
	}
}

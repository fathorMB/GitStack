package gitref

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateRef(t *testing.T) {
	ok := []string{"main", "feature/x", "v1.0.0", "release-2", "a1b2c3d", "utente/è", "x.y"}
	for _, r := range ok {
		if err := ValidateRef(r); err != nil {
			t.Errorf("%q deve essere valido: %v", r, err)
		}
	}
	bad := []string{"", "-x", "--all", "/x", "x/", "a..b", "a b", "a\tb", "a\x00b", "a\nb", "x~1", "x^", "a:b", "a?b", "a*b", "a[b", `a\b`, "a@{1}", "@", "x.lock", "x.", "a//b", strings.Repeat("a", 256)}
	for _, r := range bad {
		if err := ValidateRef(r); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("%q deve essere rifiutato, err = %v", r, err)
		}
	}
	if err := ValidateRef(strings.Repeat("a", 255)); err != nil {
		t.Errorf("255 caratteri sono ammessi: %v", err)
	}
}

func TestValidatePath(t *testing.T) {
	good := map[string]string{"a": "a", "a/b/c.txt": "a/b/c.txt", "dir/": "dir", "-x": "-x", ":(glob)x": ":(glob)x", "è/ü": "è/ü"}
	for in, want := range good {
		got, err := ValidatePath(in, true)
		if err != nil || got != want {
			t.Errorf("%q = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"/a", "a//b", "./a", "a/./b", "../a", "a/..", "/", "a\x00", "a\nb", `a\b`, strings.Repeat("a", 4097)} {
		if _, err := ValidatePath(in, false); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%q deve essere rifiutato, err = %v", in, err)
		}
	}
	if p, err := ValidatePath("", false); err != nil || p != "" {
		t.Errorf("vuoto non obbligatorio: %q %v", p, err)
	}
	if _, err := ValidatePath("", true); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("vuoto obbligatorio: %v", err)
	}
}

func TestIsHexSHA(t *testing.T) {
	for s, want := range map[string]bool{
		"abcdef1": true, "abcdef": false, "ABCDEF1": false, "abcdefg": false, strings.Repeat("a", 64): true, strings.Repeat("a", 65): false, "": false,
	} {
		if got := IsHexSHA(s); got != want {
			t.Errorf("%q = %v", s, got)
		}
	}
}

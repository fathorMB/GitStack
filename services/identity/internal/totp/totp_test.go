package totp

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Vettori di RFC 6238 appendice B (SHA1, secret "12345678901234567890"),
// troncati alle ultime 6 cifre dei codici a 8.
func TestCode_RFC6238(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		if got := Code(secret, Step(time.Unix(c.unix, 0))); got != c.want {
			t.Errorf("t=%d: codice %s, atteso %s", c.unix, got, c.want)
		}
	}
}

func TestMatch_Finestra(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111109, 0)
	cur := Step(now)
	for d, want := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		step, ok := Match(secret, Code(secret, cur+d), now)
		if ok != want {
			t.Errorf("scostamento %d: ok=%v, atteso %v", d, ok, want)
		}
		if ok && step != cur+d {
			t.Errorf("scostamento %d: passo %d, atteso %d", d, step, cur+d)
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "000000"} {
		if _, ok := Match(secret, bad, now); ok && bad != "000000" {
			t.Errorf("%q non dovrebbe passare", bad)
		}
	}
}

func TestProvisioningURI(t *testing.T) {
	u := ProvisioningURI("GitStack", "alice@example.com", []byte("12345678901234567890"))
	p, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if p.Scheme != "otpauth" || p.Host != "totp" {
		t.Fatalf("uri: %s", u)
	}
	q := p.Query()
	if q.Get("secret") != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" || q.Get("issuer") != "GitStack" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Fatalf("query: %v", q)
	}
}

func TestRecoveryCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c, err := NewRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != 11 || c[5] != '-' || seen[c] {
			t.Fatalf("codice %q non valido o ripetuto", c)
		}
		seen[c] = true
		if NormalizeRecoveryCode(strings.ToLower(c)) != strings.ReplaceAll(c, "-", "") {
			t.Fatalf("normalizzazione di %q", c)
		}
	}
}

package password

import (
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashFormatAndVerify(t *testing.T) {
	h, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("formato PHC inatteso: %q", h)
	}
	if strings.Contains(h, "correct horse") {
		t.Fatal("la password compare nell'hash")
	}
	ok, err := Verify("correct horse battery", h)
	if err != nil || !ok {
		t.Fatalf("Verify giusta: ok=%v err=%v", ok, err)
	}
	ok, err = Verify("correct horse batterY", h)
	if err != nil || ok {
		t.Fatalf("Verify sbagliata: ok=%v err=%v", ok, err)
	}
	if NeedsRehash(h) {
		t.Fatal("un hash appena fatto non deve richiedere rehash")
	}
}

func TestHashSaltIsRandom(t *testing.T) {
	a, _ := Hash("same password 123")
	b, _ := Hash("same password 123")
	if a == b {
		t.Fatal("due hash della stessa password sono identici: salt non casuale")
	}
}

func TestVerifyUsesStoredParams(t *testing.T) {
	// Un hash con parametri più leggeri resta verificabile e va riscritto.
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte("old parameters!"), salt, 1, 8*1024, 1, KeyLen)
	old := encode(8*1024, 1, 1, salt, key)
	ok, err := Verify("old parameters!", old)
	if err != nil || !ok {
		t.Fatalf("hash con parametri vecchi non verificato: ok=%v err=%v", ok, err)
	}
	if !NeedsRehash(old) {
		t.Fatal("NeedsRehash deve essere vero per parametri diversi")
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, bad := range []string{
		"", "plaintext", "$argon2i$v=19$m=1,t=1,p=1$YQ$YQ",
		"$argon2id$v=18$m=19456,t=2,p=1$YQ$YQ",
		"$argon2id$v=19$m=99999999,t=2,p=1$YQ$YQ",
		"$argon2id$v=19$m=19456,t=2,p=1$!!$YQ",
	} {
		if _, err := Verify("x", bad); err == nil {
			t.Errorf("hash %q accettato", bad)
		}
	}
}

func TestValidatePolicy(t *testing.T) {
	cases := []struct {
		pw, user, email string
		want            error
	}{
		{"short", "alice", "", ErrTooShort},
		{"            ", "alice", "", ErrBlank},
		{"alicealicealice", "alicealicealice", "", ErrSameAsUsername},
		{"alice@example.com", "alice", "alice@example.com", ErrSameAsUsername},
		{strings.Repeat("a", 1025), "alice", "", ErrTooLong},
		{"una password lunga", "alice", "", nil},
		{"àèìòùàèìòùàè", "alice", "", nil}, // 12 caratteri, più di 12 byte
	}
	for _, c := range cases {
		if got := ValidatePolicy(c.pw, c.user, c.email); got != c.want {
			t.Errorf("ValidatePolicy(%q) = %v, atteso %v", c.pw, got, c.want)
		}
	}
}

func TestVerifyDummyAlwaysFalse(t *testing.T) {
	if VerifyDummy("qualsiasi cosa") {
		t.Fatal("VerifyDummy deve dare false")
	}
}

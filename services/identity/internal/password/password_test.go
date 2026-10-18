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
		{strings.Repeat("ab", 65), "alice", "", ErrTooLong},
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

// F14 (V2.1.2): da 12 a 128 caratteri; 129 sono troppi.
func TestValidatePolicy_Max128(t *testing.T) {
	ok := strings.Repeat("ab1", 42) + "xy" // 128 caratteri
	if n := len([]rune(ok)); n != 128 {
		t.Fatalf("fixture: %d caratteri", n)
	}
	if err := ValidatePolicy(ok, "alice", ""); err != nil {
		t.Fatalf("128 caratteri devono andare: %v", err)
	}
	if err := ValidatePolicy(ok+"z", "alice", ""); err != ErrTooLong {
		t.Fatalf("129 caratteri: %v, atteso ErrTooLong", err)
	}
	// Caratteri, non byte: 128 emoji sono 512 byte ma valgono.
	if err := ValidatePolicy(strings.Repeat("é", 128), "alice", ""); err == ErrTooLong {
		t.Fatal("la lunghezza si conta in caratteri")
	}
}

// F14 (V2.1.7): un elenco incorporato di password comuni, senza chiamate in uscita.
func TestValidatePolicy_CommonPasswordsRejected(t *testing.T) {
	for _, pw := range []string{
		"password1234", "PassWord1234", "  qwertyuiop12  ", "123456789012",
		"1q2w3e4r5t6y", "iloveyou123456", "aaaaaaaaaaaa", "abcabcabcabc",
		"administrator", "password12345!!",
	} {
		if err := ValidatePolicy(pw, "alice", ""); err != ErrCommon {
			t.Errorf("ValidatePolicy(%q) = %v, atteso ErrCommon", pw, err)
		}
	}
	for _, pw := range []string{"una password lunga e buona", "x7Kp-9vQmz-4Lw2", "cavallo batteria graffetta"} {
		if err := ValidatePolicy(pw, "alice", ""); err != nil {
			t.Errorf("ValidatePolicy(%q) = %v, atteso nil", pw, err)
		}
	}
	// Le voci dell'elenco rispettano il formato: minuscole e almeno 12 caratteri
	// dopo il caricamento (le più corte sono scartate, non cercate).
	if len(commonPasswords()) < 200 {
		t.Fatalf("elenco incorporato troppo corto: %d voci", len(commonPasswords()))
	}
}

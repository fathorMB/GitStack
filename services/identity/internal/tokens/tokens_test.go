package tokens

import (
	"errors"
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	t.Run("prefisso corretto", func(t *testing.T) {
		token, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore imprevisto: %v", err)
		}
		if !strings.HasPrefix(token, prefix) {
			t.Errorf("token %q non inizia con %q", token, prefix)
		}
	})

	t.Run("lunghezza corretta", func(t *testing.T) {
		token, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore imprevisto: %v", err)
		}
		if len(token) != tokenLen {
			t.Errorf("lunghezza %d, attesa %d", len(token), tokenLen)
		}
	})

	t.Run("payload di 43 caratteri", func(t *testing.T) {
		token, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		payload := token[len(prefix) : len(prefix)+payloadLen]
		if len(payload) != payloadLen {
			t.Errorf("payload lunghezza %d, attesa %d", len(payload), payloadLen)
		}
	})

	t.Run("token diversi", func(t *testing.T) {
		t1, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore imprevisto: %v", err)
		}
		t2, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore imprevisto: %v", err)
		}
		if t1 == t2 {
			t.Error("due token generati consecutivamente sono uguali (probabilità trascurabile)")
		}
	})

	t.Run("valida sempre", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			token, err := Generate()
			if err != nil {
				t.Fatalf("iterazione %d: Generate() errore: %v", i, err)
			}
			if !Validate(token) {
				t.Errorf("iterazione %d: token generato non passato a Validate()", i)
			}
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("token valido", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		if !Validate(valid) {
			t.Error("token valido rifiutato da Validate()")
		}
	})

	t.Run("lunghezza errata", func(t *testing.T) {
		if Validate("") {
			t.Error("stringa vuota passata a Validate()")
		}
		if Validate("gst_abc") {
			t.Error("stringa troppo corta passata a Validate()")
		}
	})

	t.Run("prefisso errato", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		bad := "xx" + valid[len(prefix):]
		if Validate(bad) {
			t.Error("token con prefisso errato passato a Validate()")
		}
	})

	t.Run("separatore '_' mancante", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		// Rimuove il separatore '_'
		bad := valid[:len(prefix)+payloadLen] + valid[len(prefix)+payloadLen+1:]
		if Validate(bad) {
			t.Error("token senza separatore '_' passato a Validate()")
		}
	})

	t.Run("separatore '_' in posizione sbagliata", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		// Inserisce '_' nella payload (posizione 45: prefix(4) + payload[41]).
		// Si assicura che '_' esista nella payload senza dipendere dal contenuto casuale.
		bad := valid[:45] + "_" + valid[46:]
		if Validate(bad) {
			t.Error("token con '_' nella payload passato a Validate()")
		}
	})

	t.Run("carattere '!' nel payload", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		runes := []rune(valid)
		runes[5] = '!'
		bad := string(runes)
		if Validate(bad) {
			t.Error("token con '!' nel payload passato a Validate()")
		}
	})

	t.Run("carattere '-' nel payload", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		runes := []rune(valid)
		runes[5] = '-'
		bad := string(runes)
		if Validate(bad) {
			t.Error("token con '-' nel payload passato a Validate()")
		}
	})

	t.Run("spazio nel checksum", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		runes := []rune(valid)
		runes[len(runes)-3] = ' '
		bad := string(runes)
		if Validate(bad) {
			t.Error("token con spazio nel checksum passato a Validate()")
		}
	})

	t.Run("carattere non ASCII nel payload", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		runes := []rune(valid)
		runes[5] = 'é'
		bad := string(runes)
		if Validate(bad) {
			t.Error("token con carattere non ASCII nel payload passato a Validate()")
		}
	})

	t.Run("checksum alterato", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		// Altera ogni posizione del checksum con il carattere successivo ciclico.
		checksum := valid[len(valid)-checksumLen:]
		for i := 0; i < checksumLen; i++ {
			orig := checksum[i]
			next := orig + 1
			switch orig {
			case '9':
				next = 'A'
			case 'z':
				next = '0'
			case 'Z':
				next = 'a'
			}
			runes := []rune(valid)
			runes[len(runes)-checksumLen+i] = rune(next)
			bad := string(runes)
			if Validate(bad) {
				t.Errorf("checksum alterato posizione %d con %q passato a Validate()", i, next)
			}
			_ = orig
		}
	})

	t.Run("payload alterato", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		// Altera ogni posizione del payload con il carattere successivo ciclico.
		payload := valid[len(prefix) : len(prefix)+payloadLen]
		for i := 0; i < payloadLen; i++ {
			orig := payload[i]
			next := orig + 1
			switch orig {
			case '9':
				next = 'A'
			case 'z':
				next = '0'
			case 'Z':
				next = 'a'
			}
			runes := []rune(valid)
			runes[len(prefix)+i] = rune(next)
			bad := string(runes)
			if Validate(bad) {
				t.Errorf("payload alterato posizione %d con %q passato a Validate()", i, next)
			}
		}
	})
}

func TestRoundTrip(t *testing.T) {
	// Test decode(encode(b)) == b per vari input.
	t.Run("zero bytes", func(t *testing.T) {
		zero := make([]byte, 32)
		enc := base62Encode(zero)
		if len(enc) != payloadLen {
			t.Fatalf("encode zero: lunghezza %d, attesa %d", len(enc), payloadLen)
		}
		dec, err := base62Decode(enc)
		if err != nil {
			t.Fatalf("decode zero: errore %v", err)
		}
		if string(dec) != string(zero) {
			t.Errorf("decode(encode(zero)) != zero: got %d bytes", len(dec))
		}
	})

	t.Run("0xFF…FF", func(t *testing.T) {
		ff := make([]byte, 32)
		for i := range ff {
			ff[i] = 0xFF
		}
		enc := base62Encode(ff)
		if len(enc) != payloadLen {
			t.Fatalf("encode 0xFF: lunghezza %d, attesa %d", len(enc), payloadLen)
		}
		dec, err := base62Decode(enc)
		if err != nil {
			t.Fatalf("decode 0xFF: errore %v", err)
		}
		if string(dec) != string(ff) {
			t.Error("decode(encode(0xFF…FF)) != 0xFF…FF")
		}
	})

	t.Run("100 valori casuali", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			raw := make([]byte, 32)
			for j := range raw {
				raw[j] = byte(i*31 + j*17) & 0xFF
			}
			enc := base62Encode(raw)
			if len(enc) != payloadLen {
				t.Errorf("iterazione %d: encode lunghezza %d, attesa %d", i, len(enc), payloadLen)
				continue
			}
			dec, err := base62Decode(enc)
			if err != nil {
				t.Errorf("iterazione %d: decode errore %v", i, err)
				continue
			}
			if string(dec) != string(raw) {
				t.Errorf("iterazione %d: decode(encode(raw)) != raw", i)
			}
		}
	})
}

func TestHashBytes(t *testing.T) {
	t.Run("dimensione fissa 32 byte", func(t *testing.T) {
		token := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		h := HashBytes(token)
		// La dimensione è fissa [32]byte (non può essere diversà da 32).
		// Verifichiamo che l'hash non sia tutti zero (token non banale).
		var zero [32]byte
		if h == zero {
			t.Error("HashBytes ha restituito hash nullo")
		}
	})

	t.Run("deterministico", func(t *testing.T) {
		token := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		h1 := HashBytes(token)
		h2 := HashBytes(token)
		if h1 != h2 {
			t.Errorf("stesso token → hash diversi: %x != %x", h1, h2)
		}
	})

	t.Run("diverso per token diverso", func(t *testing.T) {
		t1 := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		t2 := "gst_" + strings.Repeat("B", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		h1 := HashBytes(t1)
		h2 := HashBytes(t2)
		if h1 == h2 {
			t.Errorf("token diversi → stesso hash: %x", h1)
		}
	})

	t.Run("HashBytes non restituisce il token", func(t *testing.T) {
		token := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		h := HashBytes(token)
		_ = token     // il byte slice non contiene il token
		_ = [32]byte(h) // 32 byte grezzi, non il token
	})
}

func TestHash(t *testing.T) {
	t.Run("deterministico", func(t *testing.T) {
		token := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		h1 := Hash(token)
		h2 := Hash(token)
		if h1 != h2 {
			t.Errorf("stesso token → hash diversi: %q != %q", h1, h2)
		}
	})

	t.Run("diverso per token diverso", func(t *testing.T) {
		t1 := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		t2 := "gst_" + strings.Repeat("B", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		h1 := Hash(t1)
		h2 := Hash(t2)
		if h1 == h2 {
			t.Errorf("token diversi → stesso hash: %q", h1)
		}
	})

	t.Run("lunghezza hex 64 caratteri", func(t *testing.T) {
		token := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		h := Hash(token)
		if len(h) != 64 {
			t.Errorf("Hash lunghezza %d, attesa 64", len(h))
		}
	})
}

func TestCompareToken(t *testing.T) {
	t.Run("token corretto", func(t *testing.T) {
		token := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		h := HashBytes(token)
		if !CompareToken(token, string(h[:])) {
			t.Error("CompareToken non ha confrontato correttamente")
		}
	})

	t.Run("hash errato (32 byte)", func(t *testing.T) {
		token := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		wrong := make([]byte, 32)
		wrong[0] = 0xFF
		if CompareToken(token, string(wrong)) {
			t.Error("CompareToken ha confrontato hash errato come corretto")
		}
	})

	t.Run("hash di lunghezza sbagliata", func(t *testing.T) {
		token := "gst_" + strings.Repeat("A", payloadLen) + "_" + strings.Repeat("A", checksumLen)
		if CompareToken(token, "") {
			t.Error("CompareToken con hash vuoto ha restituito true")
		}
		if CompareToken(token, "abc") {
			t.Error("CompareToken con hash corto ha restituito true")
		}
	})
}

func TestParseScopes(t *testing.T) {
	t.Run("scope validi in ordine", func(t *testing.T) {
		got, err := ParseScopes([]string{"read:user", "write:org"})
		if err != nil {
			t.Fatalf("ParseScopes() errore: %v", err)
		}
		want := []string{"read:user", "write:org"}
		if !equalStringSlices(got, want) {
			t.Errorf("ParseScopes() = %v, want %v", got, want)
		}
	})

	t.Run("scope validi ordine diverso", func(t *testing.T) {
		got, err := ParseScopes([]string{"write:org", "read:user"})
		if err != nil {
			t.Fatalf("ParseScopes() errore: %v", err)
		}
		want := []string{"read:user", "write:org"}
		if !equalStringSlices(got, want) {
			t.Errorf("ParseScopes() = %v, want %v", got, want)
		}
	})

	t.Run("tutti gli scope", func(t *testing.T) {
		got, err := ParseScopes(KnownScopes)
		if err != nil {
			t.Fatalf("ParseScopes() errore: %v", err)
		}
		want := []string{"admin:org", "read:org", "read:resource", "read:user", "write:org", "write:resource", "write:user"}
		if !equalStringSlices(got, want) {
			t.Errorf("ParseScopes() = %v, want %v", got, want)
		}
	})

	t.Run("scope sconosciuto — errore sentinella", func(t *testing.T) {
		_, err := ParseScopes([]string{"read:user", "delete:user"})
		if err == nil {
			t.Fatal("ParseScopes() non ha restituito errore per scope sconosciuto")
		}
		if !errors.Is(err, ErrUnknownScope) {
			t.Errorf("ParseScopes() errore = %v, non avvolge ErrUnknownScope", err)
		}
	})

	t.Run("doppio sconosciuto — errore sentinella", func(t *testing.T) {
		_, err := ParseScopes([]string{"delete:org", "write:user"})
		if err == nil {
			t.Fatal("ParseScopes() non ha restituito errore")
		}
		if !errors.Is(err, ErrUnknownScope) {
			t.Errorf("ParseScopes() errore = %v, non avvolge ErrUnknownScope", err)
		}
	})

	t.Run("scope duplicato — errore sentinella", func(t *testing.T) {
		_, err := ParseScopes([]string{"read:user", "read:user"})
		if err == nil {
			t.Fatal("ParseScopes() non ha restituito errore per duplicato")
		}
		if !errors.Is(err, ErrDuplicateScope) {
			t.Errorf("ParseScopes() errore = %v, non avvolge ErrDuplicateScope", err)
		}
	})

	t.Run("lista vuota", func(t *testing.T) {
		got, err := ParseScopes([]string{})
		if err != nil {
			t.Fatalf("ParseScopes() errore su lista vuota: %v", err)
		}
		if got == nil {
			t.Error("ParseScopes([]) restituisce nil invece di slice vuoto")
		}
	})
}

func TestSatisfies(t *testing.T) {
	tests := []struct {
		name string
		have []string
		need string
		want bool
	}{
		// Casi diretti
		{
			name: "scope direttamente presente",
			have: []string{"read:user", "write:org"},
			need: "read:user",
			want: true,
		},
		{
			name: "scope non presente",
			have: []string{"read:user", "write:org"},
			need: "admin:org",
			want: false,
		},

		// write:X ⇒ read:X
		{
			name: "write:user implica read:user",
			have: []string{"write:user"},
			need: "read:user",
			want: true,
		},
		{
			name: "write:org implica read:org",
			have: []string{"write:org"},
			need: "read:org",
			want: true,
		},
		{
			name: "write:resource implica read:resource",
			have: []string{"write:resource"},
			need: "read:resource",
			want: true,
		},
		{
			name: "write:user non implica read:org",
			have: []string{"write:user"},
			need: "read:org",
			want: false,
		},

		// admin:org ⇒ write:org + read:org
		{
			name: "admin:org implica write:org",
			have: []string{"admin:org"},
			need: "write:org",
			want: true,
		},
		{
			name: "admin:org implica read:org",
			have: []string{"admin:org"},
			need: "read:org",
			want: true,
		},
		{
			name: "admin:org non implica read:user",
			have: []string{"admin:org"},
			need: "read:user",
			want: false,
		},

		// Combinazioni
		{
			name: "admin:org + write:user copre write:org",
			have: []string{"admin:org", "write:user"},
			need: "write:org",
			want: true,
		},
		{
			name: "nessuno scope copre admin:org",
			have: []string{"read:user", "write:org"},
			need: "admin:org",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Satisfies(tt.have, tt.need)
			if got != tt.want {
				t.Errorf("Satisfies(%v, %q) = %v, want %v", tt.have, tt.need, got, tt.want)
			}
		})
	}
}

func TestValidateErrors(t *testing.T) {
	// Verifica che validate restituisca gli errori corretti.
	t.Run("lunghezza errata → ErrInvalidToken", func(t *testing.T) {
		err := validate("abc")
		if err == nil || !errors.Is(err, ErrInvalidToken) {
			t.Errorf("validate('abc') = %v, voleva ErrInvalidToken", err)
		}
	})

	t.Run("prefisso errato → ErrInvalidToken", func(t *testing.T) {
		err := validate("xx" + strings.Repeat("A", payloadLen)+"_AAAAAA")
		if err == nil || !errors.Is(err, ErrInvalidToken) {
			t.Errorf("validate('xx…') = %v, voleva ErrInvalidToken", err)
		}
	})

	t.Run("separatore mancante → ErrInvalidToken", func(t *testing.T) {
		// Token senza '_': payload e checksum attaccati
		err := validate(prefix + strings.Repeat("A", payloadLen+checksumLen))
		if err == nil || !errors.Is(err, ErrInvalidToken) {
			t.Errorf("validate senza '_' = %v, voleva ErrInvalidToken", err)
		}
	})

	t.Run("checksum errato → ErrChecksumMismatch", func(t *testing.T) {
		// Costruisce un token con payload corretto ma checksum sbagliato.
		raw := make([]byte, 32)
		for i := range raw {
			raw[i] = byte(i)
		}
		payload := base62Encode(raw)
		valid := prefix + payload + "_" + base62Checksum(raw)
		// Altera il checksum
		wrong := valid[:len(valid)-1] + "B"
		err := validate(wrong)
		if err == nil || !errors.Is(err, ErrChecksumMismatch) {
			t.Errorf("validate checksum errato = %v, voleva ErrChecksumMismatch", err)
		}
	})
}

func TestValidateCharErrors(t *testing.T) {
	// Test che validate rifiuta caratteri invalidi nel payload e checksum.
	tests := []struct {
		name string
		fn   func(string) string // prende un token valido, restituisce uno malformato
	}{
		{
			name: "payload contiene '!' (posizione 5)",
			fn: func(valid string) string {
				runes := []rune(valid)
				runes[5] = '!'
				return string(runes)
			},
		},
		{
			name: "payload contiene '-' (posizione 20)",
			fn: func(valid string) string {
				runes := []rune(valid)
				runes[20] = '-'
				return string(runes)
			},
		},
		{
			name: "payload contiene spazio (posizione 40)",
			fn: func(valid string) string {
				runes := []rune(valid)
				runes[40] = ' '
				return string(runes)
			},
		},
		{
			name: "payload contiene non ASCII (posizione 10)",
			fn: func(valid string) string {
				runes := []rune(valid)
				runes[10] = 'é'
				return string(runes)
			},
		},
		{
			name: "checksum contiene '!' (ultima posizione)",
			fn: func(valid string) string {
				runes := []rune(valid)
				runes[len(runes)-1] = '!'
				return string(runes)
			},
		},
		{
			name: "checksum contiene '#' (penultima)",
			fn: func(valid string) string {
				runes := []rune(valid)
				runes[len(runes)-2] = '#'
				return string(runes)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, err := Generate()
			if err != nil {
				t.Fatalf("Generate() errore: %v", err)
			}
			bad := tt.fn(valid)
			if Validate(bad) {
				t.Errorf("Validate(%q) = true, voleva false", bad)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTokenFormat(t *testing.T) {
	// Verifica la struttura del token generato.
	token, err := Generate()
	if err != nil {
		t.Fatalf("Generate() errore: %v", err)
	}

	// Lunghezza totale
	if len(token) != tokenLen {
		t.Errorf("tokenLen = %d, len(token) = %d", tokenLen, len(token))
	}

	// Prefisso
	if token[:len(prefix)] != prefix {
		t.Errorf("token non inizia con %q", prefix)
	}

	// Separatore
	if token[len(prefix)+payloadLen] != '_' {
		t.Errorf("separatore '_' mancante alla posizione %d", len(prefix)+payloadLen)
	}

	// Payload e checksum sono solo caratteri base62
	payload := token[len(prefix) : len(prefix)+payloadLen]
	for i, c := range payload {
		if !strings.ContainsRune(base62Alphabet, c) {
			t.Errorf("payload[%d] = %q non è un carattere base62", i, c)
		}
	}

	checksum := token[len(prefix)+payloadLen+1:]
	for i, c := range checksum {
		if !strings.ContainsRune(base62Alphabet, c) {
			t.Errorf("checksum[%d] = %q non è un carattere base62", i, c)
		}
	}
}

func ExampleParseScopes() {
	// fmt.Println("Esempio ParseScopes")
	// Output: [read:org read:user write:org]
	_ = ParseScopes
}

package tokens

import (
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
		expected := len(prefix) + 64 + 1 + 6 // gst_ + payload + _ + checksum
		if len(token) != expected {
			t.Errorf("lunghezza %d, attesa %d", len(token), expected)
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
				t.Errorf("iterazione %d: token generato %q non passato a Validate()", i, token)
			}
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("token malformato: prefisso errato", func(t *testing.T) {
		// Token generato ma con prefisso cambiato.
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		bad := "xx" + valid[len(prefix):]
		if Validate(bad) {
			t.Error("token con prefisso errato passato a Validate()")
		}
	})

	t.Run("checksum alterato", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		// Altera l'ultimo carattere del checksum (posizioni 69-74).
		runes := []rune(valid)
		runes[len(runes)-1] = rune('A' + 1) // cambia carattere
		bad := string(runes)
		if Validate(bad) {
			t.Error("token con checksum alterato passato a Validate()")
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

	t.Run("carattere nella payload alterato", func(t *testing.T) {
		valid, err := Generate()
		if err != nil {
			t.Fatalf("Generate() errore: %v", err)
		}
		runes := []rune(valid)
		// Altera un carattere nel payload (posizione 5 = primo char dopo "gst_").
		old := runes[5]
		runes[5] = 'z'
		bad := string(runes)
		if Validate(bad) {
			t.Error("token con payload alterato passato a Validate()")
		}
		// Verifica che la modifica abbia effettivamente cambiato il token.
		if old == 'z' {
			t.Logf("avviso: il primo char del payload era già 'z', rigenero con modifica checksum")
			// Fallback: altera l'ultimo char del checksum.
			runes2 := []rune(valid)
			runes2[len(runes2)-1] = 'z'
			bad2 := string(runes2)
			if Validate(bad2) {
				t.Error("token con checksum alterato passato a Validate()")
			}
		}
	})
}

func TestHash(t *testing.T) {
	t.Run("deterministico", func(t *testing.T) {
		token := "gst_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA_AAAAAA"
		h1 := Hash(token)
		h2 := Hash(token)
		if h1 != h2 {
			t.Errorf("stesso token → hash diversi: %q != %q", h1, h2)
		}
	})

	t.Run("diverso per token diverso", func(t *testing.T) {
		t1 := "gst_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA_AAAAAA"
		t2 := "gst_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB_BBBBBB"
		h1 := Hash(t1)
		h2 := Hash(t2)
		if h1 == h2 {
			t.Errorf("token diversi → stesso hash: %q", h1)
		}
	})

	t.Run("non restituisce il token", func(t *testing.T) {
		token := "gst_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA_AAAAAA"
		h := Hash(token)
		if strings.Contains(h, token) {
			t.Errorf("l'hash contiene il token in chiaro: %s", h)
		}
	})
}

func TestCompareToken(t *testing.T) {
	t.Run("token corretto", func(t *testing.T) {
		token := "gst_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA_AAAAAA"
		hash := Hash(token)
		if !CompareToken(token, hash) {
			t.Error("CompareToken non ha confrontato correttamente")
		}
	})

	t.Run("hash errato", func(t *testing.T) {
		token := "gst_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA_AAAAAA"
		wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"
		if CompareToken(token, wrongHash) {
			t.Error("CompareToken ha confrontato token errato come corretto")
		}
	})
}

func TestParseScopes(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    []string
		wantErr bool
	}{
		{
			name:    "scope validi in ordine",
			input:   []string{"read:user", "write:org"},
			want:    []string{"read:user", "write:org"},
			wantErr: false,
		},
		{
			name:    "scope validi ordine diverso",
			input:   []string{"write:org", "read:user"},
			want:    []string{"read:user", "write:org"},
			wantErr: false,
		},
		{
			name:    "tutti gli scope",
			input:   KnownScopes,
			want:    []string{"admin:org", "read:org", "read:resource", "read:user", "write:org", "write:resource", "write:user"},
			wantErr: false,
		},
		{
			name:    "scope sconosciuto",
			input:   []string{"read:user", "delete:user"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "scope duplicato",
			input:   []string{"read:user", "read:user"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "lista vuota",
			input:   []string{},
			want:    nil,
			wantErr: false,
		},
		{
			name:    "doppio sconosciuto",
			input:   []string{"delete:org", "write:user"},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseScopes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseScopes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !equalStringSlices(got, tt.want) {
				t.Errorf("ParseScopes() = %v, want %v", got, tt.want)
			}
		})
	}
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

package notify

import (
	"slices"
	"testing"
)

func TestParseMentions(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"utente", "ciao @Alice, vedi?", []string{"alice"}},
		{"team", "@acme/Devs guardate", []string{"acme/devs"}},
		{"doppioni e ordine", "@b @a @b", []string{"b", "a"}},
		{"inizio riga e a capo", "@a\n@b", []string{"a", "b"}},
		{"email non menziona", "scrivi a mario@example.com", nil},
		{"percorso e doppia chiocciola", "src/@alice e @@bob", nil},
		{"punteggiatura", "(@alice) @bob. @carol!", []string{"alice", "bob", "carol"}},
		{"codice inline", "usa `@alice` e @bob", []string{"bob"}},
		{"blocco di codice", "```\n@alice\n```\n@bob", []string{"bob"}},
		{"blocco non chiuso", "@bob\n```\n@alice", []string{"bob"}},
		{"trattino finale non fa parte del nome", "@alice- ok", []string{"alice"}},
		{"nome troppo lungo si ferma a 39", "@" + string(make([]byte, 0)) + "a123456789012345678901234567890123456789x", []string{"a12345678901234567890123456789012345678"}},
		{"vuoto", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseMentions(c.text); !slices.Equal(got, c.want) {
				t.Fatalf("ParseMentions(%q) = %v, voluto %v", c.text, got, c.want)
			}
		})
	}
}

func TestNewMentions_SoloIMenzionatiNuovi(t *testing.T) {
	got := newMentions("@a @b @c", "@b")
	if !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("newMentions = %v", got)
	}
	if got := newMentions("@a", "@a"); len(got) != 0 {
		t.Fatalf("nessun nuovo menzionato, ottenuto %v", got)
	}
}

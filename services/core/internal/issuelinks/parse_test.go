package issuelinks

import (
	"reflect"
	"testing"
)

func TestParseRefs(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want []Ref
	}{
		{"nessun riferimento", "Refactor del parser", nil},
		{"semplice", "Vedi #12", []Ref{{Number: 12}}},
		{"fixes", "Fixes #12", []Ref{{Number: 12, Keyword: "fix"}}},
		{"maiuscole", "CLOSES #3 e RESOLVED #4", []Ref{{Number: 3, Keyword: "close"}, {Number: 4, Keyword: "resolve"}}},
		{"tutte le forme", "close #1 closes #2 closed #3 fix #4 fixes #5 fixed #6 resolve #7 resolves #8 resolved #9",
			[]Ref{{Number: 1, Keyword: "close"}, {Number: 2, Keyword: "close"}, {Number: 3, Keyword: "close"},
				{Number: 4, Keyword: "fix"}, {Number: 5, Keyword: "fix"}, {Number: 6, Keyword: "fix"},
				{Number: 7, Keyword: "resolve"}, {Number: 8, Keyword: "resolve"}, {Number: 9, Keyword: "resolve"}}},
		{"due punti", "fixes: #7", []Ref{{Number: 7, Keyword: "fix"}}},
		{"altro repo", "Fixes Alice/Other#5", []Ref{{Repo: "alice/other", Number: 5, Keyword: "fix"}}},
		{"altro repo senza parola", "Dipende da alice/other#5", []Ref{{Repo: "alice/other", Number: 5}}},
		{"parola non attaccata", "prefixes #8 e fixing #9", []Ref{{Number: 8}, {Number: 9}}},
		{"dopo una parola non è un riferimento", "abc#8 e x/y#z", nil},
		{"anchor di un URL", "https://example.com/page#12", nil},
		{"duplicati", "see #5, fixes #5", []Ref{{Number: 5, Keyword: "fix"}}},
		{"consecutivi", "#1 #2,#3", []Ref{{Number: 1}, {Number: 2}, {Number: 3}}},
		{"riga nuova", "Titolo\n\nCloses #10\nFixes #11", []Ref{{Number: 10, Keyword: "close"}, {Number: 11, Keyword: "fix"}}},
		{"zero", "#0", nil},
		{"seguito da lettere", "#12abc", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseRefs(c.msg); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ParseRefs(%q) = %+v, atteso %+v", c.msg, got, c.want)
			}
		})
	}
}

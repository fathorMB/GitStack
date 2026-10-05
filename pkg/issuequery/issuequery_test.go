package issuequery

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Query
	}{
		{"vuota", "", Query{}},
		{"solo spazi", "  \t ", Query{}},
		{"is open", "is:open", Query{Is: []Cond[IsValue]{{false, IsOpen, 0}}}},
		{"is closed maiuscolo", "IS:Closed", Query{Is: []Cond[IsValue]{{false, IsClosed, 0}}}},
		{"is issue", "is:issue", Query{Is: []Cond[IsValue]{{false, IsIssue, 0}}}},
		{"reason completed", "reason:completed", Query{Reasons: []Cond[Reason]{{false, ReasonCompleted, 0}}}},
		{"reason not-planned", "reason:not-planned", Query{Reasons: []Cond[Reason]{{false, ReasonNotPlanned, 0}}}},
		{"reason duplicate", "reason:duplicate", Query{Reasons: []Cond[Reason]{{false, ReasonDuplicate, 0}}}},
		{"label semplice", "label:bug", Query{Labels: []Cond[string]{{false, "bug", 0}}}},
		{"label ripetuto", "label:bug label:ui", Query{Labels: []Cond[string]{{false, "bug", 0}, {false, "ui", 10}}}},
		{"label con virgolette", `label:"good first issue"`, Query{Labels: []Cond[string]{{false, "good first issue", 0}}}},
		{"label virgolette con escape", `label:"a \"b\" \\c"`, Query{Labels: []Cond[string]{{false, `a "b" \c`, 0}}}},
		{"assignee login", "assignee:alice", Query{Assignees: []Cond[Actor]{{false, Actor{ActorUser, "alice"}, 0}}}},
		{"assignee @login", "assignee:@alice", Query{Assignees: []Cond[Actor]{{false, Actor{ActorUser, "alice"}, 0}}}},
		{"assignee @me", "assignee:@me", Query{Assignees: []Cond[Actor]{{false, Actor{Kind: ActorMe}, 0}}}},
		{"assignee @agents", "assignee:@agents", Query{Assignees: []Cond[Actor]{{false, Actor{Kind: ActorAgents}, 0}}}},
		{"author", "author:bob", Query{Authors: []Cond[Actor]{{false, Actor{ActorUser, "bob"}, 0}}}},
		{"author @me", "author:@me", Query{Authors: []Cond[Actor]{{false, Actor{Kind: ActorMe}, 0}}}},
		{"milestone", "milestone:v1", Query{Milestones: []Cond[string]{{false, "v1", 0}}}},
		{"milestone virgolette", `milestone:"M-05 Issues"`, Query{Milestones: []Cond[string]{{false, "M-05 Issues", 0}}}},
		{"no label", "no:label", Query{No: []Cond[NoField]{{false, NoLabel, 0}}}},
		{"no assignee", "no:assignee", Query{No: []Cond[NoField]{{false, NoAssignee, 0}}}},
		{"no milestone", "no:milestone", Query{No: []Cond[NoField]{{false, NoMilestone, 0}}}},
		{"repo", "repo:acme/web", Query{Repos: []Cond[RepoRef]{{false, RepoRef{"acme", "web"}, 0}}}},
		{"org", "org:acme", Query{Orgs: []Cond[string]{{false, "acme", 0}}}},
		{"testo libero", "crash all'avvio", Query{Text: []Term{{"crash", false, false, 0}, {"all'avvio", false, false, 6}}}},
		{"testo fra virgolette", `"null pointer" crash`, Query{Text: []Term{{"null pointer", true, false, 0}, {"crash", false, false, 15}}}},
		{"testo con due punti non qualificatore", `"a:b"`, Query{Text: []Term{{"a:b", true, false, 0}}}},
		{"negazione qualificatore", "-label:wontfix", Query{Labels: []Cond[string]{{true, "wontfix", 0}}}},
		{"negazione is", "-is:closed", Query{Is: []Cond[IsValue]{{true, IsClosed, 0}}}},
		{"negazione assignee @me", "-assignee:@me", Query{Assignees: []Cond[Actor]{{true, Actor{Kind: ActorMe}, 0}}}},
		{"negazione no", "-no:label", Query{No: []Cond[NoField]{{true, NoLabel, 0}}}},
		{"negazione testo", "-foo", Query{Text: []Term{{"foo", false, true, 0}}}},
		{"negazione frase", `-"foo bar"`, Query{Text: []Term{{"foo bar", true, true, 0}}}},
		{"trattino nel mezzo è testo", "foo-bar", Query{Text: []Term{{"foo-bar", false, false, 0}}}},
		{
			"combinazione",
			`is:open label:bug -label:ui assignee:@me repo:acme/web crash "stack trace" no:milestone`,
			Query{
				Is:        []Cond[IsValue]{{false, IsOpen, 0}},
				Labels:    []Cond[string]{{false, "bug", 8}, {true, "ui", 18}},
				Assignees: []Cond[Actor]{{false, Actor{Kind: ActorMe}, 28}},
				Repos:     []Cond[RepoRef]{{false, RepoRef{"acme", "web"}, 41}},
				Text:      []Term{{"crash", false, false, 55}, {"stack trace", true, false, 61}},
				No:        []Cond[NoField]{{false, NoMilestone, 75}},
			},
		},
		{"spazi multipli e tab", "is:open \t  org:acme", Query{Is: []Cond[IsValue]{{false, IsOpen, 0}}, Orgs: []Cond[string]{{false, "acme", 11}}}},
		{"unicode", "label:été é", Query{Labels: []Cond[string]{{false, "été", 0}}, Text: []Term{{"é", false, false, 12}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q)\n got  %+v\n want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		pos     int
		contain string
	}{
		{"qualificatore sconosciuto", "is:open foo:bar", 8, `sconosciuto "foo"`},
		{"sconosciuto negato", "-foo:bar", 0, "sconosciuto"},
		{"url come testo", "http://x", 0, `"http"`},
		{"is non valido", "label:a is:merged", 11, "is"},
		{"reason non valido", "reason:wontfix", 7, "reason"},
		{"reason con underscore", "reason:not_planned", 7, "not-planned"},
		{"no non valido", "no:author", 3, "no"},
		{"valore mancante", "is:", 3, "mancante"},
		{"valore mancante in mezzo", "is: open", 3, "mancante"},
		{"assignee non valido", "assignee:a/b", 9, "assignee"},
		{"assignee solo @", "assignee:@", 9, "assignee"},
		{"author non valido", "author:x@y", 7, "author"},
		{"repo senza slash", "repo:web", 5, "owner/repo"},
		{"repo con troppi slash", "repo:a/b/c", 5, "owner/repo"},
		{"repo owner vuoto", "repo:/web", 5, "owner/repo"},
		{"repo name vuoto", "repo:acme/", 5, "owner/repo"},
		{"org con slash", "org:a/b", 4, "org"},
		{"virgolette non chiuse", `label:"abc`, 6, "virgolette"},
		{"virgolette testo non chiuse", `x "abc`, 2, "virgolette"},
		{"trattino solo", "a - b", 2, "'-'"},
		{"trattino finale", "a -", 2, "'-'"},
		{"posizione in byte dopo unicode", "é foo:x", 3, "sconosciuto"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.in)
			var pe *Error
			if !errors.As(err, &pe) {
				t.Fatalf("Parse(%q): atteso *Error, ottenuto %v", tc.in, err)
			}
			if pe.Pos != tc.pos {
				t.Errorf("Pos = %d, atteso %d (%v)", pe.Pos, tc.pos, pe)
			}
			if !strings.Contains(pe.Msg, tc.contain) {
				t.Errorf("Msg %q non contiene %q", pe.Msg, tc.contain)
			}
			if !strings.HasPrefix(pe.Error(), "posizione ") {
				t.Errorf("Error() = %q", pe.Error())
			}
		})
	}
}

func TestFreeText(t *testing.T) {
	q, err := Parse(`is:open crash -foo "stack trace"`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := q.FreeText(), "crash stack trace"; got != want {
		t.Errorf("FreeText = %q, atteso %q", got, want)
	}
}

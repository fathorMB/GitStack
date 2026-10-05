// Package issuequery trasforma una stringa di ricerca delle issues (sintassi
// I10, in stile GitHub) in una struttura tipizzata. Non accede al database: la
// traduzione in SQL è di chi usa il pacchetto.
package issuequery

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// IsValue è il valore di is:.
type IsValue string

// Valori di is:.
const (
	IsOpen   IsValue = "open"
	IsClosed IsValue = "closed"
	IsIssue  IsValue = "issue"
)

// Reason è il valore di reason:.
type Reason string

// Valori di reason:.
const (
	ReasonCompleted  Reason = "completed"
	ReasonNotPlanned Reason = "not-planned"
	ReasonDuplicate  Reason = "duplicate"
)

// NoField è il valore di no:.
type NoField string

// Valori di no:.
const (
	NoLabel     NoField = "label"
	NoAssignee  NoField = "assignee"
	NoMilestone NoField = "milestone"
)

// ActorKind distingue i riferimenti speciali dai login.
type ActorKind int

// Tipi di Actor.
const (
	ActorUser   ActorKind = iota // login esplicito
	ActorMe                      // @me: l'utente che cerca
	ActorAgents                  // @agents: qualsiasi agente
)

// Actor è il valore di assignee: e author:.
type Actor struct {
	Kind  ActorKind
	Login string // solo per ActorUser, senza '@'
}

// RepoRef è il valore di repo:.
type RepoRef struct {
	Owner string
	Name  string
}

// Cond è una condizione, eventualmente negata (-qualificatore:valore).
type Cond[T any] struct {
	Negated bool
	Value   T
	Pos     int // offset in byte del token nella stringa originale
}

// Term è un termine di testo libero.
type Term struct {
	Text    string
	Quoted  bool // era fra virgolette: frase esatta
	Negated bool // -parola
	Pos     int
}

// Query è il risultato di Parse. Le condizioni di uno stesso qualificatore
// ripetuto restano nell'ordine di scrittura; la semantica (AND/OR) è di chi le
// traduce.
type Query struct {
	Is         []Cond[IsValue]
	Reasons    []Cond[Reason]
	Labels     []Cond[string]
	Assignees  []Cond[Actor]
	Authors    []Cond[Actor]
	Milestones []Cond[string]
	No         []Cond[NoField]
	Repos      []Cond[RepoRef]
	Orgs       []Cond[string]
	Text       []Term
}

// Error è un errore di sintassi con la posizione.
type Error struct {
	Pos int    // offset in byte nella stringa di ricerca
	Msg string // messaggio leggibile
}

func (e *Error) Error() string { return fmt.Sprintf("posizione %d: %s", e.Pos, e.Msg) }

// FreeText restituisce il testo libero non negato, unito da spazi.
func (q Query) FreeText() string {
	var parts []string
	for _, t := range q.Text {
		if !t.Negated {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, " ")
}

type parser struct {
	s   string
	pos int
}

// Parse analizza la stringa di ricerca; al primo errore restituisce *Error.
func Parse(s string) (Query, error) {
	p := &parser{s: s}
	var q Query
	for {
		p.skipSpace()
		if p.pos >= len(p.s) {
			return q, nil
		}
		if err := p.token(&q); err != nil {
			return Query{}, err
		}
	}
}

func (p *parser) skipSpace() {
	for p.pos < len(p.s) {
		r, n := utf8.DecodeRuneInString(p.s[p.pos:])
		if !isSpace(r) {
			return
		}
		p.pos += n
	}
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// token legge un qualificatore (con eventuale '-') o un termine di testo.
func (p *parser) token(q *Query) error {
	start := p.pos
	neg := false
	if p.s[p.pos] == '-' {
		neg = true
		p.pos++
	}
	k := p.pos
	for k < len(p.s) && isLetter(p.s[k]) {
		k++
	}
	if k > p.pos && k < len(p.s) && p.s[k] == ':' {
		key := strings.ToLower(p.s[p.pos:k])
		p.pos = k + 1
		return p.qualifier(q, key, start, neg)
	}
	if p.pos >= len(p.s) || isSpace(rune(p.s[p.pos])) {
		return &Error{Pos: start, Msg: "'-' da solo: manca il termine da negare"}
	}
	text, quoted, err := p.value()
	if err != nil {
		return err
	}
	q.Text = append(q.Text, Term{Text: text, Quoted: quoted, Negated: neg, Pos: start})
	return nil
}

// value legge un valore: fra virgolette (con \" e \\) o fino allo spazio.
func (p *parser) value() (string, bool, error) {
	if p.pos < len(p.s) && p.s[p.pos] == '"' {
		open := p.pos
		p.pos++
		var b strings.Builder
		for p.pos < len(p.s) {
			c := p.s[p.pos]
			switch {
			case c == '\\' && p.pos+1 < len(p.s) && (p.s[p.pos+1] == '"' || p.s[p.pos+1] == '\\'):
				b.WriteByte(p.s[p.pos+1])
				p.pos += 2
			case c == '"':
				p.pos++
				return b.String(), true, nil
			default:
				b.WriteByte(c)
				p.pos++
			}
		}
		return "", true, &Error{Pos: open, Msg: "virgolette non chiuse"}
	}
	st := p.pos
	for p.pos < len(p.s) {
		r, n := utf8.DecodeRuneInString(p.s[p.pos:])
		if isSpace(r) {
			break
		}
		p.pos += n
	}
	return p.s[st:p.pos], false, nil
}

func (p *parser) qualifier(q *Query, key string, start int, neg bool) error {
	switch key {
	case "is", "reason", "label", "assignee", "author", "milestone", "no", "repo", "org":
	default:
		return &Error{Pos: start, Msg: fmt.Sprintf("qualificatore sconosciuto %q (ammessi: is, reason, label, assignee, author, milestone, no, repo, org)", key)}
	}
	vpos := p.pos
	val, _, err := p.value()
	if err != nil {
		return err
	}
	bad := func(msg string) error {
		return &Error{Pos: vpos, Msg: fmt.Sprintf("valore non valido per %s: %s", key, msg)}
	}
	if val == "" {
		return bad("valore mancante")
	}
	lv := strings.ToLower(val)
	switch key {
	case "is":
		switch IsValue(lv) {
		case IsOpen, IsClosed, IsIssue:
			q.Is = append(q.Is, Cond[IsValue]{neg, IsValue(lv), start})
		default:
			return bad(fmt.Sprintf("%q (ammessi: open, closed, issue)", val))
		}
	case "reason":
		switch Reason(lv) {
		case ReasonCompleted, ReasonNotPlanned, ReasonDuplicate:
			q.Reasons = append(q.Reasons, Cond[Reason]{neg, Reason(lv), start})
		default:
			return bad(fmt.Sprintf("%q (ammessi: completed, not-planned, duplicate)", val))
		}
	case "no":
		switch NoField(lv) {
		case NoLabel, NoAssignee, NoMilestone:
			q.No = append(q.No, Cond[NoField]{neg, NoField(lv), start})
		default:
			return bad(fmt.Sprintf("%q (ammessi: label, assignee, milestone)", val))
		}
	case "label":
		q.Labels = append(q.Labels, Cond[string]{neg, val, start})
	case "milestone":
		q.Milestones = append(q.Milestones, Cond[string]{neg, val, start})
	case "assignee", "author":
		a, ok := parseActor(val)
		if !ok {
			return bad(fmt.Sprintf("%q (atteso un login, @me o @agents)", val))
		}
		if key == "assignee" {
			q.Assignees = append(q.Assignees, Cond[Actor]{neg, a, start})
		} else {
			q.Authors = append(q.Authors, Cond[Actor]{neg, a, start})
		}
	case "repo":
		owner, name, ok := strings.Cut(val, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") || strings.ContainsAny(val, " \t") {
			return bad(fmt.Sprintf("%q (atteso owner/repo)", val))
		}
		q.Repos = append(q.Repos, Cond[RepoRef]{neg, RepoRef{owner, name}, start})
	case "org":
		if strings.ContainsAny(val, "/ \t") {
			return bad(fmt.Sprintf("%q (atteso il nome di un'organizzazione)", val))
		}
		q.Orgs = append(q.Orgs, Cond[string]{neg, val, start})
	}
	return nil
}

func parseActor(v string) (Actor, bool) {
	switch strings.ToLower(v) {
	case "@me":
		return Actor{Kind: ActorMe}, true
	case "@agents":
		return Actor{Kind: ActorAgents}, true
	}
	login := strings.TrimPrefix(v, "@")
	if login == "" || strings.ContainsAny(login, "@/ \t") {
		return Actor{}, false
	}
	return Actor{Kind: ActorUser, Login: login}, true
}

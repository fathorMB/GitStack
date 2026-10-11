//go:build integration

package stackitest

import (
	"encoding/json"
	"strings"
	"testing"
)

// M-07/F (GIT-167): `gs label` e `gs milestone` sullo stack completo. Il
// binario gs è quello vero e parla col gateway attraverso il proxy
// /api/v1→/v1 di newGsEnv. Utenti di newIssuesE2E: alice (admin), bob (write),
// carol (read: niente etichette né milestone, I5/I7). Repo: alice/iss-int.

// gsSteps esegue i passi come subtest che condividono lo stesso stack. Gli
// helper del gsEnv chiamano g.t.Fatalf: durante un passo g.t è il subtest,
// così un errore ferma solo quel passo e gli altri girano comunque.
func gsSteps(g *gsEnv, name string, fn func(t *testing.T)) {
	parent := g.t
	parent.Run(name, func(st *testing.T) {
		g.t = st
		defer func() { g.t = parent }()
		fn(st)
	})
}

type labelItem struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	OpenIssues  int    `json:"openIssues"`
}

func gsLabels(t *testing.T, g *gsEnv, user, repo string) map[string]labelItem {
	t.Helper()
	var items []labelItem
	out := g.ok(user, repo, "", "label", "list", "--json", "name,color,description,openIssues", "-L", "100").out
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("JSON di label list non valido: %v: %s", err, out)
	}
	m := map[string]labelItem{}
	for _, l := range items {
		m[l.Name] = l
	}
	return m
}

func TestGsLabel(t *testing.T) {
	g := newGsEnv(t)
	I := e2eInternal

	gsSteps(g, "list_e_create", func(t *testing.T) {
		// etichetta con nome diverso da quelle del repo, creata da write (bob)
		r := g.ok("bob", I, "", "label", "create", "area-db", "-c", "1d76db", "-d", "Database")
		if !strings.Contains(r.errOut, "Creata etichetta area-db") {
			t.Errorf("messaggio di create: %q", r.errOut)
		}
		// alice (admin) la vede in list (senza TTY niente intestazione: una riga per record, colonne a tab)
		tab := g.ok("alice", I, "", "label", "list").out
		for _, w := range []string{"area-db\t1d76db\t0"} {
			if !strings.Contains(tab, w) {
				t.Errorf("label list senza %q:\n%s", w, tab)
			}
		}
		l, ok := gsLabels(t, g, "alice", I)["area-db"]
		if !ok || l.Color != "1d76db" || l.Description != "Database" {
			t.Errorf("area-db: %+v", l)
		}
		// duplicato (senza distinguere le maiuscole): conflitto, exit 1 o 7
		if r := g.gs("alice", I, "", "label", "create", "AREA-DB", "-c", "ffffff"); r.code == 0 {
			t.Errorf("create duplicata riuscita: %s", r.out)
		}
	})

	gsSteps(g, "permessi", func(t *testing.T) {
		// read non crea etichette: exit 5
		g.fails(5, "carol", I, "label", "create", "carol-label", "-c", "123456")
		if _, ok := gsLabels(t, g, "carol", I)["carol-label"]; ok {
			t.Errorf("create negata ha creato l'etichetta")
		}
		// ma read le elenca
		g.ok("carol", I, "", "label", "list")
		// uso errato: colore non valido, nome vuoto
		g.fails(2, "bob", I, "label", "create", "x", "-c", "zzzzzz")
		g.fails(2, "bob", I, "label", "create", "x")
	})

	gsSteps(g, "edit", func(t *testing.T) {
		g.ok("bob", I, "", "label", "edit", "area-db", "-c", "ff0000", "-d", "Dati")
		l := gsLabels(t, g, "alice", I)["area-db"]
		if l.Color != "ff0000" || l.Description != "Dati" {
			t.Errorf("dopo edit: %+v", l)
		}
		// rinomina
		g.ok("bob", I, "", "label", "edit", "area-db", "-n", "area-dati")
		ls := gsLabels(t, g, "alice", I)
		if _, ok := ls["area-db"]; ok {
			t.Errorf("il vecchio nome esiste ancora")
		}
		if l := ls["area-dati"]; l.Color != "ff0000" {
			t.Errorf("dopo rinomina: %+v", l)
		}
		// senza flag: uso errato; read: exit 5
		g.fails(2, "bob", I, "label", "edit", "area-dati")
		g.fails(5, "carol", I, "label", "edit", "area-dati", "-c", "000000")
		// inesistente: exit 6
		g.fails(6, "bob", I, "label", "edit", "non-esiste", "-c", "000000")
	})

	gsSteps(g, "delete_G8", func(t *testing.T) {
		g.ok("bob", I, "", "label", "create", "da-eliminare", "-c", "ff0000")
		// senza TTY e senza --yes: exit 2 e l'etichetta resta
		g.fails(2, "bob", I, "label", "delete", "da-eliminare")
		if _, ok := gsLabels(t, g, "bob", I)["da-eliminare"]; !ok {
			t.Fatalf("delete senza --yes ha eliminato l'etichetta")
		}
		// read non elimina nemmeno con --yes
		g.fails(5, "carol", I, "label", "delete", "da-eliminare", "--yes")
		// con --yes elimina
		r := g.ok("bob", I, "", "label", "delete", "da-eliminare", "--yes")
		if !strings.Contains(r.errOut, "Eliminata etichetta da-eliminare") {
			t.Errorf("messaggio di delete: %q", r.errOut)
		}
		if _, ok := gsLabels(t, g, "bob", I)["da-eliminare"]; ok {
			t.Errorf("l'etichetta esiste ancora dopo delete --yes")
		}
		// seconda delete: non trovata (exit 6)
		g.fails(6, "bob", I, "label", "delete", "da-eliminare", "--yes")
	})

	gsSteps(g, "json", func(t *testing.T) {
		out := g.ok("alice", I, "", "label", "list", "--json", "name,color,openIssues,url").out
		var items []map[string]any
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("JSON non valido: %v: %s", err, out)
		}
		if len(items) == 0 {
			t.Fatalf("nessuna etichetta in lista")
		}
		for _, k := range []string{"name", "color", "openIssues", "url"} {
			if _, ok := items[0][k]; !ok {
				t.Errorf("campo %s assente: %v", k, items[0])
			}
		}
		if len(items[0]) != 4 {
			t.Errorf("--json con campi scelti deve dare solo quelli: %v", items[0])
		}
	})
}

type milestoneItem struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	State        string `json:"state"`
	DueOn        any    `json:"dueOn"`
	OpenIssues   int    `json:"openIssues"`
	ClosedIssues int    `json:"closedIssues"`
	Progress     string `json:"progress"`
}

func gsMilestone(t *testing.T, g *gsEnv, user, repo, n string) milestoneItem {
	t.Helper()
	var m milestoneItem
	out := g.ok(user, repo, "", "milestone", "view", n, "--json", "number,title,description,state,dueOn,openIssues,closedIssues,progress").out
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("JSON di milestone view non valido: %v: %s", err, out)
	}
	return m
}

func TestGsMilestone(t *testing.T) {
	g := newGsEnv(t)
	I := e2eInternal

	gsSteps(g, "create_list_view", func(t *testing.T) {
		// repo senza milestone: lista vuota
		if out := strings.TrimSpace(g.ok("alice", I, "", "milestone", "list", "--json", "number").out); out != "" && out != "[]" {
			t.Errorf("lista iniziale: %q", out)
		}
		r := g.ok("bob", I, "", "milestone", "create", "v1.0", "-d", "Prima versione", "-D", "2027-03-01")
		if !strings.Contains(r.errOut, "Creata milestone #1: v1.0") {
			t.Errorf("messaggio di create: %q", r.errOut)
		}
		tab := g.ok("alice", I, "", "milestone", "list").out
		for _, w := range []string{"#1\topen\t0%\t2027-03-01\tv1.0"} {
			if !strings.Contains(tab, w) {
				t.Errorf("milestone list senza %q:\n%s", w, tab)
			}
		}
		view := g.ok("carol", I, "", "milestone", "view", "#1").out
		for _, w := range []string{"alice/" + I, "v1.0", "#1", "open", "Prima versione", "2027-03-01", "Avanzamento: 0% (0/0 chiuse)"} {
			if !strings.Contains(view, w) {
				t.Errorf("milestone view senza %q:\n%s", w, view)
			}
		}
		m := gsMilestone(t, g, "carol", I, "1")
		if m.Number != 1 || m.Title != "v1.0" || m.State != "open" || m.Progress != "0%" || m.OpenIssues != 0 || m.ClosedIssues != 0 {
			t.Errorf("view --json: %+v", m)
		}
		// view di una inesistente: exit 6; numero non valido: exit 2
		g.fails(6, "alice", I, "milestone", "view", "99")
		g.fails(2, "alice", I, "milestone", "view", "abc")
	})

	gsSteps(g, "permessi", func(t *testing.T) {
		g.fails(5, "carol", I, "milestone", "create", "v-carol")
		g.fails(5, "carol", I, "milestone", "edit", "1", "-d", "x")
		g.fails(5, "carol", I, "milestone", "close", "1")
		g.fails(5, "carol", I, "milestone", "delete", "1", "--yes")
		if m := gsMilestone(t, g, "alice", I, "1"); m.State != "open" || m.Description != "Prima versione" {
			t.Errorf("le operazioni negate hanno modificato la milestone: %+v", m)
		}
		// data non valida: exit 2
		g.fails(2, "bob", I, "milestone", "create", "v2", "-D", "01/03/2027")
	})

	gsSteps(g, "avanzamento", func(t *testing.T) {
		// tre issue nella milestone: una completata, una non pianificata, una aperta
		done := g.open("bob", I, "Completata", "", nil)
		skipped := g.open("bob", I, "Non pianificata", "", nil)
		g.open("bob", I, "Ancora aperta", "", nil)
		for _, n := range []float64{done, skipped, done + 2} {
			g.must("bob", "PUT", issuePath(I, n, "/milestone"), map[string]any{"milestone": 1}, 200)
		}
		m := gsMilestone(t, g, "alice", I, "1")
		if m.OpenIssues != 3 || m.ClosedIssues != 0 || m.Progress != "0%" {
			t.Errorf("tre aperte: %+v", m)
		}
		g.must("bob", "POST", issuePath(I, done, "/close"), map[string]any{}, 200)
		g.must("bob", "POST", issuePath(I, skipped, "/close"), map[string]any{"reason": "not_planned"}, 200)
		// closedIssues conta solo le chiuse come completed (I2): 1 chiusa, 1 aperta + 1 non pianificata
		m = gsMilestone(t, g, "alice", I, "1")
		if m.ClosedIssues != 1 || m.OpenIssues != 1 || m.Progress != "50%" {
			t.Errorf("dopo le chiusure: %+v", m)
		}
		view := g.ok("alice", I, "", "milestone", "view", "1").out
		if !strings.Contains(view, "Avanzamento: 50% (1/2 chiuse)") {
			t.Errorf("view senza avanzamento:\n%s", view)
		}
		tab := g.ok("alice", I, "", "milestone", "list").out
		if !strings.Contains(tab, "50%") {
			t.Errorf("list senza avanzamento:\n%s", tab)
		}
		var items []milestoneItem
		out := g.ok("alice", I, "", "milestone", "list", "--json", "number,openIssues,closedIssues,progress").out
		if err := json.Unmarshal([]byte(out), &items); err != nil || len(items) != 1 || items[0].ClosedIssues != 1 || items[0].Progress != "50%" {
			t.Errorf("list --json: %v %s", err, out)
		}
	})

	gsSteps(g, "edit", func(t *testing.T) {
		g.ok("bob", I, "", "milestone", "edit", "1", "-t", "v1.0.1", "-d", "Corretta")
		m := gsMilestone(t, g, "alice", I, "1")
		if m.Title != "v1.0.1" || m.Description != "Corretta" || m.DueOn == nil {
			t.Errorf("dopo edit: %+v", m)
		}
		g.ok("bob", I, "", "milestone", "edit", "1", "-D", "2027-06-30")
		g.ok("bob", I, "", "milestone", "edit", "1", "--remove-due-on")
		if m := gsMilestone(t, g, "alice", I, "1"); m.DueOn != nil {
			t.Errorf("dueOn dopo --remove-due-on: %v", m.DueOn)
		}
		// senza flag, data errata, flag alternativi: exit 2
		g.fails(2, "bob", I, "milestone", "edit", "1")
		g.fails(2, "bob", I, "milestone", "edit", "1", "-D", "bad")
		g.fails(2, "bob", I, "milestone", "edit", "1", "-D", "2027-01-01", "--remove-due-on")
	})

	gsSteps(g, "close_reopen", func(t *testing.T) {
		r := g.ok("bob", I, "", "milestone", "close", "1")
		if !strings.Contains(r.errOut, "Chiusa milestone #1") {
			t.Errorf("messaggio di close: %q", r.errOut)
		}
		if m := gsMilestone(t, g, "alice", I, "1"); m.State != "closed" {
			t.Errorf("stato dopo close: %+v", m)
		}
		// la lista di default mostra solo le aperte; --state closed la trova
		if out := g.ok("alice", I, "", "milestone", "list", "--json", "number").out; strings.Contains(out, `"number": 1`) || strings.Contains(out, `"number":1`) {
			t.Errorf("la chiusa compare nella lista delle aperte: %s", out)
		}
		if out := g.ok("alice", I, "", "milestone", "list", "-s", "closed", "--json", "number").out; !strings.Contains(out, "1") {
			t.Errorf("--state closed: %s", out)
		}
		r = g.ok("bob", I, "", "milestone", "reopen", "1")
		if !strings.Contains(r.errOut, "Riaperta milestone #1") {
			t.Errorf("messaggio di reopen: %q", r.errOut)
		}
		if m := gsMilestone(t, g, "alice", I, "1"); m.State != "open" {
			t.Errorf("stato dopo reopen: %+v", m)
		}
	})

	gsSteps(g, "delete_G8", func(t *testing.T) {
		g.ok("bob", I, "", "milestone", "create", "da-eliminare")
		// numero della nuova milestone: la seconda del repo
		n := "2"
		if m := gsMilestone(t, g, "bob", I, n); m.Title != "da-eliminare" {
			t.Fatalf("milestone #2: %+v", m)
		}
		// senza TTY e senza --yes: exit 2 e la milestone resta
		g.fails(2, "bob", I, "milestone", "delete", n)
		if m := gsMilestone(t, g, "bob", I, n); m.Title != "da-eliminare" {
			t.Fatalf("delete senza --yes ha toccato la milestone: %+v", m)
		}
		r := g.ok("bob", I, "", "milestone", "delete", n, "--yes")
		if !strings.Contains(r.errOut, "Eliminata milestone #2") {
			t.Errorf("messaggio di delete: %q", r.errOut)
		}
		g.fails(6, "bob", I, "milestone", "view", n)
		g.fails(6, "bob", I, "milestone", "delete", n, "--yes")
	})
}

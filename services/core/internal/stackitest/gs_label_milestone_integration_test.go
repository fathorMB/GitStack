//go:build integration

package stackitest

import (
	"encoding/json"
	"strings"
	"testing"
)

// M-07/F (GIT-167): `gs label` e `gs milestone` sullo stack completo.
// Il binario gs è quello vero (go build da cli/cmd/gs) e parla con
// il gateway via il proxy /api/v1→/v1 di newGsEnv. Utenti: alice (admin),
// bob (write), carol (read, senza write su etichette/milestone).

func TestGsLabel(t *testing.T) {
	g := newGsEnv(t)
	const R = "label"
	const repo = "iss-int" // repo usato da issuesE2E

	t.Run("list", func(t *testing.T) {
		r := g.ok("alice", R, repo, "label", "list")
		// senza etichette nel repo iss-int (crea prima): lista vuota o senza test-label
		if !strings.Contains(r.out, "Nome") {
			t.Logf("intestazione tabella: %q", r.out)
		}
	})

	t.Run("create", func(t *testing.T) {
		r := g.ok("alice", R, repo, "label", "create", "test-label", "--color", "a2eeef", "--description", "etichetta di test")
		if !strings.Contains(r.errOut, "Creata") {
			t.Fatalf("messaggio: %q", r.errOut)
		}
		// verifica con list --json
		out := g.ok("alice", R, repo, "label", "list", "--json", "name,color,description").out
		var items []map[string]any
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("json list: %v", err)
		}
		if len(items) == 0 || items[0]["name"] != "test-label" || items[0]["color"] != "a2eeef" || items[0]["description"] != "etichetta di test" {
			t.Errorf("list json: %v", items)
		}
		// bob non ha write su etichette (I5: solo admin/write): exit 5
		g.fails(5, "bob", R, repo, "label", "create", "bob-label", "--color", "123456")
	})

	t.Run("edit", func(t *testing.T) {
		// edit con almeno un flag
		g.ok("alice", R, repo, "label", "edit", "test-label", "--color", "ff0000")
		// verifica con list --json
		out := g.ok("alice", R, repo, "label", "list", "--json", "color").out
		var items []map[string]any
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("json list: %v", err)
		}
		if len(items) == 0 || items[0]["color"] != "ff0000" {
			t.Errorf("colore dopo edit: %v", items)
		}
		// edit senza flag: exit 2
		g.fails(2, "alice", R, repo, "label", "edit", "test-label")
	})

	t.Run("rename", func(t *testing.T) {
		g.ok("alice", R, repo, "label", "edit", "test-label", "--name", "label-rinominata")
		// verifica con list --json
		out := g.ok("alice", R, repo, "label", "list", "--json", "name").out
		var items []map[string]any
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("json list: %v", err)
		}
		if len(items) == 0 {
			t.Fatalf("nessuna etichetta in lista dopo rename")
		}
		if items[0]["name"] != "label-rinominata" {
			t.Errorf("nome: %v", items[0])
		}
	})

	t.Run("delete_G8", func(t *testing.T) {
		// crea una label da eliminare
		g.ok("alice", R, repo, "label", "create", "da-eliminare", "--color", "ff0000")

		// senza TTY e senza --yes: exit 2
		g.fails(2, "alice", R, repo, "label", "delete", "da-eliminare")

		// con --yes: elimina
		r := g.ok("alice", R, repo, "label", "delete", "da-eliminare", "--yes")
		if !strings.Contains(r.errOut, "Eliminata") {
			t.Errorf("messaggio: %q", r.errOut)
		}
		// non esiste più (con api diretta)
		want(t, g.api("alice", "DELETE", "/repos/alice/iss-int/labels/da-eliminare", nil), 404, "not_found")
	})

	t.Run("json_campi", func(t *testing.T) {
		out := g.ok("alice", R, repo, "label", "list", "--json", "name,color,description").out
		var items []map[string]any
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("json list: %v", err)
		}
		if len(items) == 0 {
			t.Log("nessuna etichetta in lista")
			return
		}
		if _, ok := items[0]["name"]; !ok {
			t.Errorf("campo name non in json: %v", items[0])
		}
	})
}

func TestGsMilestone(t *testing.T) {
	g := newGsEnv(t)
	const R = "milestone"
	const repo = "iss-int" // repo usato da issuesE2E

	t.Run("list", func(t *testing.T) {
		r := g.ok("alice", R, repo, "milestone", "list")
		if !strings.Contains(r.out, "Stato") {
			t.Logf("intestazione: %q", r.out)
		}
	})

	t.Run("create", func(t *testing.T) {
		r := g.ok("alice", R, repo, "milestone", "create", "v1.0", "--description", "Prima versione", "--due-on", "2026-12-01")
		if !strings.Contains(r.errOut, "Creata") {
			t.Fatalf("messaggio: %q", r.errOut)
		}
		// bob non ha write su milestone: exit 5
		g.fails(5, "bob", R, repo, "milestone", "create", "v1.1")
	})

	t.Run("view", func(t *testing.T) {
		r := g.ok("alice", R, repo, "milestone", "view", "1")
		if !strings.Contains(r.out, "M-05") {
			t.Logf("view output: %q", r.out)
		}
		// --json
		m := jsonMap(t, g.ok("alice", R, repo, "milestone", "view", "1", "--json", "number,title,description,progress").out)
		if m["number"] != float64(1) {
			t.Errorf("json: %v", m)
		}
		if p, ok := m["progress"].(string); !ok || !strings.Contains(p, "%") {
			t.Errorf("progress: %v", m["progress"])
		}
	})

	t.Run("edit", func(t *testing.T) {
		g.ok("alice", R, repo, "milestone", "edit", "1", "--description", "desc modificata")
		r := g.ok("alice", R, repo, "milestone", "view", "1", "--json", "description")
		m := jsonMap(t, r.out)
		if m["description"] != "desc modificata" {
			t.Errorf("descrizione dopo edit: %v", m)
		}
		// edit senza flag: exit 2
		g.fails(2, "alice", R, repo, "milestone", "edit", "1")
		// data non valida: exit 2
		g.fails(2, "alice", R, repo, "milestone", "edit", "1", "--due-on", "bad")
	})

	t.Run("close_reopen", func(t *testing.T) {
		// chiudi
		r := g.ok("alice", R, repo, "milestone", "close", "1")
		if !strings.Contains(r.errOut, "Chiusa") {
			t.Fatalf("chiudi: %q", r.errOut)
		}
		// stato chiuso
		m := jsonMap(t, g.ok("alice", R, repo, "milestone", "view", "1", "--json", "state").out)
		if m["state"] != "closed" {
			t.Errorf("stato dopo close: %v", m)
		}
		// riapri
		r = g.ok("alice", R, repo, "milestone", "reopen", "1")
		if !strings.Contains(r.errOut, "Riaperta") {
			t.Fatalf("riapri: %q", r.errOut)
		}
		m = jsonMap(t, g.ok("alice", R, repo, "milestone", "view", "1", "--json", "state").out)
		if m["state"] != "open" {
			t.Errorf("stato dopo reopen: %v", m)
		}
	})

	t.Run("remove_due_on", func(t *testing.T) {
		g.ok("alice", R, repo, "milestone", "edit", "1", "--remove-due-on")
		m := jsonMap(t, g.ok("alice", R, repo, "milestone", "view", "1", "--json", "dueOn").out)
		if m["dueOn"] != nil {
			t.Errorf("dueOn dopo rimozione: %v", m["dueOn"])
		}
	})

	t.Run("delete_G8", func(t *testing.T) {
		// crea una milestone da eliminare (numero 2)
		g.ok("alice", R, repo, "milestone", "create", "da-eliminare", "--description", "da cancellare")

		// senza TTY e senza --yes: exit 2
		g.fails(2, "alice", R, repo, "milestone", "delete", "2")

		// con --yes: elimina
		r := g.ok("alice", R, repo, "milestone", "delete", "2", "--yes")
		if !strings.Contains(r.errOut, "Eliminata") {
			t.Errorf("messaggio: %q", r.errOut)
		}
		// non esiste più (con api diretta)
		want(t, g.api("alice", "GET", "/repos/alice/iss-int/milestones/2", nil), 404, "not_found")
	})

	t.Run("json_campi", func(t *testing.T) {
		out := g.ok("alice", R, repo, "milestone", "list", "--json", "title,progress,openIssues,closedIssues").out
		var items []map[string]any
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("json list: %v", err)
		}
		if len(items) == 0 {
			t.Log("nessuna milestone in lista")
			return
		}
		if _, ok := items[0]["title"]; !ok {
			t.Errorf("campo title non in json: %v", items[0])
		}
		if _, ok := items[0]["progress"]; !ok {
			t.Errorf("campo progress non in json: %v", items[0])
		}
	})
}

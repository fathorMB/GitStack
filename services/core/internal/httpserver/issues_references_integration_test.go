//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// C1 — Riferimenti tra issue: creazione evento "referenced_from", idempotenza,
// visibilità del repo sorgente.

func itoa64(n int64) string { return fmt.Sprintf("%d", n) }

func TestIssues_RiferimentiC1(t *testing.T) {
	e := newIssuesEnv(t)
	internalRepoID := e.repo("internal", true)
	_ = e.repo("private", false)
	_ = e.repo("alice-internal", true)

	// Bob ha write su internalRepo; alice crea le issue negli altri repo.
	e.id.grantWrite(internalRepoID, "bob")

	// C1.1: riferimento nello stesso repo.
	_ = e.open("internal", "carol", "issue sorgente")
	n2 := e.open("internal", "carol", "issue citata con #n")

	t.Run("stesso_repo_riferimento_con_number", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/repos/alice/internal/issues", "bob",
			`{"title":"con riferimento","body":"vedi #2"}`)
		e.want(rec, http.StatusCreated, "")

		// L'issue citata (#2) riceve l'evento referenced_from.
		evs := e.events("internal", n2, "carol")
		types := eventTypes(evs)
		found := false
		for _, tp := range types {
			if tp == "referenced_from" {
				found = true
			}
		}
		if !found {
			t.Fatalf("issue %d: evento referenced_from mancante in %v", n2, types)
		}
		if types[len(types)-1] != "referenced_from" {
			t.Fatalf("ultimo evento = %v, voluto referenced_from", types[len(types)-1])
		}

		// La sorgente non riceve auto-riferimento.
		rec2 := e.do(http.MethodGet, "/repos/alice/internal/issues/"+itoa64(e.issue(rec).Number)+"/events", "bob", "")
		e.want(rec2, http.StatusOK, "")
		var list2 openapi.IssueEventList
		_ = json.Unmarshal(rec2.Body.Bytes(), &list2)
		for _, ev := range list2.Items {
			if ev.Type == "referenced_from" {
				t.Fatalf("issue %d: non dovrebbe avere evento referenced_from", e.issue(rec).Number)
			}
		}
	})

	// C1.2: riferimento cross-repo.
	aliceN := e.open("alice-internal", "alice", "issue di alice")

	t.Run("cross_repo_riferimento", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/repos/alice/internal/issues", "bob",
			`{"title":"cita alice","body":"vedi alice/alice-internal#1"}`)
		e.want(rec, http.StatusCreated, "")

		evs := e.events("alice-internal", aliceN, "alice")
		found := false
		for _, tp := range eventTypes(evs) {
			if tp == "referenced_from" {
				found = true
			}
		}
		if !found {
			t.Fatalf("issue alice-internal#%d: referenced_from mancante", aliceN)
		}
	})

	// C1.3: riferimento a repo privato — visibilità.
	privateN := e.open("private", "alice", "issue privata")

	t.Run("repo_sorgente_privato_visibile_da_owner", func(t *testing.T) {
		// Alice cita la sua issue privata: l'evento compare.
		e.do(http.MethodPost, "/repos/alice/internal/issues", "alice",
			`{"title":"cita privato","body":"vedi alice/private#`+itoa64(privateN)+`"}`)

		evsAlice := e.events("private", privateN, "alice")
		found := false
		for _, tp := range eventTypes(evsAlice) {
			if tp == "referenced_from" {
				found = true
			}
		}
		if !found {
			t.Fatalf("alice non vede referenced_from in private#%d", privateN)
		}

		// sourceRepoId non appare nella risposta.
		recAlice := e.do(http.MethodGet, "/repos/alice/private/issues/"+itoa64(privateN)+"/events", "alice", "")
		e.want(recAlice, http.StatusOK, "")
		var listAlice openapi.IssueEventList
		_ = json.Unmarshal(recAlice.Body.Bytes(), &listAlice)
		for _, ev := range listAlice.Items {
			if ev.Data != nil {
				if _, ok := (*ev.Data)["sourceRepoId"]; ok {
					t.Fatalf("sourceRepoId presente nel data di evento %s", ev.Type)
				}
			}
		}

		// Bob non può leggere il repo privato: ottiene 404 su GET events.
		e.want(e.do(http.MethodGet, "/repos/alice/private/issues/"+itoa64(privateN)+"/events", "bob", ""), http.StatusNotFound, "not_found")
	})

	// C1.3b: visibilità cross-repo con alice-internal (tutti leggono).
	t.Run("alice-internal_riferimento_visibile_a_tutti", func(t *testing.T) {
		e.do(http.MethodPost, "/repos/alice/internal/issues", "carol",
			`{"title":"cita alice-internal","body":"alice/alice-internal#1"}`)

		for _, user := range []string{"alice", "bob", "carol"} {
			evs := e.events("alice-internal", aliceN, user)
			found := false
			for _, tp := range eventTypes(evs) {
				if tp == "referenced_from" {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s non vede referenced_from in alice-internal#%d", user, aliceN)
			}
		}
	})

	// C1.4: repo interno — tutti vedono.
	t.Run("repo_sorgente_interno_evento_visibile", func(t *testing.T) {
		e.do(http.MethodPost, "/repos/alice/internal/issues", "carol",
			`{"title":"cita interno","body":"alice/alice-internal#1"}`)

		for _, user := range []string{"alice", "bob", "carol"} {
			evs := e.events("alice-internal", aliceN, user)
			found := false
			for _, tp := range eventTypes(evs) {
				if tp == "referenced_from" {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s non vede referenced_from in repo interno: %v", user, eventTypes(evs))
			}
		}
	})

	// C1.5: idempotenza — stesso riferimento da issue diverse.
	t.Run("stesso_riferimento_due_volte_un_evento", func(t *testing.T) {
		n3 := e.open("internal", "carol", "target duplicato")

		e.do(http.MethodPost, "/repos/alice/internal/issues", "bob",
			`{"title":"r1","body":"#`+itoa64(n3)+`"}`)
		e.do(http.MethodPost, "/repos/alice/internal/issues", "bob",
			`{"title":"r2","body":"#`+itoa64(n3)+`"}`)

		evs := e.events("internal", n3, "carol")
		count := 0
		for _, tp := range eventTypes(evs) {
			if tp == "referenced_from" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("events referenced_from = %d, voluto 1", count)
		}
	})

	// C1.6: modifica — aggiungere riferimento crea evento.
	t.Run("modifica_texto_aggiunge_riferimento", func(t *testing.T) {
		n4 := e.open("internal", "carol", "target modifica")

		rec := e.do(http.MethodPost, "/repos/alice/internal/issues", "bob",
			`{"title":"da modificare","body":"nessun riferimento"}`)
		e.want(rec, http.StatusCreated, "")
		srcN := e.issue(rec).Number

		// Nessuna referenced_from prima della modifica.
		evsBefore := e.events("internal", n4, "carol")
		for _, tp := range eventTypes(evsBefore) {
			if tp == "referenced_from" {
				t.Fatalf("prima della modifica non ci dovrebbe essere referenced_from")
			}
		}

		// Modifica: aggiunge riferimento.
		e.do(http.MethodPatch, "/repos/alice/internal/issues/"+itoa64(srcN), "bob",
			`{"body":"ora cito #`+itoa64(n4)+`"}`)

		evsAfter := e.events("internal", n4, "carol")
		found := false
		count := 0
		for _, tp := range eventTypes(evsAfter) {
			if tp == "referenced_from" {
				found = true
				count++
			}
		}
		if !found {
			t.Fatalf("dopo la modifica referenced_from mancante")
		}
		if count != 1 {
			t.Fatalf("dopo modifica: %d referenced_from, voluto 1", count)
		}
	})

	// C1.7: idempotenza — stessa modifica due volte.
	t.Run("stessa_modifica_due_volte_un_evento", func(t *testing.T) {
		n5 := e.open("internal", "carol", "target modifica dup")

		rec := e.do(http.MethodPost, "/repos/alice/internal/issues", "bob",
			`{"title":"dup","body":"testo"}`)
		e.want(rec, http.StatusCreated, "")
		srcN := e.issue(rec).Number

		e.do(http.MethodPatch, "/repos/alice/internal/issues/"+itoa64(srcN), "bob",
			`{"body":"cita #`+itoa64(n5)+`"}`)
		e.do(http.MethodPatch, "/repos/alice/internal/issues/"+itoa64(srcN), "bob",
			`{"body":"cita #`+itoa64(n5)+`"}`)

		evs := e.events("internal", n5, "carol")
		count := 0
		for _, tp := range eventTypes(evs) {
			if tp == "referenced_from" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("stessa modifica: %d referenced_from, voluto 1", count)
		}
	})

	// C1.8: riferimento nel commento.
	t.Run("riferimento_in_commento", func(t *testing.T) {
		n6 := e.open("internal", "carol", "target commento")
		// Target che cita #7 (lo stesso numero non può essere autoreferenziale).
		// Creiamo prima #7 come target, poi #6 che lo commenta e cita #7.
		// In realtà il commento viene creato dopo, e il commento che cita #6 è auto-riferimento.
		// Quindi citiamo un altro numero.
		n7 := e.open("internal", "carol", "altro target")

		// Bob commenta #6 citando #7.
		e.do(http.MethodPost, "/repos/alice/internal/issues/"+itoa64(n6)+"/comments", "bob",
			`{"body":"vedi #`+itoa64(n7)+`"}`)

		// La issue #7 riceve referenced_from.
		evs := e.events("internal", n7, "carol")
		found := false
		for _, tp := range eventTypes(evs) {
			if tp == "referenced_from" {
				found = true
			}
		}
		if !found {
			t.Fatalf("commento: referenced_from mancante su #%d", n7)
		}
	})
}

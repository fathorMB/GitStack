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

	// C1.1: riferimento nello stesso repo + auto-riferimento bloccato.
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

		// Verifica data.source per la stessa issue sorgente → stessa destinazione.
		evsSrc := e.events("internal", e.issue(rec).Number, "bob")
		for _, ev := range evsSrc {
			if ev.Type == "referenced_from" {
				t.Fatalf("issue sorgente non dovrebbe mai avere referenced_from")
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

	// C1.3b: SORGENTE privata, destinazione leggibile da tutti.
	// Alice scrive in alice/private una issue che cita alice/internal#n.
	// Per alice l'evento sulla issue di destinazione c'è.
	// Per carol (legge internal ma non private) l'evento non c'è e total è più basso.
	_ = e.open("private", "alice", "issue sorgente privata")

	t.Run("sorgente_privata_visibile_da_owner", func(t *testing.T) {
		// Alice cita in private una issue di alice-internal.
		rec := e.do(http.MethodPost, "/repos/alice/private/issues", "alice",
			`{"title":"cita interno da privato","body":"vedi alice/alice-internal#`+itoa64(aliceN)+`"}`)
		privateSrcN := e.issue(rec).Number

		// Alice vede l'evento su alice-internal#1 (sorgente privata che Alice legge).
		evsAlice := e.events("alice-internal", aliceN, "alice")
		foundAlice := false
		for _, tp := range eventTypes(evsAlice) {
			if tp == "referenced_from" {
				foundAlice = true
			}
		}
		if !foundAlice {
			t.Fatalf("alice non vede referenced_from su alice-internal#%d", aliceN)
		}

		// Bob non può leggere il repo private: non vede l'evento referenced_from
		// da alice/private. Verifica che il total di alice sia > di bob (alice
		// vede l'evento da private, bob no).
		recAlice := e.do(http.MethodGet, "/repos/alice/alice-internal/issues/"+itoa64(aliceN)+"/events", "alice", "")
		e.want(recAlice, http.StatusOK, "")
		var listAlice openapi.IssueEventList
		_ = json.Unmarshal(recAlice.Body.Bytes(), &listAlice)

		recBob := e.do(http.MethodGet, "/repos/alice/alice-internal/issues/"+itoa64(aliceN)+"/events", "bob", "")
		e.want(recBob, http.StatusOK, "")
		var listBob openapi.IssueEventList
		_ = json.Unmarshal(recBob.Body.Bytes(), &listBob)

		aliceTotal := listAlice.Total
		bobTotal := listBob.Total
		if aliceTotal <= bobTotal {
			t.Fatalf("total alice=%d non è > total bob=%d", aliceTotal, bobTotal)
		}

		// sourceRepoId non appare nella risposta.
		for _, ev := range listAlice.Items {
			if ev.Data != nil {
				if _, ok := (*ev.Data)["sourceRepoId"]; ok {
					t.Fatalf("sourceRepoId presente nel data di evento %s", ev.Type)
				}
			}
		}

		// Verifica data.source: repository = "alice/private", number = privateSrcN.
		var foundSourceEvent bool
		for _, ev := range listAlice.Items {
			if ev.Type == "referenced_from" && ev.Data != nil {
				src, ok := (*ev.Data)["source"].(map[string]any)
				if !ok {
					continue
				}
				if repo, ok := src["repository"].(string); ok && repo == "alice/private" {
					if number, ok := src["number"].(float64); ok && int64(number) == privateSrcN {
						foundSourceEvent = true
						if _, ok := src["commentId"]; ok {
							t.Fatalf("commentId non dovrebbe essere presente per riferimenti su issue")
						}
					}
				}
			}
		}
		if !foundSourceEvent {
			t.Fatalf("non è stato trovato evento referenced_from con source.repository='alice/private'")
		}
	})

	// C1.3c: sorgente interna → visibile a un altro utente autenticato.
	t.Run("sorgente_interna_visibile_ad_altri", func(t *testing.T) {
		// alice/alice-internal#1 (aliceN) è interna.
		// Carol cita alice-internal#1 nel repo carol.
		_ = e.do(http.MethodPost, "/repos/alice/carol/issues", "carol",
			`{"title":"cita interna","body":"alice/alice-internal#`+itoa64(aliceN)+`"}`)

		// Alice vede l'evento.
		evsAlice := e.events("alice-internal", aliceN, "alice")
		foundAlice := false
		for _, tp := range eventTypes(evsAlice) {
			if tp == "referenced_from" {
				foundAlice = true
			}
		}
		if !foundAlice {
			t.Fatalf("alice non vede referenced_from da sorgente interna")
		}

		// Carol vede l'evento.
		evsCarol := e.events("alice-internal", aliceN, "carol")
		foundCarol := false
		for _, tp := range eventTypes(evsCarol) {
			if tp == "referenced_from" {
				foundCarol = true
			}
		}
		if !foundCarol {
			t.Fatalf("carol non vede referenced_from da sorgente interna")
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

	// C1.5: idempotenza — due issue diverse che citano la stessa destinazione: 2 eventi.
	t.Run("stesso_riferimento_due_volte_due_eventi", func(t *testing.T) {
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
		if count != 2 {
			t.Fatalf("events referenced_from = %d, voluto 2 (sorgenti diverse)", count)
		}
	})

	// C1.5b: stesso riferimento scritto due volte nella stessa issue: un solo evento.
	t.Run("stesso_riferimento_stessa_issue_un_evento", func(t *testing.T) {
		n3b := e.open("internal", "carol", "target duplicato b")

		rec := e.do(http.MethodPost, "/repos/alice/internal/issues", "bob",
			`{"title":"dup ref","body":"#`+itoa64(n3b)+` e anche #`+itoa64(n3b)+`"}`)
		e.want(rec, http.StatusCreated, "")

		// Modifica che ripete lo stesso riferimento: 1 evento.
		e.do(http.MethodPatch, "/repos/alice/internal/issues/"+itoa64(e.issue(rec).Number), "bob",
			`{"body":"#`+itoa64(n3b)+` e #`+itoa64(n3b)+`"}`)

		evs := e.events("internal", n3b, "carol")
		count := 0
		for _, tp := range eventTypes(evs) {
			if tp == "referenced_from" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("stesso riferimento nella stessa issue: %d events, voluto 1", count)
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

	// C1.6b: PATCH con solo title che contiene #n.
	t.Run("patch_solo_title_con_riferimento", func(t *testing.T) {
		n5 := e.open("internal", "carol", "target patch title")

		rec := e.do(http.MethodPost, "/repos/alice/internal/issues", "bob",
			`{"title":"senza ref","body":"nessun riferimento"}`)
		e.want(rec, http.StatusCreated, "")
		srcN := e.issue(rec).Number

		// Nessuna referenced_from prima.
		evsBefore := e.events("internal", n5, "carol")
		for _, tp := range eventTypes(evsBefore) {
			if tp == "referenced_from" {
				t.Fatalf("prima del PATCH non ci dovrebbe essere referenced_from")
			}
		}

		// PATCH solo con title contenente riferimento.
		e.do(http.MethodPatch, "/repos/alice/internal/issues/"+itoa64(srcN), "bob",
			`{"title":"fix #`+itoa64(n5)+`"}`)

		evsAfter := e.events("internal", n5, "carol")
		found := false
		for _, tp := range eventTypes(evsAfter) {
			if tp == "referenced_from" {
				found = true
			}
		}
		if !found {
			t.Fatalf("PATCH solo titolo con #n: referenced_from mancante su #%d", n5)
		}
	})

	// C1.7: idempotenza — stessa modifica due volte.
	t.Run("stessa_modifica_due_volte_un_evento", func(t *testing.T) {
		n6 := e.open("internal", "carol", "target modifica dup")

		rec := e.do(http.MethodPost, "/repos/alice/internal/issues", "bob",
			`{"title":"dup","body":"testo"}`)
		e.want(rec, http.StatusCreated, "")
		srcN := e.issue(rec).Number

		e.do(http.MethodPatch, "/repos/alice/internal/issues/"+itoa64(srcN), "bob",
			`{"body":"cita #`+itoa64(n6)+`"}`)
		e.do(http.MethodPatch, "/repos/alice/internal/issues/"+itoa64(srcN), "bob",
			`{"body":"cita #`+itoa64(n6)+`"}`)

		evs := e.events("internal", n6, "carol")
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
		n7 := e.open("internal", "carol", "target commento")

		// Alice commenta #1 citando #7.
		e.do(http.MethodPost, "/repos/alice/internal/issues/1/comments", "alice",
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

		// Verifica data.source con commentId.
		for _, ev := range evs {
			if ev.Type == "referenced_from" && ev.Data != nil {
				src, ok := (*ev.Data)["source"].(map[string]any)
				if !ok {
					continue
				}
				if repo, ok := src["repository"].(string); ok && repo == "alice/internal" {
					if number, ok := src["number"].(float64); ok && int64(number) == 1 {
						commentId, ok := src["commentId"].(string)
						if !ok || commentId == "" {
							t.Fatalf("commentId mancante per riferimento da commento")
						}
					}
				}
			}
		}
	})
}

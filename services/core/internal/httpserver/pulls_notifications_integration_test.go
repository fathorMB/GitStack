//go:build integration

package httpserver_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-265 (PR11): notifiche delle PR, stesse regole delle issues (C3-C8) con i
// revisori richiesti fra chi segue la PR. alice = admin del repo; bob (autore)
// e dave hanno write; carol legge il repo internal; mai notifiche per le
// proprie azioni, mai a chi ha Watch Ignore, mai a chi non legge il repo.

func TestPullsNotifichePR11(t *testing.T) {
	e := newPevEnv(t)
	repoID := e.repo("app", true)
	for _, u := range []string{"bob", "dave"} {
		e.id.grantWrite(repoID, u)
	}
	for _, b := range []string{"feat", "feat2", "draft", "feat3", "feat4", "feat5", "feat6"} {
		e.branch(repoID, b)
	}
	watch := func(user, mode string) {
		t.Helper()
		e.want(e.do(http.MethodPut, "/repos/alice/app/watch", user, fmt.Sprintf(`{"mode":%q}`, mode)), http.StatusOK, "")
	}

	var n int64
	t.Run("apertura_con_revisore_e_menzione", func(t *testing.T) {
		// L'autore non riceve niente; il revisore ha «review_requested», chi è
		// menzionato «mentioned»; chi non c'entra niente.
		n = e.openPR("bob", "feat", `,"body":"@carol guarda qui","reviewers":["dave"]`)
		e.expect("apertura", "dave:review_requested", "carol:mentioned")
	})

	t.Run("richiesta_di_review_a_un_altro_revisore", func(t *testing.T) {
		e.want(e.do(http.MethodPost, e.pull(n)+"/requested-reviewers", "bob", `{"reviewers":["alice"]}`), http.StatusOK, "")
		e.expect("richiesta ad alice", "alice:review_requested")
		// Chiedere di nuovo la review (rinnovo) avvisa ancora il revisore.
		e.want(e.do(http.MethodPost, e.pull(n)+"/requested-reviewers", "bob", `{"reviewers":["alice"]}`), http.StatusOK, "")
		e.expect("richiesta rinnovata", "alice:review_requested")
		// Togliere la richiesta non avvisa nessuno.
		e.want(e.do(http.MethodDelete, e.pull(n)+"/requested-reviewers/alice", "bob", ""), http.StatusOK, "")
		e.expect("richiesta tolta")
		e.want(e.do(http.MethodPost, e.pull(n)+"/requested-reviewers", "bob", `{"reviewers":["alice"]}`), http.StatusOK, "")
		e.expect("richiesta ad alice di nuovo", "alice:review_requested")
	})

	t.Run("esito_della_review_va_a_chi_segue_non_a_chi_la_scrive", func(t *testing.T) {
		e.want(e.do(http.MethodPost, e.pull(n)+"/reviews", "dave", `{"event":"approve","body":"ok per me"}`), http.StatusCreated, "")
		e.expect("approvazione di dave", "alice:participating", "bob:participating", "carol:participating")
		e.want(e.do(http.MethodPost, e.pull(n)+"/reviews", "dave", `{"event":"request_changes","body":"no, manca un test"}`), http.StatusCreated, "")
		e.expect("modifiche richieste da dave", "alice:participating", "bob:participating", "carol:participating")
		e.want(e.do(http.MethodPost, e.pull(n)+"/reviews", "dave", `{"event":"comment","body":"@bot controlli?"}`), http.StatusCreated, "")
		e.expect("commento di review con menzione", "alice:participating", "bob:participating", "carol:participating", "bot:mentioned")
	})

	t.Run("commenti_generali_e_menzioni", func(t *testing.T) {
		e.want(e.do(http.MethodPost, e.pull(n)+"/comments", "carol", `{"body":"anche per me va bene"}`), http.StatusCreated, "")
		// carol ha commentato: non riceve; chi segue sì (bot è stato menzionato, quindi segue).
		e.expect("commento di carol", "alice:participating", "bob:participating", "bot:participating", "dave:participating")
		// Una menzione vale più della partecipazione.
		e.want(e.do(http.MethodPost, e.pull(n)+"/comments", "bob", `{"body":"@dave ci penso io, @carol grazie"}`), http.StatusCreated, "")
		e.expect("commento con menzioni", "alice:participating", "bot:participating", "carol:mentioned", "dave:mentioned")
	})

	t.Run("commento_sulle_righe", func(t *testing.T) {
		head := e.git.of(repoID)["feat"]
		rec := e.do(http.MethodPost, e.pull(n)+"/comments", "dave",
			fmt.Sprintf(`{"body":"qui @alice","path":"a.txt","side":"new","line":2,"commitSha":%q}`, head))
		e.want(rec, http.StatusCreated, "")
		e.expect("commento sulla riga", "alice:mentioned", "bob:participating", "bot:participating", "carol:participating")
	})

	t.Run("unita_chiusa_riaperta_state_change_a_chi_segue", func(t *testing.T) {
		e.want(e.merge("app", n, "alice", `{"method":"squash"}`), http.StatusOK, "")
		e.expect("unione", "bob:state_change", "bot:state_change", "carol:state_change", "dave:state_change")

		m := e.openPR("bob", "feat2", "")
		e.expect("apertura senza nessuno")
		e.want(e.do(http.MethodPost, e.pull(m)+"/close", "alice", ""), http.StatusOK, "")
		e.expect("chiusura", "bob:state_change")
		e.want(e.do(http.MethodPost, e.pull(m)+"/reopen", "alice", ""), http.StatusOK, "")
		e.expect("riapertura", "bob:state_change")
		// L'autore che chiude la propria PR non riceve niente.
		e.want(e.do(http.MethodPost, e.pull(m)+"/close", "bob", ""), http.StatusOK, "")
		e.expect("chiusura dell'autore")
	})

	t.Run("bozza_pronta_e_watch", func(t *testing.T) {
		watch("carol", "all")
		watch("dave", "ignore")
		// Una bozza non avvisa il repo; la menzione di chi ha Ignore non conta.
		d := e.openPR("bob", "draft", `,"draft":true,"body":"@dave bozza"`)
		e.expect("bozza")
		// Una bozza non chiede review.
		e.want(e.do(http.MethodPost, e.pull(d)+"/requested-reviewers", "bob", `{"reviewers":["alice"]}`), http.StatusConflict, "draft")
		// Pronta: chi ha Watch all la riceve (era una bozza), Ignore resta fuori.
		e.want(e.do(http.MethodPost, e.pull(d)+"/ready", "bob", ""), http.StatusOK, "")
		e.expect("pronta", "carol:subscribed")
		e.want(e.do(http.MethodPost, e.pull(d)+"/draft", "bob", ""), http.StatusOK, "")
		e.expect("di nuovo bozza")
	})

	t.Run("watch_all_vede_le_PR_nuove_e_ignore_blocca_tutto", func(t *testing.T) {
		m := e.openPR("bob", "feat3", `,"reviewers":["dave","alice"]`)
		// dave ha Ignore: nemmeno la richiesta di review; carol Watch all: la PR nuova.
		e.expect("PR nuova", "alice:review_requested", "carol:subscribed")
		e.want(e.do(http.MethodPost, e.pull(m)+"/comments", "alice", `{"body":"una nota"}`), http.StatusCreated, "")
		e.expect("commento", "bob:participating", "carol:subscribed")
		e.want(e.do(http.MethodPost, e.pull(m)+"/reviews", "alice", `{"event":"comment","body":"letta"}`), http.StatusCreated, "")
		e.expect("review", "bob:participating", "carol:subscribed")
		// Tolto Ignore, dave torna a ricevere.
		watch("dave", "participating")
		e.want(e.do(http.MethodPost, e.pull(m)+"/requested-reviewers", "bob", `{"reviewers":["dave"]}`), http.StatusOK, "")
		e.expect("dave di nuovo", "dave:review_requested")
		watch("dave", "ignore")
		watch("carol", "participating")
	})

	t.Run("conflitti_sulla_propria_PR_una_volta_sola", func(t *testing.T) {
		m := e.openPR("bob", "feat4", "")
		e.expect("apertura")
		e.mg.mmu.Lock()
		e.mg.conflicts[key(repoID, "feat4")] = []string{"a.txt"}
		e.mg.mmu.Unlock()
		main := e.git.of(repoID)["main"]
		e.pushed(repoID, "main", main, main)
		e.expect("conflitti", "bob:state_change")
		// Lo stesso conflitto a un altro push non avvisa di nuovo.
		e.pushed(repoID, "main", main, main)
		e.expect("ancora conflitti")
		// Risolti e ricomparsi: avvisa di nuovo.
		e.mg.mmu.Lock()
		delete(e.mg.conflicts, key(repoID, "feat4"))
		e.mg.mmu.Unlock()
		e.pushed(repoID, "main", main, main)
		e.expect("risolti")
		e.mg.mmu.Lock()
		e.mg.conflicts[key(repoID, "feat4")] = []string{"b.txt"}
		e.mg.mmu.Unlock()
		e.pushed(repoID, "feat4", main, e.git.of(repoID)["feat4"])
		e.process()
		// Nella casella il numero e il testo sono della PR, senza attore (evento di sistema).
		l := decodeInto[openapi.NotificationList](t, e.do(http.MethodGet, "/notifications?reason=state_change", "bob", ""))
		if len(l.Items) != 1 || l.Items[0].Event != "pull_request.conflicts" || l.Items[0].Issue == nil || l.Items[0].Issue.Number != m ||
			l.Items[0].Actor != nil || !strings.Contains(l.Items[0].Summary, "Conflitti") {
			t.Fatalf("notifica dei conflitti: %+v", l.Items)
		}
		e.expect("conflitti di nuovo", "bob:state_change")
		e.want(e.do(http.MethodPost, e.pull(m)+"/close", "bob", ""), http.StatusOK, "")
		e.expect("chiusura dell'autore")
	})

	t.Run("stati_obbligatori_rossi_sulla_propria_PR", func(t *testing.T) {
		m := e.openPR("bob", "feat5", "")
		e.expect("apertura")
		e.want(e.settings("app", "alice", `{"requiredStatusContexts":["ci"]}`), http.StatusOK, "")
		head := e.git.of(repoID)["feat5"]
		post := func(ctx, state string) {
			t.Helper()
			e.want(e.postStatus("app", "alice", head, fmt.Sprintf(`{"state":%q,"context":%q}`, state, ctx)), http.StatusCreated, "")
		}
		post("ci", "pending")
		e.expect("in corso")
		post("ci", "failure")
		e.process()
		l := decodeInto[openapi.NotificationList](t, e.do(http.MethodGet, "/notifications?reason=state_change", "bob", ""))
		if len(l.Items) != 1 || l.Items[0].Event != "pull_request.checks_failed" || l.Items[0].Issue == nil || l.Items[0].Issue.Number != m ||
			!strings.Contains(l.Items[0].Summary, "Controlli obbligatori") {
			t.Fatalf("notifica dei controlli: %+v", l.Items)
		}
		e.expect("rosso", "bob:state_change")
		post("ci", "error") // ancora rosso: niente di nuovo
		e.expect("ancora rosso")
		post("lint", "failure") // non obbligatorio
		e.expect("contesto non obbligatorio")
		post("ci", "success")
		e.expect("verde")
		post("ci", "failure")
		e.expect("di nuovo rosso", "bob:state_change")

		e.want(e.settings("app", "alice", `{"requiredStatusContexts":[]}`), http.StatusOK, "")
	})

	t.Run("la_casella_mostra_la_PR_e_filtra_per_motivo", func(t *testing.T) {
		m := e.openPR("bob", "feat6", `,"reviewers":["alice"]`)
		e.process()
		l := decodeInto[openapi.NotificationList](t, e.do(http.MethodGet, "/notifications?reason=review_requested", "alice", ""))
		if len(l.Items) != 1 {
			t.Fatalf("notifiche review_requested di alice = %d", len(l.Items))
		}
		it := l.Items[0]
		if it.Reason != "review_requested" || it.Event != "pull_request.review_requested" || it.Issue == nil || it.Issue.Number != m ||
			it.Issue.Title != "PR feat6" || it.Issue.State != "open" || it.Actor == nil || it.Actor.Username != "bob" ||
			it.Repository == nil || it.Repository.FullName != "alice/app" || !strings.Contains(it.Summary, "review") {
			t.Fatalf("notifica inattesa: %+v", it)
		}
		// Unita: nella casella la PR risulta chiusa.
		e.want(e.merge("app", m, "bob", `{"method":"squash"}`), http.StatusOK, "")
		e.process()
		l = decodeInto[openapi.NotificationList](t, e.do(http.MethodGet, "/notifications?reason=review_requested", "alice", ""))
		if len(l.Items) != 1 || l.Items[0].Issue == nil || l.Items[0].Issue.State != "closed" {
			t.Fatalf("PR unita nella casella: %+v", l.Items)
		}
		e.sql(`DELETE FROM core.notifications`)
	})

	t.Run("email_di_default_per_richieste_di_review_e_menzioni", func(t *testing.T) {
		e.branch(repoID, "mail")
		e.eng.EmailWindow = 5 * time.Second
		defer func() { e.eng.EmailWindow = 0 }()
		watch("dave", "participating")
		m := e.openPR("bob", "mail", `,"body":"@carol ping","reviewers":["alice"]`)
		e.want(e.do(http.MethodPost, e.pull(m)+"/comments", "dave", `{"body":"commento semplice"}`), http.StatusCreated, "")
		e.process()
		due := func(user, reason string) bool {
			var c int
			if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM core.notifications WHERE user_id = $1 AND reason = $2 AND email_due_at IS NOT NULL`,
				users[user], reason).Scan(&c); err != nil {
				t.Fatal(err)
			}
			return c > 0
		}
		if !due("alice", "review_requested") || !due("carol", "mentioned") {
			t.Fatal("review_requested e mentioned devono avere l'email di default")
		}
		if due("bob", "participating") || due("alice", "participating") {
			t.Fatal("participating non ha l'email di default")
		}
		// Le preferenze di tipo valgono anche per il nuovo motivo.
		rec := e.do(http.MethodGet, "/user/notification-preferences", "alice", "")
		e.want(rec, http.StatusOK, "")
		if !strings.Contains(rec.Body.String(), `"review_requested":true`) {
			t.Fatalf("preferenze email = %s", rec.Body.String())
		}
		e.want(e.do(http.MethodPut, "/user/notification-preferences", "alice", `{"email":{"review_requested":false}}`), http.StatusOK, "")
		e.sql(`DELETE FROM core.notifications`)
		e.want(e.do(http.MethodPost, e.pull(m)+"/requested-reviewers", "bob", `{"reviewers":["alice"]}`), http.StatusOK, "")
		e.process()
		if due("alice", "review_requested") {
			t.Fatal("con la preferenza spenta l'email non parte")
		}
	})

	t.Run("chi_perde_l_accesso_al_repo_non_riceve", func(t *testing.T) {
		e.sql(`DELETE FROM core.notifications`)
		e.branch(repoID, "gone")
		watch("carol", "all")
		e.nid.revoke(repoID, "carol")
		watch("dave", "participating")
		// carol è menzionata e ha Watch all, ma non legge più il repo: niente, nemmeno il titolo.
		e.openPR("bob", "gone", `,"body":"@carol ciao","reviewers":["dave"]`)
		e.expect("senza accesso", "dave:review_requested")
	})
}

//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-300 (v1.1): commenti generali della PR (senza path) ed eliminazione dei
// commenti (PR5). Utenti come in reviewEnv: alice admin, bob write (autore
// della PR), carol solo read, bot agente con write.

func (e *reviewEnv) commentsURL(n int64) string {
	return fmt.Sprintf("/repos/alice/app/pulls/%d/comments", n)
}

func (e *reviewEnv) comment(n int64, user, body string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, e.commentsURL(n), user, body)
}

func (e *reviewEnv) decodeComment(rec *httptest.ResponseRecorder) openapi.PullRequestComment {
	e.t.Helper()
	var c openapi.PullRequestComment
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		e.t.Fatalf("commento non valido: %v: %s", err, rec.Body.String())
	}
	return c
}

func (e *reviewEnv) commentList(n int64, user string) openapi.PullRequestCommentList {
	e.t.Helper()
	rec := e.do(http.MethodGet, e.commentsURL(n), user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.PullRequestCommentList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *reviewEnv) commentRows(id string) (count int, deleted bool) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*), COALESCE(bool_or(deleted), false) FROM core.pull_request_comments WHERE id = $1`, id).
		Scan(&count, &deleted); err != nil {
		e.t.Fatal(err)
	}
	return count, deleted
}

func TestPullGeneralComments_CreaELeggi(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")

	// chi ha solo read commenta; il commento è generale e non ha conversazione
	rec := e.comment(n, "carol", `{"body":"una domanda generale"}`)
	e.want(rec, http.StatusCreated, "")
	c := e.decodeComment(rec)
	if c.Body != "una domanda generale" || c.Author.Username != "carol" || c.ThreadId != nil || c.Path != nil || c.Line != nil ||
		c.CommitSha != nil || c.Side != nil || c.Deleted || c.Edited || c.Outdated {
		t.Fatalf("commento generale = %+v", c)
	}
	var byAgent bool
	if err := e.pool.QueryRow(context.Background(), `SELECT by_agent FROM core.pull_request_comments WHERE id = $1`, c.Id).Scan(&byAgent); err != nil || byAgent {
		t.Fatalf("by_agent di una persona = %v (%v)", byAgent, err)
	}

	// un agente registra by_agent
	bot := e.decodeComment(e.comment(n, "bot", `{"body":"nota dell'agente"}`))
	if err := e.pool.QueryRow(context.Background(), `SELECT by_agent FROM core.pull_request_comments WHERE id = $1`, bot.Id).Scan(&byAgent); err != nil || !byAgent {
		t.Fatalf("by_agent di un agente = %v (%v)", byAgent, err)
	}

	// convive con i commenti sulle righe nello stesso elenco, in ordine di tempo
	line := e.comment(n, "carol", fmt.Sprintf(`{"body":"sulla riga","path":"b.txt","side":"new","line":2,"commitSha":%q}`, e.head("feat")))
	e.want(line, http.StatusCreated, "")
	l := e.commentList(n, "carol")
	if l.Total != 3 || len(l.Items) != 3 || l.Items[0].Id != c.Id || l.Items[1].Id != bot.Id || l.Items[2].ThreadId == nil {
		t.Fatalf("elenco = %+v", l)
	}
	// i commenti generali non sono conversazioni
	if ths := e.threads(n, ""); len(ths) != 1 {
		t.Fatalf("conversazioni = %d, volute 1", len(ths))
	}

	// campi delle righe senza path, risposta a un generale, corpo vuoto
	e.want(e.comment(n, "carol", `{"body":"x","line":3}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.comment(n, "carol", fmt.Sprintf(`{"body":"x","inReplyTo":%q}`, c.Id)), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.comment(n, "carol", `{"body":""}`), http.StatusUnprocessableEntity, "validation_failed")

	// l'autore modifica il proprio commento generale, gli altri no
	path := e.commentsURL(n) + "/" + c.Id.String()
	e.want(e.do(http.MethodPatch, path, "bob", `{"body":"x"}`), http.StatusForbidden, "forbidden")
	up := e.decodeComment(e.do(http.MethodPatch, path, "carol", `{"body":"corretta"}`))
	if up.Body != "corretta" || !up.Edited || up.ThreadId != nil {
		t.Fatalf("modifica = %+v", up)
	}

	// una PR chiusa accetta ancora i commenti generali, non quelli sulle righe
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/pulls/%d/close", n), "bob", ""), http.StatusOK, "")
	e.want(e.comment(n, "carol", `{"body":"dopo la chiusura"}`), http.StatusCreated, "")
	e.want(e.comment(n, "carol", fmt.Sprintf(`{"body":"x","path":"b.txt","side":"new","line":2,"commitSha":%q}`, e.head("feat"))), http.StatusConflict, "not_open")
}

func TestPullGeneralComments_Permessi(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")

	// repo privato: né chi non lo vede né un anonimo commentano o leggono
	secret := e.pullsEnv.repo("secret", false)
	e.branch(secret, "feat")
	e.pullsEnv.open("secret", "alice", "feat")
	e.want(e.do(http.MethodPost, "/repos/alice/secret/pulls/1/comments", "carol", `{"body":"x"}`), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodGet, "/repos/alice/secret/pulls/1/comments", "carol", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPost, "/repos/alice/secret/pulls/1/comments", "alice", `{"body":"ok"}`), http.StatusCreated, "")
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(m, "/repos/alice/secret/pulls/1/comments", nil)
		req.Header.Set("Content-Type", "application/json")
		anon := httptest.NewRecorder()
		e.router.ServeHTTP(anon, req)
		if anon.Code != http.StatusUnauthorized && anon.Code != http.StatusNotFound {
			t.Fatalf("anonimo %s su repo privato: %d %s", m, anon.Code, anon.Body.String())
		}
	}

	// PR inesistente e repo archiviato
	e.want(e.comment(999, "carol", `{"body":"x"}`), http.StatusNotFound, "not_found")
	e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, e.repo)
	e.want(e.comment(n, "carol", `{"body":"x"}`), http.StatusConflict, "archived")
}

func TestPullGeneralComments_Eliminazione(t *testing.T) {
	e := newReviewEnv(t)
	n := e.newPR("feat", "bob")
	head := e.head("feat")
	del := func(user, id string) *httptest.ResponseRecorder {
		return e.do(http.MethodDelete, e.commentsURL(n)+"/"+id, user, "")
	}

	t.Run("autore_e_admin_si_gli_altri_no", func(t *testing.T) {
		mine := e.decodeComment(e.comment(n, "carol", `{"body":"mio"}`))
		e.want(del("bob", mine.Id.String()), http.StatusForbidden, "forbidden")
		e.want(del("bot", mine.Id.String()), http.StatusForbidden, "forbidden")
		e.want(del("carol", mine.Id.String()), http.StatusNoContent, "")
		if cnt, _ := e.commentRows(mine.Id.String()); cnt != 0 {
			t.Fatalf("il commento deve sparire")
		}
		e.want(del("carol", mine.Id.String()), http.StatusNotFound, "not_found")

		theirs := e.decodeComment(e.comment(n, "carol", `{"body":"da moderare"}`))
		e.want(del("alice", theirs.Id.String()), http.StatusNoContent, "") // admin del repo
		if l := e.commentList(n, "carol"); l.Total != 0 {
			t.Fatalf("elenco = %+v", l)
		}
		e.want(del("carol", "00000000-0000-0000-0000-000000000000"), http.StatusNotFound, "not_found")
	})

	t.Run("segnaposto_se_la_conversazione_ha_risposte", func(t *testing.T) {
		root := e.decodeComment(e.comment(n, "carol", fmt.Sprintf(`{"body":"radice","path":"b.txt","side":"new","line":1,"commitSha":%q}`, head)))
		reply := e.decodeComment(e.comment(n, "bob", fmt.Sprintf(`{"body":"risposta","inReplyTo":%q}`, root.Id)))
		// bob non è l'autore della radice e non è admin
		e.want(del("bob", root.Id.String()), http.StatusForbidden, "forbidden")
		e.want(del("carol", root.Id.String()), http.StatusNoContent, "")
		if cnt, deleted := e.commentRows(root.Id.String()); cnt != 1 || !deleted {
			t.Fatalf("segnaposto: righe=%d deleted=%v", cnt, deleted)
		}
		l := e.commentList(n, "bob")
		if l.Total != 2 || !l.Items[0].Deleted || l.Items[0].Body != "" || l.Items[0].Id != root.Id || l.Items[0].ThreadId == nil || l.Items[1].Body != "risposta" {
			t.Fatalf("elenco con segnaposto = %+v", l.Items)
		}
		ths := e.threads(n, "")
		if len(ths) != 1 || len(ths[0].Comments) != 2 || !ths[0].Comments[0].Deleted || ths[0].Comments[0].Body != "" {
			t.Fatalf("conversazione = %+v", ths)
		}
		// un segnaposto non si elimina né si modifica di nuovo
		e.want(del("alice", root.Id.String()), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodPatch, e.commentsURL(n)+"/"+root.Id.String(), "carol", `{"body":"x"}`), http.StatusNotFound, "not_found")
		// si può ancora rispondere nella conversazione
		e.want(e.comment(n, "bob", fmt.Sprintf(`{"body":"ancora","inReplyTo":%q}`, root.Id)), http.StatusCreated, "")
		// tolte le risposte, sparisce anche il segnaposto
		e.want(del("bob", reply.Id.String()), http.StatusNoContent, "")
		if cnt, _ := e.commentRows(root.Id.String()); cnt != 1 {
			t.Fatalf("il segnaposto resta finché c'è una risposta")
		}
		var last string
		if err := e.pool.QueryRow(context.Background(), `SELECT id::text FROM core.pull_request_comments WHERE thread_id = $1 AND id <> $1`, root.Id).Scan(&last); err != nil {
			t.Fatal(err)
		}
		e.want(del("alice", last), http.StatusNoContent, "")
		if cnt, _ := e.commentRows(root.Id.String()); cnt != 0 {
			t.Fatalf("segnaposto senza risposte deve sparire")
		}
		if ths := e.threads(n, ""); len(ths) != 0 {
			t.Fatalf("conversazioni = %d", len(ths))
		}
	})

	t.Run("senza_risposte_sparisce_la_conversazione", func(t *testing.T) {
		root := e.decodeComment(e.comment(n, "carol", fmt.Sprintf(`{"body":"sola","path":"b.txt","side":"new","line":2,"commitSha":%q}`, head)))
		reply := e.decodeComment(e.comment(n, "bob", fmt.Sprintf(`{"body":"r","inReplyTo":%q}`, root.Id)))
		e.want(del("bob", reply.Id.String()), http.StatusNoContent, "") // una risposta si toglie e basta
		if ths := e.threads(n, ""); len(ths) != 1 || len(ths[0].Comments) != 1 {
			t.Fatalf("conversazioni = %+v", ths)
		}
		e.want(del("carol", root.Id.String()), http.StatusNoContent, "")
		if ths := e.threads(n, ""); len(ths) != 0 {
			t.Fatalf("conversazioni = %d", len(ths))
		}
	})

	t.Run("repo_archiviato_e_pr_inesistente", func(t *testing.T) {
		mine := e.decodeComment(e.comment(n, "carol", `{"body":"x"}`))
		e.want(e.do(http.MethodDelete, e.commentsURL(999)+"/"+mine.Id.String(), "carol", ""), http.StatusNotFound, "not_found")
		e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, e.repo)
		e.want(del("carol", mine.Id.String()), http.StatusConflict, "archived")
	})
}

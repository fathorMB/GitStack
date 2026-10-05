//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// M-05/D (GIT-104): commenti (I3), modifica con versioni e eliminazione
// tracciata (I4), blocco della discussione (I11), repo archiviato (R10).
// alice è admin (owner), bob ha write, carol solo read (repo interno).

func (e *issuesEnv) comment(path, user, body string) openapi.IssueComment {
	e.t.Helper()
	rec := e.do(http.MethodPost, path+"/comments", user, fmt.Sprintf(`{"body":%q}`, body))
	e.want(rec, http.StatusCreated, "")
	if rec.Header().Get("Location") == "" {
		e.t.Fatal("manca Location")
	}
	var c openapi.IssueComment
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *issuesEnv) comments(path, user string) openapi.IssueCommentList {
	e.t.Helper()
	rec := e.do(http.MethodGet, path+"/comments", user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.IssueCommentList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *issuesEnv) versions(path, user string) []openapi.TextVersion {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.TextVersionList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l.Items
}

// attachment inserisce un allegato non collegato (come dopo l'upload).
func (e *issuesEnv) attachment(repo uuid.UUID, uploader string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	e.sql(`INSERT INTO core.issue_attachments (id, repo_id, uploader_id, filename, content_type, size_bytes)
		VALUES ($1, $2, $3, 'f.txt', 'text/plain', 3)`, id, repo, uuid.MustParse(users[uploader]))
	return id
}

func TestIssueComments_CommentoModificaEliminazione(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.open("app", "carol", "problema")
	path := fmt.Sprintf("/repos/alice/app/issues/%d", n)

	c1 := e.comment(path, "carol", "primo **testo**") // solo read: commenta
	c2 := e.comment(path, "bob", "secondo")
	c3 := e.comment(path, "alice", "terzo")
	cp := path + "/comments/" + c1.Id.String()

	t.Run("creato", func(t *testing.T) {
		if c1.Body != "primo **testo**" || c1.Author.Username != "carol" || c1.Edited || c1.Deleted || c1.IssueNumber != n {
			t.Fatalf("commento = %+v", c1)
		}
		if x := e.issue(e.do(http.MethodGet, path, "carol", "")); x.CommentCount != 3 {
			t.Fatalf("commentCount = %d", x.CommentCount)
		}
	})
	t.Run("validazione", func(t *testing.T) {
		e.want(e.do(http.MethodPost, path+"/comments", "carol", `{"body":"   "}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPost, path+"/comments", "carol", `{"body":""}`), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPost, path+"/comments", "carol", fmt.Sprintf(`{"body":%q}`, strings.Repeat("a", 65537))), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodPost, path+"/comments", "carol", `{"testo":"x"}`), http.StatusBadRequest, "bad_request")
		e.want(e.do(http.MethodPost, path+"/comments", "carol", `{"body":"`+strings.Repeat("a", 1<<20)+`"}`), http.StatusRequestEntityTooLarge, "body_too_large")
	})
	t.Run("elenco_in_ordine_e_paginato", func(t *testing.T) {
		l := e.comments(path, "carol")
		if l.Total != 3 || len(l.Items) != 3 || l.Items[0].Id != c1.Id || l.Items[1].Id != c2.Id || l.Items[2].Id != c3.Id {
			t.Fatalf("elenco = %+v", l)
		}
		rec := e.do(http.MethodGet, path+"/comments?page=2&perPage=2", "carol", "")
		e.want(rec, http.StatusOK, "")
		var p openapi.IssueCommentList
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		if p.Total != 3 || len(p.Items) != 1 || p.Items[0].Id != c3.Id || p.Page != 2 {
			t.Fatalf("pagina 2 = %+v", p)
		}
		e.want(e.do(http.MethodGet, path+"/comments?page=0", "carol", ""), http.StatusBadRequest, "invalid_page")
		e.want(e.do(http.MethodGet, path+"/comments?perPage=101", "carol", ""), http.StatusBadRequest, "invalid_per_page")
	})
	t.Run("modifica_solo_dell_autore_con_versioni_per_admin", func(t *testing.T) {
		// Né admin né write modificano il testo altrui.
		e.want(e.do(http.MethodPatch, cp, "alice", `{"body":"x"}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPatch, cp, "bob", `{"body":"x"}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPatch, cp, "carol", `{"body":""}`), http.StatusUnprocessableEntity, "validation_failed")
		// Testo uguale: nessuna versione, nessun edited.
		rec := e.do(http.MethodPatch, cp, "carol", `{"body":"primo **testo**"}`)
		e.want(rec, http.StatusOK, "")
		if got := e.versions(cp+"/versions", "alice"); len(got) != 0 {
			t.Fatalf("versioni = %+v", got)
		}
		e.want(e.do(http.MethodPatch, cp, "carol", `{"body":"primo v2"}`), http.StatusOK, "")
		rec = e.do(http.MethodPatch, cp, "carol", `{"body":"primo v3"}`)
		e.want(rec, http.StatusOK, "")
		var c openapi.IssueComment
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		if !c.Edited || c.Body != "primo v3" {
			t.Fatalf("commento = %+v", c)
		}
		vs := e.versions(cp+"/versions", "alice")
		if len(vs) != 2 || vs[0].Version != 2 || vs[0].Body != "primo v2" || vs[1].Version != 1 || vs[1].Body != "primo **testo**" || vs[1].Editor.Username != "carol" {
			t.Fatalf("versioni = %+v", vs)
		}
		// Le versioni sono di admin.
		e.want(e.do(http.MethodGet, cp+"/versions", "carol", ""), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodGet, cp+"/versions", "bob", ""), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodGet, path+"/comments/"+uuid.NewString()+"/versions", "alice", ""), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodPatch, path+"/comments/"+uuid.NewString(), "carol", `{"body":"x"}`), http.StatusNotFound, "not_found")
	})
	t.Run("eliminazione_negata_ad_altri", func(t *testing.T) {
		e.want(e.do(http.MethodDelete, cp, "bob", ""), http.StatusForbidden, "forbidden") // write non basta
		e.want(e.do(http.MethodDelete, path+"/comments/"+c2.Id.String(), "carol", ""), http.StatusForbidden, "forbidden")
		if l := e.comments(path, "carol"); l.Items[0].Deleted || l.Items[1].Deleted {
			t.Fatal("un commento è stato eliminato")
		}
	})
	t.Run("eliminazione_dell_autore_con_traccia", func(t *testing.T) {
		e.want(e.do(http.MethodDelete, path+"/comments/"+c2.Id.String(), "bob", ""), http.StatusNoContent, "")
		l := e.comments(path, "carol")
		if l.Total != 3 || !l.Items[1].Deleted || l.Items[1].Body != "" || l.Items[1].Author.Username != "bob" {
			t.Fatalf("elenco = %+v", l)
		}
		if x := e.issue(e.do(http.MethodGet, path, "carol", "")); x.CommentCount != 2 {
			t.Fatalf("commentCount = %d", x.CommentCount)
		}
		// Eliminato: 404 a modifica e a una seconda eliminazione.
		e.want(e.do(http.MethodPatch, path+"/comments/"+c2.Id.String(), "bob", `{"body":"x"}`), http.StatusNotFound, "not_found")
		e.want(e.do(http.MethodDelete, path+"/comments/"+c2.Id.String(), "bob", ""), http.StatusNotFound, "not_found")
		// admin vede il testo eliminato tra le versioni.
		vs := e.versions(path+"/comments/"+c2.Id.String()+"/versions", "alice")
		if len(vs) != 1 || vs[0].Body != "secondo" {
			t.Fatalf("versioni = %+v", vs)
		}
	})
	t.Run("eliminazione_di_admin_con_traccia", func(t *testing.T) {
		e.want(e.do(http.MethodDelete, cp, "alice", ""), http.StatusNoContent, "")
		l := e.comments(path, "carol")
		if !l.Items[0].Deleted || l.Items[0].Body != "" {
			t.Fatalf("elenco = %+v", l)
		}
		evs := e.events("app", n, "carol")
		var del []openapi.IssueEvent
		for _, ev := range evs {
			if ev.Type == openapi.IssueEventTypeCommentDeleted {
				del = append(del, ev)
			}
		}
		if len(del) != 2 || del[1].Actor == nil || del[1].Actor.Username != "alice" || (*del[1].Data)["commentId"] != c1.Id.String() {
			t.Fatalf("eventi comment_deleted = %+v", del)
		}
		if vs := e.versions(cp+"/versions", "alice"); len(vs) != 3 || vs[0].Body != "primo v3" {
			t.Fatalf("versioni = %+v", vs)
		}
	})
}

func TestIssueComments_BloccoI11(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.open("app", "carol", "discussione")
	path := fmt.Sprintf("/repos/alice/app/issues/%d", n)
	mine := e.comment(path, "carol", "prima del blocco")

	t.Run("solo_admin_blocca", func(t *testing.T) {
		e.want(e.do(http.MethodPut, path+"/lock", "carol", `{}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPut, path+"/lock", "bob", `{}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodDelete, path+"/lock", "bob", ""), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPut, path+"/lock", "alice", fmt.Sprintf(`{"reason":%q}`, strings.Repeat("a", 257))), http.StatusUnprocessableEntity, "validation_failed")
		rec := e.do(http.MethodPut, path+"/lock", "alice", `{"reason":"troppo accesa"}`)
		e.want(rec, http.StatusOK, "")
		if x := e.issue(rec); !x.Locked {
			t.Fatalf("issue = %+v", x)
		}
		// Idempotente: nessun secondo evento.
		e.want(e.do(http.MethodPut, path+"/lock", "alice", ``), http.StatusOK, "")
	})
	t.Run("read_rifiutato_write_ammesso", func(t *testing.T) {
		e.want(e.do(http.MethodPost, path+"/comments", "carol", `{"body":"ancora io"}`), http.StatusForbidden, "locked")
		e.want(e.do(http.MethodPatch, path+"/comments/"+mine.Id.String(), "carol", `{"body":"modifica"}`), http.StatusForbidden, "locked")
		e.want(e.do(http.MethodPatch, path, "carol", `{"body":"modifica"}`), http.StatusForbidden, "locked")
		e.want(e.do(http.MethodPost, path+"/comments", "bob", `{"body":"da write"}`), http.StatusCreated, "")
		e.want(e.do(http.MethodPost, path+"/comments", "alice", `{"body":"da admin"}`), http.StatusCreated, "")
		// La lettura resta aperta.
		if l := e.comments(path, "carol"); l.Total != 3 {
			t.Fatalf("commenti = %d", l.Total)
		}
	})
	t.Run("sblocco", func(t *testing.T) {
		e.want(e.do(http.MethodDelete, path+"/lock", "alice", ""), http.StatusOK, "")
		e.want(e.do(http.MethodDelete, path+"/lock", "alice", ""), http.StatusOK, "") // idempotente
		e.want(e.do(http.MethodPost, path+"/comments", "carol", `{"body":"di nuovo"}`), http.StatusCreated, "")
	})
	t.Run("cronologia_con_motivo", func(t *testing.T) {
		evs := e.events("app", n, "carol")
		if got := eventTypes(evs); !slices.Equal(got, []string{"opened", "locked", "unlocked"}) {
			t.Fatalf("cronologia = %v", got)
		}
		if (*evs[1].Data)["reason"] != "troppo accesa" || evs[1].Actor.Username != "alice" {
			t.Fatalf("evento locked = %+v", evs[1])
		}
	})
}

func TestIssueComments_CronologiaTuttiGliEventi(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.open("app", "carol", "storia")
	path := fmt.Sprintf("/repos/alice/app/issues/%d", n)
	e.sql(`INSERT INTO core.labels (id, repo_id, name, color) VALUES ($1, $2, 'urgente', 'ff0000')`, uuid.New(), repoID)

	e.want(e.do(http.MethodPatch, path, "carol", `{"title":"storia 2"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, path+"/close", "bob", `{"reason":"not_planned"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, path+"/reopen", "bob", ``), http.StatusOK, "")
	c := e.comment(path, "carol", "commento")
	e.want(e.do(http.MethodPut, path+"/lock", "alice", `{}`), http.StatusOK, "")
	e.want(e.do(http.MethodDelete, path+"/lock", "alice", ``), http.StatusOK, "")
	e.want(e.do(http.MethodDelete, path+"/comments/"+c.Id.String(), "carol", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodPut, path+"/hidden", "alice", `{"hidden":true}`), http.StatusOK, "")
	e.want(e.do(http.MethodPut, path+"/hidden", "alice", `{"hidden":false}`), http.StatusOK, "")

	want := []string{"opened", "renamed", "closed", "reopened", "locked", "unlocked", "comment_deleted", "hidden", "unhidden"}
	got := e.events("app", n, "carol")
	if !slices.Equal(eventTypes(got), want) {
		t.Fatalf("cronologia = %v, voluta %v", eventTypes(got), want)
	}
	rec := e.do(http.MethodGet, path+"/events?page=2&perPage=4", "carol", "")
	e.want(rec, http.StatusOK, "")
	var p openapi.IssueEventList
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Total != 9 || !slices.Equal(eventTypes(p.Items), want[4:8]) {
		t.Fatalf("pagina 2 = %+v", p)
	}
}

func TestIssueComments_NascosteERepoNonLeggibili(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.repo("segreto", false)
	n := e.open("app", "carol", "da nascondere")
	path := fmt.Sprintf("/repos/alice/app/issues/%d", n)
	c := e.comment(path, "carol", "contenuto riservato")
	e.want(e.do(http.MethodPut, path+"/hidden", "alice", `{"hidden":true}`), http.StatusOK, "")
	cp := path + "/comments/" + c.Id.String()

	for _, user := range []string{"carol", "bob"} { // anche autore e write
		for _, call := range []struct{ method, path, body string }{
			{http.MethodGet, path + "/comments", ""},
			{http.MethodPost, path + "/comments", `{"body":"x"}`},
			{http.MethodPatch, cp, `{"body":"x"}`},
			{http.MethodDelete, cp, ""},
			{http.MethodGet, cp + "/versions", ""},
			{http.MethodPut, path + "/lock", `{}`},
			{http.MethodDelete, path + "/lock", ""},
		} {
			rec := e.do(call.method, call.path, user, call.body)
			e.want(rec, http.StatusNotFound, "not_found")
			if strings.Contains(rec.Body.String(), "riservato") {
				t.Fatalf("%s %s: contenuto nella risposta negativa", call.method, call.path)
			}
		}
	}
	if l := e.comments(path, "alice"); l.Total != 1 {
		t.Fatalf("admin deve vedere i commenti della nascosta: %d", l.Total)
	}
	// Repo non leggibile: 404, senza rivelarne l'esistenza.
	for _, p := range []string{"/repos/alice/segreto/issues/1/comments", "/repos/alice/inesistente/issues/1/comments"} {
		rec := e.do(http.MethodGet, p, "carol", "")
		e.want(rec, http.StatusNotFound, "not_found")
		recPost := e.do(http.MethodPost, p, "carol", `{"body":"x"}`)
		e.want(recPost, http.StatusNotFound, "not_found")
		if strings.Contains(rec.Body.String(), "segreto") || strings.Contains(recPost.Body.String(), "segreto") {
			t.Fatalf("la risposta rivela il repo: %s", rec.Body.String())
		}
	}
	// Issue inesistente.
	e.want(e.do(http.MethodGet, "/repos/alice/app/issues/999/comments", "carol", ""), http.StatusNotFound, "not_found")
}

func TestIssueComments_RepoArchiviatoR10(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.open("app", "carol", "storica")
	path := fmt.Sprintf("/repos/alice/app/issues/%d", n)
	c := e.comment(path, "carol", "commento")
	cp := path + "/comments/" + c.Id.String()
	e.want(e.do(http.MethodPatch, "/repos/alice/app", "alice", `{"archived":true}`), http.StatusOK, "")

	e.want(e.do(http.MethodPost, path+"/comments", "carol", `{"body":"x"}`), http.StatusConflict, "archived")
	e.want(e.do(http.MethodPatch, cp, "carol", `{"body":"x"}`), http.StatusConflict, "archived")
	e.want(e.do(http.MethodDelete, cp, "carol", ""), http.StatusConflict, "archived")
	e.want(e.do(http.MethodDelete, cp, "alice", ""), http.StatusConflict, "archived")
	e.want(e.do(http.MethodPut, path+"/lock", "alice", `{}`), http.StatusConflict, "archived")
	e.want(e.do(http.MethodDelete, path+"/lock", "alice", ``), http.StatusConflict, "archived")
	// Si legge ancora.
	if l := e.comments(path, "carol"); l.Total != 1 || l.Items[0].Deleted || l.Items[0].Body != "commento" {
		t.Fatalf("elenco = %+v", l)
	}
	e.want(e.do(http.MethodGet, cp+"/versions", "alice", ""), http.StatusOK, "")
}

func TestIssueComments_AllegatiNellaStessaTransazione(t *testing.T) {
	e := newIssuesEnv(t)
	repoID := e.repo("app", true)
	otherRepo := e.repo("altro", true)
	e.id.grantWrite(repoID, "bob")
	n := e.open("app", "carol", "con allegati")
	path := fmt.Sprintf("/repos/alice/app/issues/%d", n)
	linked := func(id uuid.UUID) bool {
		var issue *uuid.UUID
		if err := e.pool.QueryRow(t.Context(), `SELECT issue_id FROM core.issue_attachments WHERE id = $1`, id).Scan(&issue); err != nil {
			t.Fatal(err)
		}
		return issue != nil
	}
	body := func(text string, ids ...uuid.UUID) string {
		list := make([]string, len(ids))
		for i, id := range ids {
			list[i] = `"` + id.String() + `"`
		}
		return fmt.Sprintf(`{"body":%q,"attachmentIds":[%s]}`, text, strings.Join(list, ","))
	}

	t.Run("commento_collega_gli_allegati", func(t *testing.T) {
		a := e.attachment(repoID, "carol")
		rec := e.do(http.MethodPost, path+"/comments", "carol", body("con file", a, a)) // ripetuto: conta una volta
		e.want(rec, http.StatusCreated, "")
		var c openapi.IssueComment
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		if c.Attachments == nil || len(*c.Attachments) != 1 || (*c.Attachments)[0].Id != a {
			t.Fatalf("allegati = %+v", c.Attachments)
		}
		var cid uuid.UUID
		if err := e.pool.QueryRow(t.Context(), `SELECT comment_id FROM core.issue_attachments WHERE id = $1`, a).Scan(&cid); err != nil || cid != uuid.UUID(c.Id) {
			t.Fatalf("comment_id = %v, %v", cid, err)
		}
		if l := e.comments(path, "carol"); l.Items[0].Attachments == nil {
			t.Fatal("l'elenco non riporta gli allegati")
		}
	})
	t.Run("rifiuti_senza_effetti", func(t *testing.T) {
		mineOther := e.attachment(otherRepo, "carol") // di un altro repo
		bobs := e.attachment(repoID, "bob")           // di un altro utente
		fine := e.attachment(repoID, "carol")
		already := e.attachment(repoID, "carol")
		e.want(e.do(http.MethodPost, path+"/comments", "carol", body("uno", already)), http.StatusCreated, "")
		before := e.comments(path, "carol").Total
		for name, ids := range map[string][]uuid.UUID{
			"altro_repo":    {fine, mineOther},
			"altro_utente":  {fine, bobs},
			"gia_collegato": {fine, already},
			"inesistente":   {fine, uuid.New()},
		} {
			e.want(e.do(http.MethodPost, path+"/comments", "carol", body("no "+name, ids...)), http.StatusUnprocessableEntity, "attachment_not_linkable")
		}
		// Transazione annullata: nessun commento in più, nessun allegato valido collegato.
		if after := e.comments(path, "carol").Total; after != before {
			t.Fatalf("commenti %d -> %d", before, after)
		}
		if linked(fine) || linked(mineOther) || linked(bobs) {
			t.Fatal("un allegato è stato collegato nonostante il rifiuto")
		}
	})
	t.Run("issue_alla_creazione", func(t *testing.T) {
		a := e.attachment(repoID, "carol")
		rec := e.do(http.MethodPost, "/repos/alice/app/issues", "carol", fmt.Sprintf(`{"title":"con file","attachmentIds":["%s"]}`, a))
		e.want(rec, http.StatusCreated, "")
		x := e.issue(rec)
		if x.Attachments == nil || len(*x.Attachments) != 1 || (*x.Attachments)[0].Id != a {
			t.Fatalf("allegati = %+v", x.Attachments)
		}
		// Già collegato, di altro repo: rifiutato e la issue non nasce.
		var count0 int
		_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM core.issues WHERE repo_id = $1`, repoID).Scan(&count0)
		for _, id := range []uuid.UUID{a, e.attachment(otherRepo, "carol")} {
			e.want(e.do(http.MethodPost, "/repos/alice/app/issues", "carol", fmt.Sprintf(`{"title":"no","attachmentIds":["%s"]}`, id)), http.StatusUnprocessableEntity, "attachment_not_linkable")
		}
		var count1 int
		_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM core.issues WHERE repo_id = $1`, repoID).Scan(&count1)
		if count0 != count1 {
			t.Fatalf("issues %d -> %d: la creazione rifiutata ha lasciato una issue", count0, count1)
		}
	})
	t.Run("issue_alla_modifica", func(t *testing.T) {
		a := e.attachment(repoID, "carol")
		rec := e.do(http.MethodPatch, path, "carol", fmt.Sprintf(`{"attachmentIds":["%s"]}`, a))
		e.want(rec, http.StatusOK, "")
		if x := e.issue(rec); x.Attachments == nil || len(*x.Attachments) != 1 || x.Edited {
			t.Fatalf("issue = %+v", x)
		}
		e.want(e.do(http.MethodPatch, path, "carol", fmt.Sprintf(`{"title":"nuovo","attachmentIds":["%s"]}`, a)), http.StatusUnprocessableEntity, "attachment_not_linkable")
		if x := e.issue(e.do(http.MethodGet, path, "carol", "")); x.Title != "con allegati" {
			t.Fatalf("il titolo è cambiato nonostante il rifiuto: %q", x.Title)
		}
		e.want(e.do(http.MethodPatch, path, "carol", `{"attachmentIds":[]}`), http.StatusUnprocessableEntity, "validation_failed")
	})
}

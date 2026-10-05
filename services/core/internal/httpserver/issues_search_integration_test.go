//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// M-05/F (GIT-106): ricerca delle issues (I10) nel repo e su tutta
// l'installazione.

const roboID = "eeeeeeee-0000-0000-0000-000000000005"

func (e *issuesEnv) addIssueSQL(repo uuid.UUID, number int64, q string, args ...any) {
	e.t.Helper()
	e.sql(q, append([]any{repo, number}, args...)...)
}

func (e *issuesEnv) label(repo uuid.UUID, number int64, name string) {
	e.t.Helper()
	e.sql(`INSERT INTO core.labels (id, repo_id, name, color) VALUES (gen_random_uuid(), $1, $2, 'ffffff') ON CONFLICT DO NOTHING`, repo, name)
	e.sql(`INSERT INTO core.issue_labels (issue_id, label_id)
		SELECT i.id, l.id FROM core.issues i, core.labels l
		WHERE i.repo_id = $1 AND i.number = $2 AND l.repo_id = $1 AND lower(l.name) = lower($3)`, repo, number, name)
}

func (e *issuesEnv) assign(repo uuid.UUID, number int64, userID string) {
	e.t.Helper()
	e.sql(`INSERT INTO core.issue_assignees (issue_id, user_id) SELECT id, $3 FROM core.issues WHERE repo_id = $1 AND number = $2`,
		repo, number, uuid.MustParse(userID))
}

// keys elenca «repo#n» ordinati.
func searchKeys(t *testing.T, rec interface{ Bytes() []byte }, global bool, repo string) []string {
	t.Helper()
	var out []string
	if global {
		var l openapi.IssueSearchResultList
		if err := json.Unmarshal(rec.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		for _, it := range l.Items {
			out = append(out, fmt.Sprintf("%s#%d", it.Repo, it.Issue.Number))
		}
	} else {
		var l openapi.IssueList
		if err := json.Unmarshal(rec.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		for _, it := range l.Items {
			out = append(out, fmt.Sprintf("%s#%d", repo, it.Number))
		}
	}
	sort.Strings(out)
	return out
}

// search esegue q nel repo alice/app e, con repo:alice/app, su tutta
// l'installazione: i due percorsi devono dare lo stesso risultato.
func (e *issuesEnv) search(user, q string) []string {
	e.t.Helper()
	a := e.do(http.MethodGet, "/repos/alice/app/issues?state=all&perPage=100&q="+url.QueryEscape(q), user, "")
	e.want(a, http.StatusOK, "")
	if strings.Contains(q, "repo:") || strings.Contains(q, "org:") {
		return searchKeys(e.t, a.Body, false, "alice/app") // il percorso globale è provato a parte
	}
	b := e.do(http.MethodGet, "/search/issues?perPage=100&q="+url.QueryEscape("repo:alice/app "+q), user, "")
	e.want(b, http.StatusOK, "")
	ka, kb := searchKeys(e.t, a.Body, false, "alice/app"), searchKeys(e.t, b.Body, true, "")
	if !slices.Equal(ka, kb) {
		e.t.Fatalf("q=%q: nel repo %v, globale %v", q, ka, kb)
	}
	return ka
}

func (e *issuesEnv) global(user, q string) []string {
	e.t.Helper()
	b := e.do(http.MethodGet, "/search/issues?perPage=100&q="+url.QueryEscape(q), user, "")
	e.want(b, http.StatusOK, "")
	return searchKeys(e.t, b.Body, true, "")
}

func app(ns ...int) []string {
	var out []string
	for _, n := range ns {
		out = append(out, fmt.Sprintf("alice/app#%d", n))
	}
	sort.Strings(out)
	return out
}

func newSearchEnv(t *testing.T) (*issuesEnv, uuid.UUID) {
	e := newIssuesEnv(t)
	e.id.fakeIdentity.owners["robo"] = identityclient.Owner{Type: "user", ID: uuid.MustParse(roboID), Name: "robo"}
	appID := e.repo("app", true)
	e.id.grantWrite(appID, "bob")

	e.open("app", "carol", "Crash all'avvio")         // 1
	e.open("app", "alice", "Documentazione mancante") // 2
	e.open("app", "carol", "Duplicato del crash")     // 3
	e.open("app", "bob", "Idea futura")               // 4
	e.open("app", "carol", "Segreto interno")         // 5 (nascosta)
	e.sql(`INSERT INTO core.milestones (id, repo_id, number, title) VALUES (gen_random_uuid(), $1, 1, 'v1')`, appID)
	e.sql(`UPDATE core.issues SET milestone_id = (SELECT id FROM core.milestones WHERE repo_id = $1) WHERE repo_id = $1 AND number = 1`, appID)
	e.label(appID, 1, "bug")
	e.label(appID, 2, "documentation")
	e.assign(appID, 1, bobID)
	e.assign(appID, 4, roboID)
	e.sql(`UPDATE core.issues SET state='closed', close_reason='completed', closed_at=now() WHERE repo_id=$1 AND number=2`, appID)
	e.sql(`UPDATE core.issues SET state='closed', close_reason='duplicate', duplicate_of=1, closed_at=now() WHERE repo_id=$1 AND number=3`, appID)
	e.sql(`UPDATE core.issues SET state='closed', close_reason='not_planned', closed_at=now() WHERE repo_id=$1 AND number=4`, appID)
	e.sql(`UPDATE core.issues SET hidden = true WHERE repo_id=$1 AND number=5`, appID)
	e.sql(`INSERT INTO core.issue_comments (id, issue_id, author_id, body) SELECT gen_random_uuid(), id, $2, 'Il pinguino non parte' FROM core.issues WHERE repo_id=$1 AND number=2`,
		appID, uuid.MustParse(carolID))
	return e, appID
}

func TestIssuesSearch_Qualificatori(t *testing.T) {
	e, _ := newSearchEnv(t)
	cases := []struct {
		user, q string
		want    []string
	}{
		{"bob", "", app(1, 2, 3, 4)},
		{"bob", "is:open", app(1)},
		{"bob", "is:closed", app(2, 3, 4)},
		{"bob", "-is:closed", app(1)},
		{"bob", "is:issue", app(1, 2, 3, 4)},
		{"bob", "-is:issue", nil},
		{"bob", "reason:completed", app(2)},
		{"bob", "reason:duplicate", app(3)},
		{"bob", "reason:not-planned", app(4)},
		{"bob", "reason:completed reason:duplicate", app(2, 3)},
		{"bob", "is:closed -reason:completed", app(3, 4)},
		{"bob", "label:bug", app(1)},
		{"bob", `label:"BUG"`, app(1)},
		{"bob", "-label:bug", app(2, 3, 4)},
		{"bob", "label:bug label:documentation", nil},
		{"bob", "label:nonesiste", nil},
		{"bob", "assignee:bob", app(1)},
		{"bob", "assignee:@bob", app(1)},
		{"bob", "assignee:@me", app(1)},
		{"carol", "assignee:@me", nil},
		{"bob", "assignee:@agents", app(4)},
		{"bob", "assignee:robo", app(4)},
		{"bob", "-assignee:@agents", app(1, 2, 3)},
		{"bob", "assignee:nessuno", nil},
		{"bob", "author:carol", app(1, 3)},
		{"carol", "author:@me", app(1, 3)},
		{"bob", "author:@agents", nil},
		{"bob", "-author:carol", app(2, 4)},
		{"bob", "milestone:v1", app(1)},
		{"bob", "milestone:1", app(1)},
		{"bob", "-milestone:v1", app(2, 3, 4)},
		{"bob", "no:label", app(3, 4)},
		{"bob", "no:assignee", app(2, 3)},
		{"bob", "no:milestone", app(2, 3, 4)},
		{"bob", "-no:label", app(1, 2)},
		{"bob", "repo:alice/app", app(1, 2, 3, 4)},
		{"bob", "repo:alice/altro", nil},
		{"bob", "-repo:alice/app", nil},
		{"bob", "org:acme", nil},
		// testo libero: titolo, testo e commenti
		{"bob", "crash", app(1, 3)},
		{"bob", "CRASH", app(1, 3)},
		{"bob", "pinguino", app(2)},
		{"bob", "testo", app(1, 2, 3, 4)},
		{"bob", `"documentazione mancante"`, app(2)},
		{"bob", `"mancante documentazione"`, nil},
		{"bob", "-crash is:closed", app(2, 4)},
		{"bob", "crash is:closed", app(3)},
		{"bob", "crash label:bug", app(1)},
		{"bob", "crash futura", nil},
	}
	for _, c := range cases {
		t.Run(c.user+"_"+c.q, func(t *testing.T) {
			if got := e.search(c.user, c.q); !slices.Equal(got, c.want) {
				t.Fatalf("q=%q: %v, voluto %v", c.q, got, c.want)
			}
		})
	}

	t.Run("filtri_espliciti_del_repo_si_sommano_a_q", func(t *testing.T) {
		get := func(qs string) []string {
			rec := e.do(http.MethodGet, "/repos/alice/app/issues?"+qs, "bob", "")
			e.want(rec, http.StatusOK, "")
			return searchKeys(t, rec.Body, false, "alice/app")
		}
		if got := get("assignee=@me"); !slices.Equal(got, app(1)) {
			t.Fatalf("assignee=@me: %v", got)
		}
		if got := get("state=all&assignee=none"); !slices.Equal(got, app(2, 3)) {
			t.Fatalf("assignee=none: %v", got)
		}
		if got := get("state=all&milestone=none"); !slices.Equal(got, app(2, 3, 4)) {
			t.Fatalf("milestone=none: %v", got)
		}
		if got := get("state=all&author=carol&q=crash"); !slices.Equal(got, app(1, 3)) {
			t.Fatalf("author+q: %v", got)
		}
		if got := get("q=is:closed"); !slices.Equal(got, app(2, 3, 4)) {
			t.Fatalf("q=is:closed con state di default: %v", got)
		}
		if got := get(""); !slices.Equal(got, app(1)) {
			t.Fatalf("default open: %v", got)
		}
	})

	t.Run("ordinamento_e_paginazione", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/search/issues?q=repo:alice/app+is:closed&sort=comments&perPage=2", "bob", "")
		e.want(rec, http.StatusOK, "")
		var l openapi.IssueSearchResultList
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		if l.Total != 3 || len(l.Items) != 2 || l.Page != 1 || l.PerPage != 2 || l.Items[0].Issue.Number != 2 {
			t.Fatalf("pagina inattesa: %+v", l)
		}
		rec = e.do(http.MethodGet, "/search/issues?q=repo:alice/app+is:closed&sort=comments&perPage=2&page=2", "bob", "")
		e.want(rec, http.StatusOK, "")
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Items) != 1 || l.Total != 3 {
			t.Fatalf("seconda pagina: %v %+v", err, l)
		}
		rec = e.do(http.MethodGet, "/search/issues?q=crash&sort=relevance", "bob", "")
		e.want(rec, http.StatusOK, "")
		e.want(e.do(http.MethodGet, "/search/issues?sort=relevance", "bob", ""), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodGet, "/search/issues?q=http://x", "bob", ""), http.StatusUnprocessableEntity, "validation_failed")
		e.want(e.do(http.MethodGet, "/search/issues?page=0", "bob", ""), http.StatusBadRequest, "invalid_page")
		e.want(e.do(http.MethodGet, "/search/issues?perPage=101", "bob", ""), http.StatusBadRequest, "invalid_per_page")
		rec = e.do(http.MethodGet, "/search/issues?q=repo:alice/app&sort=updated", "bob", "")
		e.want(rec, http.StatusOK, "")
	})
}

func TestIssuesSearch_VisibilitaEPermessi(t *testing.T) {
	e, _ := newSearchEnv(t)
	secretID := e.repo("secret", false)
	e.open("secret", "alice", "Segreto privato crash")
	rec := e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"tools","visibility":"private"}`)
	e.want(rec, http.StatusCreated, "")
	e.want(e.do(http.MethodPost, "/repos/acme/tools/issues", "alice", `{"title":"Strumento crash"}`), http.StatusCreated, "")
	rec = e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"open","visibility":"internal"}`)
	e.want(rec, http.StatusCreated, "")
	e.want(e.do(http.MethodPost, "/repos/acme/open/issues", "carol", `{"title":"Interno crash"}`), http.StatusCreated, "")
	_ = secretID

	t.Run("chi_non_legge_non_vede_privati", func(t *testing.T) {
		for _, u := range []string{"bob", "carol"} {
			for _, q := range []string{"", "crash", "repo:alice/secret", "org:acme", "is:open", "author:alice", "Segreto privato"} {
				for _, k := range e.global(u, q) {
					if k == "alice/secret#1" || k == "acme/tools#1" {
						t.Fatalf("%s con q=%q vede %s di un repo privato", u, q, k)
					}
				}
			}
		}
		if got := e.global("carol", "repo:alice/secret"); len(got) != 0 {
			t.Fatalf("repo privato: %v", got)
		}
		if got := e.global("carol", "org:acme"); !slices.Equal(got, []string{"acme/open#1"}) {
			t.Fatalf("org:acme come carol = %v (solo il repo interno)", got)
		}
	})
	t.Run("repo_interno_visibile_a_tutti_gli_utenti", func(t *testing.T) {
		if got := e.global("carol", "crash"); !slices.Equal(got, []string{"acme/open#1", "alice/app#1", "alice/app#3"}) {
			t.Fatalf("carol crash = %v", got)
		}
	})
	t.Run("il_proprietario_vede_anche_i_privati_e_le_nascoste", func(t *testing.T) {
		if got := e.global("alice", "crash"); !slices.Equal(got, []string{"acme/open#1", "acme/tools#1", "alice/app#1", "alice/app#3", "alice/secret#1"}) {
			t.Fatalf("alice crash = %v", got)
		}
		if got := e.global("alice", "segreto interno"); !slices.Equal(got, app(5)) {
			t.Fatalf("alice vede la nascosta: %v", got)
		}
	})
	t.Run("nascoste_escluse_per_chi_non_ha_admin", func(t *testing.T) {
		for _, u := range []string{"bob", "carol"} {
			if got := e.global(u, "segreto interno"); len(got) != 0 {
				t.Fatalf("%s vede una issue nascosta: %v", u, got)
			}
			rec := e.do(http.MethodGet, "/repos/alice/app/issues?state=all&q=interno", u, "")
			e.want(rec, http.StatusOK, "")
			if got := searchKeys(t, rec.Body, false, "x"); len(got) != 0 {
				t.Fatalf("%s vede una nascosta nel repo: %v", u, got)
			}
		}
	})
	t.Run("repo_non_leggibile_nel_repo_e_404", func(t *testing.T) {
		e.want(e.do(http.MethodGet, "/repos/alice/secret/issues", "carol", ""), http.StatusNotFound, "not_found")
	})
	t.Run("amministratore_dell_installazione_vede_tutto", func(t *testing.T) {
		e.id.sysAdmin[bobID] = true
		defer delete(e.id.sysAdmin, bobID)
		if got := e.global("bob", "crash"); len(got) != 5 {
			t.Fatalf("sysadmin crash = %v", got)
		}
		if got := e.global("bob", "segreto"); len(got) != 2 {
			t.Fatalf("sysadmin segreto = %v", got)
		}
	})
	t.Run("repo_eliminato_escluso", func(t *testing.T) {
		e.sql(`UPDATE core.repositories SET deleted_at = now() WHERE name = 'open'`)
		if got := e.global("carol", "org:acme"); len(got) != 0 {
			t.Fatalf("repo eliminato: %v", got)
		}
	})
	t.Run("senza_accesso_a_nessun_repo", func(t *testing.T) {
		e.sql(`UPDATE core.repositories SET deleted_at = NULL WHERE name = 'open'`)
		e.id.mu.Lock()
		e.id.attrs = map[uuid.UUID]attr{}
		e.id.mu.Unlock()
		if got := e.global("carol", ""); len(got) != 0 {
			t.Fatalf("nessun repo leggibile: %v", got)
		}
	})
}

func TestIssuesSearch_TestoMaliziosoETrattatoComeTesto(t *testing.T) {
	e, appID := newSearchEnv(t)
	evil := []string{
		`'; DROP TABLE core.issues; --`,
		`" OR 1=1 --`,
		`foo & !bar | (baz) <-> qux`,
		`%_\ \\ ' " ; :* ((`,
		`$1 $2 ${x} {{x}} \x00`,
		`label:"'; DROP TABLE core.labels; --"`,
		`assignee:"x' OR '1'='1"`,
		`milestone:"1; DELETE FROM core.issues"`,
		`repo:"a'/b'" org:"';--"`,
		`-"'; --"`,
		"a\x00b",
		`ünï cödé 日本語 😀`,
	}
	for i, q := range evil {
		t.Run(fmt.Sprintf("q%d", i), func(t *testing.T) {
			for _, u := range []string{"/search/issues?q=", "/repos/alice/app/issues?state=all&q="} {
				rec := e.do(http.MethodGet, u+url.QueryEscape(q), "bob", "")
				if rec.Code != http.StatusOK && rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("%q: stato %d %s", q, rec.Code, rec.Body.String())
				}
				if rec.Code == http.StatusOK && len(searchKeys(t, rec.Body, u[1] == 's', "alice/app")) == 4 && !strings.HasPrefix(q, `-`) {
					t.Fatalf("%q ha restituito tutto: il testo è stato interpretato", q)
				}
			}
		})
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.issues WHERE repo_id = $1`, appID).Scan(&n); err != nil || n != 5 {
		t.Fatalf("le issues sono %d (%v), volute 5: iniezione riuscita?", n, err)
	}

	// Un titolo con SQL si trova come testo.
	e.open("app", "carol", `Prova '; DROP TABLE core.issues; -- fine`)
	if got := e.search("bob", `"DROP TABLE"`); !slices.Equal(got, app(6)) {
		t.Fatalf("testo SQL nel titolo: %v", got)
	}
	if got := e.search("bob", `fine`); !slices.Equal(got, app(6)) {
		t.Fatalf("fine: %v", got)
	}
	// Operatori tsquery nel testo non agiscono: «crash | idea» cerca le parole.
	if got := e.search("bob", `crash|idea`); len(got) > 2 {
		t.Fatalf("operatore tsquery interpretato: %v", got)
	}
	// Anche i parametri espliciti.
	rec := e.do(http.MethodGet, "/repos/alice/app/issues?state=all&labels="+url.QueryEscape("x'; DROP TABLE core.issues;--")+"&assignee="+url.QueryEscape("a' OR 1=1"), "bob", "")
	if rec.Code != http.StatusOK && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("parametri espliciti: %d %s", rec.Code, rec.Body.String())
	}
	if got := searchKeys(t, rec.Body, false, "x"); rec.Code == http.StatusOK && len(got) != 0 {
		t.Fatalf("parametri espliciti: %v", got)
	}
}

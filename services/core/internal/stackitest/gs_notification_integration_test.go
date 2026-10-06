//go:build integration

package stackitest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// M-07/H (GIT-169): `gs notification` (ciclo C4 degli agenti) e `gs api`
// (amministrazione, G4) sullo stack completo, con il binario gs vero.
// Utenti e repo sono quelli di newIssuesE2E.

// process elabora l'outbox di core in notifiche (nello stack di produzione lo
// fa il processo core).
func (g *gsEnv) process() {
	g.t.Helper()
	for i := 0; i < 20; i++ {
		n, err := g.notifier.Process(context.Background())
		if err != nil {
			g.t.Fatalf("notify.Process: %v", err)
		}
		if n == 0 {
			return
		}
	}
	g.t.Fatal("notify.Process non finisce")
}

type gsNote struct {
	ID         string `json:"id"`
	Reason     string `json:"reason"`
	Read       bool   `json:"read"`
	URL        string `json:"url"`
	Repository struct {
		FullName string `json:"fullName"`
	} `json:"repository"`
	Issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"issue"`
}

func (g *gsEnv) notes(token string, args ...string) []gsNote {
	g.t.Helper()
	r := g.gsToken(token, "", "", append([]string{"notification", "list", "--json", "id,reason,read,url,repository,issue"}, args...)...)
	if r.code != 0 {
		g.t.Fatalf("notification list %v: exit %d: %s", args, r.code, r.errOut)
	}
	var ns []gsNote
	if err := json.Unmarshal([]byte(r.out), &ns); err != nil {
		g.t.Fatalf("JSON non valido: %v: %s", err, r.out)
	}
	return ns
}

func TestGsNotification(t *testing.T) {
	g := newGsEnv(t)
	I, P := e2eInternal, e2ePrivate
	bob := g.tokens["bob"]

	// bob: assegnato a #1 (interno) e menzionato da alice; assegnato a #1 del privato.
	n1 := int(g.open("alice", I, "Da fare per bob", "x", map[string]any{"assignees": []string{"bob"}}))
	g.must("alice", "POST", issuePath(I, n1, "/comments"), map[string]any{"body": "@bob guarda qui"}, 201)
	p1 := int(g.open("alice", P, "Riservata a bob", "y", map[string]any{"assignees": []string{"bob"}}))
	g.process()

	t.Run("list_default_filtri_e_json", func(t *testing.T) {
		all := g.notes(bob)
		if len(all) < 3 {
			t.Fatalf("notifiche non lette di bob: %+v", all)
		}
		reasons := map[string]bool{}
		for _, n := range all {
			if n.Read {
				t.Errorf("letta fra le non lette: %+v", n)
			}
			reasons[n.Reason] = true
			if n.Repository.FullName == "" || n.Issue.Number == 0 || !strings.HasPrefix(n.URL, g.proxy+"/alice/") || !strings.Contains(n.URL, "/issues/") {
				t.Errorf("repo, issue o url mancanti: %+v", n)
			}
		}
		if !reasons["assigned"] || !reasons["mentioned"] {
			t.Errorf("motivi: %v", reasons)
		}
		// stessi dati dell'API
		if tot := g.must("bob", "GET", "/notifications", nil, 200).json()["total"]; tot != float64(len(all)) {
			t.Errorf("gs %d notifiche, API %v", len(all), tot)
		}
		// filtro per motivo e per repo
		for _, n := range g.notes(bob, "--reason", "mentioned") {
			if n.Reason != "mentioned" {
				t.Errorf("--reason: %+v", n)
			}
		}
		if len(g.notes(bob, "--reason", "mentioned")) == 0 {
			t.Error("--reason mentioned: nessuna")
		}
		inI := g.notes(bob, "-R", "alice/"+I)
		inP := g.notes(bob, "-R", "alice/"+P)
		if len(inP) != 1 || inP[0].Issue.Number != p1 || len(inI)+len(inP) != len(all) {
			t.Errorf("-R: interno %+v, privato %+v", inI, inP)
		}
		// tabella senza TTY e uso errato
		r := g.gsToken(bob, "", "", "notification", "list", "--reason", "assigned")
		if r.code != 0 || !strings.Contains(r.out, "\tassigned\talice/") {
			t.Errorf("tabella: %+v", r)
		}
		if r := g.gsToken(bob, "", "", "notification", "list", "--reason", "boh"); r.code != 2 {
			t.Errorf("--reason boh: exit %d", r.code)
		}
		// un'altra persona non vede le notifiche di bob
		if n := g.notes(g.tokens["carol"]); len(n) != 0 {
			t.Errorf("carol vede %+v", n)
		}
	})

	t.Run("view", func(t *testing.T) {
		n := g.notes(bob, "-R", "alice/"+P)[0]
		r := g.gsToken(bob, "", "", "notification", "view", n.ID)
		for _, want := range []string{"Motivo: assigned", fmt.Sprintf("Issue: #%d Riservata a bob (aperta)", p1), "Repo: alice/" + P, "/alice/" + P + fmt.Sprintf("/issues/%d", p1)} {
			if r.code != 0 || !strings.Contains(r.out, want) {
				t.Errorf("view senza %q: %+v", want, r)
			}
		}
		// view non segna come letta
		if len(g.notes(bob, "-R", "alice/"+P)) != 1 {
			t.Error("view ha segnato la notifica come letta")
		}
		if q := g.gsToken(bob, "", "", "notification", "view", n.ID, "--jq", ".issue.number"); strings.TrimSpace(q.out) != fmt.Sprint(p1) {
			t.Errorf("--jq: %+v", q)
		}
		// la notifica di bob per altri è inesistente (6); id non valido è un uso errato (2)
		if r := g.gsToken(g.tokens["carol"], "", "", "notification", "view", n.ID); r.code != 6 {
			t.Errorf("view altrui: exit %d", r.code)
		}
		if r := g.gsToken(bob, "", "", "notification", "view", "xx"); r.code != 2 {
			t.Errorf("view xx: exit %d", r.code)
		}
	})

	t.Run("read", func(t *testing.T) {
		before := g.notes(bob)
		one := g.notes(bob, "-R", "alice/"+P)[0]
		if r := g.gsToken(bob, "", "", "notification", "read", one.ID); r.code != 0 {
			t.Fatalf("read: %+v", r)
		}
		if len(g.notes(bob)) != len(before)-1 || len(g.notes(bob, "-R", "alice/"+P)) != 0 {
			t.Errorf("la letta è ancora fra le non lette")
		}
		withRead := g.notes(bob, "--all")
		found := false
		for _, n := range withRead {
			if n.ID == one.ID {
				found = n.Read
			}
		}
		if !found || len(withRead) != len(before) {
			t.Errorf("--all: %+v", withRead)
		}
		if r := g.gsToken(g.tokens["carol"], "", "", "notification", "read", one.ID); r.code != 6 {
			t.Errorf("read altrui: exit %d", r.code)
		}
		if r := g.gsToken(bob, "", "", "notification", "read"); r.code != 2 {
			t.Errorf("read senza id: exit %d", r.code)
		}
		// --all con filtro per motivo: segna solo quelle
		r := g.gsToken(bob, "", "", "notification", "read", "--all", "--reason", "mentioned", "--json", "marked")
		if r.code != 0 || !strings.Contains(r.out, `"marked":1`) {
			t.Errorf("read --all --reason: %+v", r)
		}
		for _, n := range g.notes(bob) {
			if n.Reason == "mentioned" {
				t.Errorf("mentioned ancora non letta: %+v", n)
			}
		}
		// --all: tutte
		if r := g.gsToken(bob, "", "", "notification", "read", "--all"); r.code != 0 || !strings.Contains(r.out, "notifiche segnate come lette") {
			t.Errorf("read --all: %+v", r)
		}
		if n := g.notes(bob); len(n) != 0 {
			t.Errorf("restano non lette: %+v", n)
		}
		// gs api: PATCH con un booleano tipizzato (-F read=false) la rimette fra le non lette
		if r := g.gsToken(bob, "", "", "api", "-X", "PATCH", "/notifications/"+one.ID, "-F", "read=false", "--jq", ".read"); r.code != 0 || strings.TrimSpace(r.out) != "false" {
			t.Errorf("api PATCH read=false: %+v", r)
		}
		if n := g.notes(bob); len(n) != 1 || n[0].ID != one.ID {
			t.Errorf("dopo read=false: %+v", n)
		}
	})

	t.Run("agente_C4", func(t *testing.T) {
		// G4: l'admin crea il token dell'agente con `gs api` (gli agenti non hanno token nell'helper).
		admin := g.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": newPassword}, nil, nil)
		want(t, admin, 200, "")
		exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
		at := g.gw("POST", "/user/tokens", map[string]any{"name": "admin-cli", "scopes": []string{"read:user", "write:user"}, "expiresAt": exp}, nil, g.session(admin))
		want(t, at, 201, "")
		adminTok, _ := at.json()["token"].(string)
		r := g.gsToken(adminTok, "", "", "api", "-X", "POST", "/users/botty/tokens", "-f", "name=ciclo-c4",
			"-F", "scopes[]=read:resource", "-F", "scopes[]=write:resource", "-f", "expiresAt="+exp, "--jq", ".token")
		bot := strings.TrimSpace(r.out)
		if r.code != 0 || !strings.HasPrefix(bot, "gst_") {
			t.Fatalf("token dell'agente: %+v", r)
		}

		// alice assegna #2 a botty e lo cita in un commento; il ciclo: list, view, read.
		n2 := int(g.open("alice", I, "Per l'agente", "z", map[string]any{"assignees": []string{"botty"}}))
		g.must("alice", "POST", issuePath(I, n2, "/comments"), map[string]any{"body": "ciao @botty"}, 201)
		g.process()
		list := g.notes(bot, "-R", "alice/"+I)
		if len(list) < 2 {
			t.Fatalf("notifiche dell'agente: %+v", list)
		}
		var assigned gsNote
		for _, n := range list {
			if n.Reason == "assigned" {
				assigned = n
			}
		}
		if assigned.ID == "" || assigned.Issue.Number != n2 {
			t.Fatalf("assegnazione: %+v", list)
		}
		v := g.gsToken(bot, "", "", "notification", "view", assigned.ID, "--jq", ".url")
		if strings.TrimSpace(v.out) != g.proxy+"/alice/"+I+fmt.Sprintf("/issues/%d", n2) {
			t.Errorf("view: %+v", v)
		}
		if r := g.gsToken(bot, "", "", "notification", "read", assigned.ID); r.code != 0 {
			t.Errorf("read: %+v", r)
		}
		for _, n := range g.notes(bot) {
			if n.ID == assigned.ID {
				t.Errorf("ancora non letta")
			}
		}
		// la casella dell'agente non è quella di bob (solo la notifica rimessa non letta sopra)
		if n := g.notes(bob); len(n) != 1 || n[0].Issue.Number == n2 && n[0].Repository.FullName == "alice/"+I {
			t.Errorf("bob vede %+v", n)
		}
	})
}

func TestGsApi(t *testing.T) {
	g := newGsEnv(t)
	I := e2eInternal
	api := func(user string, args ...string) gsRun {
		g.t.Helper()
		return g.gsToken(g.tokens[user], "", "", append([]string{"api"}, args...)...)
	}

	t.Run("get_e_jq", func(t *testing.T) {
		r := api("alice", "/auth/session", "--jq", ".user.username")
		if r.code != 0 || strings.TrimSpace(r.out) != "alice" {
			t.Errorf("%+v", r)
		}
		// senza slash iniziale e con il prefisso /api/v1: stessa chiamata
		for _, p := range []string{"auth/session", "/api/v1/auth/session"} {
			if r := api("alice", p, "--jq", ".user.username"); strings.TrimSpace(r.out) != "alice" {
				t.Errorf("%s: %+v", p, r)
			}
		}
		// senza --jq il corpo è quello dell'API
		var s map[string]any
		if r := api("alice", "/auth/session"); json.Unmarshal([]byte(r.out), &s) != nil || s["authMethod"] != "token" {
			t.Errorf("corpo: %+v", r)
		}
		// -i stampa lo stato
		if r := api("alice", "-i", "/auth/session"); !strings.HasPrefix(r.out, "HTTP/1.1 200 OK") {
			t.Errorf("-i: %.60q", r.out)
		}
		// -H arriva al server (Accept non JSON: il gateway risponde comunque 200)
		if r := api("alice", "/auth/session", "-H", "X-Prova: 1"); r.code != 0 {
			t.Errorf("-H: %+v", r)
		}
	})

	t.Run("post_con_campi", func(t *testing.T) {
		g.createRepo("alice", "api-prova", "internal")
		// -f testo, POST implicito; il corpo è JSON
		r := api("alice", "/repos/alice/api-prova/labels", "-f", "name=da-gs", "-f", "color=1d76db", "--jq", ".name")
		if r.code != 0 || strings.TrimSpace(r.out) != "da-gs" {
			t.Fatalf("POST -f: %+v", r)
		}
		if ls := g.must("alice", "GET", "/repos/alice/api-prova/labels", nil, 200); !strings.Contains(string(ls.body), "da-gs") {
			t.Errorf("etichetta non creata: %s", ls.body)
		}
		// -X PATCH con un campo di testo
		g.open("alice", "api-prova", "Una", "uno", nil)
		r = api("alice", "-X", "PATCH", "/repos/alice/api-prova/issues/1", "-f", "title=Rinominata")
		if r.code != 0 {
			t.Fatalf("PATCH: %+v", r)
		}
		if is := g.must("alice", "GET", "/repos/alice/api-prova/issues/1", nil, 200).json(); is["title"] != "Rinominata" {
			t.Errorf("issue: %v", is)
		}
		// un valore di -F che non è del tipo atteso arriva come tale: l'API lo rifiuta (400/422), exit 1
		if r := api("alice", "-X", "PATCH", "/repos/alice/api-prova/issues/1", "-F", "title=true"); r.code != 1 || !strings.Contains(r.out, "bad_request") {
			t.Errorf("tipo sbagliato: %+v", r)
		}
		// corpo da stdin con --input e valore da stdin con @-
		r = g.gsToken(g.tokens["alice"], "", `{"name":"da-stdin","color":"d93f0b"}`, "api", "/repos/alice/api-prova/labels", "--input", "-", "--jq", ".name")
		if r.code != 0 || strings.TrimSpace(r.out) != "da-stdin" {
			t.Errorf("--input: %+v", r)
		}
		r = g.gsToken(g.tokens["alice"], "", "testo da **stdin**\n", "api", "/repos/alice/api-prova/issues", "-f", "title=Da file", "-F", "body=@-", "--jq", ".body")
		if r.code != 0 || strings.TrimSpace(r.out) != "testo da **stdin**" {
			t.Errorf("@-: %+v", r)
		}
	})

	t.Run("paginate", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			g.open("alice", "api-prova", fmt.Sprintf("Paginata %d", i), "x", nil)
		}
		total := int(g.must("alice", "GET", "/repos/alice/api-prova/issues?state=all", nil, 200).json()["total"].(float64))
		if total < 6 {
			t.Fatalf("issue: %d", total)
		}
		// perPage=2: più pagine, un solo array con tutti gli elementi, senza doppioni
		r := api("alice", "/repos/alice/api-prova/issues", "-X", "GET", "-f", "state=all", "-F", "perPage=2", "--paginate", "--jq", "[.[].number] | sort | unique | length")
		if r.code != 0 || strings.TrimSpace(r.out) != fmt.Sprint(total) {
			t.Errorf("--paginate: atteso %d, %+v", total, r)
		}
		r = api("alice", "/repos/alice/api-prova/issues?state=all&perPage=2", "--paginate")
		var items []map[string]any
		if json.Unmarshal([]byte(r.out), &items) != nil || len(items) != total {
			t.Errorf("array unito: %d elementi, attesi %d", len(items), total)
		}
		// senza --paginate: solo la prima pagina
		r = api("alice", "/repos/alice/api-prova/issues?state=all&perPage=2", "--jq", ".items | length")
		if strings.TrimSpace(r.out) != "2" {
			t.Errorf("prima pagina: %+v", r)
		}
		if r := api("alice", "-X", "POST", "/repos/alice/api-prova/issues", "--paginate"); r.code != 2 {
			t.Errorf("--paginate con POST: exit %d", r.code)
		}
	})

	t.Run("errori", func(t *testing.T) {
		check := func(name string, r gsRun, code int, apiCode string) {
			t.Helper()
			var body struct {
				Error struct{ Code, Message string }
			}
			if r.code != code || json.Unmarshal([]byte(r.out), &body) != nil || (apiCode != "" && body.Error.Code != apiCode) || body.Error.Code == "" || !strings.HasPrefix(r.errOut, "gs: ") {
				t.Errorf("%s: exit %d (atteso %d), corpo %q (atteso codice %s), stderr %q", name, r.code, code, r.out, apiCode, r.errOut)
			}
		}
		check("404", api("alice", "/repos/alice/non-esiste/issues"), 6, "not_found")
		// carol (read) non può creare etichette
		check("403", api("carol", "/repos/alice/"+I+"/labels", "-f", "name=x", "-f", "color=1d76db"), 5, "")
		// token inventato
		r := g.gsToken("gst_inventato0000000000000000000000000000", "", "", "api", "/auth/session")
		if r.code != 4 {
			t.Errorf("401: %+v", r)
		}
		// in modalità --jq l'errore su stderr è JSON e stdout porta il corpo
		r = api("alice", "/repos/alice/non-esiste/issues", "--jq", ".")
		var e struct{ Error struct{ Code string } }
		if r.code != 6 || json.Unmarshal([]byte(r.errOut), &e) != nil || e.Error.Code != "not_found" || !strings.Contains(r.out, "not_found") {
			t.Errorf("--jq: %+v", r)
		}
		// uso errato: senza percorso, indirizzo completo
		for _, args := range [][]string{{}, {"https://altro.example.com/x"}, {"/x", "-f", "senzaValore"}} {
			if r := api("alice", args...); r.code != 2 {
				t.Errorf("%v: exit %d", args, r.code)
			}
		}
	})
}

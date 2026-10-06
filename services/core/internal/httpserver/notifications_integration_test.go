//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// M-06/E (GIT-133): motore delle notifiche in-app. Regole C3 (partecipanti,
// Watch, nessuna notifica per le proprie azioni), C4 (agenti), C9
// (conservazione, perdita d'accesso) e I8 (menzioni rispettose della
// visibilità). alice = admin dei suoi repo; bob, dave e bot hanno write dove
// serve; carol legge i repo internal.

const daveID = "eeeeeeee-0000-0000-0000-000000000007"

// notifyIdentity aggiunge al fake di identity la revoca dell'accesso a un
// repo, i team (`org/team` → nomi utente) e la risoluzione delle menzioni.
type notifyIdentity struct {
	*issuesIdentity
	rmu     sync.Mutex
	revoked map[uuid.UUID]map[string]bool
	teams   map[string][]string
	calls   int
}

func (f *notifyIdentity) revoke(repo uuid.UUID, user string) {
	f.rmu.Lock()
	defer f.rmu.Unlock()
	if f.revoked[repo] == nil {
		f.revoked[repo] = map[string]bool{}
	}
	f.revoked[repo][users[user]] = true
}

func (f *notifyIdentity) HasRole(ctx context.Context, userID, resourceID uuid.UUID, role string) (bool, error) {
	f.rmu.Lock()
	gone := f.revoked[resourceID][userID.String()]
	f.rmu.Unlock()
	if gone {
		return false, nil
	}
	return f.issuesIdentity.HasRole(ctx, userID, resourceID, role)
}

func kindOf(name string) string {
	if strings.HasPrefix(name, "bot") {
		return "agent"
	}
	return "human"
}

func (f *notifyIdentity) ResolveMentions(_ context.Context, names []string) (identityclient.Mentions, error) {
	f.rmu.Lock()
	f.calls++
	f.rmu.Unlock()
	out := identityclient.Mentions{Users: map[string]identityclient.CodeUser{}, Teams: map[string][]identityclient.CodeUser{}}
	cu := func(n string) (identityclient.CodeUser, bool) {
		id, ok := users[n]
		if !ok {
			return identityclient.CodeUser{}, false
		}
		return identityclient.CodeUser{ID: uuid.MustParse(id), Username: n, Kind: kindOf(n)}, true
	}
	for _, n := range names {
		if members, ok := f.teams[n]; ok {
			list := []identityclient.CodeUser{}
			for _, m := range members {
				if u, ok := cu(m); ok {
					list = append(list, u)
				}
			}
			out.Teams[n] = list
			continue
		}
		if u, ok := cu(n); ok {
			out.Users[n] = u
		}
	}
	return out, nil
}

type notifyEnv struct {
	*issuesEnv
	nid *notifyIdentity
	eng *notify.Engine
}

func newNotifyEnv(t *testing.T) *notifyEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	base := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	users["bot"], users["dave"] = botID, daveID
	base.owners["bot"] = ownerUser(botID, "bot")
	base.owners["dave"] = ownerUser(daveID, "dave")
	nid := &notifyIdentity{issuesIdentity: base, revoked: map[uuid.UUID]map[string]bool{}, teams: map[string][]string{}}
	e := &issuesEnv{t: t, pool: pool, id: base}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(nid), httpserver.WithReadableLister(nid), httpserver.WithGit(newFakeGit()),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	return &notifyEnv{issuesEnv: e, nid: nid, eng: &notify.Engine{Pool: pool, Identity: nid}}
}

// process elabora tutti gli eventi in coda.
func (e *notifyEnv) process() {
	e.t.Helper()
	for i := 0; i < 20; i++ {
		n, err := e.eng.Process(context.Background())
		if err != nil {
			e.t.Fatalf("Process: %v", err)
		}
		if n == 0 {
			return
		}
	}
	e.t.Fatal("Process non finisce")
}

func (e *notifyEnv) clear() {
	e.t.Helper()
	e.process()
	e.sql(`DELETE FROM core.notifications`)
}

func userName(id uuid.UUID) string {
	for n, v := range users {
		if v == id.String() {
			return n
		}
	}
	return id.String()
}

// got elabora gli eventi e ritorna le notifiche scritte come
// "utente:motivo", ordinate.
func (e *notifyEnv) got() []string {
	e.t.Helper()
	e.process()
	rows, err := e.pool.Query(context.Background(), `SELECT user_id, reason FROM core.notifications`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u uuid.UUID
		var reason string
		if err := rows.Scan(&u, &reason); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, userName(u)+":"+reason)
	}
	sort.Strings(out)
	return out
}

// expect confronta got() con quanto atteso e svuota la casella.
func (e *notifyEnv) expect(label string, want ...string) {
	e.t.Helper()
	got := e.got()
	sort.Strings(want)
	if !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
		e.t.Fatalf("%s: notifiche = %v, volute %v", label, got, want)
	}
	e.sql(`DELETE FROM core.notifications`)
}

func (e *notifyEnv) openWith(repo, user, title, body string) int64 {
	e.t.Helper()
	b, _ := json.Marshal(map[string]string{"title": title, "body": body})
	rec := e.do(http.MethodPost, "/repos/alice/"+repo+"/issues", user, string(b))
	e.want(rec, http.StatusCreated, "")
	return e.issue(rec).Number
}

func (e *notifyEnv) say(repo string, n int64, user, body string) string {
	e.t.Helper()
	b, _ := json.Marshal(map[string]string{"body": body})
	rec := e.do(http.MethodPost, fmt.Sprintf("/repos/alice/%s/issues/%d/comments", repo, n), user, string(b))
	e.want(rec, http.StatusCreated, "")
	var c openapi.IssueComment
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		e.t.Fatal(err)
	}
	return c.Id.String()
}

func (e *notifyEnv) inbox(user, query string) openapi.NotificationList {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/notifications"+query, user, "")
	e.want(rec, http.StatusOK, "")
	return decodeInto[openapi.NotificationList](e.t, rec)
}

func (e *notifyEnv) grantAdmin(repo uuid.UUID, user string) {
	e.id.mu.Lock()
	defer e.id.mu.Unlock()
	e.id.grants[repo] = append(e.id.grants[repo], users[user])
}

func TestNotifiche_PartecipantiEMotiviC3(t *testing.T) {
	e := newNotifyEnv(t)
	repoID := e.repo("app", true)
	for _, u := range []string{"bob", "dave"} {
		e.id.grantWrite(repoID, u)
	}

	// Aprire la issue: l'autore non riceve niente per la propria azione.
	n := e.openWith("app", "bob", "Crash", "testo senza menzioni")
	e.expect("apertura")
	p := fmt.Sprintf("/repos/alice/app/issues/%d", n)

	// Un commento notifica l'autore (partecipa); chi commenta no.
	e.say("app", n, "carol", "ci succede anche a me")
	e.expect("commento di carol", "bob:participating")

	// Assegnazione: l'assegnatario riceve «assigned», gli altri niente.
	e.want(e.do(http.MethodPut, p+"/assignees", "alice", `{"assignees":["dave"]}`), http.StatusOK, "")
	e.expect("assegnazione", "dave:assigned")

	// Il commento di un assegnatario arriva ad autore e commentatori, non a lui.
	e.say("app", n, "dave", "ci guardo io")
	e.expect("commento di dave", "bob:participating", "carol:participating")

	// Chiusura e riapertura: state_change a chi segue, non a chi la fa.
	e.want(e.do(http.MethodPost, p+"/close", "alice", `{}`), http.StatusOK, "")
	e.expect("chiusura", "bob:state_change", "carol:state_change", "dave:state_change")
	e.want(e.do(http.MethodPost, p+"/reopen", "bob", ``), http.StatusOK, "")
	e.expect("riapertura", "carol:state_change", "dave:state_change")

	// Chi si assegna da solo non si notifica (C3).
	e.want(e.do(http.MethodPut, p+"/assignees", "dave", `{"assignees":["dave"]}`), http.StatusOK, "")
	e.expect("autoassegnazione")
}

func TestNotifiche_SubscribeUnsubscribe(t *testing.T) {
	e := newNotifyEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.openWith("app", "bob", "Da seguire", "testo")
	sub := fmt.Sprintf("/repos/alice/app/issues/%d/subscription", n)
	state := func(user string) openapi.IssueSubscription {
		return decodeInto[openapi.IssueSubscription](t, e.do(http.MethodGet, sub, user, ""))
	}

	// L'autore segue già, prima ancora che il motore giri; carol no.
	if s := state("bob"); !s.Subscribed || s.Reason != openapi.IssueSubscriptionReasonAuthor {
		t.Fatalf("autore = %+v", s)
	}
	if s := state("carol"); s.Subscribed || s.Reason != openapi.IssueSubscriptionReasonNone {
		t.Fatalf("carol = %+v", s)
	}
	e.process()

	// Unsubscribe: l'iscrizione automatica non vale più per questa issue.
	e.want(e.do(http.MethodDelete, sub, "bob", ""), http.StatusOK, "")
	if s := state("bob"); s.Subscribed {
		t.Fatalf("dopo Unsubscribe = %+v", s)
	}
	e.say("app", n, "carol", "primo")
	e.expect("bob disiscritto")

	// Ma una menzione diretta notifica comunque.
	e.say("app", n, "carol", "ehi @bob")
	e.expect("menzione dopo Unsubscribe", "bob:mentioned")

	// Subscribe manuale (anche chi non c'entra): motivo subscribed.
	e.want(e.do(http.MethodPut, sub, "bob", ""), http.StatusOK, "")
	e.want(e.do(http.MethodPut, sub, "dave", ""), http.StatusOK, "")
	if s := state("dave"); !s.Subscribed || s.Reason != openapi.IssueSubscriptionReasonManual {
		t.Fatalf("dave = %+v", s)
	}
	e.say("app", n, "carol", "secondo")
	e.expect("dopo Subscribe", "bob:subscribed", "dave:subscribed")

	// Idempotenti, e il repo che non si legge risponde 404.
	e.want(e.do(http.MethodPut, sub, "dave", ""), http.StatusOK, "")
	e.want(e.do(http.MethodDelete, sub, "dave", ""), http.StatusOK, "")
	e.want(e.do(http.MethodDelete, sub, "dave", ""), http.StatusOK, "")
	e.repo("segreto", false)
	e.want(e.do(http.MethodGet, "/repos/alice/segreto/issues/1/subscription", "dave", ""), http.StatusNotFound, "")
}

func TestNotifiche_WatchDelRepoC3(t *testing.T) {
	e := newNotifyEnv(t)
	repoID := e.repo("app", true)
	for _, u := range []string{"bob", "carol", "dave"} {
		e.id.grantWrite(repoID, u)
	}
	watch := func(user, mode string) {
		t.Helper()
		e.want(e.do(http.MethodPut, "/repos/alice/app/watch", user, fmt.Sprintf(`{"mode":%q}`, mode)), http.StatusOK, "")
	}

	// Participating è il default.
	rec := e.do(http.MethodGet, "/repos/alice/app/watch", "dave", "")
	e.want(rec, http.StatusOK, "")
	if w := decodeInto[openapi.RepoWatch](t, rec); w.Mode != "participating" || w.UpdatedAt != nil {
		t.Fatalf("default = %+v", w)
	}

	// All: ogni nuova issue e ogni commento, anche senza partecipare.
	watch("dave", "all")
	n := e.openWith("app", "bob", "Nuova", "testo")
	e.expect("issue nuova con All", "dave:subscribed")
	e.say("app", n, "carol", "commento")
	e.expect("commento con All", "bob:participating", "dave:subscribed")

	// Chi è coinvolto ha il motivo più forte, non due notifiche.
	e.say("app", n, "dave", "intervengo")
	e.expect("dave commenta", "bob:participating", "carol:participating")
	e.say("app", n, "bob", "grazie")
	e.expect("bob commenta", "carol:participating", "dave:participating")

	// Ignore: niente, nemmeno menzioni, assegnazioni e issue seguite.
	watch("carol", "ignore")
	e.say("app", n, "bob", "@carol guarda")
	e.expect("menzione con Ignore", "dave:participating")
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/assignees", n), "alice", `{"assignees":["carol"]}`), http.StatusOK, "")
	e.expect("assegnazione con Ignore")
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/close", n), "alice", `{}`), http.StatusOK, "")
	e.expect("chiusura con Ignore", "bob:state_change", "dave:state_change")
	e.openWith("app", "bob", "Un'altra", "@carol")
	e.expect("issue con Ignore", "dave:subscribed")

	// Tornare a Participating (PUT o DELETE) riattiva le notifiche.
	e.want(e.do(http.MethodDelete, "/repos/alice/app/watch", "carol", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, "/repos/alice/app/watch", "carol", ""), http.StatusNoContent, "")
	e.openWith("app", "bob", "Ancora", "@carol")
	e.expect("dopo il Reset", "carol:mentioned", "dave:subscribed")
	watch("dave", "participating")
	if w := decodeInto[openapi.RepoWatch](t, e.do(http.MethodGet, "/repos/alice/app/watch", "dave", "")); w.Mode != "participating" {
		t.Fatalf("watch = %+v", w)
	}

	// Modo sconosciuto: 422; repo che non si legge: 404.
	e.want(e.do(http.MethodPut, "/repos/alice/app/watch", "dave", `{"mode":"tutto"}`), http.StatusUnprocessableEntity, "mode")
	e.repo("segreto", false)
	e.want(e.do(http.MethodGet, "/repos/alice/segreto/watch", "dave", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodPut, "/repos/alice/segreto/watch", "dave", `{"mode":"all"}`), http.StatusNotFound, "")
}

func TestNotifiche_NessunaNotificaPerLeProprieAzioniAncheViaToken(t *testing.T) {
	e := newNotifyEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.openWith("app", "bob", "Mia", "testo")
	e.say("app", n, "carol", "primo")
	e.expect("setup", "bob:participating")
	p := fmt.Sprintf("/repos/alice/app/issues/%d", n)
	tok := uuid.New()

	// bob agisce con un suo token: commento, menzione di sé, chiusura.
	e.want(e.doWithToken(http.MethodPost, p+"/comments", "bob", `{"body":"da token, @bob incluso"}`, tok, "ci"), http.StatusCreated, "")
	e.expect("commento via token", "carol:participating")
	e.want(e.doWithToken(http.MethodPost, p+"/close", "bob", `{}`, tok, "ci"), http.StatusOK, "")
	e.expect("chiusura via token", "carol:state_change")
	e.want(e.doWithToken(http.MethodPut, p+"/assignees", "bob", `{"assignees":["bob"]}`, tok, "ci"), http.StatusOK, "")
	e.expect("autoassegnazione via token")
}

func TestNotifiche_MenzioniEVisibilitaI8(t *testing.T) {
	e := newNotifyEnv(t)
	secret := e.repo("segreto", false) // solo alice e chi ha un grant
	e.id.grantWrite(secret, "bob")
	e.id.grantWrite(secret, "bot")
	e.nid.teams["acme/devs"] = []string{"bob", "dave", "bot"}
	// dave e carol non vedono il repo.

	// Utenti, agenti e team: notificano solo chi vede il repo; il resto è testo.
	n := e.openWith("segreto", "alice", "Riservata",
		"@bob @bot @dave @carol @acme/devs @nessuno @acme/ignoto `@carol` e mario@example.com")
	e.expect("menzioni nell'apertura", "bob:mentioned", "bot:mentioned")
	for _, u := range []string{"dave", "carol"} {
		var cnt int
		if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.issue_subscriptions WHERE user_id = $1`, users[u]).Scan(&cnt); err != nil || cnt != 0 {
			t.Fatalf("%s non vede il repo: nessuna iscrizione, ottenuti %d (%v)", u, cnt, err)
		}
	}
	// La menzione non concede accesso: dave non legge la issue.
	e.want(e.do(http.MethodGet, fmt.Sprintf("/repos/alice/segreto/issues/%d", n), "dave", ""), http.StatusNotFound, "")

	// Il codice non menziona; una modifica notifica solo i menzionati nuovi.
	e.say("segreto", n, "alice", "```\n@bob\n```")
	e.expect("menzione nel codice: solo chi segue, nessun mentioned", "bob:participating", "bot:participating")
	p := fmt.Sprintf("/repos/alice/segreto/issues/%d", n)
	e.want(e.do(http.MethodPatch, p, "alice", `{"body":"@bob @bot e adesso anche @acme/devs"}`), http.StatusOK, "")
	e.expect("modifica senza menzionati nuovi")
	e.want(e.do(http.MethodPatch, p, "alice", `{"body":"@bob @bot @acme/devs @carol"}`), http.StatusOK, "")
	e.expect("modifica col solo carol nuovo (non legge)")

	// Un commento menziona e iscrive: il menzionato segue anche dopo.
	cid := e.say("segreto", n, "alice", "@bot serve il tuo parere")
	e.expect("commento", "bot:mentioned", "bob:participating")
	patch := fmt.Sprintf("%s/comments/%s", p, cid)
	e.want(e.do(http.MethodPatch, patch, "alice", `{"body":"@bot serve il tuo parere, @bob anche"}`), http.StatusOK, "")
	e.expect("commento modificato: solo bob è nuovo", "bob:mentioned")

	// Quando dave ottiene l'accesso, una nuova menzione lo raggiunge.
	e.id.grantWrite(secret, "dave")
	e.say("segreto", n, "alice", "ora puoi vederlo, @dave")
	e.expect("dave con accesso", "bob:participating", "bot:participating", "dave:mentioned")
}

func TestNotifiche_AgentiLeggonoLaCasellaViaApiC4(t *testing.T) {
	e := newNotifyEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bot")
	e.id.grantWrite(repoID, "bob")
	n := e.openWith("app", "bob", "Per il bot", "ciao @bot")
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/assignees", n), "bob", `{"assignees":["bot"]}`), http.StatusOK, "")
	e.say("app", n, "carol", "commento")
	e.process()

	// Stessa casella e stesse regole delle persone: l'agente vede le sue.
	all := e.inbox("bot", "")
	if all.Total != 3 || all.UnreadCount != 3 {
		t.Fatalf("casella del bot = %+v", all)
	}
	reasons := map[string]bool{}
	for _, it := range all.Items {
		reasons[string(it.Reason)] = true
		if it.Repository == nil || it.Repository.FullName != "alice/app" || it.Issue == nil || it.Issue.Number != n {
			t.Fatalf("notifica senza repo o issue: %+v", it)
		}
		if it.Summary == "" {
			t.Fatalf("summary vuoto: %+v", it)
		}
	}
	if !reasons["mentioned"] || !reasons["assigned"] || !reasons["participating"] {
		t.Fatalf("motivi = %v", reasons)
	}
	// Filtro per motivo (gs notification list --json --reason).
	if l := e.inbox("bot", "?reason=assigned,mentioned"); l.Total != 2 || l.UnreadCount != 2 {
		t.Fatalf("filtro per motivo = %+v", l)
	}
	e.want(e.do(http.MethodGet, "/notifications?reason=boh", "bot", ""), http.StatusBadRequest, "invalid_reason")
	// Le notifiche del bot non sono di bob, e viceversa.
	for _, it := range e.inbox("bob", "").Items {
		if it.Reason == "assigned" {
			t.Fatalf("bob vede l'assegnazione del bot: %+v", it)
		}
	}
	e.want(e.do(http.MethodGet, "/notifications/"+all.Items[0].Id.String(), "bob", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, "/notifications/"+all.Items[0].Id.String(), "bob", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodPatch, "/notifications/"+all.Items[0].Id.String(), "bob", `{"read":true}`), http.StatusNotFound, "")
}

func TestNotifiche_CasellaApi(t *testing.T) {
	e := newNotifyEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	other := e.repo("altro", true)
	e.id.grantWrite(other, "bob")
	n := e.openWith("app", "bob", "Uno", "x")
	m := e.openWith("altro", "bob", "Due", "x")
	for i := 0; i < 3; i++ {
		e.say("app", n, "carol", fmt.Sprintf("c%d", i))
	}
	e.say("altro", m, "carol", "altro repo")
	e.process()

	all := e.inbox("bob", "?perPage=2")
	if all.Total != 4 || all.UnreadCount != 4 || len(all.Items) != 2 || all.PerPage != 2 {
		t.Fatalf("pagina 1 = %+v", all)
	}
	// Ordine: dalla più recente.
	if all.Items[0].CreatedAt.Before(all.Items[1].CreatedAt) {
		t.Fatal("ordine per data decrescente")
	}
	if l := e.inbox("bob", "?perPage=2&page=2"); len(l.Items) != 2 || l.Items[0].Id == all.Items[0].Id {
		t.Fatalf("pagina 2 = %+v", l)
	}
	if l := e.inbox("bob", "?repo=alice/altro"); l.Total != 1 || l.UnreadCount != 1 || l.Items[0].Repository.FullName != "alice/altro" {
		t.Fatalf("filtro repo = %+v", l)
	}
	e.want(e.do(http.MethodGet, "/notifications?repo=senzaslash", "bob", ""), http.StatusBadRequest, "invalid_repo")
	e.want(e.do(http.MethodGet, "/notifications?page=0", "bob", ""), http.StatusBadRequest, "invalid_page")

	// Lettura, archivio, idempotenza.
	first := e.inbox("bob", "?repo=alice/altro").Items[0].Id.String()
	rec := e.do(http.MethodPatch, "/notifications/"+first, "bob", `{"read":true}`)
	e.want(rec, http.StatusOK, "")
	read := decodeInto[openapi.Notification](t, rec)
	if !read.Read || read.ReadAt == nil {
		t.Fatalf("letta = %+v", read)
	}
	again := decodeInto[openapi.Notification](t, e.do(http.MethodPatch, "/notifications/"+first, "bob", `{"read":true}`))
	if !again.ReadAt.Equal(*read.ReadAt) {
		t.Fatal("PATCH read idempotente: readAt non cambia")
	}
	if l := e.inbox("bob", ""); l.Total != 3 || l.UnreadCount != 3 {
		t.Fatalf("non lette = %+v", l)
	}
	if l := e.inbox("bob", "?state=read"); l.Total != 1 || l.UnreadCount != 3 {
		t.Fatalf("lette = %+v", l)
	}
	second := e.inbox("bob", "?repo=alice/app").Items[0].Id.String()
	e.want(e.do(http.MethodPatch, "/notifications/"+second, "bob", `{"archived":true}`), http.StatusOK, "")
	if l := e.inbox("bob", ""); l.Total != 2 || l.UnreadCount != 2 {
		t.Fatalf("senza l'archiviata = %+v", l)
	}
	if l := e.inbox("bob", "?state=archived"); l.Total != 1 {
		t.Fatalf("archiviate = %+v", l)
	}
	if l := e.inbox("bob", "?state=all"); l.Total != 4 {
		t.Fatalf("tutte = %+v", l)
	}
	e.want(e.do(http.MethodPatch, "/notifications/"+second, "bob", `{"archived":false,"read":false}`), http.StatusOK, "")
	e.want(e.do(http.MethodPatch, "/notifications/"+second, "bob", `{}`), http.StatusBadRequest, "bad_request")
	e.want(e.do(http.MethodGet, "/notifications/"+uuid.NewString(), "bob", ""), http.StatusNotFound, "")

	// Segna tutte come lette, con filtro e senza.
	mark := decodeInto[openapi.MarkedNotifications](t, e.do(http.MethodPost, "/notifications/read-all?repo=alice/app", "bob", ""))
	if mark.Marked != 3 {
		t.Fatalf("read-all con repo = %+v", mark)
	}
	if mark = decodeInto[openapi.MarkedNotifications](t, e.do(http.MethodPost, "/notifications/read-all", "bob", "")); mark.Marked != 0 {
		t.Fatalf("read-all idempotente = %+v", mark)
	}

	// Eliminazione: in blocco (solo lette/archiviate) e singola.
	e.want(e.do(http.MethodDelete, "/notifications", "bob", ""), http.StatusBadRequest, "")
	e.want(e.do(http.MethodDelete, "/notifications?state=unread", "bob", ""), http.StatusBadRequest, "")
	del := decodeInto[openapi.DeletedNotifications](t, e.do(http.MethodDelete, "/notifications?state=read&repo=alice/altro", "bob", ""))
	if del.Deleted != 1 {
		t.Fatalf("eliminate con repo = %+v", del)
	}
	e.want(e.do(http.MethodPatch, "/notifications/"+second, "bob", `{"archived":true}`), http.StatusOK, "")
	if del = decodeInto[openapi.DeletedNotifications](t, e.do(http.MethodDelete, "/notifications?state=archived", "bob", "")); del.Deleted != 1 {
		t.Fatalf("eliminate archiviate = %+v", del)
	}
	e.want(e.do(http.MethodDelete, "/notifications/"+second, "bob", ""), http.StatusNotFound, "")
	rest := e.inbox("bob", "?state=all")
	if rest.Total != 2 {
		t.Fatalf("rimaste = %+v", rest)
	}
	one := rest.Items[0].Id.String()
	e.want(e.do(http.MethodDelete, "/notifications/"+one, "bob", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, "/notifications/"+one, "bob", ""), http.StatusNotFound, "")
	if l := e.inbox("bob", "?state=all"); l.Total != 1 {
		t.Fatalf("rimasta = %+v", l)
	}
}

func TestNotifiche_PerditaDiAccessoC9(t *testing.T) {
	e := newNotifyEnv(t)
	secret := e.repo("segreto", false)
	e.id.grantWrite(secret, "bob")
	e.id.grantWrite(secret, "dave")
	pub := e.repo("app", true)
	e.id.grantWrite(pub, "bob")
	n := e.openWith("segreto", "alice", "Titolo riservato", "@bob @dave")
	e.openWith("app", "alice", "Pubblica", "@bob")
	e.say("segreto", n, "dave", "risposta")
	e.process()
	if l := e.inbox("bob", ""); l.Total != 3 {
		t.Fatalf("prima = %+v", l)
	}
	id := e.inbox("bob", "?repo=alice/segreto").Items[0].Id.String()

	// bob perde l'accesso: non vede più quelle notifiche (né titolo né repo),
	// le sue righe si eliminano; le altre restano; dave non è toccato.
	e.nid.revoke(secret, "bob")
	l := e.inbox("bob", "?state=all")
	if l.Total != 1 || l.Items[0].Repository.FullName != "alice/app" {
		t.Fatalf("dopo la revoca = %+v", l)
	}
	for _, it := range l.Items {
		if strings.Contains(it.Summary, "riservato") {
			t.Fatalf("il titolo del repo non leggibile è nel summary: %+v", it)
		}
	}
	e.want(e.do(http.MethodGet, "/notifications/"+id, "bob", ""), http.StatusNotFound, "")
	var left int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.notifications WHERE user_id = $1 AND repo_id = $2`, users["bob"], secret).Scan(&left); err != nil || left != 0 {
		t.Fatalf("righe rimaste di bob = %d (%v)", left, err)
	}
	if l := e.inbox("dave", ""); l.Total != 1 {
		t.Fatalf("dave = %+v", l)
	}
	// E non ne arrivano di nuove: la menzione di bob resta testo.
	e.clear()
	e.say("segreto", n, "alice", "@bob ancora?")
	e.expect("menzione dopo la revoca", "dave:participating")
}

func TestNotifiche_IssueNascostaSoloAdminI4(t *testing.T) {
	e := newNotifyEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	e.id.grantWrite(repoID, "dave")
	e.grantAdmin(repoID, "dave")
	n := e.openWith("app", "bob", "Da nascondere", "x")
	e.say("app", n, "carol", "carol segue")
	e.say("app", n, "dave", "dave segue")
	e.clear()
	e.say("app", n, "carol", "visibile a tutti")
	e.process()
	if l := e.inbox("bob", ""); l.Total != 1 {
		t.Fatalf("bob prima = %+v", l)
	}
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/hidden", n), "alice", `{"hidden":true}`), http.StatusOK, "")
	e.clear()
	e.say("app", n, "alice", "nascosta: solo admin")
	e.expect("commento su issue nascosta", "dave:participating")
	// Le notifiche già arrivate a chi non è admin non si mostrano più.
	e.sql(`INSERT INTO core.notifications (id, user_id, reason, repo_id, issue_id, event_name)
		SELECT gen_random_uuid(), $1, 'participating', repo_id, id, 'issue_comment.created' FROM core.issues WHERE number = $2`, users["bob"], n)
	if l := e.inbox("bob", "?state=all"); l.Total != 0 {
		t.Fatalf("bob vede notifiche di una issue nascosta: %+v", l)
	}
	e.sql(`INSERT INTO core.notifications (id, user_id, reason, repo_id, issue_id, event_name)
		SELECT gen_random_uuid(), $1, 'participating', repo_id, id, 'issue_comment.created' FROM core.issues WHERE number = $2`, users["dave"], n)
	if l := e.inbox("dave", "?state=all"); l.Total != 1 {
		t.Fatalf("dave (admin) = %+v", l)
	}
}

func TestNotifiche_ConservazioneC9(t *testing.T) {
	e := newNotifyEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.openWith("app", "bob", "Tanti", "x")
	for i := 0; i < 4; i++ {
		e.say("app", n, "carol", fmt.Sprintf("c%d", i))
	}
	e.process()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	ids := e.inbox("bob", "").Items
	if len(ids) != 4 {
		t.Fatalf("notifiche = %d", len(ids))
	}
	// 0: letta 91 giorni fa (da eliminare); 1: letta 89 giorni fa (resta);
	// 2: non letta vecchia di 200 giorni (resta); 3: letta 91 giorni fa e
	// archiviata (si elimina: conta la lettura).
	set := func(i int, readAt *time.Time, created time.Time, archived bool) {
		var arch *time.Time
		if archived {
			arch = &now
		}
		e.sql(`UPDATE core.notifications SET read_at = $2, created_at = $3, archived_at = $4 WHERE id = $1`, ids[i].Id, readAt, created, arch)
	}
	d := func(days float64) time.Time { return now.Add(-time.Duration(days * 24 * float64(time.Hour))) }
	r91, r89 := d(91), d(89)
	set(0, &r91, d(100), false)
	set(1, &r89, d(100), false)
	set(2, nil, d(200), false)
	set(3, &r91, d(100), true)

	eng := &notify.Engine{Pool: e.pool, Identity: e.nid}
	deleted, err := eng.PurgeRead(context.Background(), now)
	if err != nil || deleted != 2 {
		t.Fatalf("PurgeRead = %d, %v, volute 2", deleted, err)
	}
	left := e.inbox("bob", "?state=all")
	got := map[string]bool{}
	for _, it := range left.Items {
		got[it.Id.String()] = true
	}
	if left.Total != 2 || !got[ids[1].Id.String()] || !got[ids[2].Id.String()] {
		t.Fatalf("rimaste = %+v", left.Items)
	}
	// Idempotente; e con un orologio più avanti scade anche la letta di 89 giorni.
	if deleted, err = eng.PurgeRead(context.Background(), now); err != nil || deleted != 0 {
		t.Fatalf("secondo giro = %d, %v", deleted, err)
	}
	if deleted, err = eng.PurgeRead(context.Background(), now.Add(2*24*time.Hour)); err != nil || deleted != 1 {
		t.Fatalf("due giorni dopo = %d, %v, volute 1", deleted, err)
	}
	// Mai le non lette, per quanto vecchie.
	if deleted, err = eng.PurgeRead(context.Background(), now.AddDate(5, 0, 0)); err != nil || deleted != 0 {
		t.Fatalf("anni dopo = %d, %v", deleted, err)
	}
	if l := e.inbox("bob", ""); l.Total != 1 {
		t.Fatalf("la non letta resta: %+v", l)
	}
	// La conservazione è configurabile e il lettore Run la usa col suo orologio.
	e.sql(`UPDATE core.notifications SET read_at = $1`, now.Add(-time.Hour))
	short := &notify.Engine{Pool: e.pool, Identity: e.nid, ReadRetention: 30 * time.Minute}
	if deleted, err = short.PurgeRead(context.Background(), now); err != nil || deleted != 1 {
		t.Fatalf("conservazione corta = %d, %v", deleted, err)
	}
}

func TestNotifiche_ElaborazioneIdempotenteEOrdine(t *testing.T) {
	e := newNotifyEnv(t)
	repoID := e.repo("app", true)
	e.id.grantWrite(repoID, "bob")
	n := e.openWith("app", "bob", "Una", "x")
	e.say("app", n, "carol", "uno")
	cnt, err := e.eng.Process(context.Background())
	if err != nil || cnt == 0 {
		t.Fatalf("Process = %d, %v", cnt, err)
	}
	before := e.got()
	if cnt, err = e.eng.Process(context.Background()); err != nil || cnt != 0 {
		t.Fatalf("secondo giro = %d, %v: gli eventi non si rielaborano", cnt, err)
	}
	if after := e.got(); !slices.Equal(before, after) {
		t.Fatalf("notifiche doppie: %v -> %v", before, after)
	}
	var pending int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.event_outbox WHERE notified_at IS NULL`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("eventi non letti = %d (%v)", pending, err)
	}
	// Un evento che si conferma in ritardo (seq più bassa) si trova lo stesso.
	e.sql(`INSERT INTO core.event_outbox (id, name, version, payload) VALUES (gen_random_uuid(), 'repository.created', 1, '{}')`)
	if cnt, err = e.eng.Process(context.Background()); err != nil || cnt != 1 {
		t.Fatalf("evento estraneo = %d, %v: va segnato e ignorato", cnt, err)
	}
}

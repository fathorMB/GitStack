//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"testing"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
)

// GIT-265 (v1.1, PR8 e PR11): ambiente comune dei test di chiusura delle
// issues al merge, notifiche e webhook delle PR. Git è finto (mergeGit: branch,
// confronto, conflitti, merge), identity finta con team, revoche e menzioni
// (notifyIdentity), i due motori (notifiche e webhook) girano a mano sullo
// stesso outbox e il ricevitore dei webhook è un server HTTP finto.

type pevEnv struct {
	*mergeEnv
	nid    *notifyIdentity
	hid    *hookIdentity
	eng    *notify.Engine
	whEng  *webhooks.Engine
	clock  *hookClock
	recv   *receiver
	push   func(ctx context.Context, env pkgevents.Envelope, p gitpush.Payload) error
	opts   []httpserver.Option
	pubKey *webhooks.Keyring
}

func newPevEnv(t *testing.T) *pevEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	pg := &prGit{fakeGit: newFakeGit(), br: map[uuid.UUID]map[string]string{}}
	mg := &mergeGit{prGit: pg, conflicts: map[string][]string{}, behind: map[string]int{}}
	base := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	users["bot"], users["dave"] = botID, daveID
	base.owners["bot"] = ownerUser(botID, "bot")
	base.owners["dave"] = ownerUser(daveID, "dave")
	nid := &notifyIdentity{issuesIdentity: base, revoked: map[uuid.UUID]map[string]bool{}, teams: map[string][]string{}}
	hid := &hookIdentity{issuesIdentity: base}
	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	_, check, err := webhooks.NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts := []httpserver.Option{
		httpserver.WithRepoIdentity(nid), httpserver.WithReadableLister(nid), httpserver.WithGit(mg),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys, CheckURL: check}), httpserver.WithEmail(true),
	}
	e := &issuesEnv{t: t, pool: pool, id: base}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret, opts...)
	recv := newReceiver(t)
	u, _ := url.Parse(recv.srv.URL)
	clock := &hookClock{t: time.Now().UTC().Add(time.Hour).Truncate(time.Second)}
	pe := &pevEnv{
		mergeEnv: &mergeEnv{pullsEnv: &pullsEnv{issuesEnv: e, git: pg}, mg: mg},
		nid:      nid, hid: hid, clock: clock, recv: recv, opts: opts, pubKey: keys,
		eng:   &notify.Engine{Pool: pool, Identity: nid},
		whEng: &webhooks.Engine{Pool: pool, Keys: keys, HTTP: &toServer{base: u}, Users: nid, Managers: hid, Now: clock.Now},
		push:  httpserver.NewPullPushHandler(pool, events.NoopPublisher{}, opts...),
	}
	return pe
}

// process elabora gli eventi in coda con il motore delle notifiche.
func (e *pevEnv) process() {
	e.t.Helper()
	for i := 0; i < 30; i++ {
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

// got elabora gli eventi e ritorna le notifiche scritte come
// "utente:motivo", ordinate.
func (e *pevEnv) got() []string {
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
func (e *pevEnv) expect(label string, want ...string) {
	e.t.Helper()
	got := e.got()
	sort.Strings(want)
	if !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
		rows, _ := e.pool.Query(context.Background(), `SELECT user_id, reason, event_name FROM core.notifications ORDER BY created_at`)
		var detail []string
		for rows.Next() {
			var u uuid.UUID
			var r, ev string
			_ = rows.Scan(&u, &r, &ev)
			detail = append(detail, userName(u)+":"+r+"<"+ev+">")
		}
		rows.Close()
		e.t.Fatalf("%s: notifiche = %v, volute %v (%v)", label, got, want, detail)
	}
	e.sql(`DELETE FROM core.notifications`)
}

// openPR apre una PR con campi in più (JSON, con la virgola davanti) e ne ritorna il numero.
func (e *pevEnv) openPR(user, source, extra string) int64 {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos/alice/app/pulls", user, fmt.Sprintf(`{"title":"PR %s","sourceBranch":%q%s}`, source, source, extra))
	e.want(rec, http.StatusCreated, "")
	return e.pr(rec).Number
}

func (e *pevEnv) pull(n int64) string { return fmt.Sprintf("/repos/alice/app/pulls/%d", n) }

// run crea le consegne dei webhook dagli eventi e invia quelle scadute.
func (e *pevEnv) run() int {
	e.t.Helper()
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		n, err := e.whEng.ProcessOutbox(ctx)
		if err != nil {
			e.t.Fatalf("ProcessOutbox: %v", err)
		}
		if n == 0 {
			break
		}
	}
	total := 0
	for i := 0; i < 30; i++ {
		n, err := e.whEng.DeliverDue(ctx)
		if err != nil {
			e.t.Fatalf("DeliverDue: %v", err)
		}
		total += n
		if n == 0 {
			break
		}
	}
	return total
}

// pushed simula l'evento git.push di un branch di bob verso il consumer delle PR.
func (e *pevEnv) pushed(repoID uuid.UUID, branch, before, after string) {
	e.t.Helper()
	p := gitpush.Payload{
		Repo:   gitpush.Repo{ID: repoID.String(), FullName: "alice/app", DefaultBranch: "main"},
		Pusher: gitpush.Pusher{ID: users["bob"], Username: "bob", Type: gitpush.PusherHuman},
		Refs:   []gitpush.RefPush{{Ref: "refs/heads/" + branch, Before: before, After: after, IsDefaultBranch: branch == "main"}},
	}
	if err := e.push(context.Background(), pkgevents.Envelope{}, p); err != nil {
		e.t.Fatalf("consumer delle PR: %v", err)
	}
}

func (e *pevEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// strictDecode decodifica rifiutando i campi che lo schema non ha
// (additionalProperties: false in tutto il contratto delle consegne).
func strictDecode(t *testing.T, raw []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("payload fuori contratto: %v\n%s", err, raw)
	}
}

// needKeys controlla i campi `required` dello schema (il tipo generato non
// distingue un campo assente da uno vuoto).
func needKeys(t *testing.T, label string, raw []byte, keys ...string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Fatalf("%s: manca il campo obbligatorio %q: %s", label, k, raw)
		}
	}
	return m
}

var (
	hookCommonKeys = []string{"version", "event", "action", "time", "repository", "sender"}
	pullFullKeys   = []string{"id", "number", "title", "body", "state", "draft", "author", "sourceBranch", "targetBranch", "headSha",
		"requestedReviewers", "reviewDecision", "approvals", "requiredApprovals", "commentCount", "unresolvedThreadCount",
		"closingIssues", "commitCount", "changedFiles", "additions", "deletions", "sourceBranchExists", "createdAt", "updatedAt"}
	pullSummaryKeys = []string{"number", "title", "state", "draft", "author", "sourceBranch", "targetBranch", "reviewDecision",
		"requestedReviewers", "commentCount", "createdAt", "updatedAt"}
	reviewKeys = []string{"id", "author", "state", "body", "commitSha", "stale", "countsAsApproval", "commentCount", "submittedAt"}
	statusKeys = []string{"id", "sha", "state", "context", "creator", "createdAt"}
)

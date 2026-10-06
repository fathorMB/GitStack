//go:build integration

package issuelinks_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	pkggitpush "github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/issuelinks"
	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// M-06/C (GIT-131): commit collegati e chiusura con fixes #n (C1, C2) con NATS
// e Postgres reali. Il push passa da JetStream e dal consumer durevole
// "core-issue-linker", come in produzione.

// Utenti: alice (admin di app e other, write su priv), bob (solo read su app
// e other), carol (read su app, nessun accesso a priv), dave (read su app e
// priv: vede entrambi).
var users = map[string]uuid.UUID{
	"alice": uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"),
	"bob":   uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002"),
	"carol": uuid.MustParse("cccccccc-0000-0000-0000-000000000003"),
	"dave":  uuid.MustParse("dddddddd-0000-0000-0000-000000000004"),
}

var roleRank = map[string]int{"read": 1, "write": 2, "admin": 3}

type fakeIdentity struct {
	mu    sync.Mutex
	roles map[uuid.UUID]map[uuid.UUID]string // repo -> user -> ruolo
}

func (f *fakeIdentity) set(repo, user uuid.UUID, role string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.roles[repo] == nil {
		f.roles[repo] = map[uuid.UUID]string{}
	}
	f.roles[repo][user] = role
}

func (f *fakeIdentity) HasRole(_ context.Context, user, repo uuid.UUID, role string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	have, ok := f.roles[repo][user]
	return ok && roleRank[have] >= roleRank[role], nil
}

func (f *fakeIdentity) ResolveMentions(context.Context, []string) (identityclient.Mentions, error) {
	return identityclient.Mentions{Users: map[string]identityclient.CodeUser{}, Teams: map[string][]identityclient.CodeUser{}}, nil
}

// fakeGit risponde alle letture "commits" del servizio git con uno storico
// fisso (dal più recente), a pagine di perPage.
type fakeGit struct {
	mu      sync.Mutex
	history []pkggitpush.Commit
	reads   int
}

func (g *fakeGit) ReadJSON(_ context.Context, _ trust.Identity, _ uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reads++
	if path != "commits" {
		return nil, fmt.Errorf("lettura inattesa %s", path)
	}
	var page, per int
	_, _ = fmt.Sscan(q.Get("page"), &page)
	_, _ = fmt.Sscan(q.Get("perPage"), &per)
	from, to := (page-1)*per, page*per
	if from > len(g.history) {
		from = len(g.history)
	}
	more := to < len(g.history)
	if to > len(g.history) {
		to = len(g.history)
	}
	items := []map[string]any{}
	for _, c := range g.history[from:to] {
		items = append(items, map[string]any{"sha": c.SHA, "subject": strings.SplitN(c.Message, "\n", 2)[0], "message": c.Message,
			"author": c.Author, "committer": c.Committer})
	}
	return json.Marshal(map[string]any{"items": items, "page": page, "perPage": per, "hasMore": more})
}

func (g *fakeGit) OpenStream(context.Context, trust.Identity, uuid.UUID, string, url.Values) (*gitclient.Stream, error) {
	return nil, fmt.Errorf("non usato")
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	js   jetstream.JetStream
	id   *fakeIdentity
	git  *fakeGit
	eng  *notify.Engine

	app, other, priv uuid.UUID
}

func startNATS(t *testing.T) jetstream.JetStream {
	t.Helper()
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server non pronto")
	}
	t.Cleanup(srv.Shutdown)
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func (e *env) addRepo(owner, name string, archived bool) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', $2)`, id, owner+"/"+name); err != nil {
		e.t.Fatal(err)
	}
	var arch any
	if archived {
		arch = time.Now()
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name, archived_at)
		VALUES ($1, 'user', $2, $3, $4, $5)`, id, users["alice"], owner, name, arch); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repo_counters (repo_id) VALUES ($1)`, id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) addIssue(repo uuid.UUID, number int) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO core.issues (id, repo_id, number, title, author_id)
		VALUES ($1, $2, $3, $4, $5)`, id, repo, number, fmt.Sprintf("Issue %d", number), users["alice"]); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) subscribe(issue uuid.UUID, user string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO core.issue_subscriptions (issue_id, user_id, reason) VALUES ($1, $2, 'manual')`, issue, users[user]); err != nil {
		e.t.Fatal(err)
	}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	e := &env{t: t, pool: pool, js: startNATS(t), id: &fakeIdentity{roles: map[uuid.UUID]map[uuid.UUID]string{}}, git: &fakeGit{}}
	e.eng = &notify.Engine{Pool: pool, Identity: e.id}
	e.app = e.addRepo("alice", "app", false)
	e.other = e.addRepo("alice", "other", false)
	e.priv = e.addRepo("alice", "priv", false)
	for _, r := range []uuid.UUID{e.app, e.other} {
		e.id.set(r, users["alice"], "admin")
		e.id.set(r, users["bob"], "read")
	}
	e.id.set(e.app, users["carol"], "read")
	e.id.set(e.app, users["dave"], "read")
	e.id.set(e.other, users["dave"], "read")
	e.id.set(e.priv, users["alice"], "write")
	e.id.set(e.priv, users["dave"], "read")
	// alice scrive anche nei repo altrui che le servono.
	e.id.set(e.app, users["alice"], "admin")

	linker := &issuelinks.Linker{Pool: pool, Identity: e.id, Notifier: e.eng, Git: e.git, Log: slog.Default()}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = gitpush.Run(ctx, e.js, gitpush.DurableIssueLinks, linker.HandlePush, slog.Default()) }()
	return e
}

var shaSeq int

func sha() string { shaSeq++; return fmt.Sprintf("%040x", shaSeq) }

func commit(msg string) pkggitpush.Commit {
	return pkggitpush.Commit{SHA: sha(), Message: msg,
		Author:    pkggitpush.Person{Name: "Ada", Email: "ada@example.com", Date: "2026-10-06T10:00:00Z"},
		Committer: pkggitpush.Person{Name: "Ada", Email: "ada@example.com", Date: "2026-10-06T10:00:00Z"}}
}

func ref(branch string, def bool, cs ...pkggitpush.Commit) pkggitpush.RefPush {
	r := pkggitpush.RefPush{Ref: "refs/heads/" + branch, Before: sha(), After: sha(), IsDefaultBranch: def, Commits: cs}
	return r
}

func (e *env) push(repo uuid.UUID, repoName, pusher string, refs ...pkggitpush.RefPush) pkgevents.Envelope {
	e.t.Helper()
	p := pkggitpush.Payload{
		Repo:   pkggitpush.Repo{ID: repo.String(), FullName: repoName, DefaultBranch: "main"},
		Pusher: pkggitpush.Pusher{ID: users[pusher].String(), Username: pusher, Type: "human"},
		Refs:   refs,
	}
	env, err := pkgevents.NewEnvelope(pkggitpush.Name, pkggitpush.Version, p)
	if err != nil {
		e.t.Fatal(err)
	}
	e.publish(env)
	return env
}

func (e *env) publish(env pkgevents.Envelope) {
	e.t.Helper()
	data, _ := env.Marshal()
	// Il consumer assicura lo stream all'avvio: si aspetta che ci sia.
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := e.js.Publish(context.Background(), pkggitpush.Name, data)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("publish: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// settle aspetta che il consumer abbia elaborato i messaggi pubblicati.
func (e *env) settle() {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cons, err := e.js.Consumer(context.Background(), "GIT", gitpush.DurableIssueLinks)
		if err == nil {
			if info, err := cons.Info(context.Background()); err == nil && info.NumPending == 0 && info.NumAckPending == 0 && info.Delivered.Consumer > 0 {
				time.Sleep(200 * time.Millisecond)
				if info, err := cons.Info(context.Background()); err == nil && info.NumAckPending == 0 {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("timeout: il consumer non ha finito")
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) state(issue uuid.UUID) (string, string) {
	e.t.Helper()
	var st string
	var reason *string
	if err := e.pool.QueryRow(context.Background(), `SELECT state, close_reason FROM core.issues WHERE id = $1`, issue).Scan(&st, &reason); err != nil {
		e.t.Fatal(err)
	}
	if reason == nil {
		return st, ""
	}
	return st, *reason
}

func (e *env) events(issue uuid.UUID, typ string) int {
	return e.count(`SELECT count(*) FROM core.issue_events WHERE issue_id = $1 AND type = $2`, issue, typ)
}

func (e *env) links(issue uuid.UUID) int {
	return e.count(`SELECT count(*) FROM core.issue_commit_links WHERE issue_id = $1`, issue)
}

func (e *env) closedOutbox(issue uuid.UUID) int {
	return e.count(`SELECT count(*) FROM core.event_outbox WHERE name = 'issue.closed' AND payload->'issue'->>'id' = $1`, issue.String())
}

func (e *env) process() {
	e.t.Helper()
	for i := 0; i < 10; i++ {
		n, err := e.eng.Process(context.Background())
		if err != nil {
			e.t.Fatalf("Process: %v", err)
		}
		if n == 0 {
			return
		}
	}
}

func (e *env) notifications(user, reason string, issue uuid.UUID) int {
	return e.count(`SELECT count(*) FROM core.notifications WHERE user_id = $1 AND reason = $2 AND issue_id = $3`, users[user], reason, issue)
}

// C2: su un branch diverso dal principale il commit crea solo il
// collegamento, con la notifica agli iscritti (C3) e senza chiudere.
func TestBranchDiversoDalPrincipale_SoloCollegamento(t *testing.T) {
	e := newEnv(t)
	iss := e.addIssue(e.app, 12)
	e.subscribe(iss, "bob")
	e.subscribe(iss, "alice") // chi fa il push non si notifica da solo
	c := commit("Sistema il parser\n\nFixes #12")
	e.push(e.app, "alice/app", "alice", ref("fix-12", false, c))
	e.settle()

	if st, _ := e.state(iss); st != "open" {
		t.Fatalf("stato = %s: un branch non principale non chiude", st)
	}
	if e.links(iss) != 1 || e.events(iss, "commit_linked") != 1 {
		t.Fatalf("link=%d commit_linked=%d", e.links(iss), e.events(iss, "commit_linked"))
	}
	if e.events(iss, "closed_by_commit") != 0 || e.closedOutbox(iss) != 0 {
		t.Fatal("nessuna chiusura attesa")
	}
	var data []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT data FROM core.issue_events WHERE issue_id = $1 AND type = 'commit_linked'`, iss).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Commit struct{ SHA, Repository, Subject, Ref, AuthorName string }
	}
	_ = json.Unmarshal(data, &d)
	if d.Commit.SHA != c.SHA || d.Commit.Repository != "alice/app" || d.Commit.Subject != "Sistema il parser" ||
		d.Commit.Ref != "refs/heads/fix-12" || d.Commit.AuthorName != "Ada" {
		t.Fatalf("dati del collegamento: %s", data)
	}
	if e.notifications("bob", "commit_linked", iss) != 1 || e.notifications("alice", "commit_linked", iss) != 0 {
		t.Fatal("la notifica va a bob, non ad alice che ha fatto il push")
	}
}

// C2: il commit nel branch principale chiude come completata, con la traccia
// "closed by commit", e le notifiche di chiusura (C3) partono dal motore.
func TestBranchPrincipale_Chiude(t *testing.T) {
	e := newEnv(t)
	iss := e.addIssue(e.app, 7)
	e.subscribe(iss, "bob")
	c := commit("Closes #7")
	e.push(e.app, "alice/app", "alice", ref("main", true, c))
	e.settle()

	st, reason := e.state(iss)
	if st != "closed" || reason != "completed" {
		t.Fatalf("stato=%s reason=%s", st, reason)
	}
	if e.events(iss, "closed_by_commit") != 1 || e.events(iss, "closed") != 0 || e.events(iss, "commit_linked") != 1 {
		t.Fatalf("cronologia: closed_by_commit=%d closed=%d linked=%d", e.events(iss, "closed_by_commit"), e.events(iss, "closed"), e.events(iss, "commit_linked"))
	}
	var actor *uuid.UUID
	var data []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT actor_id, data FROM core.issue_events WHERE issue_id = $1 AND type = 'closed_by_commit'`, iss).Scan(&actor, &data); err != nil {
		t.Fatal(err)
	}
	if actor != nil || !strings.Contains(string(data), c.SHA) || !strings.Contains(string(data), `"completed"`) {
		t.Fatalf("closed_by_commit: actor=%v data=%s", actor, data)
	}
	if e.closedOutbox(iss) != 1 {
		t.Fatal("issue.closed non è nell'outbox")
	}
	var commitSHA string
	if err := e.pool.QueryRow(context.Background(), `SELECT payload->'commit'->>'sha' FROM core.event_outbox WHERE name = 'issue.closed'`).Scan(&commitSHA); err != nil || commitSHA != c.SHA {
		t.Fatalf("payload.commit.sha = %q (%v)", commitSHA, err)
	}
	e.process()
	if e.notifications("bob", "state_change", iss) != 1 {
		t.Fatal("bob doveva ricevere la notifica di chiusura")
	}
}

// C2: il commit visto prima su un branch e poi nel branch principale chiude
// solo al secondo push, e una volta sola.
func TestDalBranchAlPrincipale_ChiudeAlloIngresso(t *testing.T) {
	e := newEnv(t)
	iss := e.addIssue(e.app, 3)
	c := commit("fixes #3")
	e.push(e.app, "alice/app", "alice", ref("feature", false, c))
	e.settle()
	if st, _ := e.state(iss); st != "open" {
		t.Fatal("chiusa troppo presto")
	}
	e.push(e.app, "alice/app", "alice", ref("main", true, c))
	e.settle()
	if st, reason := e.state(iss); st != "closed" || reason != "completed" {
		t.Fatalf("stato=%s/%s", st, reason)
	}
	if e.links(iss) != 1 || e.events(iss, "commit_linked") != 1 || e.events(iss, "closed_by_commit") != 1 {
		t.Fatalf("link=%d linked=%d closed=%d", e.links(iss), e.events(iss, "commit_linked"), e.events(iss, "closed_by_commit"))
	}
	if n := e.count(`SELECT count(*) FROM core.issue_commit_links WHERE issue_id = $1 AND on_default_branch AND closed_applied_at IS NOT NULL`, iss); n != 1 {
		t.Fatal("il collegamento doveva risultare nel branch principale e applicato")
	}
}

// C1: owner/repo#n chiude solo se chi ha fatto il push ha write sul repo della
// issue; senza resta solo il collegamento.
func TestAltroRepo_ChiudeSoloConWrite(t *testing.T) {
	e := newEnv(t)
	senza := e.addIssue(e.other, 5) // bob: solo read su other
	con := e.addIssue(e.priv, 6)    // alice: write su priv
	// bob ha write su app (può pushare sul principale) ma solo read su other.
	e.id.set(e.app, users["bob"], "write")
	e.push(e.app, "alice/app", "bob", ref("main", true, commit("Fixes alice/other#5")))
	e.settle()
	if st, _ := e.state(senza); st != "open" {
		t.Fatal("senza write sul repo della issue non si chiude")
	}
	if e.links(senza) != 1 || e.events(senza, "commit_linked") != 1 || e.events(senza, "closed_by_commit") != 0 {
		t.Fatalf("doveva restare il solo collegamento: link=%d", e.links(senza))
	}

	e.push(e.app, "alice/app", "alice", ref("main", true, commit("Resolves alice/priv#6")))
	e.settle()
	if st, reason := e.state(con); st != "closed" || reason != "completed" {
		t.Fatalf("con write si chiude: %s/%s", st, reason)
	}
	// Il payload di issue.closed non porta il repo di un altro repo.
	var has bool
	if err := e.pool.QueryRow(context.Background(), `SELECT payload ? 'commit' FROM core.event_outbox WHERE name = 'issue.closed' AND payload->'issue'->>'id' = $1`, con.String()).Scan(&has); err != nil || has {
		t.Fatalf("commit nel payload di una chiusura cross-repo: %v %v", has, err)
	}
}

// C1: chi non legge il repo della issue non crea nemmeno il collegamento.
func TestAltroRepo_SenzaAccessoNienteCollegamento(t *testing.T) {
	e := newEnv(t)
	iss := e.addIssue(e.priv, 1)
	e.id.set(e.app, users["carol"], "write")
	e.push(e.app, "alice/app", "carol", ref("main", true, commit("Fixes alice/priv#1")))
	e.settle()
	if e.links(iss) != 0 || e.events(iss, "commit_linked") != 0 {
		t.Fatal("carol non vede priv: nessun collegamento")
	}
	if st, _ := e.state(iss); st != "open" {
		t.Fatal("chiusa senza accesso")
	}
}

// C1: la notifica del commit collegato di un altro repo va solo a chi legge
// entrambi i repo.
func TestAltroRepo_NotificaSoloAChiVedeEntrambi(t *testing.T) {
	e := newEnv(t)
	iss := e.addIssue(e.priv, 2)
	e.id.set(e.app, users["alice"], "admin")
	e.subscribe(iss, "dave")  // legge app e priv
	e.subscribe(iss, "carol") // legge app, ma non priv
	e.id.set(e.priv, users["bob"], "read")
	e.subscribe(iss, "bob") // legge priv, non app? bob ha read su app e priv
	// bob perde l'accesso ad app: vede priv ma non il repo del commit.
	delete(e.id.roles[e.app], users["bob"])
	e.push(e.app, "alice/app", "alice", ref("dev", false, commit("Vedi alice/priv#2")))
	e.settle()
	if e.links(iss) != 1 {
		t.Fatalf("links = %d", e.links(iss))
	}
	if e.notifications("dave", "commit_linked", iss) != 1 {
		t.Fatal("dave vede entrambi i repo: notifica attesa")
	}
	if e.notifications("bob", "commit_linked", iss) != 0 || e.notifications("carol", "commit_linked", iss) != 0 {
		t.Fatal("bob e carol non vedono entrambi i repo: nessuna notifica")
	}
}

// C2: una issue riaperta non viene richiusa dallo stesso commit, né se lo
// stesso evento torna, né se il commit rientra da un altro ref.
func TestIssueRiaperta_NonRichiusaDalloStessoCommit(t *testing.T) {
	e := newEnv(t)
	iss := e.addIssue(e.app, 9)
	c := commit("Fixes #9")
	env := e.push(e.app, "alice/app", "alice", ref("main", true, c))
	e.settle()
	if st, _ := e.state(iss); st != "closed" {
		t.Fatal("doveva chiudersi")
	}
	// Riapertura (come ReopenIssue).
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.issues SET state = 'open', close_reason = NULL, closed_at = NULL WHERE id = $1`, iss); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO core.issue_events (id, issue_id, type, actor_id, created_at) VALUES (gen_random_uuid(), $1, 'reopened', $2, clock_timestamp())`, iss, users["alice"]); err != nil {
		t.Fatal(err)
	}
	e.publish(env) // stessa busta riconsegnata
	e.push(e.app, "alice/app", "alice", ref("release", true, c))
	e.settle()
	if st, _ := e.state(iss); st != "open" {
		t.Fatalf("stato = %s: la issue riaperta non va richiusa dallo stesso commit", st)
	}
	if e.events(iss, "closed_by_commit") != 1 {
		t.Fatalf("closed_by_commit = %d", e.events(iss, "closed_by_commit"))
	}
	// Un commit nuovo con fixes la chiude di nuovo.
	e.push(e.app, "alice/app", "alice", ref("main", true, commit("Fixes #9 davvero")))
	e.settle()
	if st, _ := e.state(iss); st != "closed" {
		t.Fatal("un commit nuovo chiude")
	}
}

// C2: riaperta fra il collegamento sul branch e l'ingresso nel principale:
// il commit non la chiude.
func TestRiaperturaTraBranchEPrincipale(t *testing.T) {
	e := newEnv(t)
	iss := e.addIssue(e.app, 4)
	c := commit("Fixes #4")
	e.push(e.app, "alice/app", "alice", ref("feature", false, c))
	e.settle()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO core.issue_events (id, issue_id, type, actor_id, created_at) VALUES (gen_random_uuid(), $1, 'reopened', $2, clock_timestamp())`, iss, users["alice"]); err != nil {
		t.Fatal(err)
	}
	e.push(e.app, "alice/app", "alice", ref("main", true, c))
	e.settle()
	if st, _ := e.state(iss); st != "open" {
		t.Fatal("riaperta dopo il collegamento: resta aperta")
	}
	if e.links(iss) != 1 {
		t.Fatal("il collegamento resta")
	}
}

// R10: su un repo archiviato le issues sono in sola lettura: solo il
// collegamento (anche da un altro repo con write).
func TestRepoArchiviato_SoloCollegamento(t *testing.T) {
	e := newEnv(t)
	arch := e.addRepo("alice", "old", true)
	e.id.set(arch, users["alice"], "admin")
	iss := e.addIssue(arch, 1)
	e.push(e.app, "alice/app", "alice", ref("main", true, commit("Fixes alice/old#1")))
	e.settle()
	if st, _ := e.state(iss); st != "open" {
		t.Fatal("repo archiviato: niente chiusura")
	}
	if e.links(iss) != 1 || e.events(iss, "commit_linked") != 1 || e.events(iss, "closed_by_commit") != 0 {
		t.Fatalf("link=%d", e.links(iss))
	}
}

// C2: se l'elenco dei commit è troncato, before..after si ricostruisce con le
// letture di git: il commit con fixes oltre il limite chiude, quelli già
// presenti prima (before e i precedenti) no.
func TestListaTroncata_RicostruisceBeforeAfter(t *testing.T) {
	e := newEnv(t)
	iss := e.addIssue(e.app, 20)
	vecchio := e.addIssue(e.app, 21)
	// storico dal più recente: 150 commit nuovi, il più vecchio chiude #20;
	// poi before e un commit precedente che cita #21 (non va rielaborato).
	var history []pkggitpush.Commit
	for i := 0; i < 150; i++ {
		msg := fmt.Sprintf("lavoro %d", i)
		if i == 149 {
			msg = "Fixes #20"
		}
		history = append(history, commit(msg))
	}
	before := commit("base")
	history = append(history, before, commit("Fixes #21 (già nel principale)"))
	e.git.history = history

	r := pkggitpush.RefPush{Ref: "refs/heads/main", Before: before.SHA, After: history[0].SHA, IsDefaultBranch: true,
		Commits: history[:pkggitpush.MaxCommits], CommitsTruncated: true}
	e.push(e.app, "alice/app", "alice", r)
	e.settle()
	if st, reason := e.state(iss); st != "closed" || reason != "completed" {
		t.Fatalf("il commit oltre il limite doveva chiudere #20: %s/%s", st, reason)
	}
	if st, _ := e.state(vecchio); st != "open" || e.links(vecchio) != 0 {
		t.Fatal("i commit prima di before non si rielaborano")
	}
	if e.git.reads == 0 {
		t.Fatal("nessuna lettura di git")
	}
}

// Idempotenza: lo stesso evento consegnato due volte non duplica
// collegamenti, cronologia, notifiche né chiusure.
func TestIdempotenza_StessoEventoDueVolte(t *testing.T) {
	e := newEnv(t)
	a := e.addIssue(e.app, 30) // chiusa dal commit
	b := e.addIssue(e.app, 31) // solo citata
	e.subscribe(a, "bob")
	e.subscribe(b, "bob")
	env := e.push(e.app, "alice/app", "alice", ref("main", true, commit("Fixes #30 e vedi #31")))
	e.settle()
	e.publish(env)
	e.publish(env)
	e.settle()
	e.process()
	for _, iss := range []uuid.UUID{a, b} {
		if e.links(iss) != 1 || e.events(iss, "commit_linked") != 1 {
			t.Fatalf("collegamenti duplicati: link=%d linked=%d", e.links(iss), e.events(iss, "commit_linked"))
		}
		if e.notifications("bob", "commit_linked", iss) != 1 {
			t.Fatalf("notifiche commit_linked duplicate: %d", e.notifications("bob", "commit_linked", iss))
		}
	}
	if e.events(a, "closed_by_commit") != 1 || e.closedOutbox(a) != 1 || e.notifications("bob", "state_change", a) != 1 {
		t.Fatalf("chiusura duplicata: events=%d outbox=%d notifiche=%d", e.events(a, "closed_by_commit"), e.closedOutbox(a), e.notifications("bob", "state_change", a))
	}
	if st, _ := e.state(b); st != "open" {
		t.Fatal("#31 è solo citata")
	}
}

//go:build integration

package httpserver_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/coreevents"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
	"github.com/google/uuid"
)

// M-06/B (GIT-130): eventi di dominio di core su NATS, con outbox
// transazionale. NATS è un nats-server v2 reale avviato in-process (come in
// pkg/events), senza Docker; Postgres è quello di dbtest. Ogni evento
// pubblicato viene decodificato con i decoder di pkg/events/coreevents: se il
// payload di core diverge dallo schema registrato, il test fallisce.

// natsHarness è un nats-server con JetStream che si può fermare e riavviare
// sulla stessa porta e sullo stesso storage.
type natsHarness struct {
	t    *testing.T
	dir  string
	port int
	srv  *server.Server
}

func newNATSHarness(t *testing.T) *natsHarness {
	t.Helper()
	h := &natsHarness{t: t, dir: t.TempDir(), port: -1}
	h.start()
	t.Cleanup(h.stop)
	return h
}

func (h *natsHarness) start() {
	h.t.Helper()
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: h.port, JetStream: true, StoreDir: h.dir, NoLog: true, NoSigs: true})
	if err != nil {
		h.t.Fatalf("avvio nats-server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		h.t.Fatal("nats-server non pronto entro 10s")
	}
	h.srv = srv
	h.port = srv.Addr().(*net.TCPAddr).Port // la stessa porta al riavvio
}

func (h *natsHarness) stop() {
	if h.srv != nil {
		h.srv.Shutdown()
		h.srv.WaitForShutdown()
		h.srv = nil
	}
}

func (h *natsHarness) url() string { return fmt.Sprintf("nats://127.0.0.1:%d", h.port) }

// eventsEnv è issuesEnv più NATS reale e il relay dell'outbox.
type eventsEnv struct {
	*issuesEnv
	nats  *natsHarness
	js    jetstream.JetStream
	pub   *events.NATSPublisher
	reg   *pkgevents.Registry
	stop  context.CancelFunc
	relay *outbox.Relay
}

func newEventsEnv(t *testing.T) *eventsEnv {
	t.Helper()
	e := &eventsEnv{issuesEnv: newIssuesEnv(t), nats: newNATSHarness(t), reg: pkgevents.NewRegistry()}
	coreevents.Register(e.reg)
	ctx := context.Background()
	pub, nc, err := events.NewNATSPublisher(ctx, e.nats.url())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	e.pub = pub
	if e.js, err = jetstream.New(nc); err != nil {
		t.Fatal(err)
	}
	e.relay = e.newRelay()
	e.startRelay()
	t.Cleanup(e.stopRelay)
	return e
}

func (e *eventsEnv) newRelay() *outbox.Relay {
	return &outbox.Relay{Pool: e.pool, Sink: e.pub, Users: e.id, Poll: 50 * time.Millisecond,
		BaseDelay: 100 * time.Millisecond, MaxDelay: 500 * time.Millisecond, PublishTimeout: 2 * time.Second}
}

func (e *eventsEnv) startRelay() {
	ctx, cancel := context.WithCancel(context.Background())
	e.stop = cancel
	go e.relay.Run(ctx)
}

func (e *eventsEnv) stopRelay() {
	if e.stop != nil {
		e.stop()
		e.stop = nil
	}
}

// published legge tutti gli eventi sugli stream di core, nell'ordine di
// pubblicazione di ogni stream (ISSUE, ISSUE_COMMENT, REPOSITORY), e li
// decodifica con i decoder registrati in pkg/events/coreevents.
func (e *eventsEnv) published() []pkgevents.Envelope {
	e.t.Helper()
	ctx := context.Background()
	var out []pkgevents.Envelope
	for _, name := range []string{"ISSUE", "ISSUE_COMMENT", "REPOSITORY"} {
		st, err := e.js.Stream(ctx, name)
		if err != nil {
			e.t.Fatalf("stream %s: %v", name, err)
		}
		info, err := st.Info(ctx)
		if err != nil {
			e.t.Fatal(err)
		}
		for seq := info.State.FirstSeq; seq <= info.State.LastSeq && info.State.Msgs > 0; seq++ {
			m, err := st.GetMsg(ctx, seq)
			if err != nil {
				e.t.Fatalf("%s #%d: %v", name, seq, err)
			}
			env, err := pkgevents.UnmarshalEnvelope(m.Data)
			if err != nil {
				e.t.Fatal(err)
			}
			if _, err := e.reg.Decode(env); err != nil {
				e.t.Fatalf("%s non rispetta lo schema registrato: %v\n%s", env.Name, err, env.Payload)
			}
			out = append(out, env)
		}
	}
	return out
}

// names sono i nomi degli eventi pubblicati, in ordine alfabetico.
func names(envs []pkgevents.Envelope) []string {
	var out []string
	for _, env := range envs {
		out = append(out, env.Name)
	}
	sort.Strings(out)
	return out
}

// fresh ritorna gli eventi dopo i primi before già visti (per id di busta).
func (e *eventsEnv) fresh(seen map[string]bool, n int) []pkgevents.Envelope {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var out []pkgevents.Envelope
		for _, env := range e.published() {
			if !seen[env.ID] {
				out = append(out, env)
			}
		}
		if len(out) >= n {
			for _, env := range out {
				seen[env.ID] = true
			}
			return out
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("eventi nuovi %v, attesi %d", names(out), n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *eventsEnv) outboxCount(where string) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.event_outbox WHERE `+where).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// TestEventi_UnoPerOgniTipo esegue ogni operazione che deve pubblicare e
// verifica, sullo stream NATS reale, nome e contenuti dell'evento (criterio:
// «test d'integrazione con NATS reale per ogni tipo di evento»).
func TestEventi_UnoPerOgniTipo(t *testing.T) {
	e := newEventsEnv(t)
	seen := map[string]bool{}
	repoID := e.labelsRepo("app") // alice admin, bob write, carol read, bot: utente agente
	e.id.grantWrite(repoID, "bot")
	e.sql(`INSERT INTO core.milestones (id, repo_id, number, title) VALUES (gen_random_uuid(), $1, 1, 'v1')`, repoID)

	// step esegue op e si aspetta esattamente gli eventi want (in più, con
	// check sul primo evento di nome check).
	step := func(name string, op func(), want ...string) []pkgevents.Envelope {
		t.Helper()
		op()
		got := e.fresh(seen, len(want))
		w := slices.Clone(want)
		sort.Strings(w)
		if g := names(got); !slices.Equal(g, w) {
			t.Fatalf("%s: eventi %v, voluti %v", name, g, w)
		}
		return got
	}
	payload := func(env pkgevents.Envelope) any {
		p, err := e.reg.Decode(env)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	var n int64
	issuePath := func() string { return fmt.Sprintf("/repos/alice/app/issues/%d", n) }

	seen = e.markSeen(seen)

	// repository.created: l'attore è alice, human, col repo com'è.
	got := step("repo creato", func() {
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"zeta","visibility":"private"}`), http.StatusCreated, "")
	}, "repository.created")
	rp := payload(got[0]).(coreevents.RepositoryPayload)
	if rp.Repo.FullName != "alice/zeta" || rp.Repo.Visibility != "private" || rp.Repo.Archived || rp.Actor == nil || rp.Actor.Username != "alice" || rp.Actor.Type != "human" || rp.Actor.ID != aliceID {
		t.Fatalf("repository.created = %+v", rp)
	}

	// issue.created da un umano, con etichetta e assegnatario agente.
	got = step("issue creata", func() {
		rec := e.do(http.MethodPost, "/repos/alice/app/issues", "bob", `{"title":"Crash","body":"b","labels":["bug"],"assignees":["bot"],"milestone":1}`)
		e.want(rec, http.StatusCreated, "")
		n = e.issue(rec).Number
	}, "issue.created", "issue.labeled", "issue.assigned", "issue.milestoned")
	for _, env := range got {
		p := payload(env).(coreevents.IssuePayload)
		if p.Actor == nil || p.Actor.Username != "bob" || p.Actor.Type != "human" || p.Issue.Number != n || p.Repo.FullName != "alice/app" {
			t.Fatalf("%s: %+v", env.Name, p)
		}
		switch env.Name {
		case "issue.created":
			if p.Issue.State != "open" || p.Issue.Title != "Crash" || len(p.Issue.AssigneeIDs) != 1 || p.Issue.AssigneeIDs[0] != botID {
				t.Fatalf("issue.created = %+v", p.Issue)
			}
		case "issue.labeled":
			if p.Label == nil || p.Label.Name != "bug" || p.Label.ID == "" || len(p.Label.Color) != 6 {
				t.Fatalf("label = %+v", p.Label)
			}
		case "issue.assigned":
			if p.Assignee == nil || p.Assignee.ID != botID || p.Assignee.Username != "bot" || p.Assignee.Type != "agent" {
				t.Fatalf("assignee = %+v", p.Assignee)
			}
		case "issue.milestoned":
			if p.Milestone == nil || p.Milestone.Number != 1 || p.Milestone.Title != "v1" || p.Milestone.ID == "" {
				t.Fatalf("milestone = %+v", p.Milestone)
			}
		}
	}

	// issue.edited: l'autore (carol) cambia titolo e testo; changes porta i precedenti.
	nCarol := e.open("app", "carol", "Primo titolo")
	seen = e.markSeen(seen)
	got = step("issue modificata", func() {
		e.want(e.do(http.MethodPatch, fmt.Sprintf("/repos/alice/app/issues/%d", nCarol), "carol", `{"title":"Titolo 2","body":"corpo 2"}`), http.StatusOK, "")
	}, "issue.edited")
	ip := payload(got[0]).(coreevents.IssuePayload)
	if ip.Changes == nil || ip.Changes.Title == nil || ip.Changes.Title.From != "Primo titolo" || ip.Changes.Body == nil || ip.Changes.Body.From != "testo" || ip.Issue.Title != "Titolo 2" {
		t.Fatalf("issue.edited = %+v", ip)
	}

	// close con motivo, reopen.
	got = step("chiusa", func() {
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/close", nCarol), "bob", `{"reason":"duplicate","duplicateOf":1}`), http.StatusOK, "")
	}, "issue.closed")
	ip = payload(got[0]).(coreevents.IssuePayload)
	if ip.Reason != "duplicate" || ip.DuplicateOf == nil || *ip.DuplicateOf != 1 || ip.Issue.State != "closed" || ip.Actor.Username != "bob" {
		t.Fatalf("issue.closed = %+v", ip)
	}
	step("riaperta", func() {
		e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/reopen", nCarol), "bob", ``), http.StatusOK, "")
	}, "issue.reopened")

	// etichette e assegnatari tolti, milestone tolta.
	got = step("etichetta tolta", func() {
		e.want(e.do(http.MethodPut, issuePath()+"/labels", "bob", `{"labels":[]}`), http.StatusOK, "")
	}, "issue.unlabeled")
	if p := payload(got[0]).(coreevents.IssuePayload); p.Label == nil || p.Label.Name != "bug" {
		t.Fatalf("issue.unlabeled = %+v", p)
	}
	got = step("assegnatario tolto", func() {
		e.want(e.do(http.MethodPut, issuePath()+"/assignees", "bob", `{"assignees":[]}`), http.StatusOK, "")
	}, "issue.unassigned")
	if p := payload(got[0]).(coreevents.IssuePayload); p.Assignee == nil || p.Assignee.Type != "agent" || len(p.Issue.AssigneeIDs) != 0 {
		t.Fatalf("issue.unassigned = %+v", p)
	}
	step("milestone tolta", func() {
		e.want(e.do(http.MethodPut, issuePath()+"/milestone", "bob", `{"milestone":null}`), http.StatusOK, "")
	}, "issue.demilestoned")

	// blocco e nascondimento (admin).
	got = step("bloccata", func() {
		e.want(e.do(http.MethodPut, issuePath()+"/lock", "alice", `{"reason":"troppo accesa"}`), http.StatusOK, "")
	}, "issue.locked")
	if p := payload(got[0]).(coreevents.IssuePayload); p.Reason != "troppo accesa" || !p.Issue.Locked {
		t.Fatalf("issue.locked = %+v", p)
	}
	step("sbloccata", func() { e.want(e.do(http.MethodDelete, issuePath()+"/lock", "alice", ``), http.StatusOK, "") }, "issue.unlocked")
	got = step("nascosta", func() {
		e.want(e.do(http.MethodPut, issuePath()+"/hidden", "alice", `{"hidden":true}`), http.StatusOK, "")
	}, "issue.hidden")
	if p := payload(got[0]).(coreevents.IssuePayload); !p.Issue.Hidden {
		t.Fatalf("issue.hidden = %+v", p)
	}
	step("visibile", func() {
		e.want(e.do(http.MethodPut, issuePath()+"/hidden", "alice", `{"hidden":false}`), http.StatusOK, "")
	}, "issue.unhidden")

	// commenti: un agente commenta, poi modifica ed elimina.
	var cid string
	got = step("commento creato", func() { cid = e.comment(issuePath(), "bot", "ciao dal bot").Id.String() }, "issue_comment.created")
	cp := payload(got[0]).(coreevents.IssueCommentPayload)
	if cp.Comment.ID != cid || cp.Comment.Body != "ciao dal bot" || cp.Comment.AuthorID != botID || cp.Actor.Username != "bot" || cp.Actor.Type != "agent" || cp.Issue.Number != n {
		t.Fatalf("issue_comment.created = %+v", cp)
	}
	got = step("commento modificato", func() {
		e.want(e.do(http.MethodPatch, issuePath()+"/comments/"+cid, "bot", `{"body":"ciao 2"}`), http.StatusOK, "")
	}, "issue_comment.edited")
	cp = payload(got[0]).(coreevents.IssueCommentPayload)
	if cp.Comment.Body != "ciao 2" || cp.Changes == nil || cp.Changes.Body == nil || cp.Changes.Body.From != "ciao dal bot" {
		t.Fatalf("issue_comment.edited = %+v", cp)
	}
	got = step("commento eliminato", func() {
		e.want(e.do(http.MethodDelete, issuePath()+"/comments/"+cid, "bot", ``), http.StatusNoContent, "")
	}, "issue_comment.deleted")
	if cp = payload(got[0]).(coreevents.IssueCommentPayload); cp.Comment.ID != cid || cp.Comment.Body != "" {
		t.Fatalf("issue_comment.deleted = %+v", cp)
	}

	// repository: visibilità, archiviazione, riattivazione, eliminazione, ripristino.
	patch := func(body string) func() {
		return func() { e.want(e.do(http.MethodPatch, "/repos/alice/zeta", "alice", body), http.StatusOK, "") }
	}
	got = step("visibilità", patch(`{"visibility":"internal"}`), "repository.visibility_changed")
	if rp = payload(got[0]).(coreevents.RepositoryPayload); rp.Changes == nil || rp.Changes.Visibility == nil || rp.Changes.Visibility.From != "private" || rp.Repo.Visibility != "internal" {
		t.Fatalf("repository.visibility_changed = %+v", rp)
	}
	got = step("archiviato", patch(`{"archived":true}`), "repository.archived")
	if rp = payload(got[0]).(coreevents.RepositoryPayload); !rp.Repo.Archived {
		t.Fatalf("repository.archived = %+v", rp)
	}
	step("riattivato", patch(`{"archived":false}`), "repository.unarchived")
	got = step("eliminato", func() { e.want(e.do(http.MethodDelete, "/repos/alice/zeta", "alice", ``), http.StatusNoContent, "") }, "repository.deleted")
	rp = payload(got[0]).(coreevents.RepositoryPayload)
	del, err1 := time.Parse(time.RFC3339, rp.DeletedAt)
	purge, err2 := time.Parse(time.RFC3339, rp.PurgeAt)
	if err1 != nil || err2 != nil || purge.Sub(del) != 7*24*time.Hour {
		t.Fatalf("repository.deleted = %+v (%v %v)", rp, err1, err2)
	}
	var zetaID uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT resource_id FROM core.repositories WHERE name = 'zeta'`).Scan(&zetaID); err != nil {
		t.Fatal(err)
	}
	step("ripristinato", func() {
		e.want(e.do(http.MethodPost, "/repos/deleted/"+zetaID.String()+"/restore", "alice", ``), http.StatusOK, "")
	}, "repository.restored")

	// Ogni riga dell'outbox è stata inviata, con l'id della busta.
	if pend := e.outboxCount(`sent_at IS NULL`); pend != 0 {
		t.Fatalf("righe non inviate: %d", pend)
	}
}

// markSeen segna come già visti tutti gli eventi finora pubblicati (dopo
// aver aspettato che l'outbox sia vuota), per isolare i passi successivi.
func (e *eventsEnv) markSeen(seen map[string]bool) map[string]bool {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for e.outboxCount(`sent_at IS NULL`) > 0 {
		if time.Now().After(deadline) {
			e.t.Fatal("outbox non svuotata")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, env := range e.published() {
		seen[env.ID] = true
	}
	return seen
}

// TestEventi_NessunEventoPerOperazioneFallita: una richiesta che fallisce
// DOPO aver già accodato eventi nella transazione (issue.created e labeled
// scritti, poi l'allegato non è collegabile) non lascia niente nell'outbox né
// sullo stream; lo stesso per i rifiuti prima della transazione.
func TestEventi_NessunEventoPerOperazioneFallita(t *testing.T) {
	e := newEventsEnv(t)
	repoID := e.labelsRepo("app")
	_ = repoID
	n := e.open("app", "carol", "esistente")
	seen := e.markSeen(map[string]bool{})
	baseOutbox := e.outboxCount(`true`)

	// 1) fallimento a transazione avviata, dopo issue.created + issue.labeled.
	rec := e.do(http.MethodPost, "/repos/alice/app/issues", "bob",
		fmt.Sprintf(`{"title":"fallirà","labels":["bug"],"attachmentIds":["%s"]}`, uuid.New()))
	e.want(rec, http.StatusUnprocessableEntity, "attachment_not_linkable")
	if c := e.outboxCount(`true`); c != baseOutbox {
		t.Fatalf("righe nell'outbox dopo la richiesta fallita: %d, prima %d", c, baseOutbox)
	}
	if c := e.outboxCount(`name = 'issue.created' AND payload->'issue'->>'title' = 'fallirà'`); c != 0 {
		t.Fatalf("issue.created di una creazione fallita nell'outbox: %d", c)
	}
	var issues int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.issues WHERE title = 'fallirà'`).Scan(&issues); err != nil || issues != 0 {
		t.Fatalf("issue rimasta: %d (%v)", issues, err)
	}
	// 2) commento con allegato non collegabile (dopo l'INSERT e l'evento).
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/comments", n), "carol",
		fmt.Sprintf(`{"body":"x","attachmentIds":["%s"]}`, uuid.New())), http.StatusUnprocessableEntity, "attachment_not_linkable")
	// 3) rifiuti prima della transazione: permessi, validazione, inesistente.
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/labels", n), "carol", `{"labels":["bug"]}`), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/labels", n), "bob", `{"labels":["nope"]}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.do(http.MethodPatch, fmt.Sprintf("/repos/alice/app/issues/%d", n), "bob", `{"title":"hack"}`), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPost, "/repos/alice/nonesiste/issues", "bob", `{"title":"x"}`), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPatch, "/repos/alice/app", "bob", `{"archived":true}`), http.StatusForbidden, "")
	e.want(e.do(http.MethodDelete, "/repos/alice/app", "bob", ``), http.StatusForbidden, "")
	if c := e.outboxCount(`true`); c != baseOutbox {
		t.Fatalf("righe nell'outbox dopo i rifiuti: %d, prima %d", c, baseOutbox)
	}
	// Dà tempo al relay: se ci fosse qualcosa lo pubblicherebbe.
	time.Sleep(500 * time.Millisecond)
	for _, env := range e.published() {
		if !seen[env.ID] {
			t.Fatalf("evento pubblicato per un'operazione fallita: %s %s", env.Name, env.Payload)
		}
	}
}

// TestEventi_NatsIrraggiungibile: con NATS fermo l'operazione riesce (201), la
// riga resta nell'outbox; quando NATS torna l'evento arriva, una volta sola.
// Documenta la politica: l'evento non si perde finché la riga è nell'outbox,
// il relay ritenta con attesa crescente (BaseDelay·2^n fino a MaxDelay) e
// riparte da solo, anche dopo un riavvio del processo.
func TestEventi_NatsIrraggiungibile(t *testing.T) {
	e := newEventsEnv(t)
	e.labelsRepo("app")
	seen := e.markSeen(map[string]bool{})

	e.nats.stop()
	rec := e.do(http.MethodPost, "/repos/alice/app/issues", "carol", `{"title":"mentre NATS è giù","body":"b"}`)
	e.want(rec, http.StatusCreated, "") // la richiesta non dipende da NATS
	if c := e.outboxCount(`name = 'issue.created' AND sent_at IS NULL`); c != 1 {
		t.Fatalf("righe pendenti = %d, voluta 1", c)
	}
	// Il relay prova e fallisce (ogni tentativo aspetta al massimo
	// PublishTimeout): attempts cresce e last_error dice perché.
	deadline := time.Now().Add(15 * time.Second)
	for e.outboxCount(`name = 'issue.created' AND sent_at IS NULL AND attempts >= 2 AND last_error IS NOT NULL`) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("la riga pendente non risulta ritentata")
		}
		time.Sleep(100 * time.Millisecond)
	}

	e.nats.start()
	got := e.fresh(seen, 1)
	if len(got) != 1 || got[0].Name != "issue.created" {
		t.Fatalf("eventi dopo il riavvio di NATS: %v", names(got))
	}
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM core.event_outbox WHERE name = 'issue.created' ORDER BY seq DESC LIMIT 1`).Scan(&id); err != nil || id.String() != got[0].ID {
		t.Fatalf("l'id della busta (%s) non è quello dell'outbox (%s, %v)", got[0].ID, id, err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for e.outboxCount(`sent_at IS NULL`) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("riga non segnata come inviata")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if all := e.published(); len(all) != len(seen) {
		t.Fatalf("eventi pubblicati %d, voluti %d (doppio invio?)", len(all), len(seen))
	}
}

// TestEventi_RiavvioDelRelay: eventi scritti mentre il relay non gira (core
// fermo o riavviato) partono appena un nuovo relay si avvia, nell'ordine di
// scrittura; un secondo relay in parallelo non duplica niente.
func TestEventi_RiavvioDelRelay(t *testing.T) {
	e := newEventsEnv(t)
	e.labelsRepo("app")
	seen := e.markSeen(map[string]bool{})
	e.stopRelay()

	n := e.open("app", "carol", "uno")
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/close", n), "bob", `{}`), http.StatusOK, "")
	time.Sleep(300 * time.Millisecond)
	if got := e.fresh(map[string]bool{}, 0); len(got) != len(seen) {
		t.Fatalf("con il relay fermo sono arrivati eventi: %v", names(got))
	}

	e.relay = e.newRelay()
	e.startRelay()
	second := e.newRelay() // seconda replica di core
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go second.Run(ctx)
	got := e.fresh(seen, 2)
	if names(got)[0] != "issue.closed" && names(got)[1] != "issue.closed" {
		t.Fatalf("eventi = %v", names(got))
	}
	time.Sleep(300 * time.Millisecond)
	if all := e.published(); len(all) != len(seen) {
		t.Fatalf("eventi pubblicati %d, voluti %d (duplicati fra relay)", len(all), len(seen))
	}
}

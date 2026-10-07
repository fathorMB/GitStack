package pushevent_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
)

// startNATS avvia un nats-server reale (in-process, JetStream attivo, storage
// temporaneo): stesso protocollo e stesso motore di uno standalone.
func startNATS(t *testing.T, port int) *server.Server {
	t.Helper()
	srv, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: port, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true,
	})
	if err != nil {
		t.Fatalf("nats-server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server non pronto")
	}
	t.Cleanup(srv.Shutdown)
	return srv
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestEventoSuNATSReale(t *testing.T) {
	srv := startNATS(t, -1)
	pub, err := pushevent.NewNATSPublisher(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pub.Close)
	n := &pushevent.Notifier{Pub: pub, Git: runner(t), Logger: quiet()}

	bare, work := bareAndWork(t)
	c1 := commit(t, work, "base")
	git(t, work, "push", "origin", "main")

	p := n.Begin(context.Background(), target(bare), alice)
	c2 := commit(t, work, "fixes #12")
	git(t, work, "switch", "-c", "feature")
	git(t, work, "tag", "v1")
	git(t, work, "push", "origin", "main", "feature", "v1")
	p.Done()
	wait(t, n)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cons, err := events.EnsureDurableConsumer(ctx, js, "GIT", "test-push", gitpush.Name)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var msg jetstream.Msg
	for m := range batch.Messages() {
		msg = m
	}
	if msg == nil {
		t.Fatal("nessun evento git.push sullo stream GIT")
	}
	if msg.Subject() != "git.push" {
		t.Fatalf("subject %q", msg.Subject())
	}
	var env events.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Name != gitpush.Name || env.Version != gitpush.Version || env.ID == "" || env.Time.IsZero() {
		t.Fatalf("busta: %+v", env)
	}
	reg := events.NewRegistry()
	gitpush.Register(reg)
	dec, err := reg.Decode(env)
	if err != nil {
		t.Fatal(err)
	}
	ev := dec.(gitpush.Payload)
	if ev.Repo.ID != "r-1" || ev.Repo.FullName != "alice/app" || ev.Repo.DefaultBranch != "main" {
		t.Fatalf("repo: %+v", ev.Repo)
	}
	if ev.Pusher.ID != "u-1" || ev.Pusher.Username != "alice" || ev.Pusher.Type != "agent" {
		t.Fatalf("pusher: %+v", ev.Pusher)
	}
	byRef := map[string]gitpush.RefPush{}
	for _, r := range ev.Refs {
		byRef[r.Ref] = r
	}
	if len(byRef) != 3 {
		t.Fatalf("attesi main, feature e v1: %+v", ev.Refs)
	}
	m := byRef["refs/heads/main"]
	if !m.IsDefaultBranch || m.Before != c1 || m.After != c2 || m.Commits[0].Message != "fixes #12\n" {
		t.Fatalf("main: %+v", m)
	}
	if byRef["refs/heads/feature"].IsDefaultBranch || byRef["refs/tags/v1"].IsDefaultBranch {
		t.Fatal("solo main è il branch principale")
	}
	if err := msg.Ack(); err != nil {
		t.Fatal(err)
	}
}

func TestNATSIrraggiungibile(t *testing.T) {
	// Porta chiusa: NATS non c'è. Il publisher si crea lo stesso (la
	// connessione si riprova in background) e il push non ne risente.
	url := "nats://127.0.0.1:" + strconv.Itoa(freePort(t))
	pub, err := pushevent.NewNATSPublisher(url)
	if err != nil {
		t.Fatalf("il servizio deve partire anche con NATS giù: %v", err)
	}
	t.Cleanup(pub.Close)
	n := &pushevent.Notifier{
		Pub: pub, Git: runner(t), Logger: quiet(),
		AttemptTimeout: 2 * time.Second, Attempts: 2, Backoff: 10 * time.Millisecond,
	}
	bare, work := bareAndWork(t)
	p := n.Begin(context.Background(), target(bare), alice)
	commit(t, work, "uno")
	git(t, work, "push", "origin", "main") // il push è accettato

	start := time.Now()
	p.Done()
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("Done non deve aspettare NATS: %v", d)
	}
	wait(t, n) // i tentativi finiscono, l'errore va nel log, nessun panic
	if git(t, bare, "rev-parse", "main") == "" {
		t.Fatal("il push deve restare nel repo")
	}
}

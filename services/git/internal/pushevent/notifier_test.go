package pushevent_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
)

// recorder è un Publisher che registra gli eventi; fail = quanti tentativi
// iniziali fallire.
type recorder struct {
	mu       sync.Mutex
	fail     int
	attempts int
	got      []gitpush.Payload
}

func (r *recorder) Publish(_ context.Context, name string, version int, payload any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts++
	if r.attempts <= r.fail {
		return errors.New("NATS non risponde")
	}
	if name != gitpush.Name || version != gitpush.Version {
		return errors.New("nome o versione inattesi")
	}
	r.got = append(r.got, payload.(gitpush.Payload))
	return nil
}

func notifier(t *testing.T, pub pushevent.Publisher) *pushevent.Notifier {
	t.Helper()
	return &pushevent.Notifier{
		Pub: pub, Git: runner(t),
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AttemptTimeout: time.Second, Attempts: 3, Backoff: time.Millisecond,
	}
}

func wait(t *testing.T, n *pushevent.Notifier) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if !n.Wait(ctx) {
		t.Fatal("pubblicazione non terminata")
	}
}

var alice = access.Principal{UserID: "u-1", Username: "alice", Kind: "agent"}

func target(bare string) pushevent.Target {
	return pushevent.Target{Dir: bare, RepoID: "r-1", Owner: "alice", Name: "app", DefaultBranch: "main"}
}

func TestPushPubblicaUnEvento(t *testing.T) {
	bare, work := bareAndWork(t)
	rec := &recorder{}
	n := notifier(t, rec)

	p := n.Begin(context.Background(), target(bare), alice)
	c := commit(t, work, "uno")
	git(t, work, "push", "origin", "main")
	p.Done()
	wait(t, n)

	if len(rec.got) != 1 {
		t.Fatalf("attesi 1 evento, ottenuti %d", len(rec.got))
	}
	ev := rec.got[0]
	if ev.Repo != (gitpush.Repo{ID: "r-1", FullName: "alice/app", DefaultBranch: "main"}) {
		t.Fatalf("repo: %+v", ev.Repo)
	}
	if ev.Pusher != (gitpush.Pusher{ID: "u-1", Username: "alice", Type: "agent"}) {
		t.Fatalf("pusher: %+v", ev.Pusher)
	}
	if len(ev.Refs) != 1 || ev.Refs[0].After != c || !ev.Refs[0].IsDefaultBranch {
		t.Fatalf("refs: %+v", ev.Refs)
	}
	// Autore dei commit e utente del push sono cose distinte.
	if ev.Refs[0].Commits[0].Author.Name == ev.Pusher.Username {
		t.Fatal("il pusher non è l'autore del commit")
	}
}

func TestTipoUtenteSconosciutoEHuman(t *testing.T) {
	bare, work := bareAndWork(t)
	rec := &recorder{}
	n := notifier(t, rec)
	p := n.Begin(context.Background(), target(bare), access.Principal{UserID: "u-2", Username: "bob"})
	commit(t, work, "uno")
	git(t, work, "push", "origin", "main")
	p.Done()
	wait(t, n)
	if rec.got[0].Pusher.Type != gitpush.PusherHuman {
		t.Fatalf("tipo = %q, atteso human", rec.got[0].Pusher.Type)
	}
}

func TestNessunRefCambiatoNessunEvento(t *testing.T) {
	bare, work := bareAndWork(t)
	commit(t, work, "uno")
	git(t, work, "push", "origin", "main")
	rec := &recorder{}
	n := notifier(t, rec)
	p := n.Begin(context.Background(), target(bare), alice)
	p.Done()
	wait(t, n)
	if rec.attempts != 0 {
		t.Fatalf("nessun cambio, ma %d pubblicazioni", rec.attempts)
	}
}

func TestPubblicazioneRitentata(t *testing.T) {
	bare, work := bareAndWork(t)
	rec := &recorder{fail: 2}
	n := notifier(t, rec)
	p := n.Begin(context.Background(), target(bare), alice)
	commit(t, work, "uno")
	git(t, work, "push", "origin", "main")
	p.Done()
	wait(t, n)
	if rec.attempts != 3 || len(rec.got) != 1 {
		t.Fatalf("attesi 3 tentativi e 1 evento: tentativi=%d eventi=%d", rec.attempts, len(rec.got))
	}
}

func TestErroreDefinitivoNonBloccaNeFaPanico(t *testing.T) {
	bare, work := bareAndWork(t)
	rec := &recorder{fail: 100}
	n := notifier(t, rec)
	p := n.Begin(context.Background(), target(bare), alice)
	commit(t, work, "uno")
	git(t, work, "push", "origin", "main")
	start := time.Now()
	p.Done()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Done deve tornare subito, ha impiegato %v", d)
	}
	wait(t, n)
	if rec.attempts != 3 || len(rec.got) != 0 {
		t.Fatalf("tentativi=%d eventi=%d", rec.attempts, len(rec.got))
	}
}

func TestNotifierNilNonFaNiente(t *testing.T) {
	var n *pushevent.Notifier
	p := n.Begin(context.Background(), pushevent.Target{}, alice)
	p.Done()
	if !n.Wait(context.Background()) {
		t.Fatal("Wait su nil")
	}
}

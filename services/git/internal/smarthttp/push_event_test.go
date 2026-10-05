package smarthttp_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
	"github.com/fathorMB/GitStack/services/git/internal/receiverules"
)

type recorder struct {
	mu  sync.Mutex
	got []gitpush.Payload
}

func (r *recorder) Publish(_ context.Context, _ string, _ int, payload any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, payload.(gitpush.Payload))
	return nil
}

func newNotifier(t *testing.T, pub pushevent.Publisher) *pushevent.Notifier {
	t.Helper()
	r, err := gitrun.New()
	if err != nil {
		t.Skip("git non installato")
	}
	return &pushevent.Notifier{
		Pub: pub, Git: r, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		AttemptTimeout: 300 * time.Millisecond, Attempts: 2, Backoff: 10 * time.Millisecond,
	}
}

func waitNotifier(t *testing.T, n *pushevent.Notifier) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if !n.Wait(ctx) {
		t.Fatal("pubblicazione non terminata")
	}
}

func TestPushHTTPPubblicaGitPush(t *testing.T) {
	rec := &recorder{}
	n := newNotifier(t, rec)
	e := setupWith(t, receiverules.DefaultLimits(), n)
	a := filepath.Join(t.TempDir(), "a")
	mustGit(t, filepath.Dir(a), "clone", e.url("x", "gst_alice", "/alice/priv.git"), a)

	mustGit(t, a, "commit", "--allow-empty", "-m", "fixes #7")
	sha := strings.TrimSpace(mustGit(t, a, "rev-parse", "HEAD"))
	mustGit(t, a, "push", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/altro")
	waitNotifier(t, n)

	if len(rec.got) != 1 {
		t.Fatalf("attesi 1 evento, ottenuti %d", len(rec.got))
	}
	ev := rec.got[0]
	if ev.Repo.ID != repoPriv || ev.Repo.FullName != "alice/priv" || ev.Repo.DefaultBranch != "main" {
		t.Fatalf("repo: %+v", ev.Repo)
	}
	if ev.Pusher.ID != alice || ev.Pusher.Username != "alice" || ev.Pusher.Type != gitpush.PusherHuman {
		t.Fatalf("pusher: %+v", ev.Pusher)
	}
	got := map[string]gitpush.RefPush{}
	for _, r := range ev.Refs {
		got[r.Ref] = r
	}
	if len(got) != 2 || got["refs/heads/main"].After != sha || !got["refs/heads/main"].IsDefaultBranch ||
		got["refs/heads/altro"].IsDefaultBranch || got["refs/heads/altro"].Before != gitpush.ZeroSHA {
		t.Fatalf("refs: %+v", ev.Refs)
	}

	// Un clone o un fetch non pubblica niente.
	mustGit(t, t.TempDir(), "clone", e.url("x", "gst_alice", "/alice/priv.git"), filepath.Join(t.TempDir(), "b"))
	waitNotifier(t, n)
	if len(rec.got) != 1 {
		t.Fatalf("il clone non è un push: %d eventi", len(rec.got))
	}
}

func TestPushRifiutatoNonPubblica(t *testing.T) {
	rec := &recorder{}
	n := newNotifier(t, rec)
	e := setupWith(t, receiverules.DefaultLimits(), n)
	a := filepath.Join(t.TempDir(), "a")
	mustGit(t, filepath.Dir(a), "clone", e.url("x", "gst_bob", "/alice/priv.git"), a)
	mustGit(t, a, "commit", "--allow-empty", "-m", "x")
	if _, err := git(t, a, "push", "origin", "HEAD:refs/heads/main"); err == nil {
		t.Fatal("bob ha solo read: il push doveva fallire")
	}
	waitNotifier(t, n)
	if len(rec.got) != 0 {
		t.Fatalf("push rifiutato, ma %d eventi", len(rec.got))
	}
}

func TestPushAccettatoConNATSGiu(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close() // porta chiusa: nessun NATS
	pub, err := pushevent.NewNATSPublisher("nats://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pub.Close)
	n := newNotifier(t, pub)
	e := setupWith(t, receiverules.DefaultLimits(), n)
	a := filepath.Join(t.TempDir(), "a")
	mustGit(t, filepath.Dir(a), "clone", e.url("x", "gst_alice", "/alice/priv.git"), a)
	mustGit(t, a, "commit", "--allow-empty", "-m", "con NATS giù")
	sha := strings.TrimSpace(mustGit(t, a, "rev-parse", "HEAD"))

	mustGit(t, a, "push", "origin", "HEAD:refs/heads/main") // accettato
	waitNotifier(t, n)

	bare, _ := e.store.RepoPath(repoPriv)
	if got := mustGit(t, bare, "rev-parse", "refs/heads/main"); got != sha+"\n" && got != sha {
		t.Fatalf("il push non è nel repo: %q", got)
	}
}

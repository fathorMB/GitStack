package sshd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
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

func TestPushSSHPubblicaGitPush(t *testing.T) {
	runner, err := gitrun.New()
	if err != nil {
		t.Skip("git non installato")
	}
	rec := &recorder{}
	n := &pushevent.Notifier{Pub: rec, Git: runner, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		AttemptTimeout: time.Second, Attempts: 1}
	e := startWith(t, n)
	work := t.TempDir()
	if out, err := e.gitCmd(t, e.rw, work, "clone", e.url("/alice/app.git"), "app"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	app := filepath.Join(work, "app")
	if err := os.WriteFile(filepath.Join(app, "x.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, app, "add", ".")
	mustGit(t, app, "commit", "-m", "via ssh")
	sha := mustGit(t, app, "rev-parse", "HEAD")
	mustGit(t, app, "tag", "v1")
	if out, err := e.gitCmd(t, e.rw, app, "push", "origin", "HEAD:refs/heads/main", "v1"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if !n.Wait(ctx) {
		t.Fatal("pubblicazione non terminata")
	}

	if len(rec.got) != 1 {
		t.Fatalf("attesi 1 evento, ottenuti %d", len(rec.got))
	}
	ev := rec.got[0]
	if ev.Pusher.ID != "u-alice" || ev.Pusher.Username != "alice" || ev.Pusher.Type != gitpush.PusherAgent {
		t.Fatalf("pusher: %+v", ev.Pusher)
	}
	if ev.Repo.ID != repoID || ev.Repo.FullName != "alice/app" || ev.Repo.DefaultBranch != "main" {
		t.Fatalf("repo: %+v", ev.Repo)
	}
	got := map[string]gitpush.RefPush{}
	for _, r := range ev.Refs {
		got[r.Ref] = r
	}
	if len(got) != 2 || got["refs/heads/main"].After != sha || !got["refs/heads/main"].IsDefaultBranch ||
		got["refs/tags/v1"].After != sha || got["refs/tags/v1"].IsDefaultBranch {
		t.Fatalf("refs: %+v", ev.Refs)
	}
}

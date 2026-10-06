//go:build integration

package gitpush_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	pkggitpush "github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/gitpush"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

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

func publish(t *testing.T, js jetstream.JetStream, p pkggitpush.Payload) pkgevents.Envelope {
	t.Helper()
	env, err := pkgevents.NewEnvelope(pkggitpush.Name, pkggitpush.Version, p)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := env.Marshal()
	if _, err := js.Publish(context.Background(), pkggitpush.Name, data); err != nil {
		t.Fatal(err)
	}
	return env
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

// Il consumer consegna git.push all'handler; un errore fa ritentare il
// messaggio; un riavvio riprende da dove era (consumer durevole).
func TestConsumer_ConsegnaRitentaERiprende(t *testing.T) {
	js := startNATS(t)
	payload := pkggitpush.Payload{
		Repo:   pkggitpush.Repo{ID: "11111111-1111-1111-1111-111111111111", FullName: "alice/app", DefaultBranch: "main"},
		Pusher: pkggitpush.Pusher{ID: "22222222-2222-2222-2222-222222222222", Username: "alice", Type: "human"},
		Refs:   []pkggitpush.RefPush{{Ref: "refs/heads/main", Before: pkggitpush.ZeroSHA, After: "3333333333333333333333333333333333333333"}},
	}
	var mu sync.Mutex
	calls := 0
	var seen []string
	handler := func(_ context.Context, env pkgevents.Envelope, p pkggitpush.Payload) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return errors.New("database giù") // il primo tentativo fallisce
		}
		seen = append(seen, env.ID+":"+p.Refs[0].Ref)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gitpush.Run(ctx, js, gitpush.DurableWebhooks, handler, slog.Default()) }()
	env := publish(t, js, payload)
	// NakWithDelay: il messaggio torna dopo gitpush.RetryDelay.
	waitFor(t, "riconsegna dopo l'errore", func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 1 })
	if seen[0] != env.ID+":refs/heads/main" {
		t.Fatalf("visto %v", seen)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Core riavviato: un push avvenuto mentre era fermo arriva al riavvio,
	// quello già confermato no.
	env2 := publish(t, js, payload)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = gitpush.Run(ctx2, js, gitpush.DurableWebhooks, handler, slog.Default()) }()
	waitFor(t, "push arrivato durante il fermo", func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 2 })
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[1] != env2.ID+":refs/heads/main" {
		t.Fatalf("visti %v: il primo push non doveva tornare", seen)
	}
}

// Messaggi illeggibili o di una versione sconosciuta si scartano, non si ritentano.
func TestConsumer_ScartaIMessaggiNonValidi(t *testing.T) {
	js := startNATS(t)
	var mu sync.Mutex
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = gitpush.Run(ctx, js, gitpush.DurableWebhooks, func(context.Context, pkgevents.Envelope, pkggitpush.Payload) error {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return nil
		}, slog.Default())
	}()
	waitFor(t, "stream GIT", func() bool { _, err := js.Stream(context.Background(), "GIT"); return err == nil })
	for _, raw := range []string{`non json`, `{"name":"git.push","version":99,"id":"x","payload":{}}`, `{"name":"git.push","version":1,"id":"x","payload":{"repo":{},"pusher":{}}}`} {
		if _, err := js.Publish(context.Background(), pkggitpush.Name, []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(time.Second)
	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("l'handler ha ricevuto %d messaggi non validi", calls)
	}
}

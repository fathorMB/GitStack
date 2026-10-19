//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/google/uuid"
)

// TestCI_LogDelJob: GET .../actions/jobs/{jobId}/log restituisce le righe scritte da
// UpdateLog nell'ordine, anche a job in corso; follow=true si chiude alla fine del job;
// il job di un altro repo o di un repo non leggibile è 404.
func TestCI_LogDelJob(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	id := newFakeIdentity()
	git := &ciGit{&readerGit{fakeGit: newFakeGit()}}
	logs := &ci.LogStore{Dir: t.TempDir()}
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(emailIdentity{id}), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCILogs(logs), httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	e := &reposEnv{t: t, pool: pool, router: router, id: id, git: git.fakeGit}
	e.create("alice", `{"owner":"alice","name":"app","visibility":"internal"}`)
	e.create("alice", `{"owner":"alice","name":"altro","visibility":"internal"}`)
	e.create("alice", `{"owner":"alice","name":"segreto","visibility":"private"}`)

	rec := e.do("POST", "/repos/alice/app/workflows/deploy.yml/dispatches", "alice", `{"ref":"main","inputs":{"env":"prod"}}`)
	e.want(rec, 202)
	var run struct {
		Jobs []struct{ Id string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil || len(run.Jobs) != 1 {
		t.Fatalf("esecuzione: %v %s", err, rec.Body.String())
	}
	jobID := uuid.MustParse(run.Jobs[0].Id)
	logPath := "/repos/alice/app/actions/jobs/" + jobID.String() + "/logs"

	// Il job non ha ancora log: 200 e corpo vuoto.
	rec = e.do("GET", logPath, "bob", "")
	e.want(rec, 200)
	if rec.Body.Len() != 0 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("log vuoto: %q %s", rec.Body.String(), rec.Header().Get("Content-Type"))
	}

	// Il job parte e scrive righe in due invii.
	now := time.Now()
	if _, _, ok, err := ci.ClaimNext(context.Background(), pool, now, 3, "pod"); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ack, err := logs.Append(context.Background(), pool, now, jobID, 0, []ci.LogLine{{Time: now, Content: "uno"}, {Time: now, Content: "due"}}); err != nil || ack != 2 {
		t.Fatalf("append: %d %v", ack, err)
	}
	if ack, err := logs.Append(context.Background(), pool, now, jobID, 2, []ci.LogLine{{Time: now, Content: "tre"}}); err != nil || ack != 3 {
		t.Fatalf("append: %d %v", ack, err)
	}
	rec = e.do("GET", logPath, "bob", "")
	e.want(rec, 200)
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], " uno") || !strings.HasSuffix(lines[1], " due") || !strings.HasSuffix(lines[2], " tre") {
		t.Fatalf("log: %q", rec.Body.String())
	}

	// follow=true: resta aperto finché il job gira, poi si chiude col resto del log.
	done := make(chan string, 1)
	go func() {
		r := e.do("GET", logPath+"?follow=true", "bob", "")
		done <- r.Body.String()
	}()
	select {
	case <-done:
		t.Fatal("follow si è chiuso mentre il job girava")
	case <-time.After(1200 * time.Millisecond):
	}
	if _, err := logs.Append(context.Background(), pool, now, jobID, 3, []ci.LogLine{{Time: now, Content: "quattro"}}); err != nil {
		t.Fatal(err)
	}
	if err := ci.CompleteJob(context.Background(), pool, now, jobID, ci.ConclusionSuccess); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-done:
		if n := strings.Count(body, "\n"); n != 4 || !strings.Contains(body, " quattro") {
			t.Fatalf("log seguito: %q", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow non si è chiuso a job concluso")
	}

	// Un altro repo non vede il job, né un job inesistente; un repo non leggibile è 404 per bob.
	e.want(e.do("GET", "/repos/alice/altro/actions/jobs/"+jobID.String()+"/logs", "bob", ""), 404)
	e.want(e.do("GET", "/repos/alice/app/actions/jobs/"+uuid.NewString()+"/logs", "bob", ""), 404)
	e.want(e.do("GET", "/repos/alice/segreto/actions/jobs/"+jobID.String()+"/logs", "bob", ""), 404)
}

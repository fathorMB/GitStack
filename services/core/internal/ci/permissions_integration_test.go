//go:build integration

package ci_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/google/uuid"
)

const wideWF = "name: Wide\non: push\npermissions:\n  contents: write\n  packages: write\n  actions: write\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n  narrow:\n    runs-on: ubuntu-latest\n    permissions:\n      contents: none\n    steps:\n      - run: echo ok\n"

// TestPermessiDelToken_TettoDiChiHaAvviatoIlRun (GIT-279, CI4): un job avviato da chi ha read
// o write non ottiene permessi superiori, nemmeno con `permissions:` più larghi e un repo
// permissivo; `permissions:` riduce ma non allarga; schedule/senza attore vale il tetto del repo.
func TestPermessiDelToken_TettoDiChiHaAvviatoIlRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := e.repo(uuid.New(), "r")
	reader, writer, outsider := uuid.New(), uuid.New(), uuid.New()
	e.eng.ActorLevel = func(_ context.Context, actor, _ uuid.UUID) (bool, bool, error) {
		switch actor {
		case reader:
			return true, false, nil
		case writer:
			return true, true, nil
		}
		return false, false, nil
	}
	e.src.files[sha("a")] = []ci.File{{Path: ".github/workflows/w.yml", Content: []byte(wideWF)}}
	// Il repo consente write su tutto: il tetto resta quello di chi avvia.
	if err := ci.SaveRepoTokenDefaults(ctx, e.pool, repo, ci.RepoTokenDefaults{Contents: "write", Packages: "write", Actions: "write"}); err != nil {
		t.Fatal(err)
	}
	perms := func(actor uuid.UUID) map[string]map[string]string {
		t.Helper()
		ids, err := e.eng.OnPush(ctx, pushOf(repo, actor, "refs/heads/"+actor.String(), sha("0"), sha("a"), "x"))
		if err != nil || len(ids) != 1 {
			t.Fatalf("push: %v %v", ids, err)
		}
		run, err := ci.GetRun(ctx, e.pool, repo, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]string{}
		for _, j := range run.Jobs {
			out[j.Key] = j.Permissions
		}
		return out
	}
	for _, k := range ci.PermissionKeys {
		if got := perms(reader)["build"][k]; got != "read" {
			t.Errorf("reader: %s = %q, atteso read", k, got)
		}
	}
	if p := perms(reader)["build"]; p[ci.PermCappedByActor] != "true" {
		t.Errorf("cappedByActor assente: %v", p)
	}
	w := perms(writer)
	if w["build"]["contents"] != "write" || w["build"]["packages"] != "write" || w["build"]["actions"] != "write" {
		t.Errorf("writer: %v", w["build"])
	}
	if w["narrow"]["contents"] != "none" {
		t.Errorf("permissions: riduce ma non lo ha fatto: %v", w["narrow"])
	}
	for _, k := range ci.PermissionKeys {
		if got := perms(outsider)["build"][k]; got != "none" {
			t.Errorf("senza accesso: %s = %q", k, got)
		}
	}
	// Con il tetto del repo a read, nemmeno chi scrive ottiene write.
	if err := ci.SaveRepoTokenDefaults(ctx, e.pool, repo, ci.DefaultRepoTokenDefaults()); err != nil {
		t.Fatal(err)
	}
	if got := perms(writer)["build"]["contents"]; got != "read" {
		t.Errorf("tetto del repo ignorato: %q", got)
	}
	// Senza ActorLevel configurato l'attore vale al più read.
	e.eng.ActorLevel = nil
	if err := ci.SaveRepoTokenDefaults(ctx, e.pool, repo, ci.RepoTokenDefaults{Contents: "write", Packages: "write", Actions: "write"}); err != nil {
		t.Fatal(err)
	}
	if got := perms(uuid.New())["build"]["contents"]; got != "read" {
		t.Errorf("senza ActorLevel: %q", got)
	}
}

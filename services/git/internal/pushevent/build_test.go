package pushevent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
)

func snap(t *testing.T, bare string) pushevent.Snapshot {
	t.Helper()
	s, err := pushevent.Take(context.Background(), runner(t), bare)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	return s
}

func build(t *testing.T, bare string, before, after pushevent.Snapshot) map[string]gitpush.RefPush {
	t.Helper()
	refs, err := pushevent.Build(context.Background(), runner(t), bare, before, after, "main")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := map[string]gitpush.RefPush{}
	for _, r := range refs {
		m[r.Ref] = r
	}
	if len(m) != len(refs) {
		t.Fatalf("ref duplicati: %+v", refs)
	}
	return m
}

func TestPrimoPushCreaIlBranchPrincipale(t *testing.T) {
	bare, work := bareAndWork(t)
	before := snap(t, bare)
	c1 := commit(t, work, "primo\n\ncorpo del messaggio")
	c2 := commit(t, work, "secondo")
	git(t, work, "push", "origin", "main")

	got := build(t, bare, before, snap(t, bare))
	if len(got) != 1 {
		t.Fatalf("attesi 1 ref, ottenuti %+v", got)
	}
	r := got["refs/heads/main"]
	if r.Before != gitpush.ZeroSHA || r.After != c2 || r.Forced || !r.IsDefaultBranch || r.CommitsTruncated {
		t.Fatalf("ref inatteso: %+v", r)
	}
	if len(r.Commits) != 2 || r.Commits[0].SHA != c2 || r.Commits[1].SHA != c1 {
		t.Fatalf("commit inattesi (più recente per primo): %+v", r.Commits)
	}
	c := r.Commits[1]
	if c.Author.Name != "Ada Lovelace" || c.Author.Email != "ada@example.com" ||
		c.Committer.Name != "Grace Hopper" || c.Committer.Email != "grace@example.com" {
		t.Fatalf("autore/committer: %+v", c)
	}
	if c.Message != "primo\n\ncorpo del messaggio\n" {
		t.Fatalf("messaggio completo atteso, ottenuto %q", c.Message)
	}
	if !strings.HasSuffix(c.Author.Date, "Z") && !strings.Contains(c.Author.Date, "+") && !strings.Contains(c.Author.Date, "-") {
		t.Fatalf("data non RFC 3339: %q", c.Author.Date)
	}
}

func TestPiuRefBranchTagECreazioni(t *testing.T) {
	bare, work := bareAndWork(t)
	c1 := commit(t, work, "base")
	git(t, work, "push", "origin", "main")
	before := snap(t, bare)

	c2 := commit(t, work, "su main")
	git(t, work, "switch", "-c", "feature")
	c3 := commit(t, work, "su feature")
	git(t, work, "tag", "-a", "v1.0.0", "-m", "release")
	git(t, work, "tag", "leggero")
	git(t, work, "push", "origin", "main", "feature", "v1.0.0", "leggero")

	got := build(t, bare, before, snap(t, bare))
	if len(got) != 4 {
		t.Fatalf("attesi 4 ref, ottenuti %+v", got)
	}
	m := got["refs/heads/main"]
	if m.Before != c1 || m.After != c2 || m.Forced || !m.IsDefaultBranch || len(m.Commits) != 1 {
		t.Fatalf("main: %+v", m)
	}
	// Un branch nuovo: solo i commit che nessun ref aveva già (non c1).
	f := got["refs/heads/feature"]
	if f.Before != gitpush.ZeroSHA || f.After != c3 || f.IsDefaultBranch {
		t.Fatalf("feature: %+v", f)
	}
	if len(f.Commits) != 2 || f.Commits[0].SHA != c3 || f.Commits[1].SHA != c2 {
		t.Fatalf("feature: commit nuovi attesi c3,c2 (c1 era già in main): %+v", f.Commits)
	}
	// Tag annotato e leggero: stessa rappresentazione dei branch.
	annotated := git(t, bare, "rev-parse", "refs/tags/v1.0.0")
	if annotated == c3 {
		t.Fatal("il tag annotato dovrebbe avere uno sha diverso dal commit")
	}
	v := got["refs/tags/v1.0.0"]
	if v.Before != gitpush.ZeroSHA || v.After != annotated || v.IsDefaultBranch || v.Forced {
		t.Fatalf("tag annotato: %+v", v)
	}
	// Il tag è nato nello stesso push di feature: i commit nuovi sono quelli
	// non raggiungibili dai ref di prima del push, quindi c3 e c2 anche qui.
	if len(v.Commits) != 2 || v.Commits[0].SHA != c3 {
		t.Fatalf("tag annotato: commit nuovi attesi c3,c2: %+v", v.Commits)
	}
	l := got["refs/tags/leggero"]
	if l.After != c3 || l.IsDefaultBranch || len(l.Commits) != 2 {
		t.Fatalf("tag leggero: %+v", l)
	}
}

func TestEliminazioneEForcePush(t *testing.T) {
	bare, work := bareAndWork(t)
	c1 := commit(t, work, "uno")
	c2 := commit(t, work, "due")
	git(t, work, "switch", "-c", "temp")
	git(t, work, "push", "origin", "main", "temp")
	before := snap(t, bare)

	// Force-push di main su una storia diversa, eliminazione di temp.
	git(t, work, "switch", "main")
	git(t, work, "reset", "--hard", c1)
	c2b := commit(t, work, "due, riscritto")
	git(t, work, "push", "--force", "origin", "main")
	git(t, work, "push", "origin", "--delete", "temp")

	got := build(t, bare, before, snap(t, bare))
	if len(got) != 2 {
		t.Fatalf("attesi 2 ref, ottenuti %+v", got)
	}
	m := got["refs/heads/main"]
	if m.Before != c2 || m.After != c2b || !m.Forced || !m.IsDefaultBranch {
		t.Fatalf("force-push: %+v", m)
	}
	if len(m.Commits) != 1 || m.Commits[0].SHA != c2b {
		t.Fatalf("commit del force-push: %+v", m.Commits)
	}
	d := got["refs/heads/temp"]
	if d.Before != c2 || d.After != gitpush.ZeroSHA || d.Forced || d.IsDefaultBranch {
		t.Fatalf("eliminazione: %+v", d)
	}
	if d.Commits == nil || len(d.Commits) != 0 {
		t.Fatalf("un ref eliminato ha commits [] (non null): %#v", d.Commits)
	}
}

func TestAvanzamentoNonForzatoEsenzaCambi(t *testing.T) {
	bare, work := bareAndWork(t)
	commit(t, work, "uno")
	git(t, work, "push", "origin", "main")
	before := snap(t, bare)
	if got := build(t, bare, before, snap(t, bare)); len(got) != 0 {
		t.Fatalf("nessun cambio, nessun ref: %+v", got)
	}
}

func TestCommitOltreIlMassimoSonoTroncati(t *testing.T) {
	bare, _ := bareAndWork(t)
	before := snap(t, bare)
	n := gitpush.MaxCommits + 5
	fastImport(t, bare, "refs/heads/main", n)
	got := build(t, bare, before, snap(t, bare))
	r := got["refs/heads/main"]
	if !r.CommitsTruncated || len(r.Commits) != gitpush.MaxCommits {
		t.Fatalf("attesi %d commit e troncamento, ottenuti %d (troncato=%v)", gitpush.MaxCommits, len(r.Commits), r.CommitsTruncated)
	}
	if r.Commits[0].SHA != r.After || r.Commits[0].Message != "c105" {
		t.Fatalf("i più recenti per primo: %+v", r.Commits[0])
	}
	// Il resto si ricostruisce da before..after (creazione: da After, senza
	// i commit già presenti negli altri ref): qui il repo era vuoto.
	if total := git(t, bare, "rev-list", "--count", r.After); total != "105" {
		t.Fatalf("il repo ha %s commit, attesi 105", total)
	}

	// Esattamente il massimo: non è troncato.
	before = snap(t, bare)
	fastImport(t, bare, "refs/heads/esatto", gitpush.MaxCommits)
	e := build(t, bare, before, snap(t, bare))["refs/heads/esatto"]
	if e.CommitsTruncated || len(e.Commits) != gitpush.MaxCommits {
		t.Fatalf("esatto al massimo: %d commit, troncato=%v", len(e.Commits), e.CommitsTruncated)
	}
}

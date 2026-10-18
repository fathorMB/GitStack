package gitmerge

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/gitread"
	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
)

var (
	ada   = Identity{Name: "Ada Lovelace", Email: "ada@example.com"}
	grace = Identity{Name: "Grace Hopper", Email: "grace@example.com"}
)

// fx è un repo bare vero più un clone di lavoro con cui si preparano i branch.
type fx struct {
	t    *testing.T
	bare string
	work string
	svc  *Service
	rec  *recorder
}

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

func (r *recorder) events() []gitpush.Payload {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]gitpush.Payload(nil), r.got...)
}

func gitEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") && !strings.HasPrefix(strings.ToUpper(kv), "HOME=") {
			env = append(env, kv)
		}
	}
	return append(env, "HOME="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
}

func newFx(t *testing.T) *fx {
	t.Helper()
	run, err := gitrun.New()
	if err != nil {
		t.Skip("git non installato")
	}
	root := t.TempDir()
	f := &fx{t: t, bare: filepath.Join(root, "bare.git"), work: filepath.Join(root, "work"), rec: &recorder{}}
	f.git(root, "init", "--bare", "-b", "main", f.bare)
	f.git(root, "init", "-b", "main", f.work)
	f.git(f.work, "config", "core.autocrlf", "false")
	f.git(f.work, "remote", "add", "origin", f.bare)
	f.write("README.md", "# demo\n")
	f.commit("iniziale")
	f.git(f.work, "push", "-q", "origin", "main")
	notifier := &pushevent.Notifier{Pub: f.rec, Git: run, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), AttemptTimeout: time.Second, Attempts: 1}
	f.svc = &Service{Run: run, Events: notifier, Dir: func(id string) (string, error) {
		if id != "r1" {
			return "", errors.New("repo non trovato")
		}
		return f.bare, nil
	}}
	return f
}

func (f *fx) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(f.t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fx) write(name, content string) {
	f.t.Helper()
	p := filepath.Join(f.work, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// commit committa tutto e torna lo sha.
func (f *fx) commit(msg string) string {
	f.t.Helper()
	f.git(f.work, "add", "-A")
	f.git(f.work, "commit", "-q", "-m", msg)
	return f.git(f.work, "rev-parse", "HEAD")
}

// branch crea (da `from`) il branch con un commit che scrive file=content e lo pubblica.
func (f *fx) branch(name, from, file, content, msg string) string {
	f.t.Helper()
	f.git(f.work, "checkout", "-q", "-B", name, "origin/"+from)
	f.write(file, content)
	sha := f.commit(msg)
	f.git(f.work, "push", "-q", "-f", "origin", name)
	f.git(f.work, "fetch", "-q", "origin")
	return sha
}

func (f *fx) ref(name string) string {
	f.t.Helper()
	return f.git(f.bare, "rev-parse", "refs/heads/"+name)
}

func (f *fx) mergeInput(method, head string) MergeInput {
	return MergeInput{
		Base: "main", Head: head, Method: method, Message: "Unisce " + head + "\n\nCloses #7",
		ExpectedBaseSHA: f.ref("main"), Author: ada, Committer: grace,
		Target: Target{Owner: "alice", Name: "app", Pusher: gitpush.Pusher{ID: "u-1", Username: "alice", Type: gitpush.PusherHuman}},
	}
}

func (f *fx) waitEvents(n int) []gitpush.Payload {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if !f.svc.Events.Wait(ctx) {
		f.t.Fatal("pubblicazione non terminata")
	}
	ev := f.rec.events()
	if len(ev) != n {
		f.t.Fatalf("eventi: %d, attesi %d: %+v", len(ev), n, ev)
	}
	return ev
}

func (f *fx) fsck() {
	f.t.Helper()
	f.git(f.bare, "fsck", "--strict")
}

func TestCompare(t *testing.T) {
	f := newFx(t)
	h1 := f.branch("feature", "main", "a.txt", "uno\n", "aggiunge a")
	f.git(f.work, "checkout", "-q", "feature")
	f.write("b.txt", "due\n")
	h2 := f.commit("aggiunge b")
	f.git(f.work, "push", "-q", "origin", "feature")
	m2 := f.branch("main2", "main", "m.txt", "m\n", "su main")
	f.git(f.work, "push", "-q", "origin", "main2:main")
	base := f.git(f.bare, "merge-base", "main", "feature")

	rs := gitread.New(f.svc.Run, f.svc.Dir)
	c, err := rs.Compare(context.Background(), "r1", "main", "feature", false)
	if err != nil {
		t.Fatal(err)
	}
	if c.MergeBase != base || c.AheadBy != 2 || c.BehindBy != 1 || c.HeadSHA != h2 || c.BaseSHA != m2 {
		t.Fatalf("confronto: %+v", c)
	}
	if len(c.Commits) != 2 || c.Commits[0].SHA != h2 || c.Commits[1].SHA != h1 {
		t.Fatalf("commit: %+v", c.Commits)
	}
	// Il diff è dal merge-base: m.txt (solo su main) non c'è.
	if c.FilesChanged != 2 || c.Files[0].Path != "a.txt" || c.Files[1].Path != "b.txt" || c.Additions != 2 || !strings.Contains(c.Files[0].Patch, "+uno") {
		t.Fatalf("diff: %+v", c.Files)
	}
	if _, err := rs.Compare(context.Background(), "r1", "main", "nonesiste", false); !errors.Is(err, gitref.ErrRefNotFound) {
		t.Fatalf("head mancante: %v", err)
	}
}

func TestMergeability(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	f.branch("clean", "main", "a.txt", "a\n", "a")
	st, err := f.svc.Mergeability(ctx, "r1", "main", "clean")
	if err != nil || st.State != StateClean || !st.Mergeable || len(st.Conflicts) != 0 {
		t.Fatalf("clean: %+v %v", st, err)
	}

	// main avanza su un altro file: «clean» resta indietro ma unibile.
	f.branch("tmp", "main", "other.txt", "x\n", "su main")
	f.git(f.work, "push", "-q", "origin", "tmp:main")
	st, err = f.svc.Mergeability(ctx, "r1", "main", "clean")
	if err != nil || st.State != StateBehind || !st.Mergeable || st.MergeBase == st.BaseSHA {
		t.Fatalf("behind: %+v %v", st, err)
	}

	// Stesso file modificato in modo diverso: conflitto, con l'elenco.
	f.branch("c1", "main", "README.md", "versione c1\n", "c1")
	f.branch("tmp2", "main", "README.md", "versione main\n", "main cambia README")
	f.git(f.work, "push", "-q", "origin", "tmp2:main")
	st, err = f.svc.Mergeability(ctx, "r1", "main", "c1")
	if err != nil || st.State != StateConflicts || st.Mergeable || len(st.Conflicts) != 1 || st.Conflicts[0] != "README.md" {
		t.Fatalf("conflicts: %+v %v", st, err)
	}

	// head già in base: niente da unire.
	st, err = f.svc.Mergeability(ctx, "r1", "main", "tmp")
	if err != nil || st.State != StateEmpty || st.Mergeable {
		t.Fatalf("empty: %+v %v", st, err)
	}

	// Storia non correlata.
	f.git(f.work, "checkout", "-q", "--orphan", "orfano")
	f.git(f.work, "rm", "-rfq", ".")
	f.write("z.txt", "z\n")
	f.commit("radice a parte")
	f.git(f.work, "push", "-q", "origin", "orfano")
	st, err = f.svc.Mergeability(ctx, "r1", "main", "orfano")
	if err != nil || st.State != StateUnrelated || st.Mergeable {
		t.Fatalf("unrelated: %+v %v", st, err)
	}

	if _, err := f.svc.Mergeability(ctx, "r1", "nessuno", "clean"); !errors.Is(err, gitref.ErrRefNotFound) {
		t.Fatalf("base mancante: %v", err)
	}
}

func TestMergeCommit(t *testing.T) {
	f := newFx(t)
	h := f.branch("feature", "main", "a.txt", "a\n", "aggiunge a")
	f.branch("tmp", "main", "m.txt", "m\n", "su main")
	f.git(f.work, "push", "-q", "origin", "tmp:main")
	oldMain := f.ref("main")
	in := f.mergeInput(MethodMerge, "feature")

	res, err := f.svc.Merge(context.Background(), "r1", in)
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA != f.ref("main") || res.BaseSHA != oldMain || res.HeadSHA != h {
		t.Fatalf("esito: %+v", res)
	}
	// Genitori: prima base, poi head.
	if p := f.git(f.bare, "rev-list", "--parents", "-1", res.SHA); p != res.SHA+" "+oldMain+" "+h {
		t.Fatalf("genitori: %s", p)
	}
	// Albero: i file di entrambi.
	if files := f.git(f.bare, "ls-tree", "-r", "--name-only", res.SHA); files != "README.md\na.txt\nm.txt" {
		t.Fatalf("albero: %q", files)
	}
	if got := f.git(f.bare, "log", "-1", "--format=%an <%ae>|%cn <%ce>|%B", res.SHA); !strings.HasPrefix(got, "Ada Lovelace <ada@example.com>|Grace Hopper <grace@example.com>|Unisce feature") || !strings.Contains(got, "Closes #7") {
		t.Fatalf("autore e committer: %q", got)
	}
	f.fsck()

	// L'evento è un git.push normale: ref, before/after, commit con messaggio.
	ev := f.waitEvents(1)[0]
	if ev.Repo.ID != "r1" || ev.Repo.FullName != "alice/app" || ev.Repo.DefaultBranch != "main" || ev.Pusher.Username != "alice" {
		t.Fatalf("evento: %+v", ev)
	}
	if len(ev.Refs) != 1 {
		t.Fatalf("ref: %+v", ev.Refs)
	}
	r := ev.Refs[0]
	if r.Ref != "refs/heads/main" || r.Before != oldMain || r.After != res.SHA || r.Forced || !r.IsDefaultBranch {
		t.Fatalf("ref evento: %+v", r)
	}
	var sawMerge, sawHead bool
	for _, c := range r.Commits {
		sawMerge = sawMerge || (c.SHA == res.SHA && strings.Contains(c.Message, "Closes #7") && c.Committer.Email == "grace@example.com")
		sawHead = sawHead || c.SHA == h
	}
	if !sawMerge || !sawHead {
		t.Fatalf("commit evento: %+v", r.Commits)
	}
}

func TestMergeSquash(t *testing.T) {
	f := newFx(t)
	f.branch("feature", "main", "a.txt", "a\n", "primo")
	f.git(f.work, "checkout", "-q", "feature")
	f.write("b.txt", "b\n")
	f.commit("secondo")
	f.git(f.work, "push", "-q", "origin", "feature")
	oldMain := f.ref("main")
	head := f.ref("feature")

	res, err := f.svc.Merge(context.Background(), "r1", f.mergeInput(MethodSquash, "feature"))
	if err != nil {
		t.Fatal(err)
	}
	// Un solo genitore (base), nessun legame con i commit di head.
	if p := f.git(f.bare, "rev-list", "--parents", "-1", res.SHA); p != res.SHA+" "+oldMain {
		t.Fatalf("genitori: %s", p)
	}
	if strings.Contains(f.git(f.bare, "rev-list", res.SHA), head) {
		t.Fatal("lo squash non deve portare i commit di head")
	}
	// L'albero è quello di head.
	if a, b := f.git(f.bare, "rev-parse", res.SHA+"^{tree}"), f.git(f.bare, "rev-parse", head+"^{tree}"); a != b {
		t.Fatalf("albero %s, atteso %s", a, b)
	}
	if got := f.git(f.bare, "log", "-1", "--format=%an|%cn|%s", res.SHA); got != "Ada Lovelace|Grace Hopper|Unisce feature" {
		t.Fatalf("metadati: %q", got)
	}
	f.fsck()
	f.waitEvents(1)

	// Senza messaggio lo squash è rifiutato.
	in := f.mergeInput(MethodSquash, "feature")
	in.Message = " "
	if _, err := f.svc.Merge(context.Background(), "r1", in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("squash senza messaggio: %v", err)
	}
}

func TestMergeConflitti(t *testing.T) {
	f := newFx(t)
	f.branch("c1", "main", "README.md", "versione c1\n", "c1")
	f.branch("tmp", "main", "README.md", "versione main\n", "main")
	f.git(f.work, "push", "-q", "origin", "tmp:main")
	before := f.ref("main")

	_, err := f.svc.Merge(context.Background(), "r1", f.mergeInput(MethodMerge, "c1"))
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Files) != 1 || ce.Files[0] != "README.md" {
		t.Fatalf("atteso conflitto su README.md: %v", err)
	}
	if f.ref("main") != before {
		t.Fatal("main è cambiato")
	}
	f.fsck()
	f.waitEvents(0)
}

func TestMergeShaAtteso(t *testing.T) {
	f := newFx(t)
	f.branch("feature", "main", "a.txt", "a\n", "a")
	in := f.mergeInput(MethodMerge, "feature")
	// La base cambia dopo che chi chiama ha letto lo sha.
	f.branch("tmp", "main", "m.txt", "m\n", "su main")
	f.git(f.work, "push", "-q", "origin", "tmp:main")
	moved := f.ref("main")

	_, err := f.svc.Merge(context.Background(), "r1", in)
	var se *StaleError
	if !errors.Is(err, ErrStale) || !errors.As(err, &se) || se.Current != moved {
		t.Fatalf("atteso stale: %v", err)
	}
	if f.ref("main") != moved {
		t.Fatal("main è cambiato")
	}
	f.fsck()

	// Anche l'head, se indicato, deve combaciare.
	in = f.mergeInput(MethodMerge, "feature")
	in.ExpectedHeadSHA = moved
	if _, err := f.svc.Merge(context.Background(), "r1", in); !errors.Is(err, ErrStale) {
		t.Fatalf("head diverso: %v", err)
	}
	if f.ref("main") != moved {
		t.Fatal("main è cambiato")
	}

	// sha atteso assente o malformato: 400.
	in.ExpectedBaseSHA = "abc"
	if _, err := f.svc.Merge(context.Background(), "r1", in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sha malformato: %v", err)
	}
}

// Due merge sulla stessa base letta: uno solo vince, l'altro non cambia nulla.
func TestMergeConcorrenti(t *testing.T) {
	f := newFx(t)
	f.branch("f1", "main", "a.txt", "a\n", "a")
	f.branch("f2", "main", "b.txt", "b\n", "b")
	expected := f.ref("main")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, h := range []string{"f1", "f2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := f.mergeInput(MethodMerge, h)
			in.ExpectedBaseSHA = expected
			_, errs[i] = f.svc.Merge(context.Background(), "r1", in)
		}()
	}
	wg.Wait()
	ok, stale := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrStale):
			stale++
		default:
			t.Fatalf("errore inatteso: %v", err)
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatalf("ok=%d stale=%d (%v)", ok, stale, errs)
	}
	if n := f.git(f.bare, "rev-list", "--count", expected+"..main"); n != "2" { // merge + commit del branch
		t.Fatalf("commit nuovi su main: %s", n)
	}
	f.fsck()
	f.waitEvents(1)
}

// updateRef è l'unico punto che sposta il ref: con lo sha vecchio sbagliato
// fallisce e non lascia nulla (anche se un push si infila dopo i controlli).
func TestUpdateRefAtomico(t *testing.T) {
	f := newFx(t)
	h := f.branch("feature", "main", "a.txt", "a\n", "a")
	main := f.ref("main")

	err := f.svc.updateRef(context.Background(), f.bare, "refs/heads/main", h, h, "prova")
	var se *StaleError
	if !errors.As(err, &se) || se.Current != main {
		t.Fatalf("atteso stale: %v", err)
	}
	if f.ref("main") != main {
		t.Fatal("main è cambiato")
	}
	if err := f.svc.updateRef(context.Background(), f.bare, "refs/heads/main", h, main, "prova"); err != nil {
		t.Fatal(err)
	}
	if f.ref("main") != h {
		t.Fatal("main non spostato")
	}
}

func TestMergeNienteDaUnire(t *testing.T) {
	f := newFx(t)
	f.branch("feature", "main", "a.txt", "a\n", "a")
	f.git(f.work, "push", "-q", "origin", "feature:main")
	if _, err := f.svc.Merge(context.Background(), "r1", f.mergeInput(MethodMerge, "feature")); !errors.Is(err, ErrNothingToMerge) {
		t.Fatalf("atteso ErrNothingToMerge: %v", err)
	}
}

func TestDeleteBranch(t *testing.T) {
	f := newFx(t)
	h := f.branch("feature", "main", "a.txt", "a\n", "a")
	ctx := context.Background()
	tg := Target{Owner: "alice", Name: "app", Pusher: gitpush.Pusher{ID: "u-1", Username: "alice", Type: gitpush.PusherHuman}}

	if _, err := f.svc.DeleteBranch(ctx, "r1", DeleteInput{Branch: "main", Target: tg}); !errors.Is(err, ErrDefaultBranch) {
		t.Fatalf("branch principale: %v", err)
	}
	if _, err := f.svc.DeleteBranch(ctx, "r1", DeleteInput{Branch: "feature", ExpectedSHA: f.ref("main"), Target: tg}); !errors.Is(err, ErrStale) {
		t.Fatalf("sha diverso: %v", err)
	}
	if _, err := f.svc.DeleteBranch(ctx, "r1", DeleteInput{Branch: "nessuno", Target: tg}); !errors.Is(err, gitref.ErrRefNotFound) {
		t.Fatalf("mancante: %v", err)
	}
	if _, err := f.svc.DeleteBranch(ctx, "r1", DeleteInput{Branch: "--x", Target: tg}); !errors.Is(err, gitref.ErrInvalidRef) {
		t.Fatalf("nome non valido: %v", err)
	}
	sha, err := f.svc.DeleteBranch(ctx, "r1", DeleteInput{Branch: "feature", ExpectedSHA: h, Target: tg})
	if err != nil || sha != h {
		t.Fatalf("eliminazione: %s %v", sha, err)
	}
	if out := f.git(f.bare, "branch", "--list", "feature"); out != "" {
		t.Fatalf("il branch c'è ancora: %q", out)
	}
	ev := f.waitEvents(1)[0]
	if len(ev.Refs) != 1 || ev.Refs[0].Ref != "refs/heads/feature" || ev.Refs[0].After != gitpush.ZeroSHA || ev.Refs[0].Before != h {
		t.Fatalf("evento: %+v", ev.Refs)
	}
}

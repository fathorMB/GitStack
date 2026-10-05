package gitread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// fixture è un repo reale creato nel test.
type fixture struct {
	t    *testing.T
	work string
	gd   string
	n    int
	svc  *Service
}

type who struct{ name, email string }

var (
	alice = who{"Alice Rossi", "alice@example.com"}
	bob   = who{"Bob Verdi", "bob@example.com"}
)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non trovato nel PATH")
	}
	work := t.TempDir()
	f := &fixture{t: t, work: work, gd: filepath.Join(work, ".git")}
	f.git(alice, "init", "--quiet", "--initial-branch=main")
	run, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	f.svc = New(run, func(id string) (string, error) {
		if id != "r1" {
			return "", errors.New("repo non trovato")
		}
		return f.gd, nil
	})
	return f
}

func (f *fixture) git(w who, args ...string) string {
	f.t.Helper()
	f.n++
	date := fmt.Sprintf("%d +0000", 1700000000+f.n*100)
	cmd := exec.Command("git", append([]string{"-c", "core.autocrlf=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
	cmd.Dir = f.work
	cmd.Env = append(cleanEnv(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME="+w.name, "GIT_AUTHOR_EMAIL="+w.email, "GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_NAME="+w.name, "GIT_COMMITTER_EMAIL="+w.email, "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(name string, content []byte) {
	f.t.Helper()
	p := filepath.Join(f.work, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commit(w who, msg string) string {
	f.t.Helper()
	f.git(w, "add", "-A")
	f.git(w, "commit", "--quiet", "-m", msg)
	return f.git(w, "rev-parse", "HEAD")
}

func lines(n int, prefix string) []byte {
	var b bytes.Buffer
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s %d\n", prefix, i)
	}
	return b.Bytes()
}

// history costruisce: c1 (Alice, iniziale), c2 (Bob), c3 (Alice, rinomina e
// file grande), feature (Bob) e merge M, un tag annotato v1 su c2.
type history struct{ c1, c2, c3, feat, merge string }

func (f *fixture) history() history {
	var h history
	f.write("a.txt", []byte("uno\ndue\ntre\n"))
	f.write("bin.dat", []byte{0, 1, 2, 0, 255, 254})
	h.c1 = f.commit(alice, "Primo commit\n\nCorpo del messaggio.")

	f.write("a.txt", []byte("uno\nDUE\ntre\nquattro\n"))
	f.write("sub/b.txt", lines(5, "b"))
	f.write("package-lock.json", lines(10, "lock"))
	h.c2 = f.commit(bob, "Secondo")
	f.git(bob, "tag", "-a", "v1", "-m", "release 1", h.c2)

	f.git(alice, "mv", "sub/b.txt", "sub/c.txt")
	f.write("big.txt", lines(600, "big"))
	h.c3 = f.commit(alice, "Rinomina e file grande")

	f.git(bob, "checkout", "--quiet", "-b", "feature", h.c2)
	f.write("f.txt", []byte("feature\n"))
	h.feat = f.commit(bob, "Feature")
	f.git(alice, "checkout", "--quiet", "main")
	f.git(alice, "merge", "--no-ff", "--quiet", "-m", "Merge feature", "feature")
	h.merge = f.git(alice, "rev-parse", "HEAD")
	return h
}

func (f *fixture) detail(sha string, ws bool) *CommitDetail {
	f.t.Helper()
	d, err := f.svc.Commit(context.Background(), "r1", sha, ws)
	if err != nil {
		f.t.Fatalf("Commit(%s): %v", sha, err)
	}
	return d
}

func fileByPath(t *testing.T, d *CommitDetail, p string) FileDiff {
	t.Helper()
	for _, f := range d.Files {
		if f.Path == p {
			return f
		}
	}
	t.Fatalf("file %q non trovato in %+v", p, d.Files)
	return FileDiff{}
}

func TestCommitsPaginationAndFilters(t *testing.T) {
	f := newFixture(t)
	h := f.history()
	ctx := context.Background()

	// main: merge, feat, c3, c2, c1 (ordine di data decrescente).
	p1, err := f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", PerPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Items) != 2 || !p1.HasMore || p1.Page != 1 || p1.PerPage != 2 {
		t.Fatalf("pagina 1: %+v", p1)
	}
	if p1.Items[0].SHA != h.merge || len(p1.Items[0].Parents) != 2 {
		t.Fatalf("il primo deve essere il merge con 2 genitori: %+v", p1.Items[0])
	}
	p3, err := f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", PerPage: 2, Page: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(p3.Items) != 1 || p3.HasMore || p3.Items[0].SHA != h.c1 || len(p3.Items[0].Parents) != 0 {
		t.Fatalf("pagina 3: %+v", p3)
	}
	if p3.Items[0].Message != "Primo commit\n\nCorpo del messaggio." || p3.Items[0].Subject != "Primo commit" {
		t.Fatalf("messaggio: %q / %q", p3.Items[0].Message, p3.Items[0].Subject)
	}
	if p3.Items[0].Author.Email != "alice@example.com" || p3.Items[0].Author.Date.Year() != 2023 {
		t.Fatalf("autore: %+v", p3.Items[0].Author)
	}
	pOut, err := f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", PerPage: 2, Page: 9})
	if err != nil || len(pOut.Items) != 0 || pOut.HasMore {
		t.Fatalf("pagina oltre la fine: %+v %v", pOut, err)
	}

	// Default 30 per pagina.
	all, _ := f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main"})
	if all.PerPage != DefaultPerPage || len(all.Items) != 5 {
		t.Fatalf("default: %+v", all)
	}

	// Autore, per nome o email, senza distinguere maiuscole.
	for _, a := range []string{"bob", "BOB@EXAMPLE.COM", "Bob Verdi"} {
		r, err := f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", Author: a})
		if err != nil || len(r.Items) != 2 {
			t.Fatalf("author %q: %+v %v", a, r, err)
		}
	}
	r, _ := f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", Author: "nessuno"})
	if len(r.Items) != 0 {
		t.Fatalf("author inesistente: %+v", r)
	}
	// Il filtro non è una regex.
	r, _ = f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", Author: ".*"})
	if len(r.Items) != 0 {
		t.Fatalf("author come regex: %+v", r)
	}

	// Percorso: file e cartella (History, B4).
	r, _ = f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", Path: "a.txt"})
	if len(r.Items) != 2 || r.Items[0].SHA != h.c2 || r.Items[1].SHA != h.c1 {
		t.Fatalf("history di a.txt: %+v", r.Items)
	}
	r, _ = f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", Path: "sub"})
	if len(r.Items) != 2 {
		t.Fatalf("history di sub: %+v", r.Items)
	}
	r, _ = f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", Path: "sub/", Author: "alice"})
	if len(r.Items) != 1 || r.Items[0].SHA != h.c3 {
		t.Fatalf("path + author: %+v", r.Items)
	}

	// Ref: branch, tag, sha completo e prefisso.
	for _, ref := range []string{"feature", "v1", h.c2, h.c2[:7]} {
		if _, err := f.svc.Commits(ctx, "r1", CommitsQuery{Ref: ref}); err != nil {
			t.Fatalf("ref %q: %v", ref, err)
		}
	}
	r, _ = f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "v1"})
	if len(r.Items) != 2 || r.Items[0].SHA != h.c2 {
		t.Fatalf("storico del tag v1: %+v", r.Items)
	}
}

func TestCommitsErrors(t *testing.T) {
	f := newFixture(t)
	f.history()
	ctx := context.Background()
	cases := []struct {
		name string
		q    CommitsQuery
		want error
	}{
		{"ref vuoto", CommitsQuery{}, gitref.ErrInvalidRef},
		{"ref con ..", CommitsQuery{Ref: "a..b"}, gitref.ErrInvalidRef},
		{"ref come opzione", CommitsQuery{Ref: "--all"}, gitref.ErrInvalidRef},
		{"ref con spazio", CommitsQuery{Ref: "a b"}, gitref.ErrInvalidRef},
		{"ref lungo", CommitsQuery{Ref: strings.Repeat("a", 256)}, gitref.ErrInvalidRef},
		{"ref inesistente", CommitsQuery{Ref: "nonesiste"}, gitref.ErrRefNotFound},
		{"sha inesistente", CommitsQuery{Ref: strings.Repeat("a", 40)}, gitref.ErrRefNotFound},
		{"path ..", CommitsQuery{Ref: "main", Path: "a/../b"}, gitref.ErrInvalidPath},
		{"path assoluto", CommitsQuery{Ref: "main", Path: "/etc"}, gitref.ErrInvalidPath},
		{"path opzione ma valido come nome", CommitsQuery{Ref: "main", Path: "./x"}, gitref.ErrInvalidPath},
		{"perPage 101", CommitsQuery{Ref: "main", PerPage: 101}, ErrInvalidInput},
		{"page negativa", CommitsQuery{Ref: "main", Page: -1}, ErrInvalidInput},
	}
	for _, c := range cases {
		if _, err := f.svc.Commits(ctx, "r1", c.q); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, voglio %v", c.name, err, c.want)
		}
	}
	// Un percorso che sembra un'opzione è solo un percorso senza risultati.
	r, err := f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", Path: "--all"})
	if err != nil || len(r.Items) != 0 {
		t.Fatalf("path --all: %+v %v", r, err)
	}
	// Un pathspec con magic o glob è letterale.
	r, err = f.svc.Commits(ctx, "r1", CommitsQuery{Ref: "main", Path: "*.txt"})
	if err != nil || len(r.Items) != 0 {
		t.Fatalf("path glob: %+v %v", r, err)
	}
	if _, err := f.svc.Commits(ctx, "altro", CommitsQuery{Ref: "main"}); !errors.Is(err, ErrRepo) {
		t.Fatalf("repo inesistente: %v", err)
	}
}

func TestCommitsEmptyRepo(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Commits(context.Background(), "r1", CommitsQuery{Ref: "main"})
	if !errors.Is(err, gitref.ErrRefNotFound) {
		t.Fatalf("repo vuoto: %v", err)
	}
}

func TestCommitInitialAgainstEmptyTree(t *testing.T) {
	f := newFixture(t)
	h := f.history()
	d := f.detail(h.c1, false)
	if d.Commit.SHA != h.c1 || len(d.Commit.Parents) != 0 || d.Commit.Message != "Primo commit\n\nCorpo del messaggio." {
		t.Fatalf("commit: %+v", d.Commit)
	}
	if d.FilesChanged != 2 || d.ListOnly || d.Truncated {
		t.Fatalf("riepilogo: %+v", d)
	}
	a := fileByPath(t, d, "a.txt")
	if a.Status != "added" || a.Additions != 3 || a.Deletions != 0 || !strings.HasPrefix(a.Patch, "@@ -0,0 +1,3 @@") {
		t.Fatalf("a.txt: %+v", a)
	}
	bin := fileByPath(t, d, "bin.dat")
	if !bin.Binary || bin.Patch != "" || bin.Truncated || bin.Status != "added" {
		t.Fatalf("binario: %+v", bin)
	}
	if d.Additions != 3 || d.Deletions != 0 {
		t.Fatalf("totali: %d/%d", d.Additions, d.Deletions)
	}
	if len(d.Tags) != 0 {
		t.Fatalf("tag: %v", d.Tags)
	}
}

func TestCommitModifiedTagsAndLock(t *testing.T) {
	f := newFixture(t)
	h := f.history()
	d := f.detail(h.c2[:9], false) // prefisso univoco
	if d.Commit.SHA != h.c2 {
		t.Fatalf("sha: %s", d.Commit.SHA)
	}
	if len(d.Tags) != 1 || d.Tags[0] != "v1" {
		t.Fatalf("tag che puntano al commit (annotato): %v", d.Tags)
	}
	a := fileByPath(t, d, "a.txt")
	if a.Status != "modified" || a.Additions != 2 || a.Deletions != 1 || !strings.Contains(a.Patch, "-due\n+DUE\n") {
		t.Fatalf("a.txt: %+v", a)
	}
	lock := fileByPath(t, d, "package-lock.json")
	if !lock.Collapsed || lock.CollapseReason != ReasonLock || lock.Patch == "" {
		t.Fatalf("lock: %+v", lock)
	}
	if b := fileByPath(t, d, "sub/b.txt"); b.Collapsed {
		t.Fatalf("sub/b.txt non va chiuso: %+v", b)
	}
}

func TestCommitRenameAndLarge(t *testing.T) {
	f := newFixture(t)
	h := f.history()
	d := f.detail(h.c3, false)
	r := fileByPath(t, d, "sub/c.txt")
	if r.Status != "renamed" || r.OldPath != "sub/b.txt" || r.Additions != 0 || r.Deletions != 0 || r.Patch != "" {
		t.Fatalf("rinomina: %+v", r)
	}
	big := fileByPath(t, d, "big.txt")
	if !big.Collapsed || big.CollapseReason != ReasonLarge || big.Additions != 600 || big.Patch == "" {
		t.Fatalf("file oltre 500 righe: %+v", big)
	}
	if d.FilesChanged != 2 {
		t.Fatalf("file: %+v", d.Files)
	}
}

func TestCommitMergeAgainstFirstParent(t *testing.T) {
	f := newFixture(t)
	h := f.history()
	d := f.detail(h.merge, false)
	if len(d.Commit.Parents) != 2 || d.Commit.Parents[0] != h.c3 {
		t.Fatalf("genitori: %v", d.Commit.Parents)
	}
	// Contro il primo genitore (c3) il merge porta solo f.txt.
	if d.FilesChanged != 1 || d.Files[0].Path != "f.txt" || d.Files[0].Status != "added" {
		t.Fatalf("merge: %+v", d.Files)
	}
}

func TestCommitWhitespace(t *testing.T) {
	f := newFixture(t)
	f.write("w.txt", []byte("uno\ndue\n"))
	f.commit(alice, "base")
	f.write("w.txt", []byte("uno  \n  due\n"))
	c := f.commit(alice, "solo spazi")

	d := f.detail(c, false)
	if w := fileByPath(t, d, "w.txt"); w.Additions != 2 || w.Deletions != 2 || d.IgnoreWhitespace {
		t.Fatalf("senza -w: %+v", w)
	}
	d = f.detail(c, true)
	if !d.IgnoreWhitespace || d.Additions != 0 || d.Deletions != 0 {
		t.Fatalf("con -w: %+v", d)
	}
	if len(d.Files) != 0 || d.FilesChanged != 0 {
		t.Fatalf("con -w il file solo-spazi non compare: %+v", d.Files)
	}
}

func TestCommitListOnlyOverFileLimit(t *testing.T) {
	f := newFixture(t)
	f.write("seed", []byte("x\n"))
	f.commit(alice, "seed")
	for i := 0; i < MaxFiles+1; i++ {
		f.write(fmt.Sprintf("many/f%03d.txt", i), []byte("riga\n"))
	}
	c := f.commit(alice, "301 file")
	d := f.detail(c, false)
	if !d.ListOnly || !d.Truncated || d.FilesChanged != MaxFiles+1 || len(d.Files) != MaxFiles {
		t.Fatalf("301 file: listOnly=%v truncated=%v changed=%d files=%d", d.ListOnly, d.Truncated, d.FilesChanged, len(d.Files))
	}
	if d.Additions != MaxFiles+1 {
		t.Fatalf("additions: %d", d.Additions)
	}
	for _, fd := range d.Files {
		if fd.Patch != "" || fd.Additions != 1 {
			t.Fatalf("senza patch, con le righe: %+v", fd)
		}
	}

	// Esattamente 300 file: patch presenti.
	f.write("seed2", []byte("y\n"))
	f.commit(alice, "seed2")
	for i := 0; i < MaxFiles; i++ {
		f.write(fmt.Sprintf("ok/f%03d.txt", i), []byte("riga\n"))
	}
	c = f.commit(alice, "300 file")
	d = f.detail(c, false)
	if d.ListOnly || d.Truncated || len(d.Files) != MaxFiles || d.Files[0].Patch == "" {
		t.Fatalf("300 file: listOnly=%v truncated=%v n=%d", d.ListOnly, d.Truncated, len(d.Files))
	}
}

func TestCommitListOnlyOverLineLimit(t *testing.T) {
	f := newFixture(t)
	f.write("seed", []byte("x\n"))
	f.commit(alice, "seed")
	f.write("huge.txt", lines(MaxLines+1, "h"))
	c := f.commit(alice, "20001 righe")
	d := f.detail(c, false)
	if !d.ListOnly || !d.Truncated || d.Additions != MaxLines+1 || len(d.Files) != 1 {
		t.Fatalf("oltre 20000 righe: %+v", d)
	}
	if fd := d.Files[0]; fd.Patch != "" || fd.Additions != MaxLines+1 || !fd.Collapsed || fd.CollapseReason != ReasonLarge {
		t.Fatalf("file: %+v", fd)
	}

	f.write("huge2.txt", lines(MaxLines, "h"))
	c = f.commit(alice, "20000 righe")
	d = f.detail(c, false)
	if d.ListOnly || d.Files[0].Patch == "" {
		t.Fatalf("20000 righe esatte: listOnly=%v", d.ListOnly)
	}
}

func TestCommitErrors(t *testing.T) {
	f := newFixture(t)
	f.history()
	ctx := context.Background()
	if _, err := f.svc.Commit(ctx, "r1", "xyz1234", false); !errors.Is(err, gitref.ErrInvalidSHA) {
		t.Fatalf("non esadecimale: %v", err)
	}
	if _, err := f.svc.Commit(ctx, "r1", "abc12", false); !errors.Is(err, gitref.ErrInvalidSHA) {
		t.Fatalf("troppo corto: %v", err)
	}
	if _, err := f.svc.Commit(ctx, "r1", strings.Repeat("a", 40), false); !errors.Is(err, gitref.ErrRefNotFound) {
		t.Fatalf("inesistente: %v", err)
	}
	// Lo sha di un albero non è un commit.
	tree := f.git(alice, "rev-parse", "HEAD^{tree}")
	if _, err := f.svc.Commit(ctx, "r1", tree, false); !errors.Is(err, gitref.ErrRefNotFound) {
		t.Fatalf("albero: %v", err)
	}
}

func TestBlameMultipleAuthors(t *testing.T) {
	f := newFixture(t)
	f.write("m.txt", []byte("a1\na2\na3\na4\n"))
	c1 := f.commit(alice, "alice scrive")
	f.write("m.txt", []byte("a1\nB2\nB2bis\na3\na4\n"))
	c2 := f.commit(bob, "bob cambia")
	f.write("m.txt", []byte("a1\nB2\nB2bis\na3\nA4\nA5\n"))
	c3 := f.commit(alice, "alice ancora")

	b, err := f.svc.Blame(context.Background(), "r1", "main", "m.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		start, end int
		sha        string
		email      string
	}{
		{1, 1, c1, "alice@example.com"},
		{2, 3, c2, "bob@example.com"},
		{4, 4, c1, "alice@example.com"},
		{5, 6, c3, "alice@example.com"},
	}
	if len(b.Ranges) != len(want) || b.Path != "m.txt" || b.Ref != "main" {
		t.Fatalf("blame: %+v", b)
	}
	for i, w := range want {
		g := b.Ranges[i]
		if g.StartLine != w.start || g.EndLine != w.end || g.Commit.SHA != w.sha || g.Commit.Author.Email != w.email {
			t.Errorf("range %d: %+v, voglio %+v", i, g, w)
		}
		if g.Commit.Author.Date.IsZero() || g.Commit.Subject == "" {
			t.Errorf("range %d senza data o oggetto: %+v", i, g.Commit)
		}
	}
	if b.Ranges[1].Commit.Subject != "bob cambia" {
		t.Fatalf("subject: %q", b.Ranges[1].Commit.Subject)
	}
}

func TestBlameRefusals(t *testing.T) {
	f := newFixture(t)
	f.write("ok.txt", []byte("x\n"))
	f.write("bin.dat", []byte{'a', 0, 'b'})
	f.write("limite.txt", bytes.Repeat([]byte("a"), MaxBlameBytes-1)[:MaxBlameBytes-1])
	f.write("grande.txt", append(bytes.Repeat([]byte("a"), MaxBlameBytes), '\n'))
	f.write("dir/x.txt", []byte("x\n"))
	f.commit(alice, "file")
	ctx := context.Background()

	if _, err := f.svc.Blame(ctx, "r1", "main", "grande.txt"); !errors.Is(err, ErrBlameUnavailable) {
		t.Fatalf("oltre 1 MB: %v", err)
	}
	if _, err := f.svc.Blame(ctx, "r1", "main", "bin.dat"); !errors.Is(err, ErrBlameUnavailable) {
		t.Fatalf("binario: %v", err)
	}
	if _, err := f.svc.Blame(ctx, "r1", "main", "limite.txt"); err != nil {
		t.Fatalf("1 MB esatto (meno un byte) ammesso: %v", err)
	}
	if _, err := f.svc.Blame(ctx, "r1", "main", "manca.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inesistente: %v", err)
	}
	if _, err := f.svc.Blame(ctx, "r1", "main", "dir"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cartella: %v", err)
	}
	if _, err := f.svc.Blame(ctx, "r1", "main", ""); !errors.Is(err, gitref.ErrInvalidPath) {
		t.Fatalf("path vuoto: %v", err)
	}
	if _, err := f.svc.Blame(ctx, "r1", "main", "../x"); !errors.Is(err, gitref.ErrInvalidPath) {
		t.Fatalf("path ..: %v", err)
	}
	if _, err := f.svc.Blame(ctx, "r1", "nonesiste", "ok.txt"); !errors.Is(err, gitref.ErrRefNotFound) {
		t.Fatalf("ref inesistente: %v", err)
	}
}

func TestCollapseReason(t *testing.T) {
	cases := []struct {
		path string
		n    int
		want string
	}{
		{"package-lock.json", 1, ReasonLock},
		{"web/pnpm-lock.yaml", 1, ReasonLock},
		{"services/git/go.sum", 3, ReasonLock},
		{"Cargo.lock", 1, ReasonLock},
		{"yarn.lock", 1, ReasonLock},
		{"web/dist/app.min.js", 1, ReasonGenerated},
		{"style.MIN.css", 1, ReasonGenerated},
		{"api/x.pb.go", 1, ReasonGenerated},
		{"src/main.go", 500, ""},
		{"src/main.go", 501, ReasonLarge},
		{"go.sum", 9999, ReasonLock},
	}
	for _, c := range cases {
		if got := CollapseReason(c.path, c.n); got != c.want {
			t.Errorf("%s/%d = %q, voglio %q", c.path, c.n, got, c.want)
		}
	}
}

// applyOn clona il repo di prova al commit base (o vuoto) e prova ad
// applicare il file: git apply deve accettarlo e dare lo stesso albero.
func (f *fixture) applyOn(base, patch string, wantTree string) {
	f.t.Helper()
	dst := f.t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "core.autocrlf=false", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
		cmd.Dir = dst
		cmd.Env = append(cleanEnv(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			f.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet")
	if base != "" {
		run("fetch", "--quiet", f.gd, base)
		run("checkout", "--quiet", "--detach", base)
	}
	run("apply", "--index", patch)
	if got := run("write-tree"); got != wantTree {
		f.t.Fatalf("albero dopo apply = %s, voglio %s", got, wantTree)
	}
}

func (f *fixture) download(sha, format string) string {
	f.t.Helper()
	d, err := f.svc.PrepareDownload(context.Background(), "r1", sha, format, false)
	if err != nil {
		f.t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := d.WriteTo(context.Background(), &buf); err != nil {
		f.t.Fatal(err)
	}
	p := filepath.Join(f.t.TempDir(), d.Filename)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if !strings.HasSuffix(d.Filename, "."+format) {
		f.t.Fatalf("nome: %s", d.Filename)
	}
	return p
}

func TestDownloadDiffAndPatchApply(t *testing.T) {
	f := newFixture(t)
	h := f.history()
	for _, c := range []struct{ name, sha, parent string }{
		{"iniziale", h.c1, ""},
		{"normale", h.c2, h.c1},
		{"rinomina", h.c3, h.c2},
		{"merge", h.merge, h.c3},
	} {
		tree := f.git(alice, "rev-parse", c.sha+"^{tree}")
		if c.name == "merge" {
			// Contro il primo genitore il risultato è comunque l'albero del merge.
			tree = f.git(alice, "rev-parse", h.merge+"^{tree}")
		}
		for _, format := range []string{FormatDiff, FormatPatch} {
			t.Run(c.name+"/"+format, func(t *testing.T) {
				p := f.download(c.sha, format)
				f.applyOn(c.parent, p, tree)
			})
		}
	}
	// La patch è nel formato di format-patch (mbox con oggetto).
	b, _ := os.ReadFile(f.download(h.c2, FormatPatch))
	if !strings.HasPrefix(string(b), "From "+h.c2+" ") || !strings.Contains(string(b), "Subject: [PATCH] Secondo") {
		t.Fatalf("intestazione patch: %.200s", b)
	}
	// Anche il binario passa (--binary): bin.dat è nel commit iniziale, già verificato da applyOn.
}

func TestDownloadErrors(t *testing.T) {
	f := newFixture(t)
	f.history()
	ctx := context.Background()
	if _, err := f.svc.PrepareDownload(ctx, "r1", "zzzzzzz", FormatDiff, false); !errors.Is(err, gitref.ErrInvalidSHA) {
		t.Fatalf("sha: %v", err)
	}
	if _, err := f.svc.PrepareDownload(ctx, "r1", strings.Repeat("b", 40), FormatPatch, false); !errors.Is(err, gitref.ErrRefNotFound) {
		t.Fatalf("inesistente: %v", err)
	}
	if _, err := f.svc.PrepareDownload(ctx, "r1", strings.Repeat("b", 40), "zip", false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("formato: %v", err)
	}
}

func TestRunnerTimeout(t *testing.T) {
	f := newFixture(t)
	f.history()
	r, _ := gitrun.New()
	r.Timeout = 1 // 1 ns
	svc := New(r, func(string) (string, error) { return f.gd, nil })
	_, err := svc.Commits(context.Background(), "r1", CommitsQuery{Ref: "main"})
	if err == nil {
		t.Fatal("con un timeout di 1 ns mi aspetto un errore")
	}
}

// cleanEnv toglie le GIT_* ereditate (hook e configurazione dell'ambiente).
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}

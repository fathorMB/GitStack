package repostore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const id1 = "0a1b2c3d-1111-4222-8333-444455556666"

func newStore(t *testing.T) *Store {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non trovato nel PATH")
	}
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func TestCreate_Vuoto(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	empty, err := s.Create(ctx, id1, CreateOptions{})
	if err != nil || !empty {
		t.Fatalf("Create = %v, %v", empty, err)
	}
	p, _ := s.RepoPath(id1)
	if !strings.HasSuffix(strings.ReplaceAll(p, "\\", "/"), "/repos/0a/"+id1+".git") {
		t.Fatalf("percorso inatteso: %s", p)
	}
	if head := strings.TrimSpace(gitOut(t, p, "symbolic-ref", "HEAD")); head != "refs/heads/main" {
		t.Fatalf("HEAD = %s", head)
	}
	info, err := s.Get(ctx, id1)
	if err != nil || info.Trashed || !info.Empty {
		t.Fatalf("Get = %+v, %v", info, err)
	}
	if _, err := s.Create(ctx, id1, CreateOptions{}); !errors.Is(err, ErrExists) {
		t.Fatalf("doppione: %v", err)
	}
	// Nessun residuo in tmp.
	if ents, _ := os.ReadDir(s.root + "/tmp"); len(ents) != 0 {
		t.Fatalf("residui in tmp: %v", ents)
	}
}

func TestCreate_BranchPersonalizzato(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create(context.Background(), id1, CreateOptions{DefaultBranch: "trunk"}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.RepoPath(id1)
	if head := strings.TrimSpace(gitOut(t, p, "symbolic-ref", "HEAD")); head != "refs/heads/trunk" {
		t.Fatalf("HEAD = %s", head)
	}
}

func TestCreate_PrimoCommit(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	empty, err := s.Create(ctx, id1, CreateOptions{
		Files: []File{
			{Path: "README.md", Content: []byte("# demo\n")},
			{Path: ".gitignore", Content: []byte("*.log\n")},
			{Path: "LICENSE", Content: []byte("MIT\n")},
		},
		Author: Author{Name: "Alice", Email: "alice@example.com"},
		Now:    time.Unix(1700000000, 0),
	})
	if err != nil || empty {
		t.Fatalf("Create = %v, %v", empty, err)
	}
	p, _ := s.RepoPath(id1)
	ls := gitOut(t, p, "ls-tree", "--name-only", "main")
	for _, f := range []string{"README.md", ".gitignore", "LICENSE"} {
		if !strings.Contains(ls, f) {
			t.Fatalf("manca %s in:\n%s", f, ls)
		}
	}
	if got := strings.TrimSpace(gitOut(t, p, "show", "-s", "--format=%an <%ae>|%cn|%at", "main")); got != "Alice <alice@example.com>|Alice|1700000000" {
		t.Fatalf("commit: %s", got)
	}
	if got := gitOut(t, p, "show", "main:README.md"); got != "# demo\n" {
		t.Fatalf("README = %q", got)
	}
	info, _ := s.Get(ctx, id1)
	if info.Empty {
		t.Fatal("con un commit il repo non è vuoto")
	}
}

func TestCreate_Rifiuti(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	cases := map[string]struct {
		id   string
		opts CreateOptions
		want error
	}{
		"id":           {"../x", CreateOptions{}, ErrInvalidID},
		"branch":       {id1, CreateOptions{DefaultBranch: "--bad"}, ErrInvalidInput},
		"branch2":      {id1, CreateOptions{DefaultBranch: "a..b"}, ErrInvalidInput},
		"senza autore": {id1, CreateOptions{Files: []File{{Path: "a", Content: []byte("x")}}}, ErrInvalidInput},
		"percorso":     {id1, CreateOptions{Files: []File{{Path: "../a"}}, Author: Author{Name: "a", Email: "a@b"}}, ErrInvalidInput},
	}
	for name, c := range cases {
		if _, err := s.Create(ctx, c.id, c.opts); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, voluto %v", name, err, c.want)
		}
	}
	if _, err := s.Get(ctx, id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nessun repo doveva nascere: %v", err)
	}
}

func TestCestinoRipristinoPurge(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Trash(id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Trash di un repo inesistente: %v", err)
	}
	if _, err := s.Create(ctx, id1, CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	// Purge di un repo attivo: rifiutato.
	if err := s.Purge(id1); !errors.Is(err, ErrNotTrashed) {
		t.Fatalf("Purge attivo: %v", err)
	}
	if err := s.Restore(id1); !errors.Is(err, ErrNotTrashed) {
		t.Fatalf("Restore attivo: %v", err)
	}

	if err := s.Trash(id1); err != nil {
		t.Fatal(err)
	}
	tp, _ := s.TrashPath(id1)
	if _, err := os.Stat(tp); err != nil {
		t.Fatalf("manca il repo nel cestino: %v", err)
	}
	if info, err := s.Get(ctx, id1); err != nil || !info.Trashed {
		t.Fatalf("Get dopo Trash = %+v, %v", info, err)
	}
	if err := s.Trash(id1); !errors.Is(err, ErrAlreadyTrashd) {
		t.Fatalf("doppio Trash: %v", err)
	}
	// Il nome (l'id) resta occupato finché non si cancella.
	if _, err := s.Create(ctx, id1, CreateOptions{}); !errors.Is(err, ErrExists) {
		t.Fatalf("Create su un repo nel cestino: %v", err)
	}

	if err := s.Restore(id1); err != nil {
		t.Fatal(err)
	}
	if info, err := s.Get(ctx, id1); err != nil || info.Trashed {
		t.Fatalf("Get dopo Restore = %+v, %v", info, err)
	}

	if err := s.Trash(id1); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(id1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get dopo Purge: %v", err)
	}
	if _, err := os.Stat(tp); !os.IsNotExist(err) {
		t.Fatalf("il cestino non è stato svuotato: %v", err)
	}
	if err := s.Purge(id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("doppio Purge: %v", err)
	}
}

func TestReady(t *testing.T) {
	s := newStore(t)
	if err := s.Ready(); err != nil {
		t.Fatal(err)
	}
	s2, _ := NewWithGit(s.root+"/non-esiste", "git")
	if err := s2.Ready(); err == nil {
		t.Fatal("Ready deve fallire se la directory non esiste")
	}
}

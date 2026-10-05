// Package repostore gestisce i repo bare su disco con il binario `git`
// ufficiale (os/exec).
//
// Layout sotto la directory dei dati:
//
//	<root>/repos/<id[0:2]>/<id>.git   repo attivo
//	<root>/trash/<id>.git             repo nel cestino
//	<root>/tmp/                       creazioni in corso (area di lavoro)
//
// Il percorso deriva solo dall'id (UUID validato), mai dal nome: nulla
// dipende da owner/repo, quindi una rinomina non sposta niente su disco.
package repostore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Errori del pacchetto.
var (
	ErrInvalidID     = errors.New("repostore: id non valido (serve un UUID)")
	ErrInvalidInput  = errors.New("repostore: dati di creazione non validi")
	ErrExists        = errors.New("repostore: il repo esiste già")
	ErrNotFound      = errors.New("repostore: repo non trovato")
	ErrNotTrashed    = errors.New("repostore: il repo non è nel cestino")
	ErrAlreadyTrashd = errors.New("repostore: il repo è già nel cestino")
)

// DefaultBranch è il branch di HEAD se non se ne indica un altro.
const DefaultBranch = "main"

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// File è un file del primo commit, nella radice del repo (percorso senza "/").
type File struct {
	Path    string
	Content []byte
}

// Author è l'autore (e committer) del primo commit.
type Author struct {
	Name  string
	Email string
}

// CreateOptions sono le opzioni di Create.
type CreateOptions struct {
	// DefaultBranch è il branch di HEAD; vuoto = DefaultBranch.
	DefaultBranch string
	// Files, se non vuoto, produce un primo commit sul branch. Richiede Author.
	Files  []File
	Author Author
	// Now è l'orario del commit; zero = time.Now().
	Now time.Time
}

// Info è lo stato di un repo.
type Info struct {
	Trashed bool
	// Empty è true se sotto refs/heads non c'è nessun ref.
	Empty bool
	// Branches sono i nomi sotto refs/heads, in ordine alfabetico (R4).
	Branches []string
}

// Store è l'archivio dei repo.
type Store struct {
	root string
	git  string
}

// New crea lo Store sulla directory root (che deve esistere) e cerca il
// binario git nel PATH.
func New(root string) (*Store, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("repostore: binario git non trovato: %w", err)
	}
	return NewWithGit(root, bin)
}

// NewWithGit è New con il percorso del binario git già noto.
func NewWithGit(root, gitBin string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Store{root: abs, git: gitBin}, nil
}

// NormalizeID valida un UUID e lo riporta in minuscolo.
func NormalizeID(id string) (string, error) {
	id = strings.ToLower(id)
	if !uuidRe.MatchString(id) {
		return "", ErrInvalidID
	}
	return id, nil
}

// RepoPath è il percorso del repo attivo.
func (s *Store) RepoPath(id string) (string, error) {
	id, err := NormalizeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, "repos", id[:2], id+".git"), nil
}

// TrashPath è il percorso del repo nel cestino.
func (s *Store) TrashPath(id string) (string, error) {
	id, err := NormalizeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, "trash", id+".git"), nil
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// Dir è il percorso del repo bare attivo per le letture, o ErrNotFound se non
// c'è (un repo nel cestino non si legge).
func (s *Store) Dir(id string) (string, error) {
	p, err := s.RepoPath(id)
	if err != nil {
		return "", err
	}
	if !exists(p) {
		return "", ErrNotFound
	}
	return p, nil
}

// Ready verifica che la directory dei dati esista e sia scrivibile.
func (s *Store) Ready() error {
	st, err := os.Stat(s.root)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s non è una directory", s.root)
	}
	f, err := os.CreateTemp(s.root, ".ready-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// Create crea un repo bare con HEAD su opts.DefaultBranch ed eventualmente
// il primo commit. Torna ErrExists se l'id è già in uso (anche nel cestino).
// Il repo compare sotto repos/ solo a creazione finita (rename atomico).
// Torna empty (nessun commit).
func (s *Store) Create(ctx context.Context, id string, opts CreateOptions) (empty bool, err error) {
	id, err = NormalizeID(id)
	if err != nil {
		return false, err
	}
	branch := opts.DefaultBranch
	if branch == "" {
		branch = DefaultBranch
	}
	if err := s.checkBranch(ctx, branch); err != nil {
		return false, err
	}
	for _, f := range opts.Files {
		if f.Path == "" || strings.ContainsAny(f.Path, "/\\\x00\n") || f.Path == "." || f.Path == ".." {
			return false, fmt.Errorf("%w: percorso %q", ErrInvalidInput, f.Path)
		}
	}
	if len(opts.Files) > 0 && (strings.TrimSpace(opts.Author.Name) == "" || strings.TrimSpace(opts.Author.Email) == "") {
		return false, fmt.Errorf("%w: autore mancante", ErrInvalidInput)
	}

	final, _ := s.RepoPath(id)
	trash, _ := s.TrashPath(id)
	if exists(final) || exists(trash) {
		return false, ErrExists
	}

	tmpRoot := filepath.Join(s.root, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return false, err
	}
	tmp, err := os.MkdirTemp(tmpRoot, id+"-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	work := filepath.Join(tmp, id+".git")

	if _, err := s.run(ctx, nil, nil, "init", "--bare", "--quiet", "--initial-branch="+branch, work); err != nil {
		return false, err
	}
	if len(opts.Files) > 0 {
		if err := s.firstCommit(ctx, work, branch, opts); err != nil {
			return false, err
		}
	}

	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return false, err
	}
	if err := os.Rename(work, final); err != nil {
		if exists(final) {
			return false, ErrExists
		}
		return false, err
	}
	return len(opts.Files) == 0, nil
}

func (s *Store) checkBranch(ctx context.Context, branch string) error {
	if strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, "\x00\n") {
		return fmt.Errorf("%w: branch %q", ErrInvalidInput, branch)
	}
	if _, err := s.run(ctx, nil, nil, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("%w: branch %q", ErrInvalidInput, branch)
	}
	return nil
}

// firstCommit costruisce il commit senza worktree: hash-object -w, mktree,
// commit-tree, update-ref.
func (s *Store) firstCommit(ctx context.Context, dir, branch string, opts CreateOptions) error {
	var tree bytes.Buffer
	for _, f := range opts.Files {
		out, err := s.run(ctx, []string{"-C", dir}, bytes.NewReader(f.Content), "hash-object", "-w", "--stdin")
		if err != nil {
			return err
		}
		fmt.Fprintf(&tree, "100644 blob %s\t%s\n", strings.TrimSpace(out), f.Path)
	}
	treeID, err := s.run(ctx, []string{"-C", dir}, &tree, "mktree")
	if err != nil {
		return err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	date := fmt.Sprintf("%d +0000", now.Unix())
	env := []string{
		"GIT_AUTHOR_NAME=" + opts.Author.Name, "GIT_AUTHOR_EMAIL=" + opts.Author.Email, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + opts.Author.Name, "GIT_COMMITTER_EMAIL=" + opts.Author.Email, "GIT_COMMITTER_DATE=" + date,
	}
	commit, err := s.runEnv(ctx, []string{"-C", dir}, nil, env, "commit-tree", strings.TrimSpace(treeID), "-m", "Initial commit")
	if err != nil {
		return err
	}
	_, err = s.run(ctx, []string{"-C", dir}, nil, "update-ref", "refs/heads/"+branch, strings.TrimSpace(commit))
	return err
}

// Get ritorna lo stato del repo, o ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Info, error) {
	active, err := s.RepoPath(id)
	if err != nil {
		return Info{}, err
	}
	trash, _ := s.TrashPath(id)
	info := Info{}
	dir := active
	switch {
	case exists(active):
	case exists(trash):
		info.Trashed = true
		dir = trash
	default:
		return Info{}, ErrNotFound
	}
	out, err := s.run(ctx, []string{"-C", dir}, nil, "for-each-ref", "--format=%(refname:lstrip=2)", "--sort=refname", "refs/heads")
	if err != nil {
		return Info{}, err
	}
	info.Branches = []string{}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			info.Branches = append(info.Branches, l)
		}
	}
	info.Empty = len(info.Branches) == 0
	return info, nil
}

// Trash sposta il repo nel cestino.
func (s *Store) Trash(id string) error {
	src, err := s.RepoPath(id)
	if err != nil {
		return err
	}
	dst, _ := s.TrashPath(id)
	if !exists(src) {
		if exists(dst) {
			return ErrAlreadyTrashd
		}
		return ErrNotFound
	}
	if exists(dst) {
		return ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// Restore riporta il repo dal cestino.
func (s *Store) Restore(id string) error {
	dst, err := s.RepoPath(id)
	if err != nil {
		return err
	}
	src, _ := s.TrashPath(id)
	if !exists(src) {
		if exists(dst) {
			return ErrNotTrashed
		}
		return ErrNotFound
	}
	if exists(dst) {
		return ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// Purge cancella definitivamente un repo, solo se è nel cestino.
func (s *Store) Purge(id string) error {
	active, err := s.RepoPath(id)
	if err != nil {
		return err
	}
	trash, _ := s.TrashPath(id)
	if !exists(trash) {
		if exists(active) {
			return ErrNotTrashed
		}
		return ErrNotFound
	}
	return os.RemoveAll(trash)
}

func (s *Store) run(ctx context.Context, pre []string, stdin io.Reader, args ...string) (string, error) {
	return s.runEnv(ctx, pre, stdin, nil, args...)
}

func (s *Store) runEnv(ctx context.Context, pre []string, stdin io.Reader, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, s.git, append(append([]string{}, pre...), args...)...)
	// Ambiente isolato: né configurazione di sistema né dell'utente, niente prompt.
	cmd.Env = append(cleanEnv(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	cmd.Env = append(cmd.Env, env...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", firstArg(args), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func firstArg(a []string) string {
	if len(a) == 0 {
		return ""
	}
	return a[0]
}

// cleanEnv è l'ambiente del processo senza le variabili GIT_*: una
// GIT_CONFIG_COUNT o GIT_DIR ereditata dal contenitore cambierebbe il
// comportamento di git (hook, percorsi) rispetto a quello atteso.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

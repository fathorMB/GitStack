// Package gitmerge fa sul server, senza un checkout di lavoro, quello che serve
// a una Pull Request: dice se due branch si possono unire, esegue il merge
// (merge commit o squash, mai rebase: PR3) e elimina un branch.
//
// Il merge usa solo plumbing: `merge-tree --write-tree` calcola l'albero
// risultante nel database degli oggetti, `commit-tree` scrive il commit e
// `update-ref <ref> <nuovo> <atteso>` sposta il ref solo se punta ancora allo
// sha atteso, così due merge concorrenti (o un push in mezzo) non si
// sovrascrivono. Il merge e l'eliminazione pubblicano git.push come un push
// (pushevent), quindi i collegamenti C1/C2 delle issue funzionano uguale.
package gitmerge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
)

// Errori del pacchetto, oltre a quelli di gitref.
var (
	// ErrInvalid: parametro non valido.
	ErrInvalid = errors.New("gitmerge: parametro non valido")
	// ErrStale: un ref non punta più allo sha atteso; nulla è stato cambiato.
	ErrStale = errors.New("gitmerge: il ref è cambiato")
	// ErrNothingToMerge: head è già contenuto in base.
	ErrNothingToMerge = errors.New("gitmerge: niente da unire")
	// ErrUnrelated: i due branch non hanno storia in comune.
	ErrUnrelated = errors.New("gitmerge: storie non correlate")
	// ErrDefaultBranch: il branch principale non si elimina.
	ErrDefaultBranch = errors.New("gitmerge: il branch principale non si elimina")
)

// ConflictError: il merge ha conflitti; Files li elenca.
type ConflictError struct{ Files []string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("gitmerge: conflitti in %d file", len(e.Files))
}

// StaleError porta lo sha corrente del ref che non combaciava.
type StaleError struct {
	Ref     string
	Current string
}

func (e *StaleError) Error() string { return "gitmerge: il ref " + e.Ref + " è cambiato" }
func (e *StaleError) Unwrap() error { return ErrStale }

// Limiti.
const (
	MaxMessageBytes = 64 << 10
	maxIdentityLen  = 255
	gitTimeout      = 60 * time.Second
)

// Metodi di merge.
const (
	MethodMerge  = "merge"
	MethodSquash = "squash"
)

// Stati di unibilità.
const (
	// StateClean: base è antenato di head, il merge è senza sorprese.
	StateClean = "clean"
	// StateBehind: head è indietro rispetto a base ma si unisce senza conflitti.
	StateBehind = "behind"
	// StateConflicts: il merge ha conflitti (Files li elenca).
	StateConflicts = "conflicts"
	// StateEmpty: head è già contenuto in base, niente da unire.
	StateEmpty = "empty"
	// StateUnrelated: nessun antenato in comune.
	StateUnrelated = "unrelated"
)

// Service esegue le operazioni.
type Service struct {
	Run *gitrun.Runner
	// Dir risolve l'id del repo nel repo bare (errore se non esiste).
	Dir func(repoID string) (string, error)
	// Events pubblica git.push; nil = nessun evento.
	Events *pushevent.Notifier
	// Now è iniettabile per i test (nil = time.Now).
	Now func() time.Time

	locks sync.Map // dir → *sync.Mutex
}

// ErrRepo avvolge l'errore di risoluzione del repo.
var ErrRepo = errors.New("gitmerge: repo")

func (s *Service) repoDir(repoID string) (string, error) {
	d, err := s.Dir(repoID)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrRepo, err)
	}
	return d, nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// lock serializza le scritture sullo stesso repo: l'evento git.push è fatto
// da una foto dei ref prima e dopo, e due scritture sovrapposte se la
// passerebbero a vicenda. La correttezza del ref non dipende da questo
// lock (la garantisce update-ref con lo sha atteso).
func (s *Service) lock(dir string) func() {
	m, _ := s.locks.LoadOrStore(dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Status è l'esito di Mergeability.
type Status struct {
	Base      string   `json:"base"`
	Head      string   `json:"head"`
	BaseSHA   string   `json:"baseSha"`
	HeadSHA   string   `json:"headSha"`
	MergeBase string   `json:"mergeBase"`
	State     string   `json:"state"`
	Mergeable bool     `json:"mergeable"`
	Conflicts []string `json:"conflicts"`
}

// Mergeability dice se head si può unire in base. base è un branch.
func (s *Service) Mergeability(ctx context.Context, repoID, base, head string) (*Status, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	baseSHA, err := s.branchSHA(ctx, dir, base)
	if err != nil {
		return nil, err
	}
	headSHA, err := gitref.Resolve(ctx, s.Run, dir, head)
	if err != nil {
		return nil, err
	}
	st := &Status{Base: base, Head: head, BaseSHA: baseSHA, HeadSHA: headSHA, Conflicts: []string{}}
	mb, err := s.mergeBase(ctx, dir, baseSHA, headSHA)
	if err != nil {
		return nil, err
	}
	st.MergeBase = mb
	switch {
	case mb == "":
		st.State = StateUnrelated
		return st, nil
	case mb == headSHA:
		st.State = StateEmpty
		return st, nil
	}
	_, _, err = s.mergeTree(ctx, dir, baseSHA, headSHA)
	var ce *ConflictError
	switch {
	case errors.As(err, &ce):
		st.State, st.Conflicts = StateConflicts, ce.Files
	case err != nil:
		return nil, err
	case mb == baseSHA:
		st.State, st.Mergeable = StateClean, true
	default:
		st.State, st.Mergeable = StateBehind, true
	}
	return st, nil
}

// Identity è un autore o un committer.
type Identity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (i Identity) validate(what string) error {
	for _, v := range []string{i.Name, i.Email} {
		if strings.TrimSpace(v) == "" || len(v) > maxIdentityLen || strings.ContainsAny(v, "\x00\n\r<>") {
			return fmt.Errorf("%w: %s {name, email}", ErrInvalid, what)
		}
	}
	return nil
}

// Target descrive il repo per l'evento git.push.
type Target struct {
	Owner, Name   string
	DefaultBranch string // vuoto = il branch di HEAD
	Pusher        gitpush.Pusher
}

// MergeInput è la richiesta di merge.
type MergeInput struct {
	// Base è il branch in cui si unisce; Head il branch (o lo sha) da unire.
	Base, Head string
	// ExpectedBaseSHA è obbligatorio: il merge fallisce con ErrStale se base
	// punta altrove. ExpectedHeadSHA, se presente, vale lo stesso per head.
	ExpectedBaseSHA string
	ExpectedHeadSHA string
	Method          string // MethodMerge | MethodSquash
	// Message è il messaggio del commit; per il merge commit, se vuoto, si usa
	// quello standard di git. Per lo squash è obbligatorio.
	Message   string
	Author    Identity
	Committer Identity // vuoto = Author
	Target    Target
}

// MergeResult è l'esito del merge.
type MergeResult struct {
	SHA       string   `json:"sha"`
	Method    string   `json:"method"`
	Parents   []string `json:"parents"`
	Tree      string   `json:"tree"`
	Base      string   `json:"base"`
	Head      string   `json:"head"`
	BaseSHA   string   `json:"baseSha"` // base prima del merge
	HeadSHA   string   `json:"headSha"`
	MergeBase string   `json:"mergeBase"`
}

// Merge unisce head in base. Errori: ErrInvalid, ErrStale (*StaleError),
// *ConflictError, ErrNothingToMerge, ErrUnrelated, gitref.ErrRefNotFound.
func (s *Service) Merge(ctx context.Context, repoID string, in MergeInput) (*MergeResult, error) {
	if in.Method != MethodMerge && in.Method != MethodSquash {
		return nil, fmt.Errorf("%w: method", ErrInvalid)
	}
	if !isFullSHA(in.ExpectedBaseSHA) || (in.ExpectedHeadSHA != "" && !isFullSHA(in.ExpectedHeadSHA)) {
		return nil, fmt.Errorf("%w: expected sha (40 o 64 cifre esadecimali minuscole)", ErrInvalid)
	}
	if len(in.Message) > MaxMessageBytes || strings.ContainsRune(in.Message, 0) {
		return nil, fmt.Errorf("%w: message", ErrInvalid)
	}
	if in.Method == MethodSquash && strings.TrimSpace(in.Message) == "" {
		return nil, fmt.Errorf("%w: message obbligatorio per lo squash", ErrInvalid)
	}
	if err := in.Author.validate("author"); err != nil {
		return nil, err
	}
	committer := in.Committer
	if committer == (Identity{}) {
		committer = in.Author
	}
	if err := committer.validate("committer"); err != nil {
		return nil, err
	}
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	defer s.lock(dir)()

	baseSHA, err := s.branchSHA(ctx, dir, in.Base)
	if err != nil {
		return nil, err
	}
	if baseSHA != in.ExpectedBaseSHA {
		return nil, &StaleError{Ref: "refs/heads/" + in.Base, Current: baseSHA}
	}
	headSHA, err := gitref.Resolve(ctx, s.Run, dir, in.Head)
	if err != nil {
		return nil, err
	}
	if in.ExpectedHeadSHA != "" && headSHA != in.ExpectedHeadSHA {
		return nil, &StaleError{Ref: in.Head, Current: headSHA}
	}
	mb, err := s.mergeBase(ctx, dir, baseSHA, headSHA)
	if err != nil {
		return nil, err
	}
	switch mb {
	case "":
		return nil, ErrUnrelated
	case headSHA:
		return nil, ErrNothingToMerge
	}
	tree, _, err := s.mergeTree(ctx, dir, baseSHA, headSHA)
	if err != nil {
		return nil, err
	}

	msg := in.Message
	parents := []string{baseSHA, headSHA}
	if in.Method == MethodSquash {
		parents = []string{baseSHA}
	} else if strings.TrimSpace(msg) == "" {
		msg = fmt.Sprintf("Merge branch '%s' into %s", in.Head, in.Base)
	}
	newSHA, err := s.commitTree(ctx, dir, tree, parents, msg, in.Author, committer)
	if err != nil {
		return nil, err
	}

	event := s.begin(ctx, dir, repoID, in.Target)
	if err := s.updateRef(ctx, dir, "refs/heads/"+in.Base, newSHA, baseSHA, "merge: "+in.Head); err != nil {
		return nil, err
	}
	event.Done()
	return &MergeResult{
		SHA: newSHA, Method: in.Method, Parents: parents, Tree: tree,
		Base: in.Base, Head: in.Head, BaseSHA: baseSHA, HeadSHA: headSHA, MergeBase: mb,
	}, nil
}

// DeleteInput è la richiesta di eliminazione di un branch.
type DeleteInput struct {
	Branch string
	// ExpectedSHA, se presente, deve essere lo sha corrente del branch.
	ExpectedSHA string
	Target      Target
}

// DeleteBranch elimina un branch (non il principale). Torna lo sha che aveva.
func (s *Service) DeleteBranch(ctx context.Context, repoID string, in DeleteInput) (string, error) {
	if in.ExpectedSHA != "" && !isFullSHA(in.ExpectedSHA) {
		return "", fmt.Errorf("%w: expected sha", ErrInvalid)
	}
	dir, err := s.repoDir(repoID)
	if err != nil {
		return "", err
	}
	defer s.lock(dir)()

	cur, err := s.branchSHA(ctx, dir, in.Branch)
	if err != nil {
		return "", err
	}
	head, err := s.headBranch(ctx, dir)
	if err != nil {
		return "", err
	}
	if in.Branch == head || in.Branch == in.Target.DefaultBranch {
		return "", ErrDefaultBranch
	}
	if in.ExpectedSHA != "" && in.ExpectedSHA != cur {
		return "", &StaleError{Ref: "refs/heads/" + in.Branch, Current: cur}
	}
	event := s.begin(ctx, dir, repoID, in.Target)
	ref := "refs/heads/" + in.Branch
	res, err := s.Run.RunEnv(ctx, dir, gitTimeout, nil, "update-ref", "-d", ref, cur)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", s.refFailure(ctx, dir, ref, cur, res)
	}
	event.Done()
	return cur, nil
}

func (s *Service) begin(ctx context.Context, dir, repoID string, t Target) *pushevent.Push {
	if t.DefaultBranch == "" {
		t.DefaultBranch, _ = s.headBranch(ctx, dir)
	}
	return s.Events.BeginAs(ctx, pushevent.Target{
		Dir: dir, RepoID: repoID, Owner: t.Owner, Name: t.Name, DefaultBranch: t.DefaultBranch,
	}, t.Pusher)
}

// headBranch è il nome corto del branch a cui punta HEAD.
func (s *Service) headBranch(ctx context.Context, dir string) (string, error) {
	out, err := s.Run.Output(ctx, dir, nil, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		var ge *gitrun.Error
		if errors.As(err, &ge) && ge.ExitCode == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// branchSHA risolve refs/heads/<name> nel commit a cui punta.
func (s *Service) branchSHA(ctx context.Context, dir, name string) (string, error) {
	if err := gitref.ValidateRef(name); err != nil {
		return "", err
	}
	out, err := s.Run.Output(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/"+name+"^{commit}")
	if err != nil {
		var ge *gitrun.Error
		if errors.As(err, &ge) && ge.ExitCode == 1 {
			return "", gitref.ErrRefNotFound
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// mergeBase torna il merge-base ("" se non c'è).
func (s *Service) mergeBase(ctx context.Context, dir, a, b string) (string, error) {
	out, err := s.Run.Output(ctx, dir, nil, "merge-base", a, b)
	if err != nil {
		var ge *gitrun.Error
		if errors.As(err, &ge) && ge.ExitCode == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// mergeTree calcola l'albero dell'unione. Con conflitti torna *ConflictError.
func (s *Service) mergeTree(ctx context.Context, dir, base, head string) (string, []string, error) {
	res, err := s.Run.RunEnv(ctx, dir, gitTimeout, nil, "merge-tree", "--write-tree", "-z", "--name-only", base, head)
	if err != nil {
		return "", nil, err
	}
	switch res.ExitCode {
	case 0, 1:
	default:
		return "", nil, &gitrun.Error{Cmd: "merge-tree", ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr)), Err: errors.New("uscita inattesa")}
	}
	// Formato: <albero> NUL (<file> NUL)* NUL <messaggi>. Con conflitti i
	// file compaiono una volta per ogni stadio: si tolgono i doppioni.
	tok := strings.Split(string(res.Stdout), "\x00")
	tree := tok[0]
	if !gitref.IsHexSHA(tree) || len(tree) < 40 {
		return "", nil, fmt.Errorf("gitmerge: risposta inattesa di merge-tree: %q", tree)
	}
	if res.ExitCode == 0 {
		return tree, nil, nil
	}
	files := []string{}
	seen := map[string]bool{}
	for _, f := range tok[1:] {
		if f == "" {
			break
		}
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	return tree, files, &ConflictError{Files: files}
}

// commitTree scrive il commit e torna il suo sha. Autore, committer e date
// passano da variabili d'ambiente del solo processo figlio.
func (s *Service) commitTree(ctx context.Context, dir, tree string, parents []string, msg string, author, committer Identity) (string, error) {
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-m", msg)
	date := s.now().UTC().Format("2006-01-02T15:04:05Z")
	env := []string{
		"GIT_AUTHOR_NAME=" + author.Name, "GIT_AUTHOR_EMAIL=" + author.Email, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + committer.Name, "GIT_COMMITTER_EMAIL=" + committer.Email, "GIT_COMMITTER_DATE=" + date,
	}
	res, err := s.Run.RunEnv(ctx, dir, gitTimeout, env, args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &gitrun.Error{Cmd: "commit-tree", ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr)), Err: errors.New("commit non scritto")}
	}
	sha := strings.TrimSpace(string(res.Stdout))
	if !isFullSHA(sha) {
		return "", fmt.Errorf("gitmerge: risposta inattesa di commit-tree: %q", sha)
	}
	return sha, nil
}

// updateRef sposta ref a newSHA solo se punta ancora a oldSHA.
func (s *Service) updateRef(ctx context.Context, dir, ref, newSHA, oldSHA, reason string) error {
	res, err := s.Run.RunEnv(ctx, dir, gitTimeout, nil, "update-ref", "-m", reason, ref, newSHA, oldSHA)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return s.refFailure(ctx, dir, ref, oldSHA, res)
	}
	return nil
}

// refFailure distingue «il ref è cambiato» (ErrStale) da un guasto vero.
func (s *Service) refFailure(ctx context.Context, dir, ref, wanted string, res gitrun.Result) error {
	out, err := s.Run.Output(ctx, dir, nil, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		var ge *gitrun.Error
		if errors.As(err, &ge) && ge.ExitCode == 1 {
			return &StaleError{Ref: ref}
		}
	} else if cur := strings.TrimSpace(string(out)); cur != wanted {
		return &StaleError{Ref: ref, Current: cur}
	}
	return &gitrun.Error{Cmd: "update-ref", ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr)), Err: errors.New("ref non aggiornato")}
}

func isFullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return gitref.IsHexSHA(s)
}

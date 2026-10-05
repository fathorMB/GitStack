// Package gitread implementa le letture del servizio git sulla storia di un
// repo: storico dei commit, dettaglio con diff, blame e download del diff.
// Parla solo per id del repo, non conosce utenti né permessi (li applica
// core): gli autori non portano `user`.
package gitread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
)

// Errori del pacchetto oltre a quelli di gitref.
var (
	// ErrNotFound: il percorso non esiste a quel ref o non è del tipo atteso.
	ErrNotFound = errors.New("gitread: non trovato")
	// ErrInvalidInput: parametro non valido (paginazione, filtro autore).
	ErrInvalidInput = errors.New("gitread: parametro non valido")
	// ErrBlameUnavailable: blame impossibile (file binario o oltre 1 MB).
	ErrBlameUnavailable = errors.New("gitread: blame non disponibile")
)

// Limiti della paginazione dello storico.
const (
	DefaultPerPage = 30
	MaxPerPage     = 100
	// MaxPage evita skip enormi: oltre, 400.
	MaxPage = 100000
)

// Person è l'autore o il committer di un commit (CommitPerson del contratto).
type Person struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Date  time.Time `json:"date"`
}

// Commit è un CommitSummary del contratto.
type Commit struct {
	SHA       string   `json:"sha"`
	Subject   string   `json:"subject"`
	Message   string   `json:"message,omitempty"`
	Author    Person   `json:"author"`
	Committer Person   `json:"committer"`
	Parents   []string `json:"parents"`
}

// CommitList è la pagina di storico (CommitList del contratto).
type CommitList struct {
	Items   []Commit `json:"items"`
	Page    int      `json:"page"`
	PerPage int      `json:"perPage"`
	HasMore bool     `json:"hasMore"`
}

// CommitsQuery sono i parametri dello storico.
type CommitsQuery struct {
	Ref     string
	Author  string
	Path    string
	Page    int // 0 = 1
	PerPage int // 0 = DefaultPerPage
}

// commitFormat: campi separati da 0x1f, record terminati da 0x1e.
const commitFormat = "%H%x1f%P%x1f%an%x1f%ae%x1f%aI%x1f%cn%x1f%ce%x1f%cI%x1f%B%x1e"

// Commits elenca i commit raggiungibili dal ref, dal più recente, con i
// filtri per autore (nome o email, senza distinguere maiuscole) e percorso.
func (s *Service) Commits(ctx context.Context, repoID string, q CommitsQuery) (*CommitList, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	page, perPage := q.Page, q.PerPage
	if page == 0 {
		page = 1
	}
	if perPage == 0 {
		perPage = DefaultPerPage
	}
	if page < 1 || page > MaxPage || perPage < 1 || perPage > MaxPerPage {
		return nil, fmt.Errorf("%w: page/perPage", ErrInvalidInput)
	}
	if len(q.Author) > 255 || strings.ContainsAny(q.Author, "\x00\n") {
		return nil, fmt.Errorf("%w: author", ErrInvalidInput)
	}
	path, err := gitref.ValidatePath(q.Path, false)
	if err != nil {
		return nil, err
	}
	sha, err := gitref.Resolve(ctx, s.run, dir, q.Ref)
	if err != nil {
		return nil, err
	}

	args := []string{"log", "--no-color", "--format=" + commitFormat,
		"--skip=" + strconv.Itoa((page-1)*perPage), "--max-count=" + strconv.Itoa(perPage+1)}
	if q.Author != "" {
		args = append(args, "--fixed-strings", "--regexp-ignore-case", "--author="+q.Author)
	}
	args = append(args, sha, "--")
	if path != "" {
		args = append(args, path)
	}
	out, err := s.run.Output(ctx, dir, nil, args...)
	if err != nil {
		return nil, err
	}
	commits, err := parseCommits(out)
	if err != nil {
		return nil, err
	}
	res := &CommitList{Items: commits, Page: page, PerPage: perPage}
	if len(commits) > perPage {
		res.Items = commits[:perPage]
		res.HasMore = true
	}
	if res.Items == nil {
		res.Items = []Commit{}
	}
	return res, nil
}

func parseCommits(out []byte) ([]Commit, error) {
	var res []Commit
	for _, rec := range bytes.Split(out, []byte{0x1e}) {
		rec = bytes.TrimLeft(rec, "\n")
		if len(rec) == 0 {
			continue
		}
		f := strings.SplitN(string(rec), "\x1f", 9)
		if len(f) != 9 {
			return nil, fmt.Errorf("gitread: record di commit inatteso (%d campi)", len(f))
		}
		ad, err := time.Parse(time.RFC3339, f[4])
		if err != nil {
			return nil, fmt.Errorf("gitread: data autore: %w", err)
		}
		cd, err := time.Parse(time.RFC3339, f[7])
		if err != nil {
			return nil, fmt.Errorf("gitread: data committer: %w", err)
		}
		msg := strings.TrimRight(f[8], "\n")
		subject, _, _ := strings.Cut(msg, "\n")
		c := Commit{
			SHA: f[0], Subject: subject, Message: msg, Parents: []string{},
			Author:    Person{Name: f[2], Email: f[3], Date: ad},
			Committer: Person{Name: f[5], Email: f[6], Date: cd},
		}
		if f[1] != "" {
			c.Parents = strings.Fields(f[1])
		}
		res = append(res, c)
	}
	return res, nil
}

// commitsByID legge i commit indicati (uno sha completo ciascuno).
func (s *Service) commitsByID(ctx context.Context, dir string, shas []string) (map[string]Commit, error) {
	m := make(map[string]Commit, len(shas))
	if len(shas) == 0 {
		return m, nil
	}
	in := strings.NewReader(strings.Join(shas, "\n") + "\n")
	out, err := s.run.Output(ctx, dir, in, "log", "--no-walk=unsorted", "--stdin", "--no-color", "--format="+commitFormat)
	if err != nil {
		return nil, err
	}
	cs, err := parseCommits(out)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		m[c.SHA] = c
	}
	return m, nil
}

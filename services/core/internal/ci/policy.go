package ci

import (
	"regexp"
	"time"
)

// DormantAfter: uno schedule si sospende se il repo non riceve push da più di
// 60 giorni (CI3).
const DormantAfter = 60 * 24 * time.Hour

// skipRe riconosce le parole chiave che saltano la CI nel messaggio del commit
// di testa: [skip ci], [ci skip], [no ci], [skip actions], [actions skip]
// (come GitHub) e il trailer «skip-checks: true».
var skipRe = regexp.MustCompile(`(?i)\[(?:skip ci|ci skip|no ci|skip actions|actions skip)\]|(?m)^skip-checks:\s*true\s*$`)

// SkipCI dice se il messaggio chiede di non avviare i workflow.
func SkipCI(message string) bool { return skipRe.MatchString(message) }

// Motivi per cui un repo non avvia workflow.
const (
	ReasonArchived   = "archived"
	ReasonDeleted    = "deleted"
	ReasonPullMirror = "pull_mirror"
)

// RepoState è lo stato del repo che conta per CI3.
type RepoState struct {
	ID        string
	OwnerType string
	OwnerID   string
	// Archived e Deleted: mai workflow su un repo archiviato o eliminato (R10, R2).
	Archived bool
	Deleted  bool
	// PullMirror: il repo è un mirror in pull attivo (anche in pausa o in errore,
	// non se interrotto). RunOnPullMirror è l'interruttore che lo consente.
	PullMirror      bool
	RunOnPullMirror bool
	// LastPushAt è l'ultimo push; zero se non ce n'è mai stato dopo la creazione.
	LastPushAt time.Time
	CreatedAt  time.Time
}

// Blocked è il motivo per cui i workflow non partono, vuoto se partono.
// Archiviato ed eliminato vincono su tutto; il mirror in pull è spento di
// default e si accende per repo (ci_repo_state.run_on_pull_mirror).
func (r RepoState) Blocked() string {
	switch {
	case r.Deleted:
		return ReasonDeleted
	case r.Archived:
		return ReasonArchived
	case r.PullMirror && !r.RunOnPullMirror:
		return ReasonPullMirror
	}
	return ""
}

// Dormant: lo schedule è sospeso perché l'ultimo push è più vecchio di 60 giorni.
// Senza push registrati vale la data di creazione del repo.
func (r RepoState) Dormant(now time.Time) bool {
	last := r.LastPushAt
	if last.IsZero() {
		last = r.CreatedAt
	}
	return now.Sub(last) > DormantAfter
}

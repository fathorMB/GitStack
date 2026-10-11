// Package access applica il controllo dei permessi sulle operazioni git
// (M-03/H, riusabile dal server SSH di M-03/I): chi è l'utente, quali scope ha
// il suo token, se vede il repo e se può scrivere.
//
// Regole (R1, P2, P3, docs/repos.md D-B):
//   - fetch/clone: scope read:resource e ruolo read sul repo (interno = read
//     per tutti gli utenti attivi, lo calcola identity);
//   - push: scope write:resource e ruolo write;
//   - un repo archiviato (R10) rifiuta il push con ErrArchived, ma si legge;
//   - un repo inesistente, eliminato, nel cestino o non leggibile dà lo stesso
//     ErrNotFound, per non rivelare che esiste; ErrForbidden solo quando il
//     repo è leggibile ma manca scope o ruolo di scrittura.
package access

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/fathorMB/GitStack/services/git/internal/repostore"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

// Scope dei token che governano le operazioni git (D-B: nessuno scope nuovo).
const (
	ScopeRead  = "read:resource"
	ScopeWrite = "write:resource"
)

// Esiti d'errore.
var (
	// ErrNotFound: repo inesistente, eliminato o non leggibile.
	ErrNotFound = errors.New("access: repo non trovato")
	// ErrForbidden: il repo è leggibile ma manca scope o ruolo richiesto.
	ErrForbidden = errors.New("access: permesso negato")
	// ErrArchived: il repo è archiviato (R10): si legge ma non si scrive.
	ErrArchived = errors.New("access: repo archiviato")
	// ErrUnknownKey: nessun utente ha registrato la chiave SSH.
	ErrUnknownKey = errors.New("access: chiave SSH sconosciuta")
	// ErrUnavailable: identity, core o il disco non hanno risposto: mai
	// aprire l'accesso per un errore.
	ErrUnavailable = errors.New("access: dipendenza non disponibile")
)

// Principal è l'utente autenticato da un token.
type Principal struct {
	UserID   string
	Username string
	// Kind è human o agent (vuoto se identity non lo dice).
	Kind string
	// Scopes sono gli scope del token; vuoti = nessuno.
	Scopes []string
}

// Identity è la parte di identity usata qui.
type Identity interface {
	// VerifyToken verifica un token personale; ok è false se sconosciuto,
	// scaduto, revocato o non è un token. Errori di trasporto: ErrUnavailable.
	VerifyToken(ctx context.Context, token string) (p Principal, ok bool, err error)
	// HasRole dice se l'utente ha almeno il ruolo (read|write|admin) sulla risorsa.
	HasRole(ctx context.Context, userID, resourceID, role string) (bool, error)
}

// KeyOwner è l'utente a cui appartiene una chiave SSH registrata.
type KeyOwner struct {
	UserID   string
	Username string
	// Kind è human o agent.
	Kind string
	// Active è false per un utente disattivato: la chiave non apre l'accesso.
	Active bool
}

// Keys risolve il fingerprint di una chiave SSH (accesso SSH, M-03/I).
type Keys interface {
	// LookupKey ritorna il proprietario del fingerprint SHA256:...;
	// ErrUnknownKey se non esiste. Errori di trasporto: ErrUnavailable.
	LookupKey(ctx context.Context, fingerprint string) (KeyOwner, error)
}

// RepoRef è il repo risolto da core.
type RepoRef struct {
	ID string
	// Owner e Name sono la forma canonica salvata da core (R11: l'indirizzo
	// usato dal chiamante può avere altre maiuscole). Vuoti se core non li dà.
	Owner, Name string
	Archived    bool
	// DefaultBranch e ProtectDefaultBranch sono il branch principale e la
	// sua protezione (R9), come li dà core.
	DefaultBranch        string
	ProtectDefaultBranch bool
}

// Core risolve owner/nome applicando la lettura (core risponde 404 a un repo
// che il chiamante non può leggere).
type Core interface {
	// ResolveRepo ritorna il repo; ErrNotFound se non esiste, è eliminato o
	// il chiamante non lo legge.
	ResolveRepo(ctx context.Context, caller trust.Identity, owner, name string) (RepoRef, error)
}

// Disk è la parte di repostore.Store usata qui.
type Disk interface {
	Get(ctx context.Context, id string) (repostore.Info, error)
	RepoPath(id string) (string, error)
}

// Authorizer decide chi può fare cosa.
type Authorizer struct {
	Identity Identity
	Core     Core
	Disk     Disk
}

// Authenticate verifica un token personale. ok false = credenziale non valida.
func (a *Authorizer) Authenticate(ctx context.Context, token string) (Principal, bool, error) {
	p, ok, err := a.Identity.VerifyToken(ctx, token)
	if err != nil {
		return Principal{}, false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return p, ok, nil
}

// Authorize controlla l'operazione su owner/name e ritorna il percorso su
// disco del repo bare. write true = push.
func (a *Authorizer) Authorize(ctx context.Context, p Principal, owner, name string, write bool) (dir string, err error) {
	dir, _, err = a.AuthorizeRepo(ctx, p, owner, name, write)
	return dir, err
}

// AuthorizeRepo è Authorize e in più ritorna il repo risolto da core (branch
// principale e sua protezione, per le regole alla ricezione del push).
func (a *Authorizer) AuthorizeRepo(ctx context.Context, p Principal, owner, name string, write bool) (dir string, ref RepoRef, err error) {
	need, role := ScopeRead, "read"
	if write {
		need, role = ScopeWrite, "write"
	}
	if !slices.Contains(p.Scopes, need) {
		return "", RepoRef{}, ErrForbidden
	}
	caller := trust.Identity{UserID: p.UserID, Username: p.Username, Scopes: p.Scopes}
	ref, err = a.Core.ResolveRepo(ctx, caller, owner, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", RepoRef{}, ErrNotFound
		}
		return "", RepoRef{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	id := ref.ID
	if write {
		// La lettura l'ha già verificata core; se non legge non scrive.
		ok, err := a.Identity.HasRole(ctx, p.UserID, id, role)
		if err != nil {
			return "", RepoRef{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if !ok {
			return "", RepoRef{}, ErrForbidden
		}
		if ref.Archived {
			return "", RepoRef{}, ErrArchived
		}
	}
	info, err := a.Disk.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repostore.ErrNotFound) {
			return "", RepoRef{}, ErrNotFound
		}
		return "", RepoRef{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if info.Trashed {
		return "", RepoRef{}, ErrNotFound
	}
	dir, err = a.Disk.RepoPath(id)
	if err != nil {
		return "", RepoRef{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return dir, ref, nil
}

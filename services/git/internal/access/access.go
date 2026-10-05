// Package access applica il controllo dei permessi sulle operazioni git
// (M-03/H, riusabile dal server SSH di M-03/I): chi è l'utente, quali scope ha
// il suo token, se vede il repo e se può scrivere.
//
// Regole (R1, P2, P3, docs/repos.md D-B):
//   - fetch/clone: scope read:resource e ruolo read sul repo (interno = read
//     per tutti gli utenti attivi, lo calcola identity);
//   - push: scope write:resource e ruolo write;
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
	// ErrUnavailable: identity, core o il disco non hanno risposto: mai
	// aprire l'accesso per un errore.
	ErrUnavailable = errors.New("access: dipendenza non disponibile")
)

// Principal è l'utente autenticato da un token.
type Principal struct {
	UserID   string
	Username string
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

// Core risolve owner/nome in id del repo applicando la lettura (core risponde
// 404 a un repo che il chiamante non può leggere).
type Core interface {
	// ResolveRepo ritorna l'id del repo; ErrNotFound se non esiste, è
	// eliminato o il chiamante non lo legge.
	ResolveRepo(ctx context.Context, caller trust.Identity, owner, name string) (repoID string, err error)
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
	need, role := ScopeRead, "read"
	if write {
		need, role = ScopeWrite, "write"
	}
	if !slices.Contains(p.Scopes, need) {
		return "", ErrForbidden
	}
	caller := trust.Identity{UserID: p.UserID, Username: p.Username, Scopes: p.Scopes}
	id, err := a.Core.ResolveRepo(ctx, caller, owner, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if write {
		// La lettura l'ha già verificata core; se non legge non scrive.
		ok, err := a.Identity.HasRole(ctx, p.UserID, id, role)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if !ok {
			return "", ErrForbidden
		}
	}
	info, err := a.Disk.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repostore.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if info.Trashed {
		return "", ErrNotFound
	}
	dir, err = a.Disk.RepoPath(id)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return dir, nil
}

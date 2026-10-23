package access

import (
	"context"
	"errors"
)

// ErrInvalidJobToken: token del job sconosciuto, scaduto, revocato o di un job che non
// gira più (il ci-gateway non distingue i casi).
var ErrInvalidJobToken = errors.New("access: token del job non valido")

// JobAccess è ciò che il token di un job può fare sul registry del repo owner/name (RG1):
// quel repo e nessun altro.
type JobAccess struct {
	RepoID string
	// Owner e Name sono la forma canonica salvata da core.
	Owner, Name string
	// Archived: il repo è archiviato (R10): si scarica, non si pubblica.
	Archived bool
	// Packages è il livello `packages` del token (CI4): none, read o write.
	Packages string
}

// Jobs risolve un token del job (prefisso gsj_) nel ci-gateway di core.
type Jobs interface {
	// JobAccess ritorna ErrInvalidJobToken per un token non valido e ErrNotFound se
	// owner/repo non è il repo del job (o è eliminato). Errori di trasporto: ErrUnavailable.
	JobAccess(ctx context.Context, token, owner, repo string) (JobAccess, error)
}

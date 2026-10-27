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
	// Workflow e Job identificano il job che usa il token.
	Workflow string
	Job      string
	// RunID, Ref (es. refs/heads/main), Event e ActorID descrivono il run del job: servono al
	// deploy automatico da branch (H7, GIT-349). Vuoti per i token che non vengono da un run.
	RunID, Ref, Event, ActorID string
}

// Jobs risolve un token del job (prefisso gsj_) nel ci-gateway di core.
type Jobs interface {
	// JobAccess ritorna ErrInvalidJobToken per un token non valido e ErrNotFound se
	// owner/repo non è il repo del job (o è eliminato). Errori di trasporto: ErrUnavailable.
	JobAccess(ctx context.Context, token, owner, repo string) (JobAccess, error)
}

package ci

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Permessi del token del job (CI4). Ogni livello è il minimo di tre limiti:
//   - quello chiesto dal workflow (`permissions:`; senza, read);
//   - quello consentito dal repo (ci_repo_state.token_*, default read);
//   - quello di chi ha avviato l'esecuzione (read → al più read, write o admin → al più write,
//     nessun accesso → none). Per `schedule` e per gli eventi senza attore vale il tetto del repo.
//
// Il token non eccede mai chi lo ha fatto partire, qualunque cosa chieda il workflow.

// PermCappedByActor è la chiave, nei permessi salvati del job, che dice che l'attore ha ridotto qualche livello.
const PermCappedByActor = "cappedByActor"

// PermissionKeys sono gli ambiti del token del job.
var PermissionKeys = []string{"contents", "packages", "actions"}

// Livelli, in ordine crescente.
const (
	LevelNone  = "none"
	LevelRead  = "read"
	LevelWrite = "write"
)

func levelRank(l string) int {
	switch l {
	case LevelRead:
		return 1
	case LevelWrite:
		return 2
	}
	return 0
}

// ValidLevel dice se l è none, read o write.
func ValidLevel(l string) bool { return l == LevelNone || l == LevelRead || l == LevelWrite }

// MinLevel è il più basso fra due livelli (un livello sconosciuto vale none).
func MinLevel(a, b string) string {
	if levelRank(a) <= levelRank(b) {
		if levelRank(a) == 0 {
			return LevelNone
		}
		return a
	}
	if levelRank(b) == 0 {
		return LevelNone
	}
	return b
}

// RepoTokenDefaults sono i permessi di default (= tetto) del token del job di un repo.
type RepoTokenDefaults struct {
	Contents, Packages, Actions string
}

// DefaultRepoTokenDefaults è il tetto di un repo che non ha mai cambiato le impostazioni: read.
func DefaultRepoTokenDefaults() RepoTokenDefaults {
	return RepoTokenDefaults{Contents: LevelRead, Packages: LevelRead, Actions: LevelRead}
}

func (d RepoTokenDefaults) get(key string) string {
	switch key {
	case "contents":
		return d.Contents
	case "packages":
		return d.Packages
	case "actions":
		return d.Actions
	}
	return LevelNone
}

// Valid dice se i tre livelli sono ammessi.
func (d RepoTokenDefaults) Valid() bool {
	return ValidLevel(d.Contents) && ValidLevel(d.Packages) && ValidLevel(d.Actions)
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// LoadRepoTokenDefaults legge i permessi di default del token del job del repo.
func LoadRepoTokenDefaults(ctx context.Context, q queryRower, repoID uuid.UUID) (RepoTokenDefaults, error) {
	d := DefaultRepoTokenDefaults()
	err := q.QueryRow(ctx, `SELECT token_contents, token_packages, token_actions FROM core.ci_repo_state WHERE repo_id = $1`, repoID).
		Scan(&d.Contents, &d.Packages, &d.Actions)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultRepoTokenDefaults(), nil
	}
	return d, err
}

// SaveRepoTokenDefaults scrive i permessi di default del token del job del repo.
func SaveRepoTokenDefaults(ctx context.Context, pool *pgxpool.Pool, repoID uuid.UUID, d RepoTokenDefaults) error {
	if !d.Valid() {
		return errors.New("livello di permesso non valido")
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO core.ci_repo_state (repo_id, token_contents, token_packages, token_actions) VALUES ($1, $2, $3, $4)
		ON CONFLICT (repo_id) DO UPDATE SET token_contents = EXCLUDED.token_contents,
			token_packages = EXCLUDED.token_packages, token_actions = EXCLUDED.token_actions`,
		repoID, d.Contents, d.Packages, d.Actions)
	return err
}

// ActorCap è il livello massimo che chi ha avviato l'esecuzione può dare al token.
// Nessun permesso sul repo → none; read → read; write o admin → write.
func ActorCap(hasRead, hasWrite bool) string {
	switch {
	case hasWrite:
		return LevelWrite
	case hasRead:
		return LevelRead
	}
	return LevelNone
}

// TokenCaps è il risultato di CapPermissions.
type TokenCaps struct {
	Permissions map[string]string
	// CappedByActor: almeno un livello è stato ridotto perché l'attore ha meno di quanto chiesto.
	CappedByActor bool
}

// CapPermissions calcola i permessi effettivi del token. requested è `permissions:` del
// job (nil = default read); actorCap è il tetto dell'attore, "" se non c'è attore
// (schedule): in quel caso vale solo il tetto del repo.
func CapPermissions(requested map[string]string, repo RepoTokenDefaults, actorCap string) TokenCaps {
	out := map[string]string{}
	capped := false
	for _, k := range PermissionKeys {
		want := LevelRead // senza permissions: il default è read
		if v, ok := requested[k]; ok && ValidLevel(v) {
			want = v
		}
		got := MinLevel(want, repo.get(k))
		if actorCap != "" {
			if lowered := MinLevel(got, actorCap); lowered != got {
				got, capped = lowered, true
			}
		}
		out[k] = got
	}
	return TokenCaps{Permissions: out, CappedByActor: capped}
}

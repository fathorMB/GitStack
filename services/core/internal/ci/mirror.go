package ci

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CI sui repo in mirror in pull (CI3, precisata il 2026-10-26): spenta di default,
// la accende chi ha admin sul repo. Con la CI accesa ogni sincronizzazione che
// porta ref nuovi avvia i workflow come un push normale; l'autore degli eventi è
// chi ha acceso la CI (e il token del job non ha più permessi dei suoi). Se
// quella persona perde admin, la CI sul mirror si spegne: la verifica avviene
// quando la CI sta per essere usata (evento, avvio manuale, orario) e quando
// l'impostazione viene letta, perché i ruoli stanno in identity e core non
// riceve un evento di cambio ruolo.

// EventMirrorCIDisabled è l'event_name della notifica «mirror» per la CI spenta.
const EventMirrorCIDisabled = "pull_mirror.ci_disabled"

// MirrorCI è l'impostazione «Esegui i workflow sul mirror» di un repo.
type MirrorCI struct {
	// Mirror: il repo è un mirror in pull attivo (l'impostazione ha effetto solo allora).
	Mirror  bool
	Enabled bool
	// EnabledBy: chi l'ha accesa; uuid.Nil se spenta o senza autore.
	EnabledBy uuid.UUID
}

// LoadMirrorCI legge l'impostazione.
func LoadMirrorCI(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, repoID uuid.UUID) (MirrorCI, error) {
	st, err := LoadRepoState(ctx, q, repoID)
	if err != nil {
		return MirrorCI{}, err
	}
	out := MirrorCI{Mirror: st.PullMirror, Enabled: st.RunOnPullMirror}
	if st.MirrorCIBy != "" {
		out.EnabledBy, _ = uuid.Parse(st.MirrorCIBy)
	}
	return out, nil
}

// SetMirrorCI accende (by = chi la accende) o spegne la CI del mirror.
func SetMirrorCI(ctx context.Context, pool *pgxpool.Pool, repoID uuid.UUID, enabled bool, by uuid.UUID) error {
	var who *uuid.UUID
	if enabled {
		who = &by
	}
	_, err := pool.Exec(ctx, `INSERT INTO core.ci_repo_state (repo_id, run_on_pull_mirror, run_on_pull_mirror_by) VALUES ($1, $2, $3)
		ON CONFLICT (repo_id) DO UPDATE SET run_on_pull_mirror = EXCLUDED.run_on_pull_mirror, run_on_pull_mirror_by = EXCLUDED.run_on_pull_mirror_by`,
		repoID, enabled, who)
	return err
}

// loadState legge lo stato del repo dopo aver verificato che chi ha acceso la CI
// del mirror abbia ancora admin.
func (e *Engine) loadState(ctx context.Context, repoID uuid.UUID) (RepoState, error) {
	if err := e.guardMirror(ctx, repoID); err != nil {
		return RepoState{}, err
	}
	return LoadRepoState(ctx, e.Pool, repoID)
}

// guardMirror spegne la CI del mirror se chi l'ha accesa non ha più admin sul
// repo (o se non è registrato chi l'ha accesa) e avvisa quella persona. Con
// IsRepoAdmin nil non verifica nulla. Un errore di identity non spegne.
func (e *Engine) guardMirror(ctx context.Context, repoID uuid.UUID) error {
	if e.IsRepoAdmin == nil {
		return nil
	}
	var by *uuid.UUID
	var mirrorID uuid.UUID
	err := e.Pool.QueryRow(ctx, `SELECT s.run_on_pull_mirror_by, m.id
		FROM core.ci_repo_state s JOIN core.repo_pull_mirrors m ON m.repo_id = s.repo_id
		WHERE s.repo_id = $1 AND s.run_on_pull_mirror`, repoID).Scan(&by, &mirrorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if by != nil {
		ok, err := e.IsRepoAdmin(ctx, *by, repoID)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return e.disableMirrorCI(ctx, repoID, mirrorID, by)
}

// disableMirrorCI spegne l'interruttore (solo se è ancora quello verificato) e
// scrive la notifica per chi l'aveva acceso.
func (e *Engine) disableMirrorCI(ctx context.Context, repoID, mirrorID uuid.UUID, by *uuid.UUID) error {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE core.ci_repo_state SET run_on_pull_mirror = false, run_on_pull_mirror_by = NULL
		WHERE repo_id = $1 AND run_on_pull_mirror AND run_on_pull_mirror_by IS NOT DISTINCT FROM $2`, repoID, by)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // già spenta o riaccesa da altri
	}
	if by != nil {
		data, _ := json.Marshal(map[string]any{"reason": "admin_lost"})
		if _, err := tx.Exec(ctx, `INSERT INTO core.notifications (id, user_id, reason, repo_id, pull_mirror_id, event_name, data, created_at)
			VALUES ($1, $2, 'mirror', $3, $4, $5, $6, $7)`, uuid.New(), *by, repoID, mirrorID, EventMirrorCIDisabled, data, e.now()); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	e.log().Warn("ci: spenta la CI del mirror in pull, chi l'aveva accesa non ha più admin", "repo", repoID)
	return nil
}

// GuardMirror è la verifica usata anche dalle rotte (lettura dell'impostazione).
func (e *Engine) GuardMirror(ctx context.Context, repoID uuid.UUID) error {
	return e.guardMirror(ctx, repoID)
}

package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Domini propri delle app pubbliche (v3-c, GIT-458). Il dominio è unico nell'istanza sul nome
// normalizzato; lo stato è quello del controllo del DNS.

// Stati del DNS di un dominio.
const (
	DomainPendingDNS = "pending_dns"
	DomainVerified   = "verified"
	DomainError      = "error"
)

// ErrDomainTaken: il dominio (normalizzato) è già di un'app.
var ErrDomainTaken = errors.New("il dominio è già usato")

// AppDomain è un dominio proprio di un'app.
type AppDomain struct {
	ID           uuid.UUID
	AppID        uuid.UUID
	Domain       string
	Normalized   string
	Status       string
	StatusReason *string
	LastChecked  *time.Time
	VerifiedAt   *time.Time
	// FailingSince: da quando un dominio verified non risulta più puntare all'istanza (periodo di grazia).
	FailingSince *time.Time

	CertMode        string
	CertState       string
	CertValidFrom   *time.Time
	CertValidUntil  *time.Time
	CertIssuer      *string
	CertError       *string
	CertLastAttempt *time.Time

	CreatedBy *AppActor
	CreatedAt time.Time
}

const appDomainCols = `id, app_id, domain, normalized, status, status_reason, last_checked_at, verified_at, failing_since,
	cert_mode, cert_state, cert_valid_from, cert_valid_until, cert_issuer, cert_error, cert_last_attempt_at, created_by, created_at`

func scanAppDomain(row pgx.Row) (AppDomain, error) {
	var d AppDomain
	var by []byte
	err := row.Scan(&d.ID, &d.AppID, &d.Domain, &d.Normalized, &d.Status, &d.StatusReason, &d.LastChecked, &d.VerifiedAt, &d.FailingSince,
		&d.CertMode, &d.CertState, &d.CertValidFrom, &d.CertValidUntil, &d.CertIssuer, &d.CertError, &d.CertLastAttempt, &by, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppDomain{}, ErrNotFound
	}
	d.CreatedBy = decodeActor(by)
	return d, err
}

// AddAppDomain salva il dominio come pending_dns. ErrDomainTaken se il nome normalizzato c'è già.
func (s *Store) AddAppDomain(ctx context.Context, appID uuid.UUID, domain, normalized string, by *AppActor, now time.Time) (AppDomain, error) {
	row := s.pool.QueryRow(ctx, `INSERT INTO core.app_domains (id, app_id, domain, normalized, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+appDomainCols, uuid.New(), appID, domain, normalized, actorJSON(by), now)
	d, err := scanAppDomain(row)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return AppDomain{}, ErrDomainTaken
	}
	return d, err
}

// ListAppDomains elenca i domini dell'app, dal più vecchio.
func (s *Store) ListAppDomains(ctx context.Context, appID uuid.UUID) ([]AppDomain, error) {
	return s.queryAppDomains(ctx, `SELECT `+appDomainCols+` FROM core.app_domains WHERE app_id = $1 ORDER BY created_at, id`, appID)
}

func (s *Store) queryAppDomains(ctx context.Context, q string, args ...any) ([]AppDomain, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppDomain{}
	for rows.Next() {
		d, err := scanAppDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetAppDomain legge un dominio dell'app per nome normalizzato; ErrNotFound se l'app non lo ha.
func (s *Store) GetAppDomain(ctx context.Context, appID uuid.UUID, normalized string) (AppDomain, error) {
	return scanAppDomain(s.pool.QueryRow(ctx, `SELECT `+appDomainCols+` FROM core.app_domains WHERE app_id = $1 AND normalized = $2`, appID, normalized))
}

// DeleteAppDomain toglie il dominio (e con lui lo stato del certificato); ErrNotFound se non c'è.
func (s *Store) DeleteAppDomain(ctx context.Context, appID uuid.UUID, normalized string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.app_domains WHERE app_id = $1 AND normalized = $2`, appID, normalized)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AppDomainCheck è l'esito da scrivere dopo un controllo del DNS.
type AppDomainCheck struct {
	Status       string
	StatusReason *string
	CheckedAt    time.Time
	VerifiedAt   *time.Time
	FailingSince *time.Time
}

// SaveAppDomainCheck scrive lo stato del controllo. ErrNotFound se il dominio è stato tolto nel frattempo.
func (s *Store) SaveAppDomainCheck(ctx context.Context, id uuid.UUID, c AppDomainCheck) (AppDomain, error) {
	return scanAppDomain(s.pool.QueryRow(ctx, `UPDATE core.app_domains SET status = $2, status_reason = $3, last_checked_at = $4,
		verified_at = $5, failing_since = $6 WHERE id = $1 RETURNING `+appDomainCols, id, c.Status, c.StatusReason, c.CheckedAt, c.VerifiedAt, c.FailingSince))
}

// ListAppDomainsForCheck dà tutti i domini delle app, per decidere quali ricontrollare.
func (s *Store) ListAppDomainsForCheck(ctx context.Context) ([]AppDomain, error) {
	return s.queryAppDomains(ctx, `SELECT `+appDomainCols+` FROM core.app_domains ORDER BY created_at, id`)
}

// VerifiedDomainHosts dà i domini attivi dell'app: quelli con DNS verified, anche durante il periodo di
// grazia (la rotta non si toglie finché il dominio non passa in errore).
func (s *Store) VerifiedDomainHosts(ctx context.Context, appID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT normalized FROM core.app_domains WHERE app_id = $1 AND status = 'verified' ORDER BY normalized`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// PublicIPs sono gli IP pubblici dichiarati dell'istanza (A/AAAA); vuoto se non ce ne sono.
func (s *Store) PublicIPs(ctx context.Context) ([]string, error) {
	var ips []string
	err := s.pool.QueryRow(ctx, `SELECT public_ips FROM core.app_capacity_settings WHERE singleton`).Scan(&ips)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return ips, err
}

// ListPublicApps elenca le app con accesso `public` dei repo non eliminati, per owner, repo e nome.
func (s *Store) ListPublicApps(ctx context.Context) ([]App, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+appCols+appFrom+` WHERE a.access_mode = 'public' AND r.deleted_at IS NULL ORDER BY r.owner_name, r.name, a.name`)
	if err != nil {
		return nil, err
	}
	var out []App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillApp(ctx, s.pool, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// VerifiedDomainHostsMode è VerifiedDomainHosts limitato a un modo di certificato (`letsencrypt` o
// `custom`): i domini letsencrypt hanno una rotta propria col resolver ACME (GIT-459).
func (s *Store) VerifiedDomainHostsMode(ctx context.Context, appID uuid.UUID, mode string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT normalized FROM core.app_domains WHERE app_id = $1 AND status = 'verified' AND cert_mode = $2 ORDER BY normalized`, appID, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// AppDomainCert è lo stato del certificato di un dominio, come lo ha osservato il controller.
type AppDomainCert struct {
	State       string
	ValidFrom   *time.Time
	ValidUntil  *time.Time
	Issuer      *string
	Error       *string
	LastAttempt time.Time
}

// SaveAppDomainCert scrive lo stato del certificato (solo per i domini col modo letsencrypt: i
// certificati caricati dal cliente non li tocca). ErrNotFound se il dominio è stato tolto o è custom.
func (s *Store) SaveAppDomainCert(ctx context.Context, id uuid.UUID, c AppDomainCert) (AppDomain, error) {
	return scanAppDomain(s.pool.QueryRow(ctx, `UPDATE core.app_domains SET cert_state = $2, cert_valid_from = $3, cert_valid_until = $4,
		cert_issuer = $5, cert_error = $6, cert_last_attempt_at = $7 WHERE id = $1 AND cert_mode = 'letsencrypt' RETURNING `+appDomainCols,
		id, c.State, c.ValidFrom, c.ValidUntil, c.Issuer, c.Error, c.LastAttempt))
}

// SetPublicIPs sostituisce gli IP pubblici dichiarati dell'istanza.
func (s *Store) SetPublicIPs(ctx context.Context, ips []string) error {
	if ips == nil {
		ips = []string{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO core.app_capacity_settings (singleton, public_ips, updated_at) VALUES (true, $1, now())
		ON CONFLICT (singleton) DO UPDATE SET public_ips = EXCLUDED.public_ips`, ips)
	return err
}

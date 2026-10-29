package store

import (
	"context"
	"errors"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Eventi dei domini e dei certificati nell'outbox (v3-c, GIT-461). Nessuna notifica ripetuta per lo
// stesso stato: l'evento si accoda solo nel passaggio da «a posto» a «guasto», letto con FOR UPDATE
// nella stessa transazione che scrive il nuovo stato (due repliche non lo duplicano). Se il dominio
// torna a posto e poi si guasta di nuovo, è un altro stato e l'avviso riparte. Per la scadenza del
// certificato del cliente lo stato «già avvisato» è expiry_notified_until (0053).

// domainFailing: lo stato del DNS è guasto, in errore o in periodo di grazia (ha smesso di puntare
// all'istanza ma la rotta c'è ancora).
func domainFailing(status string, failingSince *time.Time) bool {
	return status == DomainError || failingSince != nil
}

func enqueueAppDomainEvent(ctx context.Context, tx pgx.Tx, appID uuid.UUID, name, domain, reason string, validUntil *time.Time) error {
	a, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1`, appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	p := domainevents.AppDomainPayload{Repo: appRepo(a), AppName: a.Name, Domain: domain, Reason: reason}
	if validUntil != nil {
		p.ValidUntil = validUntil.UTC().Format(time.RFC3339)
	}
	return outbox.Enqueue(ctx, tx, name, domainevents.Version, p)
}

func deref(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

// CustomCertExpiring è un certificato del cliente per cui è partito l'avviso di scadenza.
type CustomCertExpiring struct {
	DomainID   uuid.UUID
	Domain     string
	ValidUntil time.Time
}

// NoticeCustomCertsExpiring accoda app_certificate.expiring per ogni certificato del cliente valido
// che scade entro `within` e per quella scadenza non ha ancora avvisato. Ritorna i certificati avvisati.
func (s *Store) NoticeCustomCertsExpiring(ctx context.Context, now time.Time, within time.Duration) ([]CustomCertExpiring, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	rows, err := tx.Query(ctx, `UPDATE core.app_domains SET expiry_notified_until = cert_valid_until
		WHERE cert_mode = 'custom' AND cert_state = 'valid' AND cert_valid_until > $1 AND cert_valid_until <= $2
		  AND expiry_notified_until IS DISTINCT FROM cert_valid_until
		RETURNING id, app_id, normalized, cert_valid_until`, now, now.Add(within))
	if err != nil {
		return nil, err
	}
	type hit struct {
		CustomCertExpiring
		app uuid.UUID
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.DomainID, &h.app, &h.Domain, &h.ValidUntil); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]CustomCertExpiring, 0, len(hits))
	for _, h := range hits {
		until := h.ValidUntil
		if err := enqueueAppDomainEvent(ctx, tx, h.app, domainevents.AppCertificateExpiring, h.Domain, "", &until); err != nil {
			return nil, err
		}
		out = append(out, h.CustomCertExpiring)
	}
	return out, tx.Commit(ctx)
}

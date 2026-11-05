package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
)

// Accesso dalla LAN dei database (v3-d, GIT-554, ADR 0003 punto 6): una porta TCP per database, scelta
// dall'intervallo dell'installazione, mai una porta riservata. La porta sta in core.managed_dbs.lan_port
// (indice unico): resta la stessa dopo un riavvio di core o un upgrade, e si libera solo spegnendo.

// ErrLANPortUnavailable: nessuna porta libera nell'intervallo (409 port_unavailable).
var ErrLANPortUnavailable = errors.New("nessuna porta libera nell'intervallo delle porte LAN")

// LANPortPolicy e' l'intervallo di porte assegnabili (estremi inclusi) e le porte dell'installazione che non
// si assegnano mai (22, 80, 443, 2222, 8443...).
type LANPortPolicy struct {
	From, To int
	Reserved []int
}

// Pick sceglie la porta piu' bassa libera dell'intervallo, saltando le riservate.
func (p LANPortPolicy) Pick(used map[int]bool) (int, bool) {
	reserved := map[int]bool{}
	for _, r := range p.Reserved {
		reserved[r] = true
	}
	for port := p.From; port <= p.To; port++ {
		if !used[port] && !reserved[port] {
			return port, true
		}
	}
	return 0, false
}

// SetManagedDBLAN accende o spegne l'accesso dalla LAN. Accendere assegna la porta (la stessa se gia'
// accesa: idempotente; la stessa anche se uno spegnimento non e' ancora stato chiuso). Spegnere mette
// lan_enabled=false ma TIENE la porta: resta riservata finche' il Service non e' davvero sparito
// (ReleaseManagedDBLANPort), cosi' un errore di Kubernetes non la rende riassegnabile con il vecchio Service
// ancora aperto. Scrive audit ed evento database.lan_enabled|lan_disabled.
func (s *Store) SetManagedDBLAN(ctx context.Context, id uuid.UUID, enable bool, pol LANPortPolicy, actor *AppActor, now time.Time) (ManagedDB, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedDB{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Una serratura sola per l'assegnazione: due database che accendono insieme non scelgono la stessa porta
	// (l'indice unico e' la rete di sicurezza).
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('db-lan-port', 0))`); err != nil {
		return ManagedDB{}, err
	}
	d, err := scanManagedDB(tx.QueryRow(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d WHERE d.id = $1 FOR UPDATE`, id))
	if err != nil {
		return ManagedDB{}, err
	}
	if d.Status == DBDeleting {
		return ManagedDB{}, ErrManagedDBNotFound
	}
	if enable {
		if d.LANEnabled && d.LANPort != nil {
			return d, nil
		}
		if d.LANPort != nil {
			// Riaccensione prima che la chiusura sia completata: la porta e' ancora sua.
			port := *d.LANPort
			if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET lan_enabled = true, updated_at = $2 WHERE id = $1`, id, now); err != nil {
				return ManagedDB{}, err
			}
			d.LANEnabled = true
			if err := dbAudit(ctx, tx, d, "lan_enabled", actor, map[string]any{"port": port}, now); err != nil {
				return ManagedDB{}, err
			}
			if err := dbEvent(ctx, tx, domainevents.DatabaseLanEnabled, d, actor, func(p *domainevents.DatabasePayload) { p.Port = &port }); err != nil {
				return ManagedDB{}, err
			}
			return d, tx.Commit(ctx)
		}
		rows, err := tx.Query(ctx, `SELECT lan_port FROM core.managed_dbs WHERE lan_port IS NOT NULL`)
		if err != nil {
			return ManagedDB{}, err
		}
		used := map[int]bool{}
		for rows.Next() {
			var p int
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return ManagedDB{}, err
			}
			used[p] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return ManagedDB{}, err
		}
		port, ok := pol.Pick(used)
		if !ok {
			return ManagedDB{}, ErrLANPortUnavailable
		}
		if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET lan_enabled = true, lan_port = $2, updated_at = $3 WHERE id = $1`, id, port, now); err != nil {
			return ManagedDB{}, err
		}
		d.LANEnabled, d.LANPort = true, &port
		if err := dbAudit(ctx, tx, d, "lan_enabled", actor, map[string]any{"port": port}, now); err != nil {
			return ManagedDB{}, err
		}
		if err := dbEvent(ctx, tx, domainevents.DatabaseLanEnabled, d, actor, func(p *domainevents.DatabasePayload) { p.Port = &port }); err != nil {
			return ManagedDB{}, err
		}
		return d, tx.Commit(ctx)
	}
	if !d.LANEnabled {
		// Gia' spenta (anche con la chiusura ancora da completare): niente audit doppio.
		return d, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET lan_enabled = false, updated_at = $2 WHERE id = $1`, id, now); err != nil {
		return ManagedDB{}, err
	}
	d.LANEnabled = false
	var detail map[string]any
	if d.LANPort != nil {
		detail = map[string]any{"port": *d.LANPort}
	}
	if err := dbAudit(ctx, tx, d, "lan_disabled", actor, detail, now); err != nil {
		return ManagedDB{}, err
	}
	if err := dbEvent(ctx, tx, domainevents.DatabaseLanDisabled, d, actor, nil); err != nil {
		return ManagedDB{}, err
	}
	return d, tx.Commit(ctx)
}

// ReleaseManagedDBLANPort libera la porta di un database con la LAN spenta: lo chiama solo chi ha appena
// verificato che Service e NetworkPolicy non esistono piu'. Con la LAN riaccesa nel frattempo non fa nulla.
func (s *Store) ReleaseManagedDBLANPort(ctx context.Context, id uuid.UUID) (ManagedDB, error) {
	if _, err := s.pool.Exec(ctx, `UPDATE core.managed_dbs SET lan_port = NULL WHERE id = $1 AND lan_enabled = false AND lan_port IS NOT NULL`, id); err != nil {
		return ManagedDB{}, err
	}
	return s.GetManagedDBByID(ctx, id)
}

// ListManagedDBLANPorts elenca le porte LAN in uso (per l'amministratore: nessun segreto).
func (s *Store) ListManagedDBLANPorts(ctx context.Context) ([]ManagedDBLANPort, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.lan_port, d.owner_name, d.name FROM core.managed_dbs d WHERE d.lan_port IS NOT NULL ORDER BY d.lan_port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedDBLANPort
	for rows.Next() {
		var p ManagedDBLANPort
		if err := rows.Scan(&p.Port, &p.Owner, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ManagedDBLANPort e' una porta LAN assegnata e il database che la usa.
type ManagedDBLANPort struct {
	Port  int
	Owner string
	Name  string
}

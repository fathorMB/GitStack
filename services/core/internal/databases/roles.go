package databases

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Ruoli del database (ADR 0003 punto 3, DB7, DB9, DB12). Tutto passa da SQLRunner come gitstack_admin sul
// socket locale. Le password sono esadecimali generate qui (32 caratteri) e quindi sicure da citare.

// Link: modalita' del collegamento.
const (
	ModeRW = "rw"
	ModeRO = "ro"
)

// ErrSessionsRemain: lo scollegamento non e' completo (sessioni ancora aperte): resta `unlinking` e si ritenta (DB12).
var ErrSessionsRemain = errors.New("sessioni del collegamento ancora aperte")

// QuoteIdent cita un identificatore SQL.
func QuoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// QuoteLiteral cita una stringa SQL.
func QuoteLiteral(s string) string { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }

// OwnerRole, ReadOnlyRole e LinkRole sono i nomi dei ruoli.
func OwnerRole(name string) string    { return name + "_owner" }
func ReadOnlyRole(name string) string { return name + "_ro" }
func LinkRole(linkID uuid.UUID) string {
	return "l_" + strings.ReplaceAll(linkID.String(), "-", "")
}

// NewPassword genera 32 caratteri esadecimali (128 bit), come LAN e rotazione dell'ADR.
func NewPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// SetupRoles crea i ruoli e il database in modo idempotente (DB7): <name>_owner (login, NON superuser),
// <name>_ro (nologin, membro di pg_read_all_data), il database di proprieta' dell'owner, privilegi di
// connessione tolti a PUBLIC e SELECT per _ro (la stessa routine della creazione serve al ripristino, DB15).
func SetupRoles(ctx context.Context, r SQLRunner, d store.ManagedDB, ownerPassword string) error {
	owner, ro := OwnerRole(d.Name), ReadOnlyRole(d.Name)
	err := r.Exec(ctx, d, "postgres", fmt.Sprintf(`
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %[1]s) THEN
    CREATE ROLE %[2]s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %[3]s;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %[4]s) THEN
    CREATE ROLE %[5]s NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
  END IF;
END $$;
GRANT pg_read_all_data TO %[5]s;`, QuoteLiteral(owner), QuoteIdent(owner), QuoteLiteral(ownerPassword), QuoteLiteral(ro), QuoteIdent(ro)))
	if err != nil {
		return fmt.Errorf("ruoli del database: %w", err)
	}
	rows, err := r.Query(ctx, d, "postgres", fmt.Sprintf(`SELECT 1 FROM pg_database WHERE datname = %s`, QuoteLiteral(d.Name)))
	if err != nil {
		return fmt.Errorf("lettura dei database: %w", err)
	}
	if len(rows) == 0 {
		if err := r.Exec(ctx, d, "postgres", fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, QuoteIdent(d.Name), QuoteIdent(owner))); err != nil {
			return fmt.Errorf("creazione del database: %w", err)
		}
	}
	if err := r.Exec(ctx, d, "postgres", fmt.Sprintf(`REVOKE ALL ON DATABASE %[1]s FROM PUBLIC; GRANT CONNECT ON DATABASE %[1]s TO %[2]s;`, QuoteIdent(d.Name), QuoteIdent(ro))); err != nil {
		return fmt.Errorf("privilegi di connessione: %w", err)
	}
	return ReapplyROPrivileges(ctx, r, d)
}

// ReapplyROPrivileges rifa i privilegi di <name>_ro (idempotente, anche dopo un ripristino, DB15).
func ReapplyROPrivileges(ctx context.Context, r SQLRunner, d store.ManagedDB) error {
	owner, ro := QuoteIdent(OwnerRole(d.Name)), QuoteIdent(ReadOnlyRole(d.Name))
	return r.Exec(ctx, d, d.Name, fmt.Sprintf(`
ALTER SCHEMA public OWNER TO %[1]s;
GRANT USAGE ON SCHEMA public TO %[2]s;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO %[2]s;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO %[2]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA public GRANT SELECT ON TABLES TO %[2]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA public GRANT SELECT ON SEQUENCES TO %[2]s;`, owner, ro))
}

// SetOwnerPassword cambia la password di <name>_owner (rotazione).
func SetOwnerPassword(ctx context.Context, r SQLRunner, d store.ManagedDB, password string) error {
	return r.Exec(ctx, d, "postgres", fmt.Sprintf(`ALTER ROLE %s PASSWORD %s`, QuoteIdent(OwnerRole(d.Name)), QuoteLiteral(password)))
}

// CreateLinkRole crea il ruolo di login PROPRIO del collegamento (DB9): senza CREATEROLE/CREATEDB/superuser.
// rw: membro di <name>_owner con `SET role` all'owner, cosi' ogni oggetto creato dalla sessione e' del
// proprietario e il ruolo del link non possiede nulla; ro: membro di <name>_ro.
func CreateLinkRole(ctx context.Context, r SQLRunner, d store.ManagedDB, linkID uuid.UUID, mode, password string) error {
	role := QuoteIdent(LinkRole(linkID))
	var grant, set string
	switch mode {
	case ModeRW:
		grant = fmt.Sprintf(`GRANT %s TO %s;`, QuoteIdent(OwnerRole(d.Name)), role)
		set = fmt.Sprintf(`ALTER ROLE %s SET role = %s;`, role, QuoteLiteral(OwnerRole(d.Name)))
	case ModeRO:
		grant = fmt.Sprintf(`GRANT %s TO %s;`, QuoteIdent(ReadOnlyRole(d.Name)), role)
	default:
		return fmt.Errorf("modalita' del collegamento sconosciuta: %q", mode)
	}
	return r.Exec(ctx, d, "postgres", fmt.Sprintf(`CREATE ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %s; %s %s`,
		role, QuoteLiteral(password), grant, set))
}

// TerminateSessions chiude le sessioni di un ruolo e ritorna quante ne restano (DB12).
func TerminateSessions(ctx context.Context, r SQLRunner, d store.ManagedDB, role string) (int, error) {
	if err := r.Exec(ctx, d, "postgres", fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = %s AND pid <> pg_backend_pid()`, QuoteLiteral(role))); err != nil {
		return 0, err
	}
	rows, err := r.Query(ctx, d, "postgres", fmt.Sprintf(`SELECT count(*) FROM pg_stat_activity WHERE usename = %s`, QuoteLiteral(role)))
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	var n int
	_, _ = fmt.Sscan(rows[0], &n)
	return n, nil
}

// RotateLinkRole cambia la password del link e termina le sessioni aperte con la vecchia (DB12): nessuna
// connessione sopravvive alla password precedente.
func RotateLinkRole(ctx context.Context, r SQLRunner, d store.ManagedDB, linkID uuid.UUID, password string) error {
	role := LinkRole(linkID)
	if err := r.Exec(ctx, d, "postgres", fmt.Sprintf(`ALTER ROLE %s PASSWORD %s`, QuoteIdent(role), QuoteLiteral(password))); err != nil {
		return err
	}
	if _, err := TerminateSessions(ctx, r, d, role); err != nil {
		return err
	}
	return nil
}

// UnlinkOptions regola l'attesa dello scollegamento.
type UnlinkOptions struct {
	// Timeout e Every: per quanto si ripete pg_terminate_backend (default 30 s ogni 250 ms).
	Timeout time.Duration
	Every   time.Duration
}

// DropLinkRole e' lo scollegamento nell'ordine OBBLIGATORIO dell'ADR (DB12):
//  1. ALTER ROLE ... NOLOGIN (nessun nuovo login);
//  2. pg_terminate_backend delle sessioni, ripetuto finche' pg_stat_activity non ne mostra piu';
//  3. DROP OWNED BY e DROP ROLE.
//
// Se il passo 2 non completa entro il timeout ritorna ErrSessionsRemain e il ruolo resta: il chiamante
// lascia il link `unlinking` e ritenta. Solo dopo un esito nil si tolgono variabili, Secret, label e
// regola di uscita dell'app (passo 4, GIT-552).
func DropLinkRole(ctx context.Context, r SQLRunner, d store.ManagedDB, linkID uuid.UUID, o UnlinkOptions) error {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Every <= 0 {
		o.Every = 250 * time.Millisecond
	}
	role := LinkRole(linkID)
	if err := r.Exec(ctx, d, "postgres", fmt.Sprintf(`ALTER ROLE %s NOLOGIN`, QuoteIdent(role))); err != nil {
		return fmt.Errorf("NOLOGIN del ruolo del collegamento: %w", err)
	}
	deadline := time.Now().Add(o.Timeout)
	for {
		n, err := TerminateSessions(ctx, r, d, role)
		if err != nil {
			return fmt.Errorf("chiusura delle sessioni del collegamento: %w", err)
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %d", ErrSessionsRemain, n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.Every):
		}
	}
	if err := r.Exec(ctx, d, d.Name, fmt.Sprintf(`DROP OWNED BY %s`, QuoteIdent(role))); err != nil {
		return fmt.Errorf("DROP OWNED del ruolo del collegamento: %w", err)
	}
	if err := r.Exec(ctx, d, "postgres", fmt.Sprintf(`DROP ROLE %s`, QuoteIdent(role))); err != nil {
		return fmt.Errorf("DROP ROLE del collegamento: %w", err)
	}
	return nil
}

// DatabaseSize legge pg_database_size del database dell'owner (DB3, DB21).
func DatabaseSize(ctx context.Context, r SQLRunner, d store.ManagedDB) (int64, error) {
	rows, err := r.Query(ctx, d, "postgres", fmt.Sprintf(`SELECT pg_database_size(%s)`, QuoteLiteral(d.Name)))
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, errors.New("pg_database_size non ha risposto")
	}
	var n int64
	if _, err := fmt.Sscan(rows[0], &n); err != nil {
		return 0, err
	}
	return n, nil
}

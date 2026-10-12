// Package pgcheck è il preflight del Postgres esterno (N8
// [c_691e437fe2febab6]): si connette con l'URL e le credenziali date e
// controlla versione supportata (15, 16 o 17), connessione e permessi per
// creare ruoli e schemi, con messaggi che dicono cosa fare.
//
// Lo usano `gitstack postgres check` (e quindi install.sh, prima di toccare
// l'host) e i test d'integrazione. Non scrive niente: le prove di creazione
// di ruolo e schema girano in una transazione che si annulla.
package pgcheck

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Versioni principali supportate (N8).
const (
	MinMajor = 15
	MaxMajor = 17
)

// Params sono gli ingressi del controllo.
type Params struct {
	// URL è postgres://utente[:password]@host[:porta]/database[?sslmode=...].
	URL string
	// Password, se non vuota, sostituisce quella dell'URL (file o variabile
	// d'ambiente: fuori dalla riga di comando).
	Password string
	// CAFile è il certificato PEM della CA del database (facoltativo).
	CAFile string
	// Timeout della connessione (default 15 s).
	Timeout time.Duration
}

// Result è l'esito di un controllo riuscito.
type Result struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Database string   `json:"database"`
	User     string   `json:"user"`
	SSLMode  string   `json:"ssl_mode"`
	Version  string   `json:"version"`
	Major    int      `json:"major"`
	TLS      bool     `json:"tls"`
	Warnings []string `json:"warnings,omitempty"`
}

// Failure è un controllo non superato: Msg dice cosa è andato storto e cosa
// fare. Kind è connect, version, permission o params.
type Failure struct {
	Kind string
	Msg  string
}

func (f *Failure) Error() string { return f.Msg }

func fail(kind, format string, a ...any) error {
	return &Failure{Kind: kind, Msg: fmt.Sprintf(format, a...)}
}

// PrepareHint rimanda allo script per il DBA.
const PrepareHint = "Fai preparare il database al DBA con deploy/postgres-prepare.sql (crea un utente con CREATEROLE e il database dedicato)."

// Parse legge URL, password e CA e ritorna la configurazione di pgx più i
// dati da mostrare. Esportata perché install.sh e i test ne verificano i
// rifiuti senza connettersi.
func Parse(p Params) (*pgx.ConnConfig, *Result, error) {
	raw := strings.TrimSpace(p.URL)
	if raw == "" {
		return nil, nil, fail("params", "manca l'URL del database (--url postgres://utente@host:5432/database)")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, nil, fail("params", "URL del database non valido: serve postgres://utente@host:5432/database (schema postgres:// o postgresql://)")
	}
	if u.Hostname() == "" {
		return nil, nil, fail("params", "URL del database senza host: postgres://utente@HOST:5432/database")
	}
	db := strings.TrimPrefix(u.Path, "/")
	if db == "" || strings.Contains(db, "/") {
		return nil, nil, fail("params", "URL del database senza nome del database: postgres://utente@host:5432/NOME_DATABASE")
	}
	user := u.User.Username()
	if user == "" {
		return nil, nil, fail("params", "URL del database senza utente: postgres://UTENTE@host:5432/database")
	}
	pw := p.Password
	if pw == "" {
		pw, _ = u.User.Password()
	}
	if pw == "" {
		return nil, nil, fail("params", "manca la password del database: usa --postgres-password-file, la variabile GITSTACK_POSTGRES_PASSWORD o la password nell'URL")
	}
	for _, r := range pw {
		if !urlSafe(r) {
			return nil, nil, fail("params", "la password del database contiene caratteri che GitStack non può mettere in un URL di connessione (ammessi: lettere, cifre, - . _ ~). Cambia la password dell'utente %q con una di soli caratteri URL-safe", user)
		}
	}
	q := u.Query()
	if q.Get("sslmode") == "" {
		q.Set("sslmode", "require")
	}
	if p.CAFile != "" {
		q.Set("sslrootcert", p.CAFile)
	}
	u.User = url.UserPassword(user, pw)
	u.RawQuery = q.Encode()
	timeout := 15
	if p.Timeout > 0 {
		timeout = max(1, int(p.Timeout.Seconds()))
	}
	q.Set("connect_timeout", fmt.Sprint(timeout))
	u.RawQuery = q.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		// Il messaggio di pgconn può contenere l'URL con la password: non lo
		// riportiamo.
		return nil, nil, fail("params", "URL del database non valido (controlla host, porta e parametri)")
	}
	res := &Result{Host: cfg.Host, Port: int(cfg.Port), Database: cfg.Database, User: cfg.User, SSLMode: q.Get("sslmode")}
	switch strings.ToLower(db) {
	case "postgres", "template0", "template1":
		res.Warnings = append(res.Warnings, fmt.Sprintf("il database %q è quello di sistema: GitStack vuole un database dedicato (vedi deploy/postgres-prepare.sql)", db))
	}
	return cfg, res, nil
}

// urlSafe: i soli caratteri non riservati di un URL (RFC 3986).
func urlSafe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)
}

// Check esegue il preflight. L'errore, se non nil, è sempre un *Failure.
func Check(ctx context.Context, p Params) (*Result, error) {
	cfg, res, err := Parse(p)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(cctx, cfg)
	if err != nil {
		return nil, connectFailure(res, p.CAFile, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	// pg_stat_ssl dice se questa sessione è cifrata (può non essere leggibile).
	_ = conn.QueryRow(cctx, "SELECT COALESCE((SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()), false)").Scan(&res.TLS)

	var verNum int
	var ver string
	if err := conn.QueryRow(cctx, "SELECT current_setting('server_version_num')::int, current_setting('server_version')").Scan(&verNum, &ver); err != nil {
		return nil, fail("connect", "connessione riuscita ma non riesco a leggere la versione del server: %v", sanitize(err))
	}
	res.Version = ver
	res.Major = verNum / 10000
	if res.Major < MinMajor || res.Major > MaxMajor {
		return nil, fail("version", "PostgreSQL %s non supportato: GitStack supporta le versioni %d, %d e %d. Aggiorna il server (o usa il Postgres interno, il default dell'installer).", ver, MinMajor, MinMajor+1, MaxMajor)
	}

	var super bool
	if err := conn.QueryRow(cctx, "SELECT rolsuper FROM pg_roles WHERE rolname = current_user").Scan(&super); err != nil {
		return nil, fail("permission", "non riesco a leggere i permessi dell'utente %q: %v", res.User, sanitize(err))
	}
	// Prove reali, in una transazione che si annulla: dicono la stessa cosa
	// che farà l'installazione, anche con permessi ereditati o ruoli gestiti.
	tx, err := conn.Begin(cctx)
	if err != nil {
		return nil, fail("connect", "transazione di prova non avviata: %v", sanitize(err))
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	suffix, err := randSuffix()
	if err != nil {
		return nil, fail("connect", "%v", err)
	}
	if _, err := tx.Exec(cctx, "CREATE SCHEMA gitstack_preflight_"+suffix); err != nil {
		if isPrivilege(err) {
			return nil, fail("permission", "l'utente %q non può creare schemi nel database %q (manca il privilegio CREATE sul database): GitStack crea lo schema di ogni servizio (identity, core). Dai all'utente la proprietà del database o `GRANT CREATE ON DATABASE %s TO %s`. %s", res.User, res.Database, res.Database, res.User, PrepareHint)
		}
		return nil, fail("permission", "creazione di uno schema di prova non riuscita: %v", sanitize(err))
	}
	if _, err := tx.Exec(cctx, "CREATE ROLE gitstack_preflight_"+suffix+" NOLOGIN"); err != nil {
		if isPrivilege(err) {
			return nil, fail("permission", "l'utente %q non può creare ruoli (manca CREATEROLE): GitStack crea un ruolo applicativo per servizio (identity_app). Esegui come DBA `ALTER ROLE %s CREATEROLE`. %s", res.User, res.User, PrepareHint)
		}
		return nil, fail("permission", "creazione di un ruolo di prova non riuscita: %v", sanitize(err))
	}
	if super {
		res.Warnings = append(res.Warnings, fmt.Sprintf("l'utente %q è superuser: funziona, ma GitStack non ne ha bisogno (basta CREATEROLE e la proprietà del database)", res.User))
	}
	return res, nil
}

func randSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("numero casuale: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func isPrivilege(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "42501"
}

// sanitize toglie da un errore di pgx ciò che potrebbe contenere la
// password (il DSN): tiene solo codice e messaggio del server.
func sanitize(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Message + " (" + pe.Code + ")"
	}
	return err.Error()
}

func connectFailure(r *Result, caFile string, err error) error {
	where := fmt.Sprintf("%s:%d", r.Host, r.Port)
	var pe *pgconn.PgError
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case errors.As(err, &pe) && pe.Code == "28P01", errors.As(err, &pe) && pe.Code == "28000" && strings.Contains(low, "password"):
		return fail("connect", "autenticazione rifiutata da %s per l'utente %q: password errata. Controlla il file della password.", where, r.User)
	case errors.As(err, &pe) && pe.Code == "3D000":
		return fail("connect", "il database %q non esiste su %s. Crealo con deploy/postgres-prepare.sql.", r.Database, where)
	case errors.As(err, &pe) && pe.Code == "28000":
		return fail("connect", "accesso negato a %s per l'utente %q sul database %q: %s. Controlla pg_hba.conf del server (l'indirizzo di questa macchina e dei nodi del cluster deve poter accedere).", where, r.User, r.Database, pe.Message)
	case strings.Contains(low, "server refused tls") || strings.Contains(low, "server does not support ssl") || strings.Contains(low, "sslmode is require"):
		return fail("connect", "il server %s non accetta connessioni TLS ma sslmode è %q. Abilita TLS sul server oppure usa ?sslmode=disable (solo su una rete fidata).", where, r.SSLMode)
	case strings.Contains(low, "certificate") || strings.Contains(low, "x509") || strings.Contains(low, "tls"):
		hint := "Passa la CA del database con --postgres-ca-file."
		if caFile != "" {
			hint = "Controlla che " + caFile + " sia la CA che ha firmato il certificato del server e che il nome host dell'URL sia nel certificato."
		}
		return fail("connect", "verifica del certificato TLS di %s non riuscita (sslmode %s): %s %s", where, r.SSLMode, sanitizeTLS(msg), hint)
	case strings.Contains(low, "no such host") || strings.Contains(low, "lookup"):
		return fail("connect", "il nome %q del database non si risolve: controlla l'host dell'URL e il DNS di questa macchina.", r.Host)
	case strings.Contains(low, "refused"):
		return fail("connect", "connessione rifiutata da %s: il server non è in ascolto su quell'indirizzo o la porta è sbagliata (o un firewall la blocca).", where)
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline") || strings.Contains(low, "i/o timeout"):
		return fail("connect", "nessuna risposta da %s entro il tempo limite: controlla rete e firewall tra questa macchina e il database.", where)
	}
	return fail("connect", "connessione a %s non riuscita: %s", where, sanitizeTLS(msg))
}

// sanitizeTLS toglie dal messaggio ogni occorrenza di "postgres://...@" e di
// "password=": nessuna password nei messaggi.
func sanitizeTLS(msg string) string {
	if i := strings.Index(msg, "postgres://"); i >= 0 {
		if j := strings.Index(msg[i:], " "); j >= 0 {
			msg = msg[:i] + "<url>" + msg[i+j:]
		} else {
			msg = msg[:i] + "<url>"
		}
	}
	return msg
}

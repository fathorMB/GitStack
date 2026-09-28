// Package migrate applica le migrazioni versionate dello schema Postgres
// dedicato a core ("core", D6 [c_4df04d65b3ac4910]). Le migrazioni sono
// incorporate nel binario (embed.FS): nessun file esterno da distribuire
// insieme all'immagine Docker.
//
// Idempotenza: golang-migrate tiene una tabella di stato
// (core.schema_migrations) con l'ultima versione applicata; Up() applica
// solo le migrazioni successive a quella versione, quindi rilanciarlo senza
// migrazioni nuove non fa nulla (nessun errore, nessuna riapplicazione).
//
// Rollback: ogni migrazione *.up.sql ha il proprio *.down.sql (vedi sql/).
// Down(ctx, dsn, 1) applica il rollback dell'ultima migrazione; è pensato
// per lo sviluppo/CI, non per la produzione (un rollback che droppa
// colonne/tabelle perde dati). Il binario espone questo stesso pacchetto
// anche come comando dedicato ("core migrate up|down|version", vedi
// main.go), utilizzabile da un job k8s separato invece che all'avvio del
// server (entrambe le modalità richieste dal criterio di accettazione).
package migrate

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // registra lo schema URL "postgres://" per golang-migrate
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var sqlFS embed.FS

// Schema è lo schema Postgres dedicato a core (D6): sia le tabelle di
// dominio (core.resources, ...) sia la tabella di stato delle migrazioni
// vivono qui, non in "public": nessun'altra tabella di GitStack condivide
// questo schema.
const Schema = "core"

// migrationsTable è il nome (non qualificato) della tabella di stato delle
// migrazioni di golang-migrate: vive dentro Schema grazie al search_path
// impostato in databaseURLFor.
const migrationsTable = "schema_migrations"

// EnsureSchema crea lo schema dedicato "core" se non esiste ancora. Va
// eseguito prima di New/Up: golang-migrate, con search_path=core, crea la
// propria tabella di stato dentro il primo schema del search_path che
// esiste già, quindi lo schema deve esistere prima di aprire la connessione
// di migrazione.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+Schema)
	if err != nil {
		return fmt.Errorf("creazione schema %q non riuscita: %w", Schema, err)
	}
	return nil
}

// New apre un'istanza di migrazione basata sulle SQL incorporate. Il
// chiamante deve chiamere Close() sull'istanza restituita.
func New(databaseURL string) (*migrate.Migrate, error) {
	source, err := iofs.New(sqlFS, "sql")
	if err != nil {
		return nil, fmt.Errorf("apertura sorgente migrazioni incorporate non riuscita: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, databaseURLFor(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("apertura istanza di migrazione non riuscita: %w", err)
	}

	return m, nil
}

// Up applica tutte le migrazioni non ancora applicate, in ordine di
// versione. Non ritorna errore se non ci sono migrazioni nuove
// (migrate.ErrNoChange), che è il caso comune di un riavvio senza modifiche
// allo schema: questo è ciò che rende l'operazione idempotente.
func Up(ctx context.Context, pool *pgxpool.Pool, databaseURL string) error {
	if err := EnsureSchema(ctx, pool); err != nil {
		return err
	}

	m, err := New(databaseURL)
	if err != nil {
		return err
	}
	defer closeQuietly(m)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("applicazione migrazioni non riuscita: %w", err)
	}

	return nil
}

// Down applica il rollback delle ultime `steps` migrazioni (vedi i file
// *.down.sql in sql/). Pensato per sviluppo e CI: in produzione preferire
// una migrazione "up" correttiva piuttosto che un rollback distruttivo.
func Down(ctx context.Context, pool *pgxpool.Pool, databaseURL string, steps int) error {
	if steps <= 0 {
		return fmt.Errorf("steps deve essere positivo, ricevuto %d", steps)
	}

	if err := EnsureSchema(ctx, pool); err != nil {
		return err
	}

	m, err := New(databaseURL)
	if err != nil {
		return err
	}
	defer closeQuietly(m)

	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("rollback migrazioni non riuscito: %w", err)
	}

	return nil
}

// databaseURLFor aggiunge alla stringa di connessione i parametri che
// scopano golang-migrate allo schema dedicato "core": search_path per far
// vivere la tabella di stato dentro core invece che in "public", e
// x-migrations-table per essere espliciti sul nome (anche se coincide col
// default), a beneficio di chi legge la configurazione.
func databaseURLFor(databaseURL string) string {
	sep := "?"
	if containsQuery(databaseURL) {
		sep = "&"
	}
	return fmt.Sprintf("%s%ssearch_path=%s&x-migrations-table=%s", databaseURL, sep, Schema, migrationsTable)
}

func containsQuery(databaseURL string) bool {
	for _, r := range databaseURL {
		if r == '?' {
			return true
		}
	}
	return false
}

func closeQuietly(m *migrate.Migrate) {
	_, _ = m.Close()
}

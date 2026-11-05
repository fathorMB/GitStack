package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Collegamenti app-database (v3-d, GIT-552, ADR 0003 DB9-DB12). Le variabili iniettate nell'app sono
// segreti dell'app (core.app_secrets, cifrati come gli altri): il collegamento, la versione di
// configurazione e il suo deploy nascono nella stessa transazione, come per PutAppSecret.

// ManagedDBEnvNames sono le variabili che un collegamento inietta nell'app (DB10). Stanno nei segreti
// dell'app: un nome gia' presente come variabile o segreto blocca il collegamento (409 variable_conflict).
var ManagedDBEnvNames = []string{"DATABASE_URL", "PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSSLMODE", "PGSSLROOTCERT"}

// IsManagedDBEnvName dice se il nome e' una delle variabili dei collegamenti.
func IsManagedDBEnvName(name string) bool {
	for _, n := range ManagedDBEnvNames {
		if n == name {
			return true
		}
	}
	return false
}

var (
	// ErrManagedDBLinkNotFound: il collegamento non esiste (o e' di un altro database).
	ErrManagedDBLinkNotFound = errors.New("collegamento non trovato")
	// ErrManagedDBDeleting: il database e' in eliminazione, non si collega.
	ErrManagedDBDeleting = errors.New("il database e' in eliminazione")
	// ErrManagedEnvName: il nome e' una variabile del collegamento database, si cambia scollegando.
	ErrManagedEnvName = errors.New("il nome e' una variabile gestita dal collegamento con un database")
)

// DBVariableConflictError elenca i nomi gia' usati dall'app: il collegamento e' rifiutato e l'app non cambia.
type DBVariableConflictError struct{ Names []string }

func (e *DBVariableConflictError) Error() string {
	return "l'app ha gia' variabili o segreti con questi nomi: " + strings.Join(e.Names, ", ")
}

// ManagedDBLink e' un collegamento app-database (DB9).
type ManagedDBLink struct {
	ID        uuid.UUID
	DBID      uuid.UUID
	AppID     uuid.UUID
	Mode      string
	State     string
	CreatedBy *AppActor
	CreatedAt time.Time
}

// ManagedDBAppRef e' l'app di un collegamento, come la mostrano API ed eventi.
type ManagedDBAppRef struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Name  string `json:"name"`
}

const linkCols = `id, db_id, app_id, mode, state, created_by, created_at`

func scanLink(row pgx.Row) (ManagedDBLink, error) {
	var l ManagedDBLink
	var by []byte
	err := row.Scan(&l.ID, &l.DBID, &l.AppID, &l.Mode, &l.State, &by, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrManagedDBLinkNotFound
	}
	if err != nil {
		return l, err
	}
	l.CreatedBy, err = scanActor(by)
	return l, err
}

func (s *Store) queryLinks(ctx context.Context, where string, args ...any) ([]ManagedDBLink, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+linkCols+` FROM core.managed_db_links `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedDBLink
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListManagedDBLinks elenca i collegamenti del database.
func (s *Store) ListManagedDBLinks(ctx context.Context, dbID uuid.UUID) ([]ManagedDBLink, error) {
	return s.queryLinks(ctx, `WHERE db_id = $1`, dbID)
}

// ListAppDBLinks elenca i collegamenti dell'app (anche `unlinking`: finche' il ruolo esiste la rete resta).
func (s *Store) ListAppDBLinks(ctx context.Context, appID uuid.UUID) ([]ManagedDBLink, error) {
	return s.queryLinks(ctx, `WHERE app_id = $1`, appID)
}

// GetManagedDBLink legge un collegamento del database.
func (s *Store) GetManagedDBLink(ctx context.Context, dbID, linkID uuid.UUID) (ManagedDBLink, error) {
	return scanLink(s.pool.QueryRow(ctx, `SELECT `+linkCols+` FROM core.managed_db_links WHERE id = $1 AND db_id = $2`, linkID, dbID))
}

// SetManagedDBLinkState porta il link in `unlinking` (o `active`).
func (s *Store) SetManagedDBLinkState(ctx context.Context, id uuid.UUID, state string) error {
	_, err := s.pool.Exec(ctx, `UPDATE core.managed_db_links SET state = $2 WHERE id = $1`, id, state)
	return err
}

// ManagedDBLinkIDs elenca gli id di tutti i collegamenti del database: servono a riconoscere i ruoli orfani.
func (s *Store) ManagedDBLinkIDs(ctx context.Context, dbID uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM core.managed_db_links WHERE db_id = $1`, dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// linkEvent scrive database.linked / database.unlinked: mai password, mai variabili.
func linkEvent(ctx context.Context, tx pgx.Tx, name string, d ManagedDB, l ManagedDBLink, app ManagedDBAppRef, state string, actor *AppActor) error {
	raw, _ := json.Marshal(map[string]any{"id": l.ID.String(), "app": app, "mode": l.Mode, "state": state})
	return dbEvent(ctx, tx, name, d, actor, func(p *domainevents.DatabasePayload) { p.Link = raw })
}

// LinkAppDBInput sono i dati gia' validati di un collegamento.
type LinkAppDBInput struct {
	DB     ManagedDB
	LinkID uuid.UUID
	AppID  uuid.UUID
	App    ManagedDBAppRef
	Mode   string
	// Env sono le variabili da iniettare (ManagedDBEnvNames): finiscono cifrate nei segreti dell'app.
	Env   map[string]string
	Actor *AppActor
}

// LinkAppDB registra il collegamento, scrive le variabili come segreti dell'app e crea la versione di
// configurazione con il suo deploy (come PutAppSecret), tutto in una transazione. `before` gira prima del
// commit e crea il ruolo di login nel database: se fallisce non resta niente. ErrManagedDBLinkTaken se
// l'app e' gia' collegata, *DBVariableConflictError se un nome e' occupato, ErrManagedDBDeleting.
func (s *Store) LinkAppDB(ctx context.Context, keys AppSealer, in LinkAppDBInput, before func(context.Context) error, now time.Time) (ManagedDBLink, AppVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockApp(ctx, tx, in.AppID); err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM core.managed_dbs WHERE id = $1 FOR SHARE`, in.DB.ID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ManagedDBLink{}, AppVersion{}, ErrManagedDBNotFound
		}
		return ManagedDBLink{}, AppVersion{}, err
	}
	if status == DBDeleting {
		return ManagedDBLink{}, AppVersion{}, ErrManagedDBDeleting
	}
	names := make([]string, 0, len(in.Env))
	for n := range in.Env {
		names = append(names, n)
	}
	sort.Strings(names)
	if taken, err := takenEnvNames(ctx, tx, in.AppID, names); err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	} else if len(taken) > 0 {
		return ManagedDBLink{}, AppVersion{}, &DBVariableConflictError{Names: taken}
	}
	link := ManagedDBLink{ID: in.LinkID, DBID: in.DB.ID, AppID: in.AppID, Mode: in.Mode, State: "active", CreatedBy: in.Actor, CreatedAt: now}
	if _, err := tx.Exec(ctx, `INSERT INTO core.managed_db_links (id, db_id, app_id, mode, created_by, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		link.ID, link.DBID, link.AppID, link.Mode, actorJSON(in.Actor), now); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return ManagedDBLink{}, AppVersion{}, ErrManagedDBLinkTaken
		}
		return ManagedDBLink{}, AppVersion{}, err
	}
	number, err := allocVersion(ctx, tx, in.AppID, now)
	if err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	}
	if err := putManagedSecrets(ctx, tx, keys, in.AppID, in.Env, number, in.Actor, now); err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	}
	cfg, err := currentConfig(ctx, tx, in.AppID)
	if err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	}
	ver, err := writeConfigVersion(ctx, tx, in.AppID, number, cfg, "database "+in.DB.Name+" collegato", in.Actor, now)
	if err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	}
	if err := dbAudit(ctx, tx, in.DB, "linked", in.Actor, map[string]any{"linkId": link.ID, "app": in.App, "mode": link.Mode}, now); err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	}
	if err := linkEvent(ctx, tx, domainevents.DatabaseLinked, in.DB, link, in.App, link.State, in.Actor); err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	}
	if before != nil {
		if err := before(ctx); err != nil {
			return ManagedDBLink{}, AppVersion{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ManagedDBLink{}, AppVersion{}, err
	}
	return link, ver, nil
}

// takenEnvNames dice quali nomi sono gia' una variabile o un segreto dell'app.
func takenEnvNames(ctx context.Context, tx pgx.Tx, appID uuid.UUID, names []string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT name FROM core.app_variables WHERE app_id = $1 AND name = ANY($2)
		UNION SELECT name FROM core.app_secrets WHERE app_id = $1 AND name = ANY($2) ORDER BY 1`, appID, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// putManagedSecrets cifra e scrive (crea o sostituisce) i segreti del collegamento con la versione number.
func putManagedSecrets(ctx context.Context, tx pgx.Tx, keys AppSealer, appID uuid.UUID, env map[string]string, number int, actor *AppActor, now time.Time) error {
	names := make([]string, 0, len(env))
	for n := range env {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		id, found := uuid.New(), true
		err := tx.QueryRow(ctx, `SELECT id FROM core.app_secrets WHERE app_id = $1 AND name = $2 FOR UPDATE`, appID, name).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			found = false
		} else if err != nil {
			return err
		}
		ct, nonce, keyID, err := keys.Seal(id, env[name])
		if err != nil {
			return err
		}
		if found {
			_, err = tx.Exec(ctx, `UPDATE core.app_secrets SET ciphertext = $2, nonce = $3, secret_key_id = $4, version = $5,
				updated_by = $6, updated_at = $7 WHERE id = $1`, id, ct, nonce, keyID, number, actorJSON(actor), now)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO core.app_secrets (id, app_id, name, ciphertext, nonce, secret_key_id, version, updated_by, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, appID, name, ct, nonce, keyID, number, actorJSON(actor), now)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// RotateAppDBLink riscrive le variabili del collegamento (password nuova) e crea la versione con il suo
// deploy: l'app riparte con i valori nuovi (DB12). I segreti mancanti si ricreano.
func (s *Store) RotateAppDBLink(ctx context.Context, keys AppSealer, db ManagedDB, linkID, appID uuid.UUID, env map[string]string, actor *AppActor, now time.Time) (AppVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppVersion{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockApp(ctx, tx, appID); err != nil {
		return AppVersion{}, err
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM core.managed_db_links WHERE id = $1 AND db_id = $2 FOR UPDATE`, linkID, db.ID).Scan(&state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppVersion{}, ErrManagedDBLinkNotFound
		}
		return AppVersion{}, err
	}
	number, err := allocVersion(ctx, tx, appID, now)
	if err != nil {
		return AppVersion{}, err
	}
	if err := putManagedSecrets(ctx, tx, keys, appID, env, number, actor, now); err != nil {
		return AppVersion{}, err
	}
	cfg, err := currentConfig(ctx, tx, appID)
	if err != nil {
		return AppVersion{}, err
	}
	ver, err := writeConfigVersion(ctx, tx, appID, number, cfg, "database "+db.Name+": password del collegamento ruotata", actor, now)
	if err != nil {
		return AppVersion{}, err
	}
	if err := dbAudit(ctx, tx, db, "link_rotated", actor, map[string]any{"linkId": linkID}, now); err != nil {
		return AppVersion{}, err
	}
	return ver, tx.Commit(ctx)
}

// CompleteDBUnlink chiude lo scollegamento DOPO che il ruolo e' stato tolto (DB12 passo 4): toglie le
// variabili dall'app (nuova versione e deploy: l'app riavvia), poi la riga del collegamento. Se l'app non
// c'e' piu' o non ha piu' le variabili toglie solo la riga. Ritorna la versione creata (zero se nessuna).
func (s *Store) CompleteDBUnlink(ctx context.Context, db ManagedDB, link ManagedDBLink, app ManagedDBAppRef, reason string, actor *AppActor, now time.Time) (AppVersion, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppVersion{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var ver AppVersion
	made := false
	// Prima di qualsiasi modifica, sotto la serratura dell'app, si blocca la riga ESATTA del collegamento
	// (id e app). Se non c'e' piu' (scollegamento gia' completato da un altro giro, snapshot obsoleto) non si
	// tocca niente: l'app puo' nel frattempo essere stata collegata ad altro o avere segreti con quei nomi.
	switch err := lockApp(ctx, tx, link.AppID); {
	case err == nil:
		var cur uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM core.managed_db_links WHERE id = $1 AND db_id = $2 AND app_id = $3 FOR UPDATE`, link.ID, db.ID, link.AppID).Scan(&cur)
		if errors.Is(err, pgx.ErrNoRows) {
			return AppVersion{}, false, nil
		}
		if err != nil {
			return AppVersion{}, false, err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM core.app_secrets WHERE app_id = $1 AND name = ANY($2)`, link.AppID, ManagedDBEnvNames)
		if err != nil {
			return AppVersion{}, false, err
		}
		if tag.RowsAffected() > 0 {
			number, err := allocVersion(ctx, tx, link.AppID, now)
			if err != nil {
				return AppVersion{}, false, err
			}
			cfg, err := currentConfig(ctx, tx, link.AppID)
			if err != nil {
				return AppVersion{}, false, err
			}
			summary := "database " + db.Name + " scollegato"
			if ver, err = writeConfigVersion(ctx, tx, link.AppID, number, cfg, summary, actor, now); err != nil {
				return AppVersion{}, false, err
			}
			made = true
		}
	case errors.Is(err, ErrNotFound):
		// App eliminata: il collegamento e' sparito a cascata, niente da fare.
		return AppVersion{}, false, tx.Commit(ctx)
	default:
		return AppVersion{}, false, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM core.managed_db_links WHERE id = $1`, link.ID)
	if err != nil {
		return AppVersion{}, false, err
	}
	if tag.RowsAffected() == 0 {
		// Gia' scollegato da un altro giro: niente audit doppio.
		return ver, made, tx.Commit(ctx)
	}
	if err := dbAudit(ctx, tx, db, "unlinked", actor, map[string]any{"linkId": link.ID, "app": app, "reason": reason}, now); err != nil {
		return AppVersion{}, false, err
	}
	if err := linkEvent(ctx, tx, domainevents.DatabaseUnlinked, db, link, app, "unlinked", actor); err != nil {
		return AppVersion{}, false, err
	}
	return ver, made, tx.Commit(ctx)
}

// appHasDBLink dice se l'app ha un collegamento (anche in `unlinking`): le sue variabili non si toccano a mano.
func appHasDBLink(ctx context.Context, tx pgx.Tx, appID uuid.UUID) (bool, error) {
	var has bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.managed_db_links WHERE app_id = $1)`, appID).Scan(&has)
	return has, err
}

// guardManagedEnvName rifiuta di cambiare a mano una variabile gestita da un collegamento database
// (DATABASE_URL, PG*): si cambia scollegando o ruotando il collegamento.
func guardManagedEnvName(ctx context.Context, tx pgx.Tx, appID uuid.UUID, name string) error {
	if !IsManagedDBEnvName(name) {
		return nil
	}
	has, err := appHasDBLink(ctx, tx, appID)
	if err != nil {
		return err
	}
	if has {
		return ErrManagedEnvName
	}
	return nil
}

// AppDBLinkPassword legge il PGPASSWORD corrente dell'app (vuoto se manca): serve a riconoscere una rotazione
// gia' arrivata in fondo, per non ripeterla.
func (s *Store) AppDBLinkPassword(ctx context.Context, keys AppSealer, appID uuid.UUID) (string, error) {
	v, err := s.AppSecretValuesForNames(ctx, keys, appID, []string{"PGPASSWORD"})
	if err != nil {
		return "", err
	}
	return v["PGPASSWORD"], nil
}

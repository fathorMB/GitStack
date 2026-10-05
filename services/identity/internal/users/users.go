// Package users implementa gli utenti locali di identity: creazione,
// lettura, elenco, aggiornamento del profilo, disattivazione, eliminazione e
// cambio password, sulle tabelle identity.users e identity.credentials.
//
// Le regole che toccano più tabelle (revoca delle sessioni e dei token alla
// disattivazione o al cambio password, protezione dell'ultimo amministratore)
// stanno nella stessa transazione della modifica. Le password sono salvate
// solo come hash argon2id (package password); password e hash non compaiono
// mai negli errori.
package users

import (
	"github.com/fathorMB/GitStack/pkg/names"
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/identity/internal/password"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errori di dominio (mappati a 404/409/401 dal livello HTTP).
var (
	ErrNotFound           = errors.New("utente non trovato")
	ErrLastAdmin          = errors.New("non si può disattivare, eliminare o degradare l'ultimo amministratore")
	ErrInvalidCredentials = errors.New("credenziali non valide")
)

// AlreadyExistsError: username o email già usati (Field dice quale).
type AlreadyExistsError struct{ Field string }

// ReservedNameError: il nome è riservato (R1, pkg/names): 400.
type ReservedNameError struct{ Name string }

func (e *ReservedNameError) Error() string { return "il nome " + e.Name + " è riservato" }

func (e *AlreadyExistsError) Error() string { return e.Field + " già in uso" }

// ValidationError raccoglie i motivi di rifiuto per campo (422 validation_failed).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for k, v := range e.Fields {
		parts = append(parts, k+": "+v)
	}
	return "validazione non riuscita: " + strings.Join(parts, "; ")
}

var usernameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?$`)

const (
	KindHuman = "human"
	KindAgent = "agent"

	// adminLockKey serializza le operazioni che possono togliere un
	// amministratore (pg_advisory_xact_lock), per non lasciare mai zero admin.
	adminLockKey = 0x67735f61646d696e
)

// User è la riga di identity.users.
type User struct {
	ID          uuid.UUID
	Username    string
	Email       *string
	DisplayName string
	Bio         string
	AvatarURL   *string
	Kind        string
	IsAdmin     bool
	IsActive    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const userCols = `id, username, email, display_name, bio, avatar_url, kind, is_admin, is_active, created_at, updated_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.Bio, &u.AvatarURL, &u.Kind, &u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// Service è il servizio utenti.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New crea il servizio. now nil usa time.Now.
func New(pool *pgxpool.Pool, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, now: now}
}

// CreateInput: come CreateUserInput del contratto.
type CreateInput struct {
	Username    string
	Kind        string // "" = human
	Email       string
	DisplayName string
	Password    string // vuoto per agenti e per chi entra solo via OIDC
	IsAdmin     bool
}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && a.Name == "" && len(s) <= 254
}

func validAvatar(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && len(s) <= 2048
}

// Create crea un utente (e la sua credenziale password, se data).
func (s *Service) Create(ctx context.Context, in CreateInput) (User, error) {
	if in.Kind == "" {
		in.Kind = KindHuman
	}
	f := map[string]string{}
	if !usernameRe.MatchString(in.Username) {
		f["username"] = "minuscole, cifre e trattini, 1-39 caratteri, inizia e finisce con un carattere alfanumerico"
	}
	if in.Kind != KindHuman && in.Kind != KindAgent {
		f["kind"] = "deve essere human o agent"
	}
	if in.Email != "" && !validEmail(in.Email) {
		f["email"] = "indirizzo email non valido"
	}
	if utf8.RuneCountInString(in.DisplayName) > 128 {
		f["displayName"] = "al massimo 128 caratteri"
	}
	if in.Password != "" {
		if in.Kind == KindAgent {
			f["password"] = "gli agenti accedono solo con token"
		} else if err := password.ValidatePolicy(in.Password, in.Username, in.Email); err != nil {
			f["password"] = err.Error()
		}
	}
	if len(f) > 0 {
		return User{}, &ValidationError{Fields: f}
	}
	if names.IsReservedOwnerName(in.Username) {
		return User{}, &ReservedNameError{Name: in.Username}
	}

	var hash string
	if in.Password != "" {
		var err error
		if hash, err = password.Hash(in.Password); err != nil {
			return User{}, err
		}
	}
	var email *string
	if in.Email != "" {
		email = &in.Email
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := s.now()
	u, err := scanUser(tx.QueryRow(ctx, `
		INSERT INTO identity.users (id, username, email, display_name, kind, is_admin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7) RETURNING `+userCols,
		uuid.New(), in.Username, email, in.DisplayName, in.Kind, in.IsAdmin, now))
	if err != nil {
		return User{}, mapUnique(err)
	}
	if hash != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO identity.credentials (user_id, kind, secret_hash, created_at, updated_at)
			VALUES ($1, 'password', $2, $3, $3)`, u.ID, hash, now); err != nil {
			return User{}, fmt.Errorf("salvataggio della credenziale non riuscito: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return u, nil
}

func mapUnique(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		if strings.Contains(pg.ConstraintName, "email") {
			return &AlreadyExistsError{Field: "email"}
		}
		return &AlreadyExistsError{Field: "username"}
	}
	return err
}

// Get legge un utente per username.
func (s *Service) Get(ctx context.Context, username string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM identity.users WHERE username = $1`, username))
}

// GetByEmail legge un utente per email, senza distinzione di maiuscole
// (indice users_email_key). ErrNotFound se non esiste.
func (s *Service) GetByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM identity.users WHERE lower(email) = lower($1)`, email))
}

// GetByID legge un utente per id.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM identity.users WHERE id = $1`, id))
}

// List elenca gli utenti per username; q è un prefisso di username o nome
// visualizzato. page parte da 1.
func (s *Service) List(ctx context.Context, q string, page, perPage int) ([]User, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	like := escapeLike(strings.ToLower(q)) + "%"
	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM identity.users
		WHERE $1 = '' OR username LIKE $2 OR lower(display_name) LIKE $2`, q, like).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+userCols+` FROM identity.users
		WHERE $1 = '' OR username LIKE $2 OR lower(display_name) LIKE $2
		ORDER BY username LIMIT $3 OFFSET $4`, q, like, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// UpdateInput: un puntatore nil = campo invariato. AvatarURL "" = cancella.
// IsAdmin e IsActive li può impostare solo un amministratore: il controllo
// dei permessi è del chiamante (livello HTTP).
type UpdateInput struct {
	DisplayName *string
	Bio         *string
	AvatarURL   *string
	Email       *string
	IsAdmin     *bool
	IsActive    *bool
}

// Update applica un aggiornamento parziale. Disattivare un utente
// (IsActive=false) revoca in transazione tutte le sue sessioni e i suoi
// token personali. Degradare o disattivare l'ultimo amministratore attivo
// dà ErrLastAdmin.
func (s *Service) Update(ctx context.Context, username string, in UpdateInput) (User, error) {
	f := map[string]string{}
	if in.DisplayName != nil && utf8.RuneCountInString(*in.DisplayName) > 128 {
		f["displayName"] = "al massimo 128 caratteri"
	}
	if in.Bio != nil && utf8.RuneCountInString(*in.Bio) > 1024 {
		f["bio"] = "al massimo 1024 caratteri"
	}
	if in.AvatarURL != nil && *in.AvatarURL != "" && !validAvatar(*in.AvatarURL) {
		f["avatarUrl"] = "URL http(s) non valido"
	}
	if in.Email != nil && !validEmail(*in.Email) {
		f["email"] = "indirizzo email non valido"
	}
	if len(f) > 0 {
		return User{}, &ValidationError{Fields: f}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if in.IsAdmin != nil || in.IsActive != nil {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(adminLockKey)); err != nil {
			return User{}, err
		}
	}
	cur, err := scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM identity.users WHERE username = $1 FOR UPDATE`, username))
	if err != nil {
		return User{}, err
	}
	newAdmin, newActive := cur.IsAdmin, cur.IsActive
	if in.IsAdmin != nil {
		newAdmin = *in.IsAdmin
	}
	if in.IsActive != nil {
		newActive = *in.IsActive
	}
	if cur.IsAdmin && cur.IsActive && (!newAdmin || !newActive) {
		if err := requireOtherAdmin(ctx, tx, cur.ID); err != nil {
			return User{}, err
		}
	}

	var avatar any = cur.AvatarURL
	if in.AvatarURL != nil {
		if *in.AvatarURL == "" {
			avatar = nil
		} else {
			avatar = *in.AvatarURL
		}
	}
	var email any = cur.Email
	if in.Email != nil {
		email = *in.Email
	}
	display, bio := cur.DisplayName, cur.Bio
	if in.DisplayName != nil {
		display = *in.DisplayName
	}
	if in.Bio != nil {
		bio = *in.Bio
	}
	now := s.now()
	u, err := scanUser(tx.QueryRow(ctx, `
		UPDATE identity.users SET display_name=$2, bio=$3, avatar_url=$4, email=$5, is_admin=$6, is_active=$7, updated_at=$8
		WHERE id = $1 RETURNING `+userCols,
		cur.ID, display, bio, avatar, email, newAdmin, newActive, now))
	if err != nil {
		return User{}, mapUnique(err)
	}
	if cur.IsActive && !newActive {
		if err := revokeCredentials(ctx, tx, cur.ID, now); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return u, nil
}

func requireOtherAdmin(ctx context.Context, tx pgx.Tx, except uuid.UUID) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM identity.users WHERE is_admin AND is_active AND id <> $1`, except).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

// revokeCredentials revoca tutte le sessioni e i token personali dell'utente.
func revokeCredentials(ctx context.Context, tx pgx.Tx, userID uuid.UUID, now time.Time) error {
	if _, err := tx.Exec(ctx, sessions.RevokeAllSQL, userID, now, nil); err != nil {
		return fmt.Errorf("revoca delle sessioni non riuscita: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.api_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, now); err != nil {
		return fmt.Errorf("revoca dei token non riuscita: %w", err)
	}
	return nil
}

// Delete elimina l'utente (a cascata sessioni, token, chiavi, credenziali).
// L'ultimo amministratore attivo non si elimina: ErrLastAdmin.
func (s *Service) Delete(ctx context.Context, username string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(adminLockKey)); err != nil {
		return err
	}
	cur, err := scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM identity.users WHERE username = $1 FOR UPDATE`, username))
	if err != nil {
		return err
	}
	if cur.IsAdmin && cur.IsActive {
		if err := requireOtherAdmin(ctx, tx, cur.ID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM identity.users WHERE id = $1`, cur.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ChangePasswordInput descrive un cambio password.
type ChangePasswordInput struct {
	Username string
	// Admin: chi chiama è un amministratore che agisce su un altro utente;
	// non serve la password attuale. Se false, CurrentPassword è obbligatoria.
	Admin           bool
	CurrentPassword string
	NewPassword     string
	// KeepSessionID, se non nil, è la sessione del chiamante, risparmiata
	// dalla revoca (il contratto dice "le altre sessioni"). Nil = revoca tutte.
	KeepSessionID *uuid.UUID
}

// ChangePassword imposta la nuova password e revoca, nella stessa
// transazione, le sessioni dell'utente (tutte, o tutte tranne
// KeepSessionID). Password attuale sbagliata: ErrInvalidCredentials.
func (s *Service) ChangePassword(ctx context.Context, in ChangePasswordInput) error {
	cur, err := s.Get(ctx, in.Username)
	if err != nil {
		return err
	}
	email := ""
	if cur.Email != nil {
		email = *cur.Email
	}
	if err := password.ValidatePolicy(in.NewPassword, cur.Username, email); err != nil {
		return &ValidationError{Fields: map[string]string{"newPassword": err.Error()}}
	}
	if cur.Kind == KindAgent {
		return &ValidationError{Fields: map[string]string{"newPassword": "gli agenti accedono solo con token"}}
	}
	if !in.Admin {
		if in.CurrentPassword == "" {
			return &ValidationError{Fields: map[string]string{"currentPassword": "obbligatoria"}}
		}
		var hash string
		err := s.pool.QueryRow(ctx, `SELECT secret_hash FROM identity.credentials WHERE user_id = $1 AND kind = 'password'`, cur.ID).Scan(&hash)
		if errors.Is(err, pgx.ErrNoRows) {
			password.VerifyDummy(in.CurrentPassword)
			return ErrInvalidCredentials
		}
		if err != nil {
			return err
		}
		if ok, _ := password.Verify(in.CurrentPassword, hash); !ok {
			return ErrInvalidCredentials
		}
	}
	newHash, err := password.Hash(in.NewPassword)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now := s.now()
	if _, err := tx.Exec(ctx, `
		INSERT INTO identity.credentials (user_id, kind, secret_hash, must_change, created_at, updated_at)
		VALUES ($1, 'password', $2, false, $3, $3)
		ON CONFLICT (user_id, kind) DO UPDATE SET secret_hash = EXCLUDED.secret_hash, must_change = false, updated_at = EXCLUDED.updated_at`,
		cur.ID, newHash, now); err != nil {
		return fmt.Errorf("salvataggio della password non riuscito: %w", err)
	}
	if _, err := tx.Exec(ctx, sessions.RevokeAllSQL, cur.ID, now, in.KeepSessionID); err != nil {
		return fmt.Errorf("revoca delle sessioni non riuscita: %w", err)
	}
	return tx.Commit(ctx)
}

// Account è un utente con l'hash della password, per il login.
type Account struct {
	User User
	// PasswordHash è la stringa PHC, vuota se l'utente non ha password
	// (agente, solo OIDC).
	PasswordHash string
}

// FindForLogin trova l'utente da username o email (senza distinzione di
// maiuscole). ErrNotFound se non esiste.
func (s *Service) FindForLogin(ctx context.Context, login string) (Account, error) {
	login = strings.ToLower(strings.TrimSpace(login))
	col := "u.username"
	if strings.Contains(login, "@") {
		col = "lower(u.email)"
	}
	var a Account
	var hash *string
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.username, u.email, u.display_name, u.bio, u.avatar_url, u.kind, u.is_admin, u.is_active, u.created_at, u.updated_at, c.secret_hash
		FROM identity.users u LEFT JOIN identity.credentials c ON c.user_id = u.id AND c.kind = 'password'
		WHERE `+col+` = $1`, login).
		Scan(&a.User.ID, &a.User.Username, &a.User.Email, &a.User.DisplayName, &a.User.Bio, &a.User.AvatarURL, &a.User.Kind,
			&a.User.IsAdmin, &a.User.IsActive, &a.User.CreatedAt, &a.User.UpdatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	if hash != nil {
		a.PasswordHash = *hash
	}
	return a, nil
}

// UpgradeHash riscrive l'hash della password con i parametri correnti
// (dopo un login riuscito con un hash a parametri vecchi).
func (s *Service) UpgradeHash(ctx context.Context, userID uuid.UUID, pw string) error {
	h, err := password.Hash(pw)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE identity.credentials SET secret_hash = $2, updated_at = $3 WHERE user_id = $1 AND kind = 'password'`,
		userID, h, s.now())
	return err
}

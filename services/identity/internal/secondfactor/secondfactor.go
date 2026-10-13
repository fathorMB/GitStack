// Package secondfactor gestisce il secondo fattore TOTP degli account locali
// (F11): attivazione in due tempi (enroll, poi conferma con un codice),
// verifica al login, codici di recupero monouso, disattivazione.
//
// Garanzie:
//   - il segreto è cifrato con AES-256-GCM prima di entrare nel database, con
//     una chiave del keyring (id nella riga: la rotazione è "nuova,vecchia") e
//     l'id utente come AAD, così un blob non vale per un altro utente;
//   - il codice TOTP accetta la finestra ±1 passo e non è riusabile: il passo
//     accettato viene registrato con un UPDATE condizionato, quindi due
//     richieste concorrenti con lo stesso codice non passano entrambe;
//   - i codici di recupero sono salvati solo come hash argon2id salato (stesso
//     formato delle password) e si consumano con un UPDATE condizionato;
//   - segreti e codici non compaiono mai in errori o log.
package secondfactor

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/password"
	"github.com/fathorMB/GitStack/services/identity/internal/totp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errori di dominio.
var (
	// ErrUnavailable: nessuna chiave di cifratura configurata.
	ErrUnavailable = errors.New("secondo fattore non disponibile: chiavi TOTP non configurate")
	// ErrAlreadyEnabled: il TOTP è già attivo.
	ErrAlreadyEnabled = errors.New("il TOTP è già attivo")
	// ErrNotPending: nessuna attivazione in corso.
	ErrNotPending = errors.New("nessuna attivazione TOTP in corso")
	// ErrInvalidCode: codice sbagliato, scaduto o già usato.
	ErrInvalidCode = errors.New("codice non valido")
)

// Key è una chiave del keyring: 32 byte e un id che finisce nella riga.
type Key struct {
	ID  string
	Key []byte
}

var aadPrefix = []byte("gitstack-identity-totp-secret:")

// Service è il servizio del secondo fattore.
type Service struct {
	pool   *pgxpool.Pool
	now    func() time.Time
	keys   []Key // la prima è quella attiva
	issuer string
}

// New crea il servizio. keys vuoto: Available() è falso e le operazioni di
// attivazione rispondono ErrUnavailable (le verifiche su credenziali già
// presenti falliscono con un errore). now nil usa time.Now; issuer vuoto usa
// "GitStack".
func New(pool *pgxpool.Pool, now func() time.Time, keys []Key, issuer string) *Service {
	if now == nil {
		now = time.Now
	}
	if issuer == "" {
		issuer = "GitStack"
	}
	return &Service{pool: pool, now: now, keys: keys, issuer: issuer}
}

// Available dice se si può attivare il TOTP (c'è almeno una chiave).
func (s *Service) Available() bool { return len(s.keys) > 0 }

// Status è lo stato del TOTP di un utente.
type Status struct {
	Enabled                bool
	Pending                bool
	RecoveryCodesRemaining int
}

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("la chiave TOTP deve essere di 32 byte")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func aad(userID uuid.UUID) []byte { return append(append([]byte{}, aadPrefix...), userID[:]...) }

func (s *Service) seal(userID uuid.UUID, secret []byte) ([]byte, string, error) {
	if len(s.keys) == 0 {
		return nil, "", ErrUnavailable
	}
	k := s.keys[0]
	g, err := gcm(k.Key)
	if err != nil {
		return nil, "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", fmt.Errorf("generazione del nonce non riuscita: %w", err)
	}
	return g.Seal(nonce, nonce, secret, aad(userID)), k.ID, nil
}

func (s *Service) open(userID uuid.UUID, blob []byte, keyID string) ([]byte, error) {
	for _, k := range s.keys {
		if k.ID != keyID {
			continue
		}
		g, err := gcm(k.Key)
		if err != nil {
			return nil, err
		}
		if len(blob) < g.NonceSize()+g.Overhead() {
			return nil, errors.New("segreto TOTP cifrato non valido")
		}
		plain, err := g.Open(nil, blob[:g.NonceSize()], blob[g.NonceSize():], aad(userID))
		if err != nil {
			return nil, errors.New("segreto TOTP cifrato non valido")
		}
		return plain, nil
	}
	return nil, fmt.Errorf("chiave TOTP %q non presente nel keyring", keyID)
}

// Status legge lo stato del TOTP dell'utente.
func (s *Service) Status(ctx context.Context, userID uuid.UUID) (Status, error) {
	var confirmed bool
	err := s.pool.QueryRow(ctx,
		`SELECT confirmed_at IS NOT NULL FROM identity.totp_credentials WHERE user_id = $1`, userID).Scan(&confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	st := Status{Enabled: confirmed, Pending: !confirmed}
	if confirmed {
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM identity.totp_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID).
			Scan(&st.RecoveryCodesRemaining); err != nil {
			return Status{}, err
		}
	}
	return st, nil
}

// Enabled dice se l'utente ha un TOTP confermato.
func (s *Service) Enabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	st, err := s.Status(ctx, userID)
	return st.Enabled, err
}

// Enrollment è il risultato di Enroll: segreto in base32 e URI otpauth.
type Enrollment struct {
	Secret string
	URI    string
}

// Enroll genera un segreto nuovo e lo salva (non confermato). Se esiste
// un'attivazione in attesa la sostituisce; con TOTP già attivo: ErrAlreadyEnabled.
func (s *Service) Enroll(ctx context.Context, userID uuid.UUID, account string) (Enrollment, error) {
	if !s.Available() {
		return Enrollment{}, ErrUnavailable
	}
	secret, err := totp.NewSecret()
	if err != nil {
		return Enrollment{}, err
	}
	blob, keyID, err := s.seal(userID, secret)
	if err != nil {
		return Enrollment{}, err
	}
	now := s.now()
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO identity.totp_credentials (user_id, secret_enc, enc_key_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (user_id) DO UPDATE
		   SET secret_enc = EXCLUDED.secret_enc, enc_key_id = EXCLUDED.enc_key_id,
		       last_used_step = 0, created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
		 WHERE identity.totp_credentials.confirmed_at IS NULL`,
		userID, blob, keyID, now)
	if err != nil {
		return Enrollment{}, fmt.Errorf("salvataggio del segreto TOTP non riuscito: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Enrollment{}, ErrAlreadyEnabled
	}
	return Enrollment{Secret: totp.EncodeSecret(secret), URI: totp.ProvisioningURI(s.issuer, account, secret)}, nil
}

// load legge e decifra il segreto dell'utente. confirmed dice se è attivo.
func (s *Service) load(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID uuid.UUID, lock bool) (secret []byte, confirmed bool, ok bool, err error) {
	sql := `SELECT secret_enc, enc_key_id, confirmed_at IS NOT NULL FROM identity.totp_credentials WHERE user_id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	var blob []byte
	var keyID string
	err = q.QueryRow(ctx, sql, userID).Scan(&blob, &keyID, &confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	secret, err = s.open(userID, blob, keyID)
	if err != nil {
		return nil, false, false, err
	}
	return secret, confirmed, true, nil
}

// Confirm verifica il primo codice del segreto in attesa, attiva il TOTP e
// genera i codici di recupero (in chiaro solo qui). Nessuna attivazione in
// attesa: ErrNotPending; codice sbagliato: ErrInvalidCode.
func (s *Service) Confirm(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	secret, confirmed, ok, err := s.load(ctx, tx, userID, true)
	if err != nil {
		return nil, err
	}
	if !ok || confirmed {
		return nil, ErrNotPending
	}
	step, match := totp.Match(secret, code, s.now())
	if !match {
		return nil, ErrInvalidCode
	}
	now := s.now()
	if _, err := tx.Exec(ctx,
		`UPDATE identity.totp_credentials SET confirmed_at = $2, last_used_step = $3, updated_at = $2 WHERE user_id = $1`,
		userID, now, step); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM identity.totp_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, totp.RecoveryCodeCount)
	for len(codes) < totp.RecoveryCodeCount {
		c, err := totp.NewRecoveryCode()
		if err != nil {
			return nil, err
		}
		h, err := password.Hash(totp.NormalizeRecoveryCode(c))
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO identity.totp_recovery_codes (id, user_id, code_hash, created_at) VALUES ($1, $2, $3, $4)`,
			uuid.New(), userID, h, now); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return codes, nil
}

// Verify controlla un secondo fattore di un utente con TOTP attivo: un codice
// TOTP (finestra ±1, non riusabile) oppure un codice di recupero (si
// consuma). Con entrambi vuoti o sbagliati ritorna false. Un utente senza
// TOTP confermato ritorna false: il chiamante controlla prima Enabled.
func (s *Service) Verify(ctx context.Context, userID uuid.UUID, totpCode, recoveryCode string) (bool, error) {
	if recoveryCode != "" {
		norm := totp.NormalizeRecoveryCode(recoveryCode)
		if len(norm) != 10 {
			return false, nil
		}
		rows, err := s.pool.Query(ctx, `
			SELECT r.id, r.code_hash FROM identity.totp_recovery_codes r
			  JOIN identity.totp_credentials c ON c.user_id = r.user_id AND c.confirmed_at IS NOT NULL
			 WHERE r.user_id = $1 AND r.used_at IS NULL`, userID)
		if err != nil {
			return false, err
		}
		type stored struct {
			id   uuid.UUID
			hash string
		}
		var all []stored
		for rows.Next() {
			var st stored
			if err := rows.Scan(&st.id, &st.hash); err != nil {
				rows.Close()
				return false, err
			}
			all = append(all, st)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, err
		}
		// Si verificano tutti i codici non usati (tempo costante rispetto a
		// quale combacia); poi si consuma quello trovato con un UPDATE
		// condizionato, così due richieste concorrenti non lo usano entrambe.
		var match uuid.UUID
		for _, st := range all {
			if good, verr := password.Verify(norm, st.hash); verr == nil && good {
				match = st.id
			}
		}
		if match == uuid.Nil {
			return false, nil
		}
		tag, err := s.pool.Exec(ctx,
			`UPDATE identity.totp_recovery_codes SET used_at = $2 WHERE id = $1 AND used_at IS NULL`, match, s.now())
		if err != nil {
			return false, err
		}
		return tag.RowsAffected() == 1, nil
	}
	if totpCode == "" {
		return false, nil
	}
	secret, confirmed, ok, err := s.load(ctx, s.pool, userID, false)
	if err != nil {
		return false, err
	}
	if !ok || !confirmed {
		return false, nil
	}
	step, match := totp.Match(secret, totpCode, s.now())
	if !match {
		return false, nil
	}
	// Anti-replay: il passo vale solo se è più recente dell'ultimo usato; la
	// condizione nell'UPDATE rende l'accettazione atomica.
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.totp_credentials SET last_used_step = $2, updated_at = $3
		 WHERE user_id = $1 AND confirmed_at IS NOT NULL AND last_used_step < $2`,
		userID, step, s.now())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Remove toglie segreto e codici di recupero dell'utente (disattivazione o
// azzeramento da parte di un amministratore). Idempotente.
func (s *Service) Remove(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM identity.totp_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM identity.totp_credentials WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

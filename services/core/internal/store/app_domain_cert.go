package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Certificato del cliente per un dominio proprio (v3-c, GIT-460). La catena pubblica è in chiaro; la
// chiave privata è cifrata col Keyring dei segreti (l'id del dominio è il dato autenticato) e non sta
// in appDomainCols: nessuna query di lettura dei domini la porta con sé, l'unica che la legge è
// CustomCertificates, per il controller che la scrive nel Secret del namespace Traefik.

// ErrNotCustom: il dominio non ha un certificato caricato dal cliente.
var ErrNotCustom = errors.New("il dominio non ha un certificato del cliente")

// CustomCertInput è il certificato validato da salvare.
type CustomCertInput struct {
	ChainPEM   string
	KeyPEM     string
	ValidFrom  time.Time
	ValidUntil time.Time
	Issuer     string
}

// SetAppDomainCustomCert salva il certificato del cliente (sostituendo un precedente) e porta il dominio
// in modo custom, stato valid. La chiave si cifra qui e non esce più dallo store se non per il controller.
func (s *Store) SetAppDomainCustomCert(ctx context.Context, keys AppSealer, id uuid.UUID, in CustomCertInput, now time.Time) (AppDomain, error) {
	ct, nonce, keyID, err := keys.Seal(id, in.KeyPEM)
	if err != nil {
		return AppDomain{}, errors.New("cifratura della chiave non riuscita")
	}
	from, until, issuer := in.ValidFrom.UTC(), in.ValidUntil.UTC(), in.Issuer
	return scanAppDomain(s.pool.QueryRow(ctx, `UPDATE core.app_domains SET cert_mode = 'custom', cert_state = 'valid',
		cert_valid_from = $2, cert_valid_until = $3, cert_issuer = $4, cert_error = NULL, cert_last_attempt_at = $5,
		cert_pem = $6, cert_key_ciphertext = $7, cert_key_nonce = $8, cert_key_id = $9
		WHERE id = $1 RETURNING `+appDomainCols, id, from, until, issuer, now, in.ChainPEM, ct, nonce, keyID))
}

// ClearAppDomainCustomCert toglie certificato e chiave del cliente e riporta il dominio a letsencrypt,
// con l'emissione da rifare (stato none: lo riscrive il controllo dei certificati). ErrNotCustom se il
// dominio non era custom, ErrNotFound se non c'è.
func (s *Store) ClearAppDomainCustomCert(ctx context.Context, id uuid.UUID) (AppDomain, error) {
	d, err := scanAppDomain(s.pool.QueryRow(ctx, `UPDATE core.app_domains SET cert_mode = 'letsencrypt', cert_state = 'none',
		cert_valid_from = NULL, cert_valid_until = NULL, cert_issuer = NULL, cert_error = NULL, cert_last_attempt_at = NULL,
		cert_pem = NULL, cert_key_ciphertext = NULL, cert_key_nonce = NULL, cert_key_id = NULL
		WHERE id = $1 AND cert_mode = 'custom' RETURNING `+appDomainCols, id))
	if errors.Is(err, ErrNotFound) {
		var n int
		if qerr := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.app_domains WHERE id = $1`, id).Scan(&n); qerr == nil && n > 0 {
			return AppDomain{}, ErrNotCustom
		}
	}
	return d, err
}

// SetCustomCertState scrive lo stato di un certificato del cliente (valid o error con il motivo): lo usa
// il controller quando il certificato scade. ErrNotFound se il dominio non è custom.
func (s *Store) SetCustomCertState(ctx context.Context, id uuid.UUID, state string, errMsg *string, now time.Time) (AppDomain, error) {
	return scanAppDomain(s.pool.QueryRow(ctx, `UPDATE core.app_domains SET cert_state = $2, cert_error = $3, cert_last_attempt_at = $4
		WHERE id = $1 AND cert_mode = 'custom' RETURNING `+appDomainCols, id, state, errMsg, now))
}

// CustomCertificate è un certificato del cliente con la chiave in chiaro, solo per il controller.
type CustomCertificate struct {
	DomainID   uuid.UUID
	AppID      uuid.UUID
	Domain     string
	ChainPEM   string
	KeyPEM     string
	ValidUntil *time.Time
	State      string
}

// String non espone la chiave: un CustomCertificate stampato per sbaglio non la porta nei log.
func (c CustomCertificate) String() string { return "certificato del cliente per " + c.Domain }

// CustomCertificates dà i certificati del cliente con la chiave decifrata. Un certificato che non si
// decifra è un errore: meglio non scrivere niente che un Secret sbagliato.
func (s *Store) CustomCertificates(ctx context.Context, keys AppSealer) ([]CustomCertificate, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, app_id, normalized, cert_pem, cert_key_ciphertext, cert_key_nonce, cert_key_id, cert_valid_until, cert_state
		FROM core.app_domains WHERE cert_mode = 'custom' AND cert_pem IS NOT NULL ORDER BY normalized`)
	if err != nil {
		return nil, err
	}
	type raw struct {
		c         CustomCertificate
		ct, nonce []byte
		keyID     string
	}
	var all []raw
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.c.DomainID, &r.c.AppID, &r.c.Domain, &r.c.ChainPEM, &r.ct, &r.nonce, &r.keyID, &r.c.ValidUntil, &r.c.State); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]CustomCertificate, 0, len(all))
	for _, r := range all {
		key, err := keys.Open(r.c.DomainID, r.ct, r.nonce, r.keyID)
		if err != nil {
			return nil, errors.New("la chiave del certificato di " + r.c.Domain + " non si decifra")
		}
		r.c.KeyPEM = key
		out = append(out, r.c)
	}
	return out, nil
}

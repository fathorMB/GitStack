// Package sshkeys analizza chiavi pubbliche in formato authorized_keys,
// valida il tipo e la lunghezza, e calcola il fingerprint SHA256.
//
// Non dipende da database o HTTP: è un insieme di funzioni pure.
package sshkeys

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ErrInvalidFormat indica che la stringa fornita non è una riga authorized_keys
// valida.
var ErrInvalidFormat = errors.New("formato chiave non valido")

// ErrPrivateKey indica che è stata fornita una chiave privata invece di una
// pubblica.
var ErrPrivateKey = errors.New("chiave privata, attesa una chiave pubblica")

// ErrWeakKeyType indica che il tipo di chiave (es. DSA) è considerato debole
// e non accettato.
var ErrWeakKeyType = errors.New("tipo di chiave debole non accettato")

// ErrKeyTooShort indica che la chiave è valida per tipo ma troppo corta
// (es. RSA < 3072 bit).
var ErrKeyTooShort = errors.New("chiave troppo corta")

// Key rappresenta una chiave pubblica analizzata e validata.
type Key struct {
	Type        string
	Bits        int
	Fingerprint string
	Comment     string
}

// Parse analizza una riga authorized_keys e restituisce la chiave
// rappresentata, oppure un errore se la riga non è valida.
func Parse(line string) (Key, error) {
	line = strings.TrimRight(line, "\r\n\t ")

	if line == "" {
		return Key{}, ErrInvalidFormat
	}

	// Salta i commenti.
	fields := strings.Fields(line)
	if len(fields) > 0 && strings.Contains(fields[0], "#") {
		return Key{}, ErrInvalidFormat
	}

	// Chiavi private: cerca "PRIVATE KEY" nel testo.
	if strings.Contains(line, "PRIVATE KEY") {
		return Key{}, ErrPrivateKey
	}

	// Accetta solo una singola riga: nessun newline interno.
	if strings.Contains(line, "\n") {
		return Key{}, ErrInvalidFormat
	}

	// Blocchi PEM pubblici (es. BEGIN PUBLIC KEY, BEGIN PGP PUBLIC KEY BLOCK)
	// non sono formato authorized_keys.
	if strings.HasPrefix(line, "-----BEGIN") {
		return Key{}, ErrInvalidFormat
	}

	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return Key{}, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
	}

	if pub == nil {
		return Key{}, ErrInvalidFormat
	}

	k := Key{
		Fingerprint: ssh.FingerprintSHA256(pub),
		Comment:     normalizeComment(comment),
	}

	switch t := pub.Type(); t {
	case "ssh-ed25519":
		k.Type = "ssh-ed25519"
		k.Bits = 256
	case "ecdsa-sha2-nistp256":
		k.Type = "ecdsa-sha2-nistp256"
		k.Bits = 256
	case "ecdsa-sha2-nistp384":
		k.Type = "ecdsa-sha2-nistp384"
		k.Bits = 384
	case "ecdsa-sha2-nistp521":
		k.Type = "ecdsa-sha2-nistp521"
		k.Bits = 521
	case "ssh-rsa":
		k.Type = "ssh-rsa"
		k.Bits = rsaBits(pub)
	default:
		return Key{}, fmt.Errorf("%w: %s", ErrWeakKeyType, t)
	}

	if err := validateType(k.Type, k.Bits); err != nil {
		return Key{}, err
	}

	return k, nil
}

// rsaBits estrae il numero di bit della chiave RSA.
func rsaBits(pub ssh.PublicKey) int {
	cp, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return 0
	}
	r, ok := cp.CryptoPublicKey().(*rsa.PublicKey)
	if !ok {
		return 0
	}
	return r.N.BitLen()
}

// validateType controlla il tipo e la lunghezza della chiave.
func validateType(tp string, bits int) error {
	switch tp {
	case "ssh-ed25519", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		return nil
	case "ssh-rsa":
		if bits < 3072 {
			return fmt.Errorf("%w: RSA %d bit, minimo 3072", ErrKeyTooShort, bits)
		}
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrWeakKeyType, tp)
	}
}

// normalizeComment normalizza il commento.
func normalizeComment(c string) string {
	c = strings.TrimRight(c, "\r\n\t ")
	return strings.Join(strings.Fields(c), " ")
}

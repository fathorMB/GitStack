package backup

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Cifratura opzionale dell'archivio con una chiave fornita dall'operatore
// (file con almeno 16 byte, per esempio `openssl rand -base64 32`).
//
// Formato: magic (8 byte) | salt (16 byte) | blocchi. Ogni blocco è
// lunghezza (uint32 big-endian) | AES-256-GCM(testo, nonce = contatore a 12
// byte, AAD = 1 se è l'ultimo blocco, 0 altrimenti). L'AAD finale rende
// rilevabile un archivio troncato. La chiave AES è HKDF-SHA256(chiave
// fornita, salt).

var encMagic = []byte("GSBKENC1")

const (
	saltLen   = 16
	chunkSize = 64 * 1024
	// MinKeyLen è la lunghezza minima accettata per la chiave fornita.
	MinKeyLen = 16
)

// ErrWrongKey indica chiave sbagliata o archivio cifrato alterato.
var ErrWrongKey = errors.New("chiave di cifratura errata o archivio alterato")

func deriveKey(secret, salt []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, secret, salt, "gitstack-backup-v1", 32)
}

func newGCM(secret, salt []byte) (cipher.AEAD, error) {
	k, err := deriveKey(secret, salt)
	if err != nil {
		return nil, err
	}
	b, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func nonceFor(n uint64) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], n)
	return nonce
}

type encWriter struct {
	w       io.Writer
	aead    cipher.AEAD
	buf     []byte
	counter uint64
	closed  bool
}

// NewEncryptWriter cifra tutto ciò che si scrive; Close scrive il blocco finale.
func NewEncryptWriter(w io.Writer, secret []byte) (io.WriteCloser, error) {
	if len(secret) < MinKeyLen {
		return nil, fmt.Errorf("chiave troppo corta (almeno %d byte)", MinKeyLen)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	aead, err := newGCM(secret, salt)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(append(append([]byte{}, encMagic...), salt...)); err != nil {
		return nil, err
	}
	return &encWriter{w: w, aead: aead, buf: make([]byte, 0, chunkSize)}, nil
}

func (e *encWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		room := chunkSize - len(e.buf)
		take := min(room, len(p))
		e.buf = append(e.buf, p[:take]...)
		p = p[take:]
		if len(e.buf) == chunkSize {
			if err := e.flush(false); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (e *encWriter) flush(final bool) error {
	aad := []byte{0}
	if final {
		aad[0] = 1
	}
	ct := e.aead.Seal(nil, nonceFor(e.counter), e.buf, aad)
	e.counter++
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(ct)))
	if _, err := e.w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := e.w.Write(ct); err != nil {
		return err
	}
	e.buf = e.buf[:0]
	return nil
}

func (e *encWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.flush(true)
}

type encReader struct {
	r       *bufio.Reader
	aead    cipher.AEAD
	counter uint64
	plain   []byte
	done    bool
}

// IsEncrypted dice se r inizia con il magic della cifratura; consuma i byte
// letti dal bufio.Reader solo se restituisce true.
func IsEncrypted(br *bufio.Reader) bool {
	head, err := br.Peek(len(encMagic))
	return err == nil && bytes.Equal(head, encMagic)
}

// NewDecryptReader legge un flusso prodotto da NewEncryptWriter. br deve
// essere posizionato sul magic.
func NewDecryptReader(br *bufio.Reader, secret []byte) (io.Reader, error) {
	hdr := make([]byte, len(encMagic)+saltLen)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return nil, fmt.Errorf("intestazione cifrata illeggibile: %w", err)
	}
	aead, err := newGCM(secret, hdr[len(encMagic):])
	if err != nil {
		return nil, err
	}
	return &encReader{r: br, aead: aead}, nil
}

func (d *encReader) Read(p []byte) (int, error) {
	for len(d.plain) == 0 {
		if d.done {
			return 0, io.EOF
		}
		var hdr [4]byte
		if _, err := io.ReadFull(d.r, hdr[:]); err != nil {
			return 0, fmt.Errorf("archivio cifrato troncato: %w", ErrWrongKey)
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n > chunkSize+uint32(d.aead.Overhead()) {
			return 0, ErrWrongKey
		}
		ct := make([]byte, n)
		if _, err := io.ReadFull(d.r, ct); err != nil {
			return 0, fmt.Errorf("archivio cifrato troncato: %w", ErrWrongKey)
		}
		// Il blocco finale è l'ultimo del file: si prova prima come
		// intermedio, poi come finale.
		pt, err := d.aead.Open(nil, nonceFor(d.counter), ct, []byte{0})
		if err != nil {
			pt, err = d.aead.Open(nil, nonceFor(d.counter), ct, []byte{1})
			if err != nil {
				return 0, ErrWrongKey
			}
			d.done = true
		}
		d.counter++
		d.plain = pt
	}
	n := copy(p, d.plain)
	d.plain = d.plain[n:]
	return n, nil
}

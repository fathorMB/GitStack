package databases

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

// Cifratura dei dump (ADR 0003, punto 5, DB13): AES-256-GCM nello STESSO formato dell'archivio di
// `gitstack backup` (admin/internal/backup/crypto.go: magic | salt | blocchi da 64 KiB con nonce a contatore
// e AAD 1 sull'ultimo blocco, chiave AES = HKDF-SHA256(chiave, salt)), cosi' un solo formato da documentare e
// da leggere. core e admin sono moduli distinti: il formato e' copiato qui e fissato dal test
// TestDumpCrypto_FormatoCompatibile, che decifra un vettore prodotto da admin.

var dumpMagic = []byte("GSBKENC1")

const (
	dumpSaltLen   = 16
	dumpChunkSize = 64 * 1024
	// MinDumpKeyLen e' la lunghezza minima della chiave di dump (il Secret ne ha 32).
	MinDumpKeyLen = 16
)

// ErrDumpWrongKey: chiave di dump errata o file alterato/troncato.
var ErrDumpWrongKey = errors.New("chiave di dump errata o dump alterato")

func dumpGCM(secret, salt []byte) (cipher.AEAD, error) {
	k, err := hkdf.Key(sha256.New, secret, salt, "gitstack-backup-v1", 32)
	if err != nil {
		return nil, err
	}
	b, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func dumpNonce(n uint64) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], n)
	return nonce
}

type dumpEncWriter struct {
	w       io.Writer
	aead    cipher.AEAD
	buf     []byte
	counter uint64
	closed  bool
}

// NewDumpEncryptWriter cifra tutto cio' che si scrive; Close scrive il blocco finale.
func NewDumpEncryptWriter(w io.Writer, secret []byte) (io.WriteCloser, error) {
	if len(secret) < MinDumpKeyLen {
		return nil, fmt.Errorf("chiave di dump troppo corta (almeno %d byte)", MinDumpKeyLen)
	}
	salt := make([]byte, dumpSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	aead, err := dumpGCM(secret, salt)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(append(append([]byte{}, dumpMagic...), salt...)); err != nil {
		return nil, err
	}
	return &dumpEncWriter{w: w, aead: aead, buf: make([]byte, 0, dumpChunkSize)}, nil
}

func (e *dumpEncWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		take := min(dumpChunkSize-len(e.buf), len(p))
		e.buf = append(e.buf, p[:take]...)
		p = p[take:]
		if len(e.buf) == dumpChunkSize {
			if err := e.flush(false); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (e *dumpEncWriter) flush(final bool) error {
	aad := []byte{0}
	if final {
		aad[0] = 1
	}
	ct := e.aead.Seal(nil, dumpNonce(e.counter), e.buf, aad)
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

func (e *dumpEncWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.flush(true)
}

type dumpDecReader struct {
	r       *bufio.Reader
	aead    cipher.AEAD
	counter uint64
	plain   []byte
	done    bool
}

// NewDumpDecryptReader legge un flusso prodotto da NewDumpEncryptWriter (o da NewEncryptWriter di admin).
func NewDumpDecryptReader(r io.Reader, secret []byte) (io.Reader, error) {
	br := bufio.NewReader(r)
	head, err := br.Peek(len(dumpMagic))
	if err != nil || !bytes.Equal(head, dumpMagic) {
		return nil, fmt.Errorf("il file non e' un dump cifrato di GitStack: %w", ErrDumpWrongKey)
	}
	hdr := make([]byte, len(dumpMagic)+dumpSaltLen)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return nil, fmt.Errorf("intestazione del dump illeggibile: %w", err)
	}
	aead, err := dumpGCM(secret, hdr[len(dumpMagic):])
	if err != nil {
		return nil, err
	}
	return &dumpDecReader{r: br, aead: aead}, nil
}

func (d *dumpDecReader) Read(p []byte) (int, error) {
	for len(d.plain) == 0 {
		if d.done {
			return 0, io.EOF
		}
		var hdr [4]byte
		if _, err := io.ReadFull(d.r, hdr[:]); err != nil {
			return 0, fmt.Errorf("dump troncato: %w", ErrDumpWrongKey)
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n > dumpChunkSize+uint32(d.aead.Overhead()) {
			return 0, ErrDumpWrongKey
		}
		ct := make([]byte, n)
		if _, err := io.ReadFull(d.r, ct); err != nil {
			return 0, fmt.Errorf("dump troncato: %w", ErrDumpWrongKey)
		}
		pt, err := d.aead.Open(nil, dumpNonce(d.counter), ct, []byte{0})
		if err != nil {
			pt, err = d.aead.Open(nil, dumpNonce(d.counter), ct, []byte{1})
			if err != nil {
				return 0, ErrDumpWrongKey
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

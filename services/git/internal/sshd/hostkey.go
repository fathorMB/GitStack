package sshd

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// LoadOrCreateHostKey legge la chiave host da path; se il file non esiste ne
// genera una ed25519 e la scrive (0600, creazione atomica), così al riavvio
// successivo i client ritrovano la stessa chiave e non vedono "host key
// changed". Nel chart il file arriva da un Secret montato in sola lettura:
// esiste già e non viene mai riscritto.
func LoadOrCreateHostKey(path string) (ssh.Signer, error) {
	pemBytes, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		pemBytes, err = createHostKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("chiave host SSH %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("chiave host SSH %s non leggibile: %w", path, err)
	}
	return signer, nil
}

func createHostKey(path string) ([]byte, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".hostkey-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return nil, err
	}
	// Link + remove invece di Rename: non sovrascrive mai una chiave scritta
	// nel frattempo da un altro processo.
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return os.ReadFile(path)
		}
		// File system senza hard link: ripiego su una creazione esclusiva.
		f, oerr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if oerr != nil {
			if errors.Is(oerr, fs.ErrExist) {
				return os.ReadFile(path)
			}
			return nil, oerr
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(path)
			return nil, werr
		}
	}
	return data, nil
}

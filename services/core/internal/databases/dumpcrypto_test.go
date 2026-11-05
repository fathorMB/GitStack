package databases

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

var testDumpKey = []byte("0123456789abcdef0123456789abcdef")

func encryptDump(t *testing.T, plain []byte, key []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w, err := NewDumpEncryptWriter(&b, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// DB13: AES-256-GCM, stesso formato dell'archivio di `gitstack backup`. Il vettore e' stato prodotto da
// admin/internal/backup.NewEncryptWriter con la chiave di prova: se i due formati divergono, questo test cade.
func TestDumpCrypto_FormatoCompatibile(t *testing.T) {
	const vec = "R1NCS0VOQzGm0MubW1/j8mvOEy9IeWySAAAALl1E9A+/+WDP2Hn+DFhMJnp+okIAyPft1nKPlBWWqaQddC9/yBt7a0lfZA25woA="
	raw, err := base64.StdEncoding.DecodeString(vec)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewDumpDecryptReader(bytes.NewReader(raw), testDumpKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PGDMP vettore di prova GIT-553" {
		t.Fatalf("decifrato %q", got)
	}
}

func TestDumpCrypto_RoundtripChiaveSbagliataETroncato(t *testing.T) {
	plain := bytes.Repeat([]byte("riga di dati noti\n"), 20000) // > 1 blocco da 64 KiB
	enc := encryptDump(t, plain, testDumpKey)
	if bytes.Contains(enc, []byte("riga di dati noti")) {
		t.Fatal("il dump cifrato contiene il testo in chiaro")
	}
	r, err := NewDumpDecryptReader(bytes.NewReader(enc), testDumpKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(r); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip: err=%v uguale=%v", err, bytes.Equal(got, plain))
	}
	// Chiave sbagliata.
	r, _ = NewDumpDecryptReader(bytes.NewReader(enc), []byte(strings.Repeat("x", 32)))
	if _, err := io.ReadAll(r); !errors.Is(err, ErrDumpWrongKey) {
		t.Fatalf("chiave sbagliata: %v", err)
	}
	// Troncato: l'AAD sull'ultimo blocco lo rende rilevabile.
	r, _ = NewDumpDecryptReader(bytes.NewReader(enc[:len(enc)/2]), testDumpKey)
	if _, err := io.ReadAll(r); !errors.Is(err, ErrDumpWrongKey) {
		t.Fatalf("troncato: %v", err)
	}
	// Alterato.
	bad := append([]byte(nil), enc...)
	bad[len(bad)/2] ^= 0xff
	r, _ = NewDumpDecryptReader(bytes.NewReader(bad), testDumpKey)
	if _, err := io.ReadAll(r); !errors.Is(err, ErrDumpWrongKey) {
		t.Fatalf("alterato: %v", err)
	}
	// Non e' un dump cifrato (file in chiaro).
	if _, err := NewDumpDecryptReader(strings.NewReader("PGDMP in chiaro"), testDumpKey); err == nil {
		t.Fatal("un file in chiaro non deve essere accettato")
	}
	// Chiave troppo corta.
	if _, err := NewDumpEncryptWriter(io.Discard, []byte("corta")); err == nil {
		t.Fatal("chiave corta accettata")
	}
}

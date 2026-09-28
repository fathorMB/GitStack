package sshkeys

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var chiavi = map[string]string{
	"ed25519":  "test ed25519 comment",
	"ecdsa256": "test ecdsa 256",
	"ecdsa384": "test ecdsa 384",
	"ecdsa521": "test ecdsa 521",
	"rsa3072":  "test rsa 3072",
}

var loadKey = func(name string) string {
	data, err := os.ReadFile(filepath.Join("testdata", name+".pub"))
	if err != nil {
		panic(err)
	}
	return string(data)
}

var loadFingerprint = func(name string) string {
	data, err := os.ReadFile(filepath.Join("testdata", name+".fingerprint"))
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestParse_Tabella(t *testing.T) {
	// Chiavi rifiutate.
	rsa2048 := loadKey("rsa2048")
	privKey := string(loadFile("privkey"))

	tests := []struct {
		name     string
		line     string
		wantErr  error
		wantFp   string
		wantCmt  string
		wantBits int
	}{
		// Casi accettati: leggono da testdata.
		{"ed25519", loadKey("ed25519"), nil, loadFingerprint("ed25519"), chiavi["ed25519"], 256},
		{"ecdsa256", loadKey("ecdsa256"), nil, loadFingerprint("ecdsa256"), chiavi["ecdsa256"], 256},
		{"ecdsa384", loadKey("ecdsa384"), nil, loadFingerprint("ecdsa384"), chiavi["ecdsa384"], 384},
		{"ecdsa521", loadKey("ecdsa521"), nil, loadFingerprint("ecdsa521"), chiavi["ecdsa521"], 521},
		{"rsa3072", loadKey("rsa3072"), nil, loadFingerprint("rsa3072"), chiavi["rsa3072"], 3072},
		// Casi rifiutati.
		{"rsa2048", rsa2048, ErrKeyTooShort, "", "", 0},
		{"dsa", loadKey("dsa"), ErrWeakKeyType, "", "", 0},
		{"chiave_privata", privKey, ErrPrivateKey, "", "", 0},
		{"testo_non_valido", "non è una chiave", ErrInvalidFormat, "", "", 0},
		{"riga_vuota", "", ErrInvalidFormat, "", "", 0},
		{"base64_corrotto", "ssh-rsa AAAA", ErrInvalidFormat, "", "", 0},
		{"pem_pubblico", "-----BEGIN PUBLIC KEY-----\nMIIBIjAN", ErrInvalidFormat, "", "", 0},
		{"righe_multipla", loadKey("ed25519") + "\n" + loadKey("rsa2048"), ErrInvalidFormat, "", "", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, err := Parse(tc.line)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("errore atteso %v, nessun errore ricevuto", tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("errore %v atteso, ricevuto: %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("nessun errore atteso, ricevuto: %v", err)
			}
			if k.Fingerprint != tc.wantFp {
				t.Errorf("fingerprint: got %q, want %q", k.Fingerprint, tc.wantFp)
			}
			if k.Comment != tc.wantCmt {
				t.Errorf("comment: got %q, want %q", k.Comment, tc.wantCmt)
			}
			if tc.wantBits > 0 && k.Bits != tc.wantBits {
				t.Errorf("bits: got %d, want %d", k.Bits, tc.wantBits)
			}
		})
	}
}

func TestParse_Commenti(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		wantC string
	}{
		{
			name:  "spazi_multipli_interni",
			line:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPKpbgVcRIzCVFalmNHqNRoFQoUZrhOhs6+1XyGk8UrG   test   multi   spaces  ",
			wantC: "test multi spaces",
		},
		{
			name:  "spazi_CR_LF_finali",
			line:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPKpbgVcRIzCVFalmNHqNRoFQoUZrhOhs6+1XyGk8UrG test comment  \r\n",
			wantC: "test comment",
		},
		{
			name:  "nessun_commento",
			line:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPKpbgVcRIzCVFalmNHqNRoFQoUZrhOhs6+1XyGk8UrG",
			wantC: "",
		},
		{
			name:  "spazi_e_tab",
			line:  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPKpbgVcRIzCVFalmNHqNRoFQoUZrhOhs6+1XyGk8UrG \t test \t comment \t",
			wantC: "test comment",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, err := Parse(tc.line)
			if err != nil {
				t.Fatalf("Parse(%q) errore inatteso: %v", tc.name, err)
			}
			if k.Comment != tc.wantC {
				t.Errorf("comment: got %q, want %q", k.Comment, tc.wantC)
			}
		})
	}
}

func loadFile(name string) string {
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return string(data)
}

func Test_normalizeComment(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"test comment", "test comment"},
		{"  test   comment  ", "test comment"},
		{"test\ncomment\n", "test comment"},
		{"", ""},
		{"   multi   spaces   ", "multi spaces"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := normalizeComment(tc.input)
			if got != tc.want {
				t.Errorf("normalizeComment(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

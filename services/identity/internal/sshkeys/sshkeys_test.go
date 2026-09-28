package sshkeys

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var chiaviAccettate = map[string]string{
	"ed25519":  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPKpbgVcRIzCVFalmNHqNRoFQoUZrhOhs6+1XyGk8UrG test ed25519 comment",
	"ecdsa256": "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEtw5VUPfeV7D8OKkj5rUN2+xrroRVyAUcnLag3VAoPOcBeFwOFRZ8KdgcS2E0KmTpZll+qDSicboQkXHJmE9Yo= test ecdsa 256",
	"ecdsa384": "ecdsa-sha2-nistp384 AAAAE2VjZHNhLXNoYTItbmlzdHAzODQAAAAIbmlzdHAzODQAAABhBBNzA3jDm5pb66yOOkRrRO4Z98Hyn0PQ1+Z/FAadti/Z+FhXiXoDZbmFZoxUEpM8+5exTV45Gj4C2ENxjiPcnYTe38PQN+2tGjskeuzf2vl8dBvdJM9J1NpDsQzvuC3e6g== test ecdsa 384",
	"ecdsa521": "ecdsa-sha2-nistp521 AAAAE2VjZHNhLXNoYTItbmlzdHA1MjEAAAAIbmlzdHA1MjEAAACFBAGfYAGPMJJY9Nw0JlRlBIWxP/OhPHAtdfwf063KqOq0+zEwo6x5R7bZX1c7/+lngdDZhNK1sWqnKyT6xRHnrScKYwHQC6rhJpy7LF3unEBqV5B3BOJy+ARYWr3oh7cYmpsWkSFicMJ/j28D8Qqy7I0gYS4Dc4Row+uIrW6U9I9i4HpnMQ== test ecdsa 521",
	"rsa3072":  "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCzeoygq+4yujpvd6NmQiZHnYyC6/LbgyL7KCvWZOKgrSLlob8dAD/GkCq/J4ddfIiQ61k/adhMx/cZfWa81Q3Y53a5+GN3iSVc275FTGEXNEmVuim474EjZO73PUg7ZCEOCYRDoNYEbXHU3hyhMi5i25DJQVGuHZjx255mst9IwXVaM5WPaJL4+ue90Q4pWCim6Sl+R2qvS0HtxtBzp/VwC5RdPNxibqtS7EC3iFG01D+aXBm3maXykYqBVFz7IuGJ/7bPDw8qy3irbRMr+hHauVhegNmFXXGx2vBT3tRdOEdZHbJ9yufqIVHxtyHzlyUEtLXO3d1n8wTve+hkd95emyxTnZYcvsQPd2t8+A8TIcCnYcHffX6wzQS9yfsLttWmlqzm/toxcHJy9Ce/EkRvOwHwlyBlCDuUIHncXrBR4TaFeoPU/g+t9fTSRII2PR+nIF6dsnPbx09qqvrrAPkxvRKI1yuBCJYGuoGTNrN2atMuZBHDnJ7zHfS9bokX/Mc= test rsa 3072",
}

var expectedFP = map[string]string{
	"ed25519":  "SHA256:aGD3iLx3pFfP7eW7XafHPm8CzBmjZu/U+BWP5gyPBfs",
	"ecdsa256": "SHA256:jA+U6KBN5qr64qJzzkmzvk9pza9FbN3medlMGh9xEkM",
	"ecdsa384": "SHA256:Gdv8phAElfs734zTXvrYvqstNJjxzd9UA7FpmKt8h7k",
	"ecdsa521": "SHA256:e1MZEAJ3hL4mMTjssgxORu8YPZ0eheUNQtJ9NVbzXfQ",
	"rsa3072":  "SHA256:zQe0XOHc6+8mEo1yjtzfiTOetcC3iTaEsIPjjlClbvE",
}

var expectedCmt = map[string]string{
	"ed25519":  "test ed25519 comment",
	"ecdsa256": "test ecdsa 256",
	"ecdsa384": "test ecdsa 384",
	"ecdsa521": "test ecdsa 521",
	"rsa3072":  "test rsa 3072",
}

func loadKey(name string) string {
	data, err := os.ReadFile(filepath.Join("testdata", name+".pub"))
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(data))
}

func loadFile(name string) string {
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(data))
}

func TestParse_Tabella(t *testing.T) {
	if len(chiaviAccettate) != len(expectedFP) {
		t.Fatalf("chiaviAccettate (%d) e expectedFP (%d) disallineati",
			len(chiaviAccettate), len(expectedFP))
	}

	// Carica chiavi dal filesystem per casi di rifiuto.
	rsa2048 := loadKey("rsa2048")
	privKey := loadFile("privkey")
	dsaKey := loadKey("dsa")

	tests := []struct {
		name     string
		line     string
		wantErr  error
		wantFp   string
		wantCmt  string
		wantBits int
	}{
		{"ed25519", chiaviAccettate["ed25519"], nil, expectedFP["ed25519"], expectedCmt["ed25519"], 256},
		{"ecdsa256", chiaviAccettate["ecdsa256"], nil, expectedFP["ecdsa256"], expectedCmt["ecdsa256"], 256},
		{"ecdsa384", chiaviAccettate["ecdsa384"], nil, expectedFP["ecdsa384"], expectedCmt["ecdsa384"], 384},
		{"ecdsa521", chiaviAccettate["ecdsa521"], nil, expectedFP["ecdsa521"], expectedCmt["ecdsa521"], 521},
		{"rsa3072", chiaviAccettate["rsa3072"], nil, expectedFP["rsa3072"], expectedCmt["rsa3072"], 3072},
		{"rsa2048", rsa2048, ErrKeyTooShort, "", "", 0},
		{"dsa", dsaKey, ErrWeakKeyType, "", "", 0},
		{"chiave_privata", privKey, ErrPrivateKey, "", "", 0},
		{"testo_non_valido", "non è una chiave", ErrInvalidFormat, "", "", 0},
		{"riga_vuota", "", ErrInvalidFormat, "", "", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, err := Parse(tc.line)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("errore atteso %v, nessun errore ricevuto", tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) && !strings.Contains(err.Error(), tc.wantErr.Error()) {
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

	// Verifica file testdata.
	td := "testdata"
	for name := range chiaviAccettate {
		for _, ext := range []string{".pub", ".fingerprint"} {
			f := filepath.Join(td, name+ext)
			if _, err := os.Stat(f); os.IsNotExist(err) {
				t.Errorf("file testdata %q mancante (per chiave %q)", f, name)
			}
		}
	}
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

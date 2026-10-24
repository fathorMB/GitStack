package registry

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignPayload_CosignSimpleSigning(t *testing.T) {
	key, err := loadSigningKey(t.TempDir() + "/key.pem")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{signingKey: key}
	payload, encoded, err := h.signPayload("registry.test/alice/app", "sha256:"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"docker-reference":"registry.test/alice/app"`) || !strings.Contains(string(payload), `"cosign container image signature"`) {
		t.Fatalf("payload=%s", payload)
	}
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(&key.PublicKey, sum[:], sig) {
		t.Fatal("firma non verificabile")
	}
}

func TestLoadSigningKey_SEC1P256(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadSigningKey(path)
	if err != nil || got.Curve != elliptic.P256() {
		t.Fatalf("load SEC1: %v", err)
	}
}

func TestLoadSigningKey_RifiutaP384(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSigningKey(path); err == nil {
		t.Fatal("P-384 accettata")
	}
}

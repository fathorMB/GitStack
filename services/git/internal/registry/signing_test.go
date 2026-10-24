package registry

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
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

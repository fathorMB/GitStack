package oidc

import (
	"bytes"
	"testing"
	"time"
)

func TestSecretRoundTrip(t *testing.T) {
	enc, err := EncryptSecret(testKey, "il-segreto")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("il-segreto")) {
		t.Error("il testo cifrato contiene il segreto")
	}
	enc2, _ := EncryptSecret(testKey, "il-segreto")
	if bytes.Equal(enc, enc2) {
		t.Error("nonce riusato: due cifrature identiche")
	}
	got, err := DecryptSecret(testKey, enc)
	if err != nil || got != "il-segreto" {
		t.Errorf("got %q err %v", got, err)
	}
	if _, err := DecryptSecret([]byte("ffffffffffffffffffffffffffffffff"), enc); err == nil {
		t.Error("chiave sbagliata accettata")
	}
	enc[len(enc)-1] ^= 1
	if _, err := DecryptSecret(testKey, enc); err == nil {
		t.Error("blob manomesso accettato")
	}
	if _, err := DecryptSecret(testKey, []byte("corto")); err == nil {
		t.Error("blob corto accettato")
	}
	if _, err := EncryptSecret([]byte("corta"), "x"); err == nil {
		t.Error("chiave da meno di 32 byte accettata")
	}
}

func TestStateAndSecretAreNotInterchangeable(t *testing.T) {
	enc, _ := EncryptSecret(testKey, `{"s":"kc"}`)
	if _, err := open(testKey, enc, aadState); err == nil {
		t.Error("un segreto cifrato non deve valere come cookie di stato")
	}
}

func TestStateRoundTripAndExpiry(t *testing.T) {
	now := time.Now()
	v, err := sealState(testKey, flowState{Slug: "kc", State: "s", Nonce: "n", Verifier: "v", RedirectTo: "/x", Expires: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	st, err := openState(testKey, v, now)
	if err != nil || st.Slug != "kc" || st.State != "s" || st.Nonce != "n" || st.Verifier != "v" || st.RedirectTo != "/x" {
		t.Errorf("st=%+v err=%v", st, err)
	}
	if _, err := openState(testKey, v, now.Add(2*time.Minute)); err != ErrStateExpired {
		t.Errorf("scaduto: %v", err)
	}
}

package appenv

import (
	"encoding/base64"
	"strings"
	"testing"
)

// GIT-348 (H8): un segreto stampato dall'app compare mascherato; una variabile no.
func TestMasker_SegretoStampatoMascherato(t *testing.T) {
	const secret = "sk-live-9f8e7d6c5b4a"
	m := Masker(map[string]string{"STRIPE_KEY": secret})

	line := "avvio: token=" + secret + " ok"
	got := m.Mask(line)
	if strings.Contains(got, secret) || !strings.Contains(got, "***") {
		t.Fatalf("il segreto in chiaro è rimasto nel log: %q", got)
	}

	// Anche codificato in base64, come fanno di solito le app che lo stampano.
	enc := base64.StdEncoding.EncodeToString([]byte(secret))
	got = m.Mask("header: " + enc)
	if strings.Contains(got, enc) {
		t.Fatalf("il segreto in base64 è rimasto nel log: %q", got)
	}
}

// Le variabili non entrano nel masker: un valore di variabile resta leggibile nei log.
func TestMasker_VariabiliNonMascherate(t *testing.T) {
	m := Masker(map[string]string{"STRIPE_KEY": "sk-live-9f8e7d6c5b4a"})
	const region = "eu-west-1"
	if got := m.Mask("region=" + region); got != "region="+region {
		t.Fatalf("una variabile non deve essere mascherata: %q", got)
	}
}

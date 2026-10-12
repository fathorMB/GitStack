package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestV5AllowUnverifiedSkips(t *testing.T) {
	v := V5Config{AllowUnverified: true}
	// Anche se il bundle non esiste, AllowUnverified = true跳过 verifica.
	err := v.VerifyBinary(context.Background(), "/tmp/finto")
	if err != nil {
		t.Fatalf("AllowUnverified=true non dovrebbe fallire: %v", err)
	}
}

func TestV5NoBundleSkips(t *testing.T) {
	v := DefaultV5Main()
	// Nessun bundle .sigstore.json: la verifica viene saltata.
	err := v.VerifyBinary(context.Background(), "/tmp/finto")
	if err != nil {
		t.Fatalf("nessun bundle non dovrebbe causare errore: %v", err)
	}
}

func TestV5BinaryRejectsNoCosign(t *testing.T) {
	v := DefaultV5Main()
	// Creiamo un bundle finto per forzare la chiamata a cosign.
	dir := t.TempDir()
	bundle := filepath.Join(dir, "binario.sigstore.json")
	if err := os.WriteFile(bundle, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Cosign non è installato, quindi la chiamata fallisce.
	err := v.VerifyBinary(context.Background(), filepath.Join(dir, "binario"))
	if err == nil {
		t.Fatal("atteso errore quando cosign non è installato e il bundle esiste")
	}
}

func TestV5ImageAllowUnverifiedSkips(t *testing.T) {
	v := V5Config{AllowUnverified: true}
	err := v.VerifyImage(context.Background(), "ghcr.io/test:latest")
	if err != nil {
		t.Fatalf("AllowUnverified=true non dovrebbe fallire: %v", err)
	}
}

func TestV5ImageNoBundleSkips(t *testing.T) {
	v := DefaultV5Main()
	v.AllowUnverified = true // cosign non è installato, AllowUnverified salta.
	// Nessun digest = nessun bundle: la verifica viene saltata.
	err := v.VerifyImage(context.Background(), "ghcr.io/test:latest")
	if err != nil {
		t.Fatalf("nessun digest non dovrebbe causare errore: %v", err)
	}
}

func TestV5ChartAllowUnverifiedSkips(t *testing.T) {
	v := V5Config{AllowUnverified: true}
	err := v.VerifyChart(context.Background(), "/tmp/finto")
	if err != nil {
		t.Fatalf("AllowUnverified=true non dovrebbe fallire: %v", err)
	}
}

func TestV5ImageTagVerified(t *testing.T) {
	tests := []struct {
		tag    string
		verif  bool
	}{
		{"sha-abcdef1234567890abcdef1234567890abcdef12", true},
		{"v1.2.3", true},
		{"", false},
		{"latest", false},
		{"abc123", false},
	}
	for _, tc := range tests {
		if got := ImageTagVerified(tc.tag); got != tc.verif {
			t.Errorf("ImageTagVerified(%q) = %v, voglio %v", tc.tag, got, tc.verif)
		}
	}
}

func TestV5MirrorStatus(t *testing.T) {
	v := DefaultV5Main()

	// Mirror interno senza AllowUnverified: non verificato.
	m := v.CheckMirror("https://mirror.interna/repo", true)
	if m.Verified {
		t.Error("mirror interno non firmato dovrebbe essere non verificato")
	}

	// Mirror interno con AllowUnverified: ok con avviso.
	v.AllowUnverified = true
	m = v.CheckMirror("https://mirror.interna/repo", true)
	if !m.Verified {
		t.Error("mirror interno con AllowUnverified dovrebbe essere verificato")
	}
	if !m.WarnUnverified {
		t.Error("dovrebbe esserci l'avviso di non verifica")
	}

	// Mirror esterno: sempre ok (senza firma).
	v.AllowUnverified = false
	m = v.CheckMirror("https://github.com/altro/repo", false)
	if !m.Verified {
		t.Error("mirror esterno dovrebbe essere verificato")
	}
}

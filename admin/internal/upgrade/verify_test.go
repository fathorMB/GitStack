package upgrade

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestV5AllowUnverifiedSkips(t *testing.T) {
	v := V5Config{AllowUnverified: true}
	// Anche se il bundle non esiste, AllowUnverified = true salta la verifica.
	err := v.VerifyBinary(context.Background(), "/tmp/finto")
	if err != nil {
		t.Fatalf("AllowUnverified=true non dovrebbe fallire: %v", err)
	}
}

// TestV5NoBundleRefuses verifica che, quando il bundle cosign non è presente,
// la verifica fallisca (non salti più).
func TestV5NoBundleRefuses(t *testing.T) {
	v := DefaultV5Main()
	dir := t.TempDir()
	bin := filepath.Join(dir, "binario")
	if err := os.WriteFile(bin, []byte("dati"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nessun bundle .sigstore.json: la verifica deve fallire.
	err := v.VerifyBinary(context.Background(), bin)
	if err == nil {
		t.Fatal("atteso errore quando il bundle cosign non esiste")
	}
	if !errors.Is(err, ErrBundleMissing) {
		t.Fatalf("errore diverso da ErrBundleMissing: %v", err)
	}
}

// TestV5BinaryRejectsAltered verifica che un binario alterato (diverso da quello
// firmato) venga rifiutato dal test con un fake cosign.
func TestV5BinaryRejectsAltered(t *testing.T) {
	v := DefaultV5Main()
	dir := t.TempDir()
	bin := filepath.Join(dir, "binario")
	bundle := filepath.Join(dir, "binario.sigstore.json")

	// Script finto cosign: controlla gli argomenti.
	// Se il nome del file contiene "ALTERATO", esce 1.
	fakeCosign := filepath.Join(dir, "cosign")
	if err := os.WriteFile(fakeCosign, []byte("#!/bin/sh\nif echo \"$@\" | grep -q ALTERATO; then exit 1; fi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	v.CosignPath = fakeCosign

	// Binario originale (firmato): nessun argomento speciale → cosign 0.
	if err := os.WriteFile(bin, []byte("originale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test: binario originale → nessun errore (cosign finto esce 0).
	if err := v.VerifyBinary(context.Background(), bin); err != nil {
		t.Fatalf("binario originale non dovrebbe fallire: %v", err)
	}

	// Binario alterato: cambia i dati → cosign finto esce 1.
	if err := os.WriteFile(bin, []byte("ALTERATO-altered"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := v.VerifyBinary(context.Background(), bin)
	if err == nil {
		t.Fatal("atteso errore per binario alterato")
	}
	// L'errore non è ErrBundleMissing perché il bundle esiste: è l'errore di execution.
}

// TestV5ChartAllowUnverifiedSkips verifica che AllowUnverified=true salti
// la verifica del chart.
func TestV5ChartAllowUnverifiedSkips(t *testing.T) {
	v := V5Config{AllowUnverified: true}
	err := v.VerifyChart(context.Background(), "/tmp/finto")
	if err != nil {
		t.Fatalf("AllowUnverified=true non dovrebbe fallire: %v", err)
	}
}

// TestV5ChartNoBundleRefuses verifica che, quando il bundle chart non esiste,
// la verifica fallisca.
func TestV5ChartNoBundleRefuses(t *testing.T) {
	v := DefaultV5Main()
	dir := t.TempDir()
	chart := filepath.Join(dir, "chart.tgz")
	if err := os.WriteFile(chart, []byte("chart"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := v.VerifyChart(context.Background(), chart)
	if err == nil {
		t.Fatal("atteso errore quando il bundle chart non esiste")
	}
	if !errors.Is(err, ErrBundleMissing) {
		t.Fatalf("errore diverso da ErrBundleMissing: %v", err)
	}
}

// TestV5ImageAllowUnverifiedSkips verifica che AllowUnverified=true salti
// la verifica dell'immagine.
func TestV5ImageAllowUnverifiedSkips(t *testing.T) {
	v := V5Config{AllowUnverified: true}
	err := v.VerifyImage(context.Background(), "ghcr.io/test:latest")
	if err != nil {
		t.Fatalf("AllowUnverified=true non dovrebbe fallire: %v", err)
	}
}

// TestV5IdentityRegexVerifies verifica che, quando Identity è vuoto,
// venga usato --certificate-identity-regexp ".*".
func TestV5IdentityRegexVerifies(t *testing.T) {
	// Creiamo una struttura che catturi gli argomenti passati a exec.Command.
	type argsCapture struct {
		name string
		args []string
	}
	var captured argsCapture
	capturedDone := make(chan struct{})

	originalCommand := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		captured = argsCapture{name: name, args: args}
		close(capturedDone)
		// Ritorna un comando che non viene eseguito realmente (block forever).
		// Il test legge captured e termina.
		cmd := exec.CommandContext(ctx, "sleep", "999999")
		return cmd
	}
	defer func() { execCommand = originalCommand }()

	v := DefaultV5Tag() // Identity vuoto
	dir := t.TempDir()
	bin := filepath.Join(dir, "binario")
	bundle := filepath.Join(dir, "binario.sigstore.json")
	_ = os.WriteFile(bin, []byte("x"), 0o644)
	_ = os.WriteFile(bundle, []byte("{}"), 0o644)

	// Avvia la verifica in goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- v.verifyBlob(context.Background(), bin)
	}()

	// Aspetta che il mock sia chiamato.
	select {
	case <-capturedDone:
		// Il mock è stato chiamato.
	case <-errCh:
		// Il mock ha restituito un errore (dovrebbe bloccarsi).
		t.Fatal("verifyBlob ha restituito errore prima che il mock fosse chiamato")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: verifyBlob non ha chiamato il mock")
	}

	// Verifica che sia stato usato --certificate-identity-regexp ".*"
	foundRegexp := false
	for i, a := range captured.args {
		if a == "--certificate-identity-regexp" && i+1 < len(captured.args) && captured.args[i+1] == ".*" {
			foundRegexp = true
			break
		}
	}
	if !foundRegexp {
		t.Fatalf("atteso --certificate-identity-regexp .*; args: %v", captured.args)
	}
}

// execCommand è la funzione usata per creare i comandi (test: mock).
var execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

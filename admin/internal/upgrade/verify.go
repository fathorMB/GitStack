package upgrade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CosignPath è il percorso del binario cosign (test: percorso alternativo).
var CosignPath = "cosign"

// V5Config raccoglie i parametri di verifica V5 (sigstore/cosign).
// OIDCIssuer è fissato; AllowUnverified consente di saltare
// la verifica (eccezione per mirror interni non firmati, N9).
// Identity non viene passato a cosign: si usa sempre
// --certificate-identity-regexp ".*" come la CI (ci.yml).
type V5Config struct {
	// Identity del certificato firmato (workflow URL).
	// Non viene passato a cosign: la verifica usa sempre
	// --certificate-identity-regexp ".*" (corrisponde a qualsiasi identità).
	// Mantenuto per compatibilità con config.yaml esistenti.
	Identity string
	// OIDCIssuer è l'issuer OIDC (token.actions.githubusercontent.com).
	OIDCIssuer string
	// AllowUnverified: se true, saltare la verifica senza errore (N9).
	AllowUnverified bool
	// Path del binario cosign (default "cosign").
	CosignPath string
}

// DefaultV5Main restituisce la configurazione V5 per il ramo main.
func DefaultV5Main() V5Config {
	return V5Config{
		Identity:        "https://github.com/fathorMB/GitStack/.github/workflows/ci.yml@refs/heads/main",
		OIDCIssuer:      "https://token.actions.githubusercontent.com",
		AllowUnverified: false,
		CosignPath:      CosignPath,
	}
}

// DefaultV5Tag restituisce la configurazione V5 per i tag di rilascio.
// Usa --certificate-identity-regexp ".*" (corrisponde a qualsiasi identità):
// la verifica dipende dal bundle .sigstore.json contenuto nella release,
// non dal nome del tag (per i tag si fida del bundle).
func DefaultV5Tag() V5Config {
	return V5Config{
		Identity:        "", // non usato: la verifica usa --certificate-identity-regexp ".*"
		OIDCIssuer:      "https://token.actions.githubusercontent.com",
		AllowUnverified: false,
		CosignPath:      CosignPath,
	}
}

// ErrBundleMissing è l'errore restituito quando il bundle .sigstore.json
// non è presente e la verifica è attiva (AllowUnverified=false).
var ErrBundleMissing = errors.New("file di bundle cosign (.sigstore.json) non trovato: la verifica non può essere eseguita")

// VerifyBinary esegue `cosign verify-blob` sul binario.
// Se AllowUnverified è true, restituisce nil senza verificare.
func (v V5Config) VerifyBinary(ctx context.Context, path string) error {
	if v.AllowUnverified {
		return nil
	}
	return v.verifyBlob(ctx, path)
}

// VerifyChart esegue `cosign verify-blob` sul file chart.
// Se AllowUnverified è true, restituisce nil senza verificare.
func (v V5Config) VerifyChart(ctx context.Context, path string) error {
	if v.AllowUnverified {
		return nil
	}
	return v.verifyBlob(ctx, path)
}

// VerifyImage esegue `cosign verify` sull'immagine container.
// Se AllowUnverified è true, restituisce nil senza verificare.
func (v V5Config) VerifyImage(ctx context.Context, image string) error {
	if v.AllowUnverified {
		return nil
	}
	return v.verifyImage(ctx, image)
}

func (v V5Config) verifyBlob(ctx context.Context, path string) error {
	// Se il bundle non esiste e AllowUnverified è false, è un errore:
	// la verifica non può essere eseguita senza il bundle firmato.
	bundlePath := path + ".sigstore.json"
	if _, err := os.Stat(bundlePath); err != nil {
		return fmt.Errorf("%w (per %s)", ErrBundleMissing, path)
	}

	bin := v.CosignPath
	if bin == "" {
		bin = CosignPath
	}

	// --certificate-identity-regexp ".*" come ci.yml (linea 864):
	// il bundle .sigstore.json contenuto nella release è la prova
	// che l'artefatto è stato firmato dalla CI (keyless OIDC).
	args := []string{"verify-blob", "--bundle", bundlePath, path}
	args = append([]string{"--certificate-identity-regexp", ".*"}, args...)
	args = append(args, "--certificate-oidc-issuer", v.OIDCIssuer)

	cmd := execCommand(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = "verifica firma fallita (cosign non disponibile o errore sconosciuto)"
		}
		return fmt.Errorf("verifica firma cosign fallita per %s: %s", path, msg)
	}
	return nil
}

func (v V5Config) verifyImage(ctx context.Context, image string) error {
	bin := v.CosignPath
	if bin == "" {
		bin = CosignPath
	}

	// --certificate-identity-regexp ".*" come ci.yml (linea 843):
	// il bundle .sigstore.json contenuto nella release è la prova
	// che l'immagine è stata firmata dalla CI (keyless OIDC).
	args := []string{"verify", image}
	args = append([]string{"--certificate-identity-regexp", ".*"}, args...)
	args = append(args, "--certificate-oidc-issuer", v.OIDCIssuer)

	cmd := execCommand(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = "verifica firma fallita (cosign non disponibile o errore sconosciuto)"
		}
		return fmt.Errorf("verifica firma cosign fallita per immagine %s: %s", image, msg)
	}
	return nil
}

// CosignAvailable controlla che il binario cosign esista e sia eseguibile.
func CosignAvailable() bool {
	_, err := exec.LookPath(CosignPath)
	return err == nil
}

// ErrNoCosign è l'errore restituito quando cosign non è installato.
var ErrNoCosign = errors.New("cosign non trovato nel PATH: installalo per la verifica delle firme V5")

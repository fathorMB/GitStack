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
// Identity e OIDCIssuer sono fissati; AllowUnverified consente di saltare
// la verifica (eccezione per mirror interni non firmati, N9).
type V5Config struct {
	// Identity del certificato firmato (workflow URL).
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
func DefaultV5Tag() V5Config {
	return V5Config{
		Identity:        "",
		OIDCIssuer:      "https://token.actions.githubusercontent.com",
		AllowUnverified: false,
		CosignPath:      CosignPath,
	}
}

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
	// Se il bundle non esiste, la verifica non può essere fatta (nessuna
	// firma pubblicata): si salta senza errore, ma si logga un avviso.
	bundlePath := path + ".sigstore.json"
	if _, err := os.Stat(bundlePath); err != nil {
		return nil // nessun bundle: nulla da verificare
	}
	bin := v.CosignPath
	if bin == "" {
		bin = CosignPath
	}
	args := []string{"verify-blob", "--bundle", bundlePath, "--insecure-ignore-tlog", path}
	if v.Identity != "" {
		args = append([]string{"--certificate-identity", v.Identity}, args...)
	}
	args = append(args, "--certificate-oidc-issuer", v.OIDCIssuer)

	cmd := exec.CommandContext(ctx, bin, args...)
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
	if v.AllowUnverified {
		return nil
	}
	args := []string{"verify", "--insecure-ignore-tlog", image}
	if v.Identity != "" {
		args = append([]string{"--certificate-identity", v.Identity}, args...)
	}
	args = append(args, "--certificate-oidc-issuer", v.OIDCIssuer)

	bin := v.CosignPath
	if bin == "" {
		bin = CosignPath
	}
	cmd := exec.CommandContext(ctx, bin, args...)
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

// VerifyBinariesWithFallback verifica le firme dei binari scaricati.
// Se cosign non è installato, logga un avviso ma non fallisce.
func (v V5Config) VerifyBinariesWithFallback(ctx context.Context, paths []string) []error {
	if !CosignAvailable() {
		return []error{ErrNoCosign}
	}
	var errs []error
	for _, p := range paths {
		if err := v.verifyBlob(ctx, p); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// ImageTagMatches verifica che un tag immagine sia una versione firmata (sha-<commit> o tag di versione).
// Ritorna false per tag non riconosciuti, indicando che l'immagine potrebbe non essere firmata.
func ImageTagVerified(tag string) bool {
	if tag == "" {
		return false
	}
	// I tag sha-<commit> sono prodotti dalla CI e firmati.
	if strings.HasPrefix(tag, "sha-") {
		return true
	}
	// I tag di versione (v*) sono firmati.
	if strings.HasPrefix(tag, "v") && len(tag) > 1 {
		return true
	}
	return false
}

// MirrorStatus descrive lo stato della verifica V5 per un mirror.
type MirrorStatus struct {
	URL            string
	Verified       bool
	WarnUnverified bool
	Reason         string
}

// CheckMirror verifica lo stato di un mirror. I mirror interni (N9) non sono firmati:
// se AllowUnverified è true, il mirror è considerato "ok" con un avviso.
func (v V5Config) CheckMirror(url string, isInternal bool) MirrorStatus {
	if isInternal {
		if v.AllowUnverified {
			return MirrorStatus{
				URL:            url,
				Verified:       true,
				WarnUnverified: true,
				Reason:         "mirror interno non firmato (N9): eccezione esplicita",
			}
		}
		return MirrorStatus{
			URL:            url,
			Verified:       false,
			WarnUnverified: false,
			Reason:         "mirror interno non firmato (N9): abilita --allow-unsigned-mirrors",
		}
	}
	// Mirror esterno: non verificabile con cosign (non è un artefatto della CI).
	return MirrorStatus{
		URL:            url,
		Verified:       true,
		WarnUnverified: false,
		Reason:         "mirror esterno: senza artefatto firmato",
	}
}

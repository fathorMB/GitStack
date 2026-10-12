package upgrade

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Verifica V5 della catena di fornitura: firme Sigstore/cosign di binario,
// chart e immagini, come le produce il job di firma di .github/workflows/ci.yml
// (firma keyless con OIDC di GitHub Actions, tlog incluso).

const (
	// DefaultOIDCIssuer è l'issuer OIDC dei certificati firmati da GitHub Actions.
	DefaultOIDCIssuer = "https://token.actions.githubusercontent.com"
	// DefaultCosignPath è dove deploy/install.sh installa il cosign pinnato
	// (stessa versione della CI, SHA-256 verificato). Il config può
	// sovrascriverlo (v5.cosign_path).
	DefaultCosignPath = "/usr/local/bin/gitstack-cosign"
)

// ErrBundleMissing: il bundle .sigstore.json dell'artefatto non è disponibile.
// Senza bundle la firma non si può verificare: è un rifiuto, mai un via libera.
var ErrBundleMissing = errors.New("bundle cosign (.sigstore.json) non disponibile: l'artefatto non risulta firmato")

// ErrSignature: cosign ha rifiutato la firma (artefatto alterato, non firmato
// o firmato da un'altra identità).
var ErrSignature = errors.New("verifica firma cosign fallita")

// V5Config è la configurazione della verifica. Il valore zero VERIFICA:
// saltare la verifica richiede AllowUnsigned esplicito (N9).
type V5Config struct {
	// Repo è owner/nome del repository la cui CI ha firmato (default DefaultRepo).
	Repo string
	// OIDCIssuer (default DefaultOIDCIssuer).
	OIDCIssuer string
	// CosignPath è il binario cosign pinnato (default DefaultCosignPath).
	CosignPath string
	// RegistryMirror (network.registry_mirror) è il mirror delle immagini: le
	// immagini stanno lì col nome originale senza host, e la firma si cerca
	// lì (il mirror deve portare anche le firme: è un pull-through cache, o
	// vanno copiate con le immagini). Credenziali facoltative.
	RegistryMirror   string
	RegistryUsername string
	RegistryPassword string
	// AllowUnsigned è l'eccezione N9 per i mirror interni non firmati: con
	// true niente viene verificato (e il chiamante deve avvisare).
	AllowUnsigned bool
}

func (v V5Config) repo() string {
	if v.Repo != "" {
		return v.Repo
	}
	return DefaultRepo
}

func (v V5Config) issuer() string {
	if v.OIDCIssuer != "" {
		return v.OIDCIssuer
	}
	return DefaultOIDCIssuer
}

func (v V5Config) cosign() string {
	if v.CosignPath != "" {
		return v.CosignPath
	}
	return DefaultCosignPath
}

// IdentityRegexp è l'identità accettata per il certificato: il workflow ci.yml
// del repository, da main o da un tag di versione (v*). Come la CI, che per i
// tag verifica con --certificate-identity-regexp.
func (v V5Config) IdentityRegexp() string {
	return `^https://github\.com/` + regexp.QuoteMeta(v.repo()) +
		`/\.github/workflows/ci\.yml@refs/(heads/main|tags/v.+)$`
}

// VerifyBlob verifica con `cosign verify-blob` il file contro il suo bundle.
// Il bundle deve esistere: se manca è ErrBundleMissing.
func (v V5Config) VerifyBlob(ctx context.Context, file, bundle string) error {
	if _, err := os.Stat(bundle); err != nil {
		return fmt.Errorf("%w: %s", ErrBundleMissing, bundle)
	}
	return v.run(ctx, file, "verify-blob",
		"--certificate-identity-regexp", v.IdentityRegexp(),
		"--certificate-oidc-issuer", v.issuer(),
		"--bundle", bundle,
		file)
}

// VerifyImage verifica con `cosign verify` la firma di un'immagine.
func (v V5Config) VerifyImage(ctx context.Context, image string) error {
	ref, insecure := v.mirroredImage(image)
	args := []string{"--certificate-identity-regexp", v.IdentityRegexp(), "--certificate-oidc-issuer", v.issuer()}
	if insecure {
		args = append(args, "--allow-insecure-registry")
	}
	var env []string
	if v.RegistryMirror != "" && v.RegistryUsername != "" {
		dir, err := dockerConfig(strings.TrimRight(v.RegistryMirror, "/"), v.RegistryUsername, v.RegistryPassword)
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		env = append(os.Environ(), "DOCKER_CONFIG="+dir)
	}
	return v.runEnv(ctx, image, env, append([]string{"verify"}, append(args, ref)...)...)
}

// mirroredImage dà il riferimento da interrogare: l'immagine com'è, oppure con
// un registry mirror il suo percorso sul mirror (stessa regola del
// registries.yaml di k3s: nome originale senza host). insecure è vero per un
// mirror http.
func (v V5Config) mirroredImage(image string) (ref string, insecure bool) {
	if v.RegistryMirror == "" {
		return image, false
	}
	u, err := url.Parse(strings.TrimRight(v.RegistryMirror, "/"))
	if err != nil || u.Host == "" {
		return image, false
	}
	_, path, ok := strings.Cut(image, "/")
	if !ok {
		path = image
	}
	return u.Host + "/" + path, u.Scheme == "http"
}

// dockerConfig scrive un config.json temporaneo con le credenziali del mirror:
// cosign le legge dal DOCKER_CONFIG (niente password negli argomenti).
func dockerConfig(registryURL, user, pass string) (string, error) {
	u, err := url.Parse(registryURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("registry_mirror %q non valido", registryURL)
	}
	dir, err := os.MkdirTemp("", "gitstack-docker-")
	if err != nil {
		return "", err
	}
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	body, _ := json.Marshal(map[string]any{"auths": map[string]any{u.Host: map[string]string{"auth": auth}}})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), body, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func (v V5Config) run(ctx context.Context, what string, args ...string) error {
	return v.runEnv(ctx, what, nil, args...)
}

func (v V5Config) runEnv(ctx context.Context, what string, env []string, args ...string) error {
	bin := v.cosign()
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("cosign non trovato in %s: rilancia deploy/install.sh (installa il cosign pinnato) o imposta v5.cosign_path nel config: %w", bin, err)
	}
	cmd := execCommand(ctx, bin, args...)
	if env != nil {
		cmd.Env = env
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%w per %s: %s", ErrSignature, what, msg)
	}
	return nil
}

// execCommand crea il comando di cosign (sostituibile nei test).
var execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

var gitstackImageRe = regexp.MustCompile(`(^|/)gitstack-(gateway|identity|git|core|web)([:@]|$)`)

// IsGitstackImage dice se l'immagine è una di quelle firmate dalla CI di
// GitStack (gitstack-gateway/identity/git/core/web). Postgres e NATS sono
// immagini di terzi: non le firma GitStack e non si verificano qui.
func IsGitstackImage(image string) bool { return gitstackImageRe.MatchString(image) }

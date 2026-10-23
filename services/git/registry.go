package main

import (
	"log/slog"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/config"
	"github.com/fathorMB/GitStack/services/git/internal/registry"
	"github.com/fathorMB/GitStack/services/git/internal/upstream"
)

// newRegistry costruisce il registry OCI (GIT-286, RG1) o ritorna nil se è spento
// (GITSTACK_GIT_REGISTRY=off) o se mancano identity, core e segreto di servizio: senza
// non si può autenticare nessuno, e un registry aperto non esiste.
func newRegistry(cfg config.Config, logger *slog.Logger) (*registry.Handler, error) {
	if cfg.RegistryOff {
		logger.Info("registry OCI disattivato", "env", config.EnvRegistry)
		return nil, nil
	}
	if cfg.IdentityURL == "" || cfg.CoreURL == "" || cfg.ServiceSecret == "" {
		logger.Warn("registry OCI non avviato: servono identity, core e segreto di servizio", "env", []string{config.EnvIdentityURL, config.EnvCoreURL, config.EnvSecretGit})
		return nil, nil
	}
	up := upstream.New(cfg.IdentityURL, cfg.CoreURL, cfg.ServiceSecret, 5*time.Second)
	if cfg.CIGatewayURL != "" {
		up = up.WithCIGateway(cfg.CIGatewayURL)
	} else {
		logger.Warn("token del job non accettati dal registry: manca il ci-gateway", "env", config.EnvCIGatewayURL)
	}
	rc := registry.Config{Dir: cfg.RegistryDir, Secret: firstSecret(cfg.ServiceSecret), Logger: logger, KeepUploadPurging: true}
	if s := cfg.RegistryS3; s != nil {
		rc.S3 = &registry.S3{
			Bucket: s.Bucket, Region: s.Region, Endpoint: s.Endpoint, AccessKey: s.AccessKey, SecretKey: s.SecretKey,
			PathStyle: s.PathStyle, Prefix: s.Prefix, Insecure: strings.HasPrefix(strings.ToLower(s.Endpoint), "http://"),
		}
		logger.Info("registry OCI su S3", "bucket", s.Bucket, "endpoint", s.Endpoint)
	} else {
		logger.Info("registry OCI su disco", "dir", cfg.RegistryDir)
	}
	return registry.New(rc, up)
}

// firstSecret è il primo di un elenco «nuova,vecchia» (rotazione): il segreto degli
// upload di distribution non deve cambiare con la rotazione di quello di servizio,
// ma serve uno stabile fra le repliche, e la prima voce lo è fra una rotazione e l'altra.
func firstSecret(s string) string {
	first, _, _ := strings.Cut(s, ",")
	return strings.TrimSpace(first)
}

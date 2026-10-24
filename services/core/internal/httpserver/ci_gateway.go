package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CIGatewayConfig è la parte di configurazione del ci-gateway (GIT-316) che arriva da main.
type CIGatewayConfig struct {
	RunnerSecrets []string
	ArchiveURL    string
	Logs          *ci.LogStore
	// Blobs e SpoolDir: artefatti e cache dei job (CI5).
	Blobs    ci.Blobs
	SpoolDir string
	Capacity int
	// Keys decifra i segreti della CI (stesso keyring dei webhook).
	Keys ci.Sealer
	Log  *slog.Logger
	// Options sono le opzioni del router (identità, per i nomi dei creatori degli
	// stati, GIT-281): come per NewCIPushHandler.
	Options []Option
}

// NewCIGateway costruisce l'handler del ci-gateway sulle letture di git: i workflow
// al commit del job e l'archivio del repo. Va servito su una porta dedicata, mai
// su quella dell'API. I cambi di stato dei job passano dal Reporter della CI
// (stati dei commit, webhook, notifiche; GIT-281).
func NewCIGateway(pool *pgxpool.Pool, rd gitclient.Reader, c CIGatewayConfig) http.Handler {
	return cigateway.New(cigateway.Config{
		Pool: pool, Source: gitCISource{rd: rd}, Reader: rd, Logs: c.Logs, Blobs: c.Blobs, SpoolDir: c.SpoolDir,
		RunnerSecrets: c.RunnerSecrets, Capacity: c.Capacity, Keys: c.Keys, ArchiveURL: c.ArchiveURL, Log: c.Log,
		Reporter: NewCIReporter(pool, c.Options...),
	})
}

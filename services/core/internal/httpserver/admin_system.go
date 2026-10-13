package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// SystemInfoConfig dice dove core trova le informazioni dell'host (Admin ·
// System, GIT-213). Il file è il ConfigMap `<release>-system-info` montato
// come volume: lo scrive `gitstack system-info publish` (installer, upgrade,
// backup). Core non parla con l'host né con l'API di Kubernetes.
type SystemInfoConfig struct {
	// File è il percorso di system.json; vuoto = nessuna informazione dell'host.
	File string
}

// WithSystemInfo imposta il file delle informazioni dell'host.
func WithSystemInfo(c SystemInfoConfig) Option {
	return func(o *routerOptions) { o.systemInfo = c }
}

const pgVersionTimeout = 3 * time.Second

// GetAdminSystem implementa GET /admin/system: solo l'amministratore
// dell'installazione. Ordine: identità (401) → admin (403) → dati.
func (s *apiServer) GetAdminSystem(w http.ResponseWriter, r *http.Request) {
	_, callerID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if s.readable == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile verificare l'amministratore.")
		return
	}
	isAdmin, _, err := s.readable.ReadableResources(r.Context(), callerID)
	if err != nil {
		slog.Default().Warn("verifica dell'amministratore non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare l'amministratore.")
		return
	}
	if !isAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "Solo l'amministratore dell'installazione può vedere lo stato del sistema.")
		return
	}

	var raw []byte
	if s.systemInfo.File != "" {
		b, err := os.ReadFile(s.systemInfo.File)
		switch {
		case err == nil:
			raw = b
		case errors.Is(err, os.ErrNotExist):
			// non ancora pubblicato: sezioni «non configurato»
		default:
			slog.Default().Warn("informazioni dell'host non leggibili", "err", err)
		}
	}
	out := buildAdminSystem(raw)

	// La versione del server la legge core ora, sulla sua connessione.
	if v, err := s.postgresVersion(r.Context()); err == nil {
		out.Database.ServerVersion = &v
		live := true
		out.Database.VersionLive = &live
	} else {
		slog.Default().Warn("SHOW server_version non riuscito", "err", err)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *apiServer) postgresVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pgVersionTimeout)
	defer cancel()
	var v string
	if err := s.pool.QueryRow(ctx, "SHOW server_version").Scan(&v); err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

// buildAdminSystem decodifica system.json (nil = non pubblicato) nel tipo del
// contratto. Il tipo non ha altri campi: ciò che il file contenesse in più
// (segreti compresi) non arriva alla risposta. Le credenziali di un URL del
// proxy si tolgono comunque: difesa in profondità.
func buildAdminSystem(raw []byte) openapi.AdminSystem {
	var out openapi.AdminSystem
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			slog.Default().Warn("system.json non valido", "err", err)
			out = openapi.AdminSystem{}
		} else {
			out.Published = true
		}
	}
	if out.Database.Mode == "" {
		out.Database.Mode = openapi.AdminSystemDatabaseModeUnknown
	}
	for _, run := range []*openapi.AdminSystemBackupRun{&out.Backup.Last, &out.Backup.S3.Upload} {
		if run.State == "" {
			run.State = openapi.AdminSystemBackupRunStateNone
		}
	}
	// Sezioni di GIT-218: un system.json pubblicato da un gitstack più vecchio
	// non le ha; i valori «non disponibile» sono quelli del contratto.
	if out.Access.TlsMode == "" {
		out.Access.TlsMode = openapi.AdminSystemAccessTlsModeUnknown
	}
	if out.Services.Items == nil {
		out.Services.Items = []openapi.AdminSystemService{}
	}
	for _, m := range []*openapi.AdminSystemMetric{&out.Node.Cpu, &out.Node.Memory} {
		if m.Level == "" {
			m.Level = openapi.AdminSystemMetricLevelUnknown
		}
	}
	if out.Node.Disk.Level == "" {
		out.Node.Disk.Level = openapi.AdminSystemDiskLevelUnknown
	}
	if out.Versions.K3sUpdate == "" {
		out.Versions.K3sUpdate = openapi.AdminSystemVersionsK3sUpdateNone
	}
	for _, p := range []**string{&out.Network.HttpProxy, &out.Network.HttpsProxy} {
		if *p == nil {
			continue
		}
		clean, had := stripCredentials(**p)
		*p = &clean
		if had {
			out.Network.ProxyHasCredentials = true
		}
	}
	for _, p := range []**string{&out.Network.RegistryMirror, &out.Network.DownloadMirror} {
		if *p == nil {
			continue
		}
		clean, had := stripCredentials(**p)
		*p = &clean
		if had {
			out.Network.RegistryHasCredentials = true
		}
	}
	if out.Network.HttpProxy != nil || out.Network.HttpsProxy != nil || out.Network.CorporateCA || out.Network.MirrorCA ||
		(out.Network.RegistryMirror != nil && *out.Network.RegistryMirror != "") ||
		(out.Network.DownloadMirror != nil && *out.Network.DownloadMirror != "") {
		out.Network.Configured = true
	}
	return out
}

// stripCredentials toglie user:password da un URL; had dice se c'erano.
func stripCredentials(raw string) (clean string, had bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// non è un URL leggibile: meglio niente di un valore che potrebbe
		// contenere credenziali.
		if strings.Contains(raw, "@") {
			return "", true
		}
		return raw, false
	}
	if u.User == nil {
		return raw, false
	}
	u.User = nil
	return u.String(), true
}

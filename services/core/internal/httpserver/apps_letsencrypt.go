package httpserver

import (
	"context"
	"net/netip"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Stato di Let's Encrypt nelle impostazioni d'istanza delle app (v3-c, GIT-459).

// appsLetsEncrypt riempie `letsEncrypt` di AppsInstanceSettings. Senza controller (hosting spento)
// o senza resolver ACME installato il blocco c'è lo stesso, con enabled=false. La porta 80 è l'esito
// dell'ultimo controllo del controller, nil finché non c'è (nessun IP pubblico dichiarato o giro non
// ancora fatto).
func (s *apiServer) appsLetsEncrypt(ctx context.Context, publicOn bool) *openapi.AppsLetsEncryptStatus {
	out := &openapi.AppsLetsEncryptStatus{}
	if ips, err := s.resources.PublicIPs(ctx); err == nil {
		if ips == nil {
			ips = []string{}
		}
		out.PublicIps = &ips
	}
	cfg, p80, ok := s.acmeStatus()
	if !ok {
		return out
	}
	if cfg.Email != "" {
		e := openapi_types.Email(cfg.Email)
		out.AcmeEmail = &e
	}
	out.Enabled = cfg.Enabled() && cfg.Email != "" && publicOn
	out.Port80Reachable = p80.Reachable
	out.Port80CheckedAt = p80.CheckedAt
	if p80.Error != "" {
		out.Port80Error = &p80.Error
	}
	return out
}

// validatePublicIPs: solo indirizzi IP validi e pubblici (non privati, loopback, link-local, non
// specificati, multicast). Ritorna la lista normalizzata o il motivo del rifiuto.
func validatePublicIPs(in []string) ([]string, string) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		a, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil {
			return nil, "«" + raw + "» non è un indirizzo IP"
		}
		a = a.Unmap()
		if a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
			return nil, "«" + raw + "» non è un indirizzo pubblico"
		}
		if !seen[a.String()] {
			seen[a.String()] = true
			out = append(out, a.String())
		}
	}
	return out, ""
}

// acmeReporter è ciò che il controller reale offre in più dell'AppController: la configurazione ACME e
// l'esito sulla porta 80. I controller finti dei test non lo hanno e valgono «Let's Encrypt spento».
type acmeReporter interface {
	ACMEStatus() (apps.ACMEConfig, apps.Port80Status)
}

func (s *apiServer) acmeStatus() (apps.ACMEConfig, apps.Port80Status, bool) {
	if r, ok := s.appCtl.(acmeReporter); ok && s.appCtl != nil {
		c, p := r.ACMEStatus()
		return c, p, true
	}
	return apps.ACMEConfig{}, apps.Port80Status{}, false
}

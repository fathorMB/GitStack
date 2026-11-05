package httpserver

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Accesso dalla LAN (GIT-554, ADR 0003 punto 6, DB16-DB18). Serve `admin` sull'owner come ogni operazione
// sui database: chi non lo e' riceve 404 (databaseFor).

func (s *apiServer) databaseLanView(r *http.Request, d store.ManagedDB) (openapi.DatabaseLan, error) {
	out := openapi.DatabaseLan{Enabled: d.LANEnabled, Port: d.LANPort}
	if !d.LANEnabled || d.LANPort == nil {
		return out, nil
	}
	info, err := s.databases.Controller.LANInfo(r.Context(), d, r.Host)
	if err != nil {
		return out, err
	}
	if info.Host != "" {
		out.Host = &info.Host
	}
	if info.ChainPEM != "" {
		out.CaCertificate = &info.ChainPEM
	}
	ufw := databases.UFWCommand(*d.LANPort)
	out.UfwCommand = &ufw
	return out, nil
}

// GetDatabaseLan implementa GET /databases/{owner}/{name}/lan.
func (s *apiServer) GetDatabaseLan(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, _, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	out, err := s.databaseLanView(r, d)
	if err != nil {
		appInternalError(w, "lettura dell'accesso LAN del database", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SetDatabaseLan implementa PUT /databases/{owner}/{name}/lan: accende (porta assegnata) o spegne (porta chiusa
// prima della risposta).
func (s *apiServer) SetDatabaseLan(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, u, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	// `enabled` e' obbligatorio: un corpo `{}` non deve spegnere la LAN in silenzio.
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Enabled == nil {
		writeFieldError(w, "enabled", "Serve `enabled`: true accende la LAN, false la spegne.")
		return
	}
	next, err := s.databases.Controller.SetLAN(r.Context(), d, *in.Enabled, u.actor())
	switch {
	case errors.Is(err, store.ErrLANPortUnavailable):
		writeError(w, http.StatusConflict, "port_unavailable", "Nessuna porta libera nell'intervallo delle porte LAN: l'amministratore puo' allargarlo o spegnere la LAN di un altro database.")
		return
	case errors.Is(err, store.ErrManagedDBNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
		return
	case errors.Is(err, databases.ErrLANCloseFailed):
		// Spento nel modello ma Service/policy ancora presenti: la porta puo' rispondere e resta riservata.
		// Mai 200: chi chiama deve sapere che non e' chiusa. Il ciclo e un nuovo PUT false ritentano.
		slog.Default().Error("chiusura della LAN non riuscita", "db", d.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "lan_close_failed", "L'accesso LAN e' spento ma la porta non e' ancora chiusa: Kubernetes non ha risposto. Si ritenta da solo; ripeti la richiesta per conferma.")
		return
	case err != nil:
		appInternalError(w, "accesso LAN del database", err)
		return
	}
	out, verr := s.databaseLanView(r, next)
	if verr != nil {
		appInternalError(w, "lettura dell'accesso LAN del database", verr)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// lanPortIfOn: con la LAN spenta la porta e' null nell'API anche se il modello la tiene ancora riservata
// perche' la chiusura non e' completata.
func lanPortIfOn(d store.ManagedDB) *int {
	if !d.LANEnabled {
		return nil
	}
	return d.LANPort
}

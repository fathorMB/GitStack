package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/fathorMB/GitStack/services/gateway/internal/openapi"
)

// apiServer implementa openapi.ServerInterface: il gateway non ha logica di
// business propria, ogni operazione instrada semplicemente verso core
// tramite l'handler condiviso (proxy generico). Implementare l'interfaccia
// generata dal contratto, invece di definire le rotte a mano, è ciò che
// impedisce al gateway di discostarsi dal contratto: se il contratto
// cambia, il generato cambia, e questo file smette di compilare finché non
// lo si adegua.
type apiServer struct {
	proxy   http.Handler
	version string // versione dell'installazione (GITSTACK_VERSION)
}

var _ openapi.ServerInterface = (*apiServer)(nil)

func (s *apiServer) GetHealth(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

// GetMeta risponde il gateway stesso, senza passare da core: la versione
// dell'installazione è quella dell'immagine del gateway.
func (s *apiServer) GetMeta(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(openapi.Meta{ServerVersion: s.version, ApiVersion: GatewayVersion})
}

func (s *apiServer) ListResources(w http.ResponseWriter, r *http.Request, _ openapi.ListResourcesParams) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) CreateResource(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) GetResource(w http.ResponseWriter, r *http.Request, _ openapi.ResourceIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) UpdateResource(w http.ResponseWriter, r *http.Request, _ openapi.ResourceIdParam) {
	s.proxy.ServeHTTP(w, r)
}

func (s *apiServer) DeleteResource(w http.ResponseWriter, r *http.Request, _ openapi.ResourceIdParam) {
	s.proxy.ServeHTTP(w, r)
}

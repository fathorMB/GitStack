package httpserver

import (
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
	proxy http.Handler
}

var _ openapi.ServerInterface = (*apiServer)(nil)

func (s *apiServer) GetHealth(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
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

package httpserver

import "net/http"

// GetAdminSystem (GET /v1/admin/system) è servita da core: il gateway
// autentica e applica gli scope della tabella di sicurezza, poi inoltra.
func (s *apiServer) GetAdminSystem(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

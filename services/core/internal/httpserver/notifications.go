package httpserver

import (
	"net/http"
)

// Notifiche, iscrizioni, Watch, preferenze email e webhook (M-06, GIT-129):
// il contratto (api/openapi.yaml) e lo schema (migrazione 0006) sono fissati,
// le operazioni rispondono 501 finché gli item a valle (M-06/B-G) non le
// implementano. Regole C1-C9: .prisma/knowledge/topics/collegamenti-notifiche-webhook.md.
func notificationsNotImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Notifiche e webhook non ancora disponibili.")
}

func (s *apiServer) GetNotificationPreferences(w http.ResponseWriter, _ *http.Request) {
	notificationsNotImplemented(w)
}

func (s *apiServer) UpdateNotificationPreferences(w http.ResponseWriter, _ *http.Request) {
	notificationsNotImplemented(w)
}

package httpserver

import "net/http"

// Database gestiti (v3-d, ADR 0003). Elenco, creazione, lettura, ridimensionamento, eliminazione,
// rotazione, log e metriche sono in databases.go (GIT-551); impostazioni d'istanza e elenco di tutta
// l'istanza (console Admin) in databases_settings.go (GIT-557). Con la funzione spenta ogni operazione
// risponde 422 `databases_disabled`, l'errore stabile del contratto — mai 501 (difetto di GIT-547).
func databaseDisabled(w http.ResponseWriter) {
	writeError(w, http.StatusUnprocessableEntity, "databases_disabled",
		"I database gestiti sono disattivati in questa installazione: li attiva l'amministratore (gitstack config set databases).")
}

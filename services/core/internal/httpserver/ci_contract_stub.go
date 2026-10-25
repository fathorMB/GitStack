package httpserver

import (
	"net/http"
)

// Le superfici CI, registry e release sono fissate dal contratto GIT-275;
// l'implementazione arriva negli item successivi. Restituire 501 mantiene il
// gateway e core allineati senza fingere che la funzione sia disponibile.
// Artefatti, cache, coda e impostazioni di archiviazione sono in ci_storage.go (GIT-280);
// i runner in ci_runners.go (GIT-278); i pacchetti in packages.go (GIT-361).
func notImplemented(w http.ResponseWriter) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

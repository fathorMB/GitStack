package cigateway

import (
	"net/http"
	"strings"
)

// TwirpArtifactPath è il prefisso del servizio Twirp che actions/upload-artifact@v4 e
// download-artifact@v4 chiamano su ACTIONS_RESULTS_URL (GIT-330).
const TwirpArtifactPath = "/twirp/github.actions.results.api.v1.ArtifactService/"

// ErrMsgArtifactsV4 è il messaggio che upload-artifact@v4 mostra nel log del job. Core non implementa
// il protocollo v4 (Twirp + upload su Azure Blob con URL firmati): vedi ADR 0001 §5.
const ErrMsgArtifactsV4 = "GitStack non supporta actions/upload-artifact@v4 e download-artifact@v4 (protocollo Results/Azure Blob): " +
	"carica l'artefatto con PUT /ci/v1/artifacts/{nome} del ci-gateway (corpo = ZIP, token del job) " +
	"oppure usa un runner esterno con un servizio di artefatti compatibile; vedi docs/adr/0001, sezione 5"

// artifactsV4 risponde a ogni chiamata Twirp con un errore chiaro. Prima verifica il token del
// job: senza, 401 come per ogni altro endpoint; un percorso Twirp fuori dal servizio è 404.
func (g *gateway) artifactsV4(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, TwirpArtifactPath) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := g.jobAuth(w, r); !ok {
		return
	}
	// Formato degli errori di Twirp: HTTP 501 per «unimplemented», corpo JSON {code,msg}.
	writeJSON(w, http.StatusNotImplemented, map[string]string{"code": "unimplemented", "msg": ErrMsgArtifactsV4})
}

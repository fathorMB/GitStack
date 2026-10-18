package middleware

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// crossSiteUnsafe dice se r è una richiesta con metodo non sicuro che il
// browser ha originato da un'altra origine (F16, V4.2.2 e V13.2.3): va
// rifiutata quando è autenticata dal cookie di sessione, che il browser
// allega da solo. SameSite=Lax non copre i sottodomini dello stesso sito né i
// browser vecchi, quindi si guardano anche gli header che il browser non lascia
// scrivere alle pagine:
//
//   - Sec-Fetch-Site: ammessi solo "same-origin" e "none" (azione dell'utente
//     sulla barra degli indirizzi); "same-site" e "cross-site" sono estranei;
//   - Origin: l'host (con la porta) deve essere quello della richiesta
//     (Host o X-Forwarded-Host del proxy); "null" è estraneo.
//
// Senza nessuno dei due header (curl, la CLI, un client non browser) non c'è
// niente da giudicare e la richiesta passa: chi non è un browser non porta
// cookie ambientali. Le rotte con token Bearer non sono toccate.
func crossSiteUnsafe(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	}
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site != "" {
		if site != "same-origin" && site != "none" {
			return true
		}
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return true // "null" o non interpretabile
	}
	hosts := []string{r.Host}
	if fwd := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); fwd != "" {
		hosts = append(hosts, fwd)
	}
	for _, h := range hosts {
		if sameHost(u, h) {
			return false
		}
	}
	return true
}

// sameHost confronta l'host di un Origin con quello di una richiesta. Se
// l'header Host non porta la porta (nginx del chart passa `$host`, che la
// toglie) si confronta il solo nome: i cookie non sono isolati per porta,
// quindi la porta non aggiunge una protezione che il cookie non ha.
func sameHost(origin *url.URL, host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(origin.Host, host) {
		return true
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		return strings.EqualFold(origin.Hostname(), strings.Trim(host, "[]"))
	}
	return false
}

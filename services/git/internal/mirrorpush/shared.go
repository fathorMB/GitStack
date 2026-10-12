package mirrorpush

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
)

// Questo file espone ai gemelli (mirrorpull, GIT-196) le stesse protezioni di
// rete e lo stesso redactor del push, senza duplicarli: il trasporto di un
// mirror in pull deve irrigidirsi esattamente come quello del push.

// ValidateRemote controlla URL (https, senza userinfo, niente opzioni
// mascherate) e IP fissato con le stesse regole di Push. Gli errori avvolgono
// ErrInvalid.
func ValidateRemote(rawURL, ip string) (*url.URL, netip.Addr, error) {
	// Credenziale e branch fittizi: qui si validano solo URL e IP.
	return validate(Input{URL: rawURL, Username: "x", Token: "x", IP: ip, DefaultBranch: "main"})
}

// TransportArgs sono le opzioni -c che irrigidiscono il trasporto: solo https,
// niente redirect, nessun proxy, credential.helper vuoto e DNS fissato.
func TransportArgs(u *url.URL, ip netip.Addr) []string { return pushArgs(u, ip, false) }

// TransportArgsProxy è TransportArgs per una destinazione che esce da un
// proxy (N9): senza `-c http.proxy=` (annullerebbe quello dell'ambiente) e
// senza IP fissato, che risolve il proxy. Il proxy lo passa NetEnvProxy.
func TransportArgsProxy(u *url.URL, ip netip.Addr, proxied bool) []string {
	return pushArgs(u, ip, proxied)
}

// ProxyFor dice quale proxy vale per u secondo proxy (in produzione
// http.ProxyFromEnvironment: HTTPS_PROXY e NO_PROXY del chart); nil = nessuno.
func ProxyFor(proxy func(*http.Request) (*url.URL, error), u *url.URL) *url.URL {
	return (&Service{Proxy: proxy}).proxyFor(u)
}

// Redactor toglie da un testo token, credenziale base64 e intestazioni
// Authorization.
func Redactor(username, token string) func(string) string {
	return redactor(Input{Username: username, Token: token})
}

// NetEnv è l'ambiente aggiuntivo dei processi git che parlano con un remote:
// la credenziale (se c'è) passa solo da http.extraHeader in GIT_CONFIG_*,
// nessun prompt, GIT_ASKPASS vuoto, nessun proxy. extra si aggiunge in coda
// (per esempio GIT_SSL_CAINFO).
func NetEnv(username, token string, extra []string) ([]string, error) {
	return NetEnvProxy(username, token, extra, nil)
}

// NetEnvProxy è NetEnv con il proxy scelto per la destinazione (N9): http.proxy
// passa come GIT_CONFIG_*, quindi anche le credenziali del proxy restano fuori
// dall'argv. proxy nil = nessun proxy.
func NetEnvProxy(username, token string, extra []string, proxy *url.URL) ([]string, error) {
	var env []string
	switch {
	case username == "" && token == "":
		// origine pubblica: nessuna intestazione
	case username == "" || token == "":
		return nil, fmt.Errorf("%w: servono utente e token insieme", ErrInvalid)
	default:
		s := &Service{ExtraEnv: extra}
		return s.envWithProxy(Input{Username: username, Token: token}, proxy), nil
	}
	env = []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
	}
	if proxy != nil {
		env = append(env,
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.proxy", "GIT_CONFIG_VALUE_0="+proxy.String(),
			"NO_PROXY=", "no_proxy=",
			"HTTP_PROXY=", "http_proxy=", "HTTPS_PROXY=", "https_proxy=", "ALL_PROXY=", "all_proxy=",
		)
		return append(env, extra...), nil
	}
	env = append(env,
		"NO_PROXY=*", "no_proxy=*",
		"HTTP_PROXY=", "http_proxy=", "HTTPS_PROXY=", "https_proxy=", "ALL_PROXY=", "all_proxy=",
	)
	return append(env, extra...), nil
}

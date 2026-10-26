package apps

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Login GitStack davanti alle app (v3-a, GIT-347, H6, ADR 0002). Ogni app con accesso `login` ha
// nel suo namespace due Middleware Traefik, in quest'ordine davanti alla rotta:
//
//  1. gitstack-strip (headers): svuota ogni intestazione X-GitStack-* mandata dal client, così
//     una intestazione falsificata non arriva mai all'app;
//  2. gitstack-auth (forwardAuth): chiede al gateway se la richiesta può passare. Il gateway
//     risponde 2xx con le sole intestazioni firmate dell'utente (authResponseHeaders) e con
//     Cookie/Authorization ripuliti dalle credenziali GitStack: Traefik le sostituisce a quelle
//     del client, quindi il cookie di sessione non arriva all'app.
//
// L'indirizzo del forward-auth porta app e repo e una firma HMAC: chi chiama il gateway da fuori
// non può scegliere un repo qualsiasi per farsi firmare le intestazioni.
const (
	MiddlewareStrip = "gitstack-strip"
	MiddlewareAuth  = "gitstack-auth"
)

// AuthResponseHeaders sono le intestazioni che il forward-auth del gateway restituisce e che
// Traefik copia sulla richiesta verso l'app (sostituendo quelle del client). Cookie e Authorization
// portano le credenziali del client senza quelle di GitStack.
var AuthResponseHeaders = []string{
	"X-GitStack-User", "X-GitStack-Email", "X-GitStack-Teams", "X-GitStack-Auth-Time",
	"X-GitStack-Key-Id", "X-GitStack-Signature", "Cookie", "Authorization",
}

// StripHeaders sono le intestazioni X-GitStack-* che il client non può mandare all'app.
var StripHeaders = []string{
	"X-GitStack-User", "X-GitStack-Email", "X-GitStack-Teams", "X-GitStack-Auth-Time",
	"X-GitStack-Key-Id", "X-GitStack-Signature", "X-GitStack-App", "X-GitStack-Repo",
}

const authSigPrefix = "gitstack-appauth-v1\n"

// AuthSignature è la firma HMAC-SHA-256 (esadecimale) di app e repo con il segreto del gateway; il
// gateway la ricalcola con tutte le chiavi del suo elenco (rotazione).
func AuthSignature(secret string, app, repo uuid.UUID) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(authSigPrefix + app.String() + "\n" + repo.String()))
	return hex.EncodeToString(m.Sum(nil))
}

// AuthAddress è l'indirizzo del forward-auth di un'app: base?app=<id>&repo=<id>&sig=<hmac>.
func AuthAddress(base, secret string, app, repo uuid.UUID) string {
	q := url.Values{"app": {app.String()}, "repo": {repo.String()}, "sig": {AuthSignature(secret, app, repo)}}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + q.Encode()
}

func middleware(kind, name, ns string, labels map[string]any, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "Middleware",
		"metadata":   map[string]any{"name": name, "namespace": ns, "labels": labels},
		"spec":       spec,
	}}
}

func labelsAny(id uuid.UUID) map[string]any {
	out := map[string]any{}
	for k, v := range objectLabels(id) {
		out[k] = v
	}
	return out
}

// BuildStripMiddleware svuota le intestazioni X-GitStack-* del client.
func BuildStripMiddleware(id uuid.UUID) *unstructured.Unstructured {
	h := map[string]any{}
	for _, n := range StripHeaders {
		h[n] = ""
	}
	return middleware("headers", MiddlewareStrip, Namespace(id), labelsAny(id),
		map[string]any{"headers": map[string]any{"customRequestHeaders": h}})
}

// BuildAuthMiddleware è il forward-auth verso il gateway per l'app. trustForwardHeader resta falso:
// Traefik descrive al gateway la richiesta vera, non quella che il client dice di aver fatto.
func BuildAuthMiddleware(id, repo uuid.UUID, authURL, secret string) *unstructured.Unstructured {
	resp := make([]any, len(AuthResponseHeaders))
	for i, h := range AuthResponseHeaders {
		resp[i] = h
	}
	return middleware("forwardAuth", MiddlewareAuth, Namespace(id), labelsAny(id), map[string]any{
		"forwardAuth": map[string]any{
			"address":             AuthAddress(authURL, secret, id, repo),
			"trustForwardHeader":  false,
			"authResponseHeaders": resp,
		},
	})
}

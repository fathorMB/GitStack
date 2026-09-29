// Package security contiene la dichiarazione di sicurezza di ogni rotta del
// gateway, ricavata da api/openapi.yaml (routes.gen.go, generato da
// ./gen): rotta pubblica (`security: []`) o autenticata, con gli scope di
// `x-required-scopes`. Il gateway non decide da sé cosa è pubblico: una
// richiesta a /v1/* che non corrisponde a nessuna dichiarazione non passa.
package security

//go:generate go run github.com/fathorMB/GitStack/api/cmd/specdump .openapi.spec.yaml
//go:generate go run ./gen -contract .openapi.spec.yaml -out routes.gen.go

import (
	"strings"
)

// CredentialKind è il tipo di credenziale accettato da una rotta.
type CredentialKind string

const (
	// CredentialSession è il cookie di sessione `gst_session`.
	CredentialSession CredentialKind = "session"
	// CredentialToken è il token personale `Authorization: Bearer gst_...`.
	CredentialToken CredentialKind = "token"
)

// Service è il servizio a valle a cui il gateway instrada la rotta.
type Service string

const (
	ServiceCore     Service = "core"
	ServiceIdentity Service = "identity"
)

// Exemption dice se una rotta resta raggiungibile con una sessione la cui
// password iniziale è ancora da cambiare (x-password-change-exempt).
type Exemption int

const (
	// ExemptNever: la rotta risponde 403 password_change_required.
	ExemptNever Exemption = iota
	// ExemptAlways: la rotta passa (GET /auth/session, POST /auth/logout).
	ExemptAlways
	// ExemptSelf: passa solo se il parametro {username} del percorso è
	// l'utente stesso (PUT /users/{username}/password).
	ExemptSelf
)

// Route è la dichiarazione di sicurezza di un'operazione del contratto.
type Route struct {
	Method      string
	Path        string // senza prefisso di versione, es. "/users/{username}"
	OperationID string
	Service     Service
	// Public: `security: []`, raggiungibile senza credenziali.
	Public bool
	// Credentials: tipi di credenziale ammessi (vuoto se Public).
	Credentials []CredentialKind
	// Scopes: scope richiesti a un token (tutti); le sessioni non hanno
	// scope e passano.
	Scopes               []string
	PasswordChangeExempt Exemption
}

// Accepts dice se la rotta ammette il tipo di credenziale.
func (r Route) Accepts(k CredentialKind) bool {
	for _, c := range r.Credentials {
		if c == k {
			return true
		}
	}
	return false
}

// Match è una rotta trovata per una richiesta, con i valori dei parametri di
// percorso.
type Match struct {
	Route  Route
	Params map[string]string
}

type compiled struct {
	route    Route
	segments []string
	literals int
}

// Table cerca la dichiarazione di sicurezza di una richiesta.
type Table struct {
	routes []compiled
}

// NewTable costruisce la tabella. Ritorna errore per dichiarazioni
// incoerenti (duplicati, rotta non pubblica senza credenziali).
func NewTable(routes []Route) (*Table, error) {
	t := &Table{}
	seen := map[string]bool{}
	for _, r := range routes {
		key := r.Method + " " + r.Path
		if seen[key] {
			return nil, &DeclarationError{Route: key, Reason: "dichiarata due volte"}
		}
		seen[key] = true
		if !r.Public && len(r.Credentials) == 0 {
			return nil, &DeclarationError{Route: key, Reason: "né pubblica né con credenziali ammesse"}
		}
		segs := splitPath(r.Path)
		lit := 0
		for _, s := range segs {
			if !isParam(s) {
				lit++
			}
		}
		t.routes = append(t.routes, compiled{route: r, segments: segs, literals: lit})
	}
	return t, nil
}

// DeclarationError descrive una dichiarazione di sicurezza mancante o
// incoerente.
type DeclarationError struct {
	Route  string
	Reason string
}

func (e *DeclarationError) Error() string {
	return "dichiarazione di sicurezza della rotta " + e.Route + ": " + e.Reason
}

// Lookup trova la dichiarazione per metodo e percorso (senza prefisso di
// versione). A parità di forma vince la rotta con più segmenti letterali.
func (t *Table) Lookup(method, path string) (Match, bool) {
	segs := splitPath(path)
	var best *compiled
	var bestParams map[string]string
	for i := range t.routes {
		c := &t.routes[i]
		if c.route.Method != method || len(c.segments) != len(segs) {
			continue
		}
		params := map[string]string{}
		ok := true
		for j, s := range c.segments {
			if isParam(s) {
				if segs[j] == "" {
					ok = false
					break
				}
				params[s[1:len(s)-1]] = segs[j]
				continue
			}
			if s != segs[j] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if best == nil || c.literals > best.literals {
			best, bestParams = c, params
		}
	}
	if best == nil {
		return Match{}, false
	}
	return Match{Route: best.route, Params: bestParams}, true
}

// Routes ritorna tutte le dichiarazioni della tabella.
func (t *Table) Routes() []Route {
	out := make([]Route, len(t.routes))
	for i, c := range t.routes {
		out[i] = c.route
	}
	return out
}

func splitPath(p string) []string {
	return strings.Split(strings.TrimPrefix(p, "/"), "/")
}

func isParam(s string) bool {
	return len(s) > 2 && s[0] == '{' && s[len(s)-1] == '}'
}

// Grants dice se l'insieme di scope del token soddisfa lo scope richiesto:
// `write:*` include `read:*` dello stesso ambito e `admin:org` include
// `write:org` (e quindi `read:org`), come da catalogo TokenScope del
// contratto.
func Grants(have []string, required string) bool {
	for _, h := range have {
		if h == required || implies(h, required) {
			return true
		}
	}
	return false
}

func implies(have, required string) bool {
	hAct, hScope, ok := strings.Cut(have, ":")
	rAct, rScope, ok2 := strings.Cut(required, ":")
	if !ok || !ok2 || hScope != rScope {
		return false
	}
	switch hAct {
	case "admin":
		return rAct == "write" || rAct == "read"
	case "write":
		return rAct == "read"
	}
	return false
}

// Missing ritorna gli scope richiesti che l'insieme non soddisfa.
func Missing(have, required []string) []string {
	var miss []string
	for _, r := range required {
		if !Grants(have, r) {
			miss = append(miss, r)
		}
	}
	return miss
}

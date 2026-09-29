// Package oidc implementa il login esterno con provider OpenID Connect
// configurati dall'amministratore (D5): authorization code flow con PKCE
// (S256), state e nonce verificati, token ID validato da go-oidc (firma,
// issuer, audience, scadenza) e collegamento a un utente locale.
//
// La configurazione è un file JSON (GITSTACK_IDENTITY_OIDC_CONFIG_FILE, nel
// deploy montato da un Secret Kubernetes). All'avvio i provider del file sono
// sincronizzati in identity.oidc_providers con il segreto cifrato
// (AES-256-GCM); quelli tolti dal file diventano enabled=false. Il segreto
// non compare mai nei log né negli errori.
package oidc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Default dei claim e degli scope.
const (
	DefaultClaimEmail         = "email"
	DefaultClaimEmailVerified = "email_verified"
	DefaultClaimUsername      = "preferred_username"
	DefaultClaimDisplayName   = "name"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// Claims è la mappatura fra i claim del token ID e i campi dell'utente.
type Claims struct {
	Email         string
	EmailVerified string
	Username      string
	DisplayName   string
}

// Provider è un provider OIDC validato, con i default applicati.
type Provider struct {
	Slug        string
	DisplayName string
	Issuer      string
	ClientID    string
	// ClientSecret è in chiaro solo in memoria, dal file di configurazione.
	ClientSecret        string
	Scopes              []string
	Claims              Claims
	LinkByVerifiedEmail bool
	AutoCreateUsers     bool
}

// Options regola la validazione. AllowInsecureIssuer accetta issuer http
// (solo per i test con un IdP locale: in produzione l'issuer è https).
type Options struct {
	AllowInsecureIssuer bool
}

type rawClaims struct {
	Email         *string `json:"email"`
	EmailVerified *string `json:"emailVerified"`
	Username      *string `json:"username"`
	DisplayName   *string `json:"displayName"`
}

type rawProvider struct {
	Slug                string     `json:"slug"`
	DisplayName         string     `json:"displayName"`
	Issuer              string     `json:"issuer"`
	ClientID            string     `json:"clientId"`
	ClientSecret        string     `json:"clientSecret"`
	Scopes              []string   `json:"scopes"`
	Claims              *rawClaims `json:"claims"`
	LinkByVerifiedEmail bool       `json:"linkByVerifiedEmail"`
	AutoCreateUsers     bool       `json:"autoCreateUsers"`
}

type rawFile struct {
	Providers []rawProvider `json:"providers"`
}

// LoadFile legge e valida il file di configurazione.
func LoadFile(path string, opts Options) ([]Provider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		// Il messaggio di os.ReadFile contiene solo il percorso.
		return nil, fmt.Errorf("lettura del file di configurazione OIDC non riuscita: %w", err)
	}
	return Parse(data, opts)
}

// Parse valida il contenuto del file: un oggetto {"providers": [...]} oppure
// direttamente la lista. Gli errori elencano tutti i problemi e non
// contengono mai i segreti.
func Parse(data []byte, opts Options) ([]Provider, error) {
	trimmed := bytes.TrimSpace(data)
	var rp []rawProvider
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := dec.Decode(&rp); err != nil {
			return nil, fmt.Errorf("configurazione OIDC non valida: %s", jsonErr(err))
		}
	} else {
		var f rawFile
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("configurazione OIDC non valida: %s", jsonErr(err))
		}
		rp = f.Providers
	}
	if dec.More() {
		return nil, errors.New("configurazione OIDC non valida: dati dopo la fine del JSON")
	}

	var errs []string
	seen := map[string]bool{}
	out := make([]Provider, 0, len(rp))
	for i, r := range rp {
		p, perrs := validate(r, opts)
		label := fmt.Sprintf("provider[%d]", i)
		if r.Slug != "" {
			label += " (" + r.Slug + ")"
		}
		for _, e := range perrs {
			errs = append(errs, label+": "+e)
		}
		if seen[r.Slug] && r.Slug != "" {
			errs = append(errs, label+": slug duplicato")
		}
		seen[r.Slug] = true
		out = append(out, p)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("configurazione OIDC non valida:\n- %s", strings.Join(errs, "\n- "))
	}
	return out, nil
}

// jsonErr riduce l'errore di decodifica al minimo utile: i tipi di
// json.UnmarshalTypeError e i campi sconosciuti non contengono valori.
func jsonErr(err error) string {
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		return fmt.Sprintf("JSON malformato (byte %d)", syn.Offset)
	}
	return err.Error()
}

func validate(r rawProvider, opts Options) (Provider, []string) {
	var errs []string
	if !slugRe.MatchString(r.Slug) {
		errs = append(errs, "slug non valido (minuscole, cifre e trattini, 1-32 caratteri)")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(r.DisplayName)); n < 1 || n > 64 {
		errs = append(errs, "displayName obbligatorio, al massimo 64 caratteri")
	}
	if !validIssuer(r.Issuer, opts) {
		errs = append(errs, "issuer deve essere un URL https senza query né frammento")
	}
	if strings.TrimSpace(r.ClientID) == "" {
		errs = append(errs, "clientId obbligatorio")
	}
	if r.ClientSecret == "" {
		errs = append(errs, "clientSecret obbligatorio")
	}

	scopes := r.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	hasOpenID := false
	for _, s := range scopes {
		if strings.TrimSpace(s) == "" || strings.ContainsAny(s, " \t\r\n") {
			errs = append(errs, "gli scope non possono essere vuoti né contenere spazi")
			break
		}
		if s == "openid" {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		scopes = append([]string{"openid"}, scopes...)
	}

	c := Claims{
		Email: DefaultClaimEmail, EmailVerified: DefaultClaimEmailVerified,
		Username: DefaultClaimUsername, DisplayName: DefaultClaimDisplayName,
	}
	if r.Claims != nil {
		set := func(dst *string, src *string) {
			if src != nil {
				*dst = strings.TrimSpace(*src)
			}
		}
		set(&c.Email, r.Claims.Email)
		set(&c.EmailVerified, r.Claims.EmailVerified)
		set(&c.Username, r.Claims.Username)
		set(&c.DisplayName, r.Claims.DisplayName)
	}
	if r.LinkByVerifiedEmail || r.AutoCreateUsers {
		if c.Email == "" {
			errs = append(errs, "claims.email è obbligatorio con linkByVerifiedEmail o autoCreateUsers")
		}
	}
	if r.LinkByVerifiedEmail && c.EmailVerified == "" {
		errs = append(errs, "claims.emailVerified è obbligatorio con linkByVerifiedEmail")
	}

	return Provider{
		Slug: r.Slug, DisplayName: strings.TrimSpace(r.DisplayName), Issuer: r.Issuer,
		ClientID: strings.TrimSpace(r.ClientID), ClientSecret: r.ClientSecret,
		Scopes: scopes, Claims: c,
		LinkByVerifiedEmail: r.LinkByVerifiedEmail, AutoCreateUsers: r.AutoCreateUsers,
	}, errs
}

func validIssuer(issuer string, opts Options) bool {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || (opts.AllowInsecureIssuer && u.Scheme == "http")
}

// ParseKey decodifica la chiave di cifratura: 32 byte in base64.
func ParseKey(b64 string) ([]byte, error) {
	b64 = strings.TrimSpace(b64)
	var raw []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err = enc.DecodeString(b64); err == nil {
			break
		}
	}
	if err != nil {
		return nil, errors.New("la chiave non è base64 valido")
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("la chiave deve essere di 32 byte, sono %d", len(raw))
	}
	return raw, nil
}

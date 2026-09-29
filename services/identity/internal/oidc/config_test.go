package oidc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validFile = `{"providers":[
 {"slug":"keycloak","displayName":"Keycloak","issuer":"https://kc.example.com/realms/gs","clientId":"gitstack","clientSecret":"TOP-SECRET-1",
  "linkByVerifiedEmail":true,"autoCreateUsers":true},
 {"slug":"google","displayName":"Google","issuer":"https://accounts.google.com","clientId":"id.apps.googleusercontent.com","clientSecret":"TOP-SECRET-2",
  "scopes":["openid","email"],"claims":{"username":"email","displayName":"name"}}
]}`

func TestParse_OK(t *testing.T) {
	ps, err := Parse([]byte(validFile), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("provider = %d", len(ps))
	}
	kc, g := ps[0], ps[1]
	if strings.Join(kc.Scopes, " ") != "openid profile email" {
		t.Errorf("scope di default: %v", kc.Scopes)
	}
	if kc.Claims != (Claims{Email: "email", EmailVerified: "email_verified", Username: "preferred_username", DisplayName: "name"}) {
		t.Errorf("claim di default: %+v", kc.Claims)
	}
	if !kc.LinkByVerifiedEmail || !kc.AutoCreateUsers || g.LinkByVerifiedEmail || g.AutoCreateUsers {
		t.Error("le opzioni di collegamento hanno default false")
	}
	if g.Claims.Username != "email" || g.Claims.Email != "email" {
		t.Errorf("claim parziali: %+v", g.Claims)
	}
}

func TestParse_BareList(t *testing.T) {
	ps, err := Parse([]byte(`[{"slug":"a","displayName":"A","issuer":"https://a.example","clientId":"c","clientSecret":"s"}]`), Options{})
	if err != nil || len(ps) != 1 {
		t.Fatalf("%v %v", ps, err)
	}
}

func TestParse_OpenIDScopeAlwaysPresent(t *testing.T) {
	ps, err := Parse([]byte(`[{"slug":"a","displayName":"A","issuer":"https://a.example","clientId":"c","clientSecret":"s","scopes":["email"]}]`), Options{})
	if err != nil || ps[0].Scopes[0] != "openid" {
		t.Fatalf("%v %v", ps, err)
	}
}

func TestParse_Errors(t *testing.T) {
	base := func(mod string) string {
		return `{"providers":[{"slug":"a","displayName":"A","issuer":"https://a.example","clientId":"c","clientSecret":"TOP-SECRET"` + mod + `}]}`
	}
	cases := map[string]string{
		"JSON rotto":                 `{"providers":[`,
		"campo sconosciuto":          base(`,"clientSecrt":"x"`),
		"slug maiuscolo":             strings.Replace(base(""), `"a"`, `"Abc"`, 1),
		"slug troppo lungo":          strings.Replace(base(""), `"slug":"a"`, `"slug":"`+strings.Repeat("a", 33)+`"`, 1),
		"issuer http":                strings.Replace(base(""), "https://a.example", "http://a.example", 1),
		"issuer con query":           strings.Replace(base(""), "https://a.example", "https://a.example/?x=1", 1),
		"issuer assente":             strings.Replace(base(""), `"issuer":"https://a.example"`, `"issuer":""`, 1),
		"clientId assente":           strings.Replace(base(""), `"clientId":"c"`, `"clientId":""`, 1),
		"clientSecret assente":       strings.Replace(base(""), `"clientSecret":"TOP-SECRET"`, `"clientSecret":""`, 1),
		"displayName assente":        strings.Replace(base(""), `"displayName":"A"`, `"displayName":""`, 1),
		"scope con spazio":           base(`,"scopes":["openid email"]`),
		"claim email vuota con link": base(`,"linkByVerifiedEmail":true,"claims":{"email":""}`),
		"claim email vuota con auto": base(`,"autoCreateUsers":true,"claims":{"email":""}`),
		"claim verified vuota":       base(`,"linkByVerifiedEmail":true,"claims":{"emailVerified":""}`),
		"dati dopo il JSON":          base("") + `{}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(in), Options{})
			if err == nil {
				t.Fatal("attendevo un errore")
			}
			if strings.Contains(err.Error(), "TOP-SECRET") {
				t.Errorf("l'errore contiene il segreto: %v", err)
			}
		})
	}
	t.Run("slug duplicato", func(t *testing.T) {
		one := `{"slug":"a","displayName":"A","issuer":"https://a.example","clientId":"c","clientSecret":"s"}`
		if _, err := Parse([]byte(`[`+one+`,`+one+`]`), Options{}); err == nil || !strings.Contains(err.Error(), "duplicato") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("claim email vuota senza link né auto è ammessa", func(t *testing.T) {
		if _, err := Parse([]byte(base(`,"claims":{"email":""}`)), Options{}); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("issuer http solo con l'opzione dei test", func(t *testing.T) {
		in := strings.Replace(base(""), "https://a.example", "http://127.0.0.1:8080/realms/x", 1)
		if _, err := Parse([]byte(in), Options{AllowInsecureIssuer: true}); err != nil {
			t.Errorf("err = %v", err)
		}
	})
}

func TestLoadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "oidc.json")
	if err := os.WriteFile(p, []byte(validFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if ps, err := LoadFile(p, Options{}); err != nil || len(ps) != 2 {
		t.Fatalf("%v %v", ps, err)
	}
	if _, err := LoadFile(filepath.Join(t.TempDir(), "manca.json"), Options{}); err == nil {
		t.Error("file mancante: attendevo un errore")
	}
}

func TestParseKey(t *testing.T) {
	if _, err := ParseKey("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="); err != nil {
		t.Errorf("chiave valida: %v", err)
	}
	for _, bad := range []string{"", "@@", "c2hvcnQ="} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("chiave %q accettata", bad)
		}
	}
}

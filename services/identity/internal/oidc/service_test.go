package oidc

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

func linkedTo(t *testing.T, r *rig, subject string) (users.User, bool) {
	t.Helper()
	row, err := r.repo.Provider(context.Background(), "kc")
	if err != nil {
		t.Fatal(err)
	}
	u, ok, _ := r.repo.IdentityUser(context.Background(), row.ID, subject)
	return u, ok
}

func TestStart_BuildsAuthorizationRequest(t *testing.T) {
	r := newRig(t, nil)
	res, err := r.svc.Start(context.Background(), "kc", "/repos")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(res.URL)
	q := u.Query()
	if q.Get("redirect_uri") != "https://git.test/api/v1/auth/oidc/kc/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("scope") != "openid profile email" {
		t.Errorf("scope = %q", q.Get("scope"))
	}
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 43 {
		t.Errorf("PKCE mancante: %v", q)
	}
	if len(q.Get("state")) < 32 || len(q.Get("nonce")) < 32 {
		t.Errorf("state/nonce troppo corti: %v", q)
	}
	c := res.Cookie
	if c.Name != StateCookieName || !c.HttpOnly || !c.Secure || c.Path != "/api/v1/auth/oidc/kc/callback" || c.MaxAge != 600 {
		t.Errorf("cookie di stato inatteso: %+v", c)
	}
	if strings.Contains(c.Value, q.Get("state")) || strings.Contains(c.Value, q.Get("nonce")) {
		t.Error("il cookie non è cifrato: contiene state o nonce in chiaro")
	}
	// Due start diversi non ripetono state, nonce e verifier.
	res2, _ := r.svc.Start(context.Background(), "kc", "")
	u2, _ := url.Parse(res2.URL)
	if u2.Query().Get("state") == q.Get("state") || u2.Query().Get("nonce") == q.Get("nonce") ||
		u2.Query().Get("code_challenge") == q.Get("code_challenge") {
		t.Error("state, nonce o challenge riusati")
	}
}

func TestStart_Errors(t *testing.T) {
	r := newRig(t, nil)
	if _, err := r.svc.Start(context.Background(), "altro", ""); !errors.Is(err, ErrDisabled) {
		t.Errorf("provider sconosciuto: %v", err)
	}
	for _, bad := range []string{"https://evil.example", "//evil.example", "/\\evil", "relativo", "/a\r\nSet-Cookie: x=y", strings.Repeat("/a", 300)} {
		if _, err := r.svc.Start(context.Background(), "kc", bad); !errors.Is(err, ErrInvalidRedirect) {
			t.Errorf("redirectTo %q accettato: %v", bad, err)
		}
	}
	// Provider tolto dal file: sync lo disabilita.
	r.repo.mu.Lock()
	r.repo.enabled["kc"] = false
	r.repo.mu.Unlock()
	if _, err := r.svc.Start(context.Background(), "kc", ""); !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("provider disabilitato: %v", err)
	}
}

func TestFinish_LinkedIdentityLogsIn(t *testing.T) {
	r := newRig(t, nil)
	u := r.repo.addUser("alice", "", true)
	row, _ := r.repo.Provider(context.Background(), "kc")
	_ = r.repo.LinkIdentity(context.Background(), u.ID, row.ID, "sub-1", "")

	res, err := r.finish(r.begin("/repos/x"))
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if res.User.ID != u.ID || res.SessionValue == "" || res.Session.AuthMethod != "oidc" || res.RedirectTo != "/repos/x" {
		t.Errorf("esito inatteso: %+v", res)
	}
}

func TestFinish_StateChecks(t *testing.T) {
	t.Run("state diverso", func(t *testing.T) {
		r := newRig(t, nil)
		in := r.begin("")
		in.State += "x"
		if _, err := r.finish(in); !errors.Is(err, ErrInvalidState) {
			t.Errorf("err = %v", err)
		}
		if r.idp.hits() != 0 {
			t.Error("il token endpoint non va chiamato con uno state sbagliato")
		}
	})
	t.Run("state di un altro flusso", func(t *testing.T) {
		r := newRig(t, nil)
		a, b := r.begin(""), r.begin("")
		a.State = b.State
		if _, err := r.finish(a); !errors.Is(err, ErrInvalidState) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("senza cookie", func(t *testing.T) {
		r := newRig(t, nil)
		in := r.begin("")
		in.StateCookie = ""
		if _, err := r.finish(in); !errors.Is(err, ErrInvalidState) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cookie manomesso", func(t *testing.T) {
		r := newRig(t, nil)
		in := r.begin("")
		b := []byte(in.StateCookie)
		if b[len(b)-3] == 'A' {
			b[len(b)-3] = 'B'
		} else {
			b[len(b)-3] = 'A'
		}
		in.StateCookie = string(b)
		if _, err := r.finish(in); !errors.Is(err, ErrInvalidState) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cookie non decifrabile", func(t *testing.T) {
		r := newRig(t, nil)
		in := r.begin("")
		in.StateCookie = "AAAA"
		if _, err := r.finish(in); !errors.Is(err, ErrInvalidState) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cookie cifrato con un'altra chiave", func(t *testing.T) {
		r := newRig(t, nil)
		in := r.begin("")
		v, _ := sealState([]byte("ffffffffffffffffffffffffffffffff"), flowState{Slug: "kc", State: in.State, Nonce: "n", Verifier: "v", Expires: time.Now().Add(time.Hour).Unix()})
		in.StateCookie = v
		if _, err := r.finish(in); !errors.Is(err, ErrInvalidState) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cookie di un altro provider", func(t *testing.T) {
		r := newRig(t, nil)
		in := r.begin("")
		st, _ := openState(testKey, in.StateCookie, r.clock.Now())
		st.Slug = "altro"
		in.StateCookie, _ = sealState(testKey, st)
		if _, err := r.finish(in); !errors.Is(err, ErrInvalidState) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cookie scaduto", func(t *testing.T) {
		r := newRig(t, nil)
		in := r.begin("")
		r.clock.advance(StateTTL + time.Second)
		if _, err := r.finish(in); !errors.Is(err, ErrStateExpired) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("riuso dello stesso cookie e state", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
		in := r.begin("")
		if _, err := r.finish(in); err != nil {
			t.Fatalf("prima callback: %v", err)
		}
		// Anche con un code nuovo e valido, lo state è già consumato.
		in2 := in
		in2.Code, _ = r.idp.authorize(r.svcStartURL(t, in))
		if _, err := r.finish(in2); !errors.Is(err, ErrInvalidState) {
			t.Errorf("riuso: err = %v", err)
		}
	})
}

// svcStartURL ricostruisce un authorization URL con lo state e il nonce di in
// (per simulare un attaccante che riusa cookie e state).
func (r *rig) svcStartURL(t *testing.T, in FinishInput) string {
	t.Helper()
	st, err := openState(testKey, in.StateCookie, r.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{
		"response_type": {"code"}, "client_id": {testClientID}, "state": {st.State}, "nonce": {st.Nonce},
		"code_challenge": {"x"}, "code_challenge_method": {"S256"}, "redirect_uri": {r.svc.CallbackURL("kc")},
	}
	return r.idp.srv.URL + "/authorize?" + q.Encode()
}

func TestFinish_TokenValidation(t *testing.T) {
	past := func() int64 { return time.Now().Add(-time.Hour).Unix() }
	cases := []struct {
		name  string
		setup func(p *fakeIdP)
	}{
		{"firma con un'altra chiave", func(p *fakeIdP) { p.signWith = p.other }},
		{"issuer sbagliato", func(p *fakeIdP) { p.claims = func(c map[string]any, _ authCode) { c["iss"] = "https://evil.example" } }},
		{"audience sbagliata", func(p *fakeIdP) { p.claims = func(c map[string]any, _ authCode) { c["aud"] = "un-altro-client" } }},
		{"audience multipla senza il client", func(p *fakeIdP) {
			p.claims = func(c map[string]any, _ authCode) { c["aud"] = []string{"a", "b"} }
		}},
		{"token scaduto", func(p *fakeIdP) { p.claims = func(c map[string]any, _ authCode) { c["exp"] = past() } }},
		{"nonce diverso", func(p *fakeIdP) { p.claims = func(c map[string]any, _ authCode) { c["nonce"] = "altro-nonce" } }},
		{"nonce assente", func(p *fakeIdP) { p.claims = func(c map[string]any, _ authCode) { delete(c, "nonce") } }},
		{"subject assente", func(p *fakeIdP) { p.claims = func(c map[string]any, _ authCode) { c["sub"] = "" } }},
		{"nessun id_token", func(p *fakeIdP) { p.noIDToken = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
			tc.setup(r.idp)
			_, err := r.finish(r.begin(""))
			if !errors.Is(err, ErrLoginFailed) {
				t.Errorf("err = %v, voluto ErrLoginFailed", err)
			}
			if len(r.repo.users) != 0 || r.repo.sessions != 0 {
				t.Error("nessun utente né sessione doveva nascere")
			}
		})
	}
}

func TestFinish_CodeAndPKCE(t *testing.T) {
	t.Run("code sbagliato", func(t *testing.T) {
		r := newRig(t, nil)
		in := r.begin("")
		in.Code = "inventato"
		if _, err := r.finish(in); !errors.Is(err, ErrLoginFailed) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("code_verifier che non corrisponde alla challenge", func(t *testing.T) {
		r := newRig(t, nil)
		in := r.begin("")
		// Lo stato porta un verifier diverso da quello della challenge.
		st, _ := openState(testKey, in.StateCookie, r.clock.Now())
		st.Verifier = "un-altro-verifier-un-altro-verifier-un-altro-verifier"
		in.StateCookie, _ = sealState(testKey, st)
		if _, err := r.finish(in); !errors.Is(err, ErrLoginFailed) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("client secret decifrato dal database", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
		row, _ := r.repo.Provider(context.Background(), "kc")
		if strings.Contains(string(row.SecretEnc), testClientSecret) {
			t.Error("client_secret_enc contiene il segreto in chiaro")
		}
		if _, err := r.finish(r.begin("")); err != nil {
			t.Errorf("login con segreto cifrato: %v", err)
		}
	})
	t.Run("chiave di cifratura ruotata senza sync", func(t *testing.T) {
		r := newRig(t, nil)
		r.svc.cfg.KeyID = "k2"
		if _, err := r.svc.Start(context.Background(), "kc", ""); err == nil {
			t.Error("con key id diverso il segreto non va decifrato")
		}
	})
}

func TestFinish_LinkByVerifiedEmail(t *testing.T) {
	cases := []struct {
		name     string
		verified any // valore del claim email_verified; nil = assente
		absent   bool
		link     bool
		wantLink bool
	}{
		{"true booleano", true, false, true, true},
		{"stringa true", "true", false, true, true},
		{"false", false, false, true, false},
		{"assente", nil, true, true, false},
		{"stringa True", "True", false, true, false},
		{"stringa 1", "1", false, true, false},
		{"numero 1", 1, false, true, false},
		{"stringa false", "false", false, true, false},
		{"null", nil, false, true, false},
		{"verificata ma opzione spenta", true, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, func(p *Provider) { p.LinkByVerifiedEmail = tc.link })
			existing := r.repo.addUser("alice-locale", "Alice@Example.com", true)
			r.idp.claims = func(c map[string]any, _ authCode) {
				if tc.absent {
					delete(c, "email_verified")
				} else {
					c["email_verified"] = tc.verified
				}
			}
			res, err := r.finish(r.begin(""))
			if tc.wantLink {
				if err != nil || res.User.ID != existing.ID {
					t.Fatalf("doveva collegare all'utente esistente: %v %+v", err, res.User)
				}
				if u, ok := linkedTo(t, r, "sub-1"); !ok || u.ID != existing.ID {
					t.Error("identità non registrata")
				}
				return
			}
			if !errors.Is(err, ErrUnlinked) {
				t.Fatalf("err = %v, voluto ErrUnlinked", err)
			}
			if _, ok := linkedTo(t, r, "sub-1"); ok {
				t.Error("un'email non verificata non deve collegare")
			}
			if r.repo.sessions != 0 {
				t.Error("sessione creata senza collegamento")
			}
		})
	}
}

func TestFinish_LinkUsesConfiguredClaimNames(t *testing.T) {
	r := newRig(t, func(p *Provider) {
		p.LinkByVerifiedEmail = true
		p.Claims.Email = "mail"
		p.Claims.EmailVerified = "mail_ok"
	})
	existing := r.repo.addUser("bob", "bob@corp.example", true)
	r.idp.claims = func(c map[string]any, _ authCode) {
		delete(c, "email")
		c["mail"], c["mail_ok"] = "bob@corp.example", true
	}
	res, err := r.finish(r.begin(""))
	if err != nil || res.User.ID != existing.ID {
		t.Fatalf("err = %v user = %+v", err, res.User)
	}
}

func TestFinish_InactiveUser(t *testing.T) {
	t.Run("collegato per email", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.LinkByVerifiedEmail = true })
		r.repo.addUser("alice", "alice@example.com", false)
		if _, err := r.finish(r.begin("")); !errors.Is(err, ErrUserInactive) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("già collegato", func(t *testing.T) {
		r := newRig(t, nil)
		u := r.repo.addUser("alice", "", false)
		row, _ := r.repo.Provider(context.Background(), "kc")
		_ = r.repo.LinkIdentity(context.Background(), u.ID, row.ID, "sub-1", "")
		if _, err := r.finish(r.begin("")); !errors.Is(err, ErrUserInactive) {
			t.Errorf("err = %v", err)
		}
		if r.repo.sessions != 0 {
			t.Error("sessione creata per utente disattivato")
		}
	})
}

func TestFinish_DefaultIsUnlinked(t *testing.T) {
	r := newRig(t, nil) // entrambe le opzioni a false
	r.repo.addUser("alice", "alice@example.com", true)
	if _, err := r.finish(r.begin("")); !errors.Is(err, ErrUnlinked) {
		t.Fatalf("err = %v", err)
	}
	if len(r.repo.users) != 1 {
		t.Error("non si crea nessun utente col default")
	}
}

func TestFinish_AutoCreate(t *testing.T) {
	t.Run("crea e collega", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
		res, err := r.finish(r.begin("/x"))
		if err != nil {
			t.Fatal(err)
		}
		u := res.User
		if u.Username != "alice" || u.Kind != users.KindHuman || u.IsAdmin || u.DisplayName != "Alice Example" ||
			u.Email == nil || *u.Email != "alice@example.com" {
			t.Errorf("utente inatteso: %+v", u)
		}
		if _, ok := linkedTo(t, r, "sub-1"); !ok {
			t.Error("identità non collegata")
		}
		// Secondo login: stesso utente, nessuna creazione.
		res2, err := r.finish(r.begin(""))
		if err != nil || res2.User.ID != u.ID || len(r.repo.users) != 1 {
			t.Errorf("secondo login: %v users=%d", err, len(r.repo.users))
		}
	})
	t.Run("username preso: suffisso numerico", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
		r.repo.addUser("alice", "", true)
		r.repo.addUser("alice-2", "", true)
		res, err := r.finish(r.begin(""))
		if err != nil || res.User.Username != "alice-3" {
			t.Fatalf("err = %v user = %+v", err, res.User)
		}
	})
	t.Run("username normalizzato", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
		r.idp.claims = func(c map[string]any, _ authCode) { c["preferred_username"] = "Alice.O'Neil@Corp" }
		res, err := r.finish(r.begin(""))
		if err != nil || res.User.Username != "alice-o-neil-corp" {
			t.Fatalf("err = %v user = %+v", err, res.User)
		}
	})
	t.Run("senza claim username si usa l'email", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
		r.idp.claims = func(c map[string]any, _ authCode) { delete(c, "preferred_username") }
		res, err := r.finish(r.begin(""))
		if err != nil || res.User.Username != "alice" {
			t.Fatalf("err = %v user = %+v", err, res.User)
		}
	})
	t.Run("email già di un utente: 409 anche se verificata e senza opzione di collegamento", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
		r.repo.addUser("altra-persona", "alice@example.com", true)
		if _, err := r.finish(r.begin("")); !errors.Is(err, ErrUnlinked) {
			t.Fatalf("err = %v", err)
		}
		if len(r.repo.users) != 1 {
			t.Error("nessun utente doveva essere creato")
		}
	})
	t.Run("email non verificata già presa: mai collegata, nemmeno con entrambe le opzioni", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers, p.LinkByVerifiedEmail = true, true })
		victim := r.repo.addUser("vittima", "alice@example.com", true)
		r.idp.claims = func(c map[string]any, _ authCode) { c["email_verified"] = false }
		if _, err := r.finish(r.begin("")); !errors.Is(err, ErrUnlinked) {
			t.Fatalf("err = %v", err)
		}
		if _, ok := linkedTo(t, r, "sub-1"); ok {
			t.Error("l'identità non verificata è stata collegata all'utente esistente")
		}
		if len(r.repo.users) != 1 || r.repo.sessions != 0 {
			t.Errorf("utenti=%d sessioni=%d", len(r.repo.users), r.repo.sessions)
		}
		_ = victim
	})
	t.Run("email non verificata e libera: utente creato senza email", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
		r.idp.claims = func(c map[string]any, _ authCode) { delete(c, "email_verified") }
		res, err := r.finish(r.begin(""))
		if err != nil {
			t.Fatal(err)
		}
		if res.User.Email != nil {
			t.Errorf("email non verificata salvata: %v", *res.User.Email)
		}
	})
	t.Run("senza claim email", func(t *testing.T) {
		r := newRig(t, func(p *Provider) { p.AutoCreateUsers = true })
		r.idp.claims = func(c map[string]any, _ authCode) { delete(c, "email"); delete(c, "email_verified") }
		res, err := r.finish(r.begin(""))
		if err != nil || res.User.Email != nil || res.User.Username != "alice" {
			t.Fatalf("err = %v user = %+v", err, res.User)
		}
	})
}

func TestList(t *testing.T) {
	r := newRig(t, nil)
	l := r.svc.List()
	if len(l) != 1 || l[0].Slug != "kc" || l[0].DisplayName != "Keycloak" {
		t.Errorf("elenco inatteso: %+v", l)
	}
	off, _ := New(newMemRepo(), Config{}, nil, nil)
	if len(off.List()) != 0 {
		t.Error("senza provider l'elenco è vuoto")
	}
	if _, err := off.Start(context.Background(), "kc", ""); !errors.Is(err, ErrDisabled) {
		t.Errorf("start su servizio spento: %v", err)
	}
}

func TestNormalizeUsername(t *testing.T) {
	for in, want := range map[string]string{
		"Alice": "alice", "a.b_c": "a-b-c", "--x--": "x", "": "user", "!!!": "user", "José": "jos",
		strings.Repeat("a", 60): strings.Repeat("a", 39),
	} {
		if got := NormalizeUsername(in); got != want {
			t.Errorf("NormalizeUsername(%q) = %q, voluto %q", in, got, want)
		}
	}
	if got := usernameWithSuffix(strings.Repeat("a", 39), 12); len(got) != 39 || !strings.HasSuffix(got, "-12") {
		t.Errorf("suffisso oltre i 39 caratteri: %q", got)
	}
}

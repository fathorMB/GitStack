//go:build integration

package httpapi_test

import (
	"bytes"
	"encoding/base32"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/secondfactor"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/totp"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

// newTotpEnv monta identity con il secondo fattore (F11). noKeys: keyring vuoto.
func newTotpEnv(t *testing.T, required, noKeys bool) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	var keys []secondfactor.Key
	if !noKeys {
		keys = []secondfactor.Key{{ID: "k1", Key: bytes.Repeat([]byte{7}, 32)}}
	}
	sf := secondfactor.New(pool, c.now, keys, "GitStack")
	svc := &auth.Service{
		Users: us, Sessions: sessions.New(pool, c.now, time.Hour),
		Limiter: loginlimit.New(loginlimit.Config{MaxPerUser: 20, MaxPerIP: 200, Window: 10 * time.Minute}, c.now),
		Second:  sf, RequireAdminTOTP: required,
	}
	var logs bytes.Buffer
	h := httpapi.New(svc, slog.New(slog.NewTextHandler(&logs, nil)),
		httpapi.WithServiceSecret(secret), httpapi.WithSecondFactor(sf))
	return &env{t: t, h: h, clock: c, users: us}
}

// enrollOverHTTP attiva il TOTP via API con la sessione ck e ritorna il
// segreto e i codici di recupero; poi avanza l'orologio di un passo.
func (e *env) enrollOverHTTP(ck *http.Cookie) ([]byte, []any) {
	e.t.Helper()
	en := e.do("POST", "/auth/totp/enroll", nil, ck)
	status(e.t, en, 200)
	b := en.json()
	secretB32, _ := b["secret"].(string)
	if uri, _ := b["otpauthUri"].(string); len(uri) < 20 || uri[:15] != "otpauth://totp/" {
		e.t.Fatalf("otpauthUri inatteso: %s", en.body)
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretB32)
	if err != nil {
		e.t.Fatal(err)
	}
	v := e.do("POST", "/auth/totp/verify", map[string]string{"code": totp.Code(raw, totp.Step(e.clock.now()))}, ck)
	status(e.t, v, 200)
	codes, _ := v.json()["recoveryCodes"].([]any)
	if len(codes) != 10 {
		e.t.Fatalf("codici di recupero: %s", v.body)
	}
	e.clock.advance(totp.Period * time.Second)
	return raw, codes
}

func (e *env) loginBody(body map[string]string) (*http.Cookie, resp) {
	e.t.Helper()
	r := e.do("POST", "/auth/login", body, nil)
	for _, c := range r.Cookies() {
		if c.Name == "gst_session" {
			return c, r
		}
	}
	return nil, r
}

func TestTotpLoginTwoStepsOverHTTP(t *testing.T) {
	e := newTotpEnv(t, false, false)
	e.mk("alice", false)
	ck := e.mustLogin("alice")

	st := e.do("GET", "/auth/totp", nil, ck)
	status(t, st, 200)
	if j := st.json(); j["enabled"] != false || j["available"] != true || j["requiredForAdmins"] != false {
		t.Fatalf("stato iniziale: %s", st.body)
	}
	status(t, e.do("GET", "/auth/totp", nil, nil), 401)

	// Conferma con un codice sbagliato: 422, e il TOTP non si attiva.
	status(t, e.do("POST", "/auth/totp/enroll", nil, ck), 200)
	errCode(t, e.do("POST", "/auth/totp/verify", map[string]string{"code": "000000"}, ck), 422, "validation_failed")
	errCode(t, e.do("POST", "/auth/totp/verify", map[string]string{"code": "12"}, ck), 422, "validation_failed")
	if _, r := e.login("alice", pw); r.StatusCode != 200 {
		t.Fatalf("login con attivazione in attesa: %d %s", r.StatusCode, r.body)
	}

	secretRaw, _ := e.enrollOverHTTP(ck)
	st = e.do("GET", "/auth/totp", nil, ck)
	if j := st.json(); j["enabled"] != true || j["recoveryCodesRemaining"] != float64(10) {
		t.Fatalf("stato dopo l'attivazione: %s", st.body)
	}
	errCode(t, e.do("POST", "/auth/totp/enroll", nil, ck), 409, "already_enabled")

	// Primo passo: solo password → 401 second_factor_required, nessun cookie.
	ck2, r := e.login("alice", pw)
	errCode(t, r, 401, "second_factor_required")
	if ck2 != nil {
		t.Fatal("cookie impostato senza secondo fattore")
	}
	// Secondo passo con un codice sbagliato: invalid_credentials.
	_, r = e.loginBody(map[string]string{"username": "alice", "password": pw, "totpCode": "000000"})
	errCode(t, r, 401, "invalid_credentials")
	// Formato non valido: 422.
	_, r = e.loginBody(map[string]string{"username": "alice", "password": pw, "totpCode": "abc"})
	errCode(t, r, 422, "validation_failed")
	// Codice giusto: sessione.
	code := totp.Code(secretRaw, totp.Step(e.clock.now()))
	ck2, r = e.loginBody(map[string]string{"username": "alice", "password": pw, "totpCode": code})
	if ck2 == nil || r.StatusCode != 200 {
		t.Fatalf("login a due passi: %d %s", r.StatusCode, r.body)
	}
	// Replay dello stesso codice: rifiutato.
	_, r = e.loginBody(map[string]string{"username": "alice", "password": pw, "totpCode": code})
	errCode(t, r, 401, "invalid_credentials")

	// Disattivazione: password sbagliata → 403; poi password e codice → 204,
	// le altre sessioni sono revocate e basta di nuovo la password.
	e.clock.advance(totp.Period * time.Second)
	next := totp.Code(secretRaw, totp.Step(e.clock.now()))
	errCode(t, e.do("POST", "/auth/totp/disable", map[string]string{"password": "sbagliata sbagliata", "code": next}, ck2), 403, "forbidden")
	errCode(t, e.do("POST", "/auth/totp/disable", map[string]string{"password": pw, "code": "000000"}, ck2), 403, "forbidden")
	status(t, e.do("POST", "/auth/totp/disable", map[string]string{"password": pw, "code": next}, ck2), 204)
	errCode(t, e.do("GET", "/auth/session", nil, ck), 401, "unauthenticated")
	status(t, e.do("GET", "/auth/session", nil, ck2), 200)
	if _, r := e.login("alice", pw); r.StatusCode != 200 {
		t.Fatalf("login dopo la disattivazione: %d %s", r.StatusCode, r.body)
	}
	errCode(t, e.do("POST", "/auth/totp/disable", map[string]string{"password": pw}, ck2), 409, "not_enabled")
}

func TestTotpRecoveryCodeOverHTTPAndAdminReset(t *testing.T) {
	e := newTotpEnv(t, false, false)
	e.mk("alice", false)
	e.mk("root", true)
	e.mk("bob", false)
	ck := e.mustLogin("alice")
	_, codes := e.enrollOverHTTP(ck)

	rc, _ := codes[0].(string)
	got, r := e.loginBody(map[string]string{"username": "alice", "password": pw, "recoveryCode": rc})
	if got == nil || r.StatusCode != 200 {
		t.Fatalf("login con codice di recupero: %d %s", r.StatusCode, r.body)
	}
	_, r = e.loginBody(map[string]string{"username": "alice", "password": pw, "recoveryCode": rc})
	errCode(t, r, 401, "invalid_credentials")

	// Chi ha perso tutto: un admin azzera il TOTP; un non admin no.
	bobCk := e.mustLogin("bob")
	errCode(t, e.do("DELETE", "/users/alice/totp", nil, bobCk), 403, "forbidden")
	rootCk := e.mustLogin("root")
	errCode(t, e.do("DELETE", "/users/nessuno/totp", nil, rootCk), 404, "not_found")
	status(t, e.do("DELETE", "/users/alice/totp", nil, rootCk), 204)
	errCode(t, e.do("GET", "/auth/session", nil, got), 401, "unauthenticated")
	if _, r := e.login("alice", pw); r.StatusCode != 200 {
		t.Fatalf("login dopo l'azzeramento: %d %s", r.StatusCode, r.body)
	}
}

// Obbligatorio per gli admin: finché non lo attiva, ogni chiamata autenticata
// risponde 403 totp_enrollment_required, tranne sessione, logout e /auth/totp*.
func TestTotpRequiredForAdminsOverHTTP(t *testing.T) {
	e := newTotpEnv(t, true, false)
	e.mk("root", true)
	e.mk("alice", false)

	ck, r := e.login("root", pw)
	if ck == nil || r.json()["mustEnrollTotp"] != true {
		t.Fatalf("login admin: %d %s", r.StatusCode, r.body)
	}
	errCode(t, e.do("GET", "/users", nil, ck), 403, httpapi.CodeTotpEnrollmentRequired)
	sess := e.do("GET", "/auth/session", nil, ck)
	status(t, sess, 200)
	if sess.json()["mustEnrollTotp"] != true {
		t.Fatalf("sessione: %s", sess.body)
	}
	if j := e.do("GET", "/auth/totp", nil, ck).json(); j["requiredForAdmins"] != true {
		t.Fatalf("stato: %v", j)
	}

	// /internal/verify lo dice al gateway.
	v := e.do("POST", "/internal/verify", map[string]any{"credential": ck.Value}, nil, bearer(secret)...)
	status(t, v, 200)
	if p, _ := v.json()["principal"].(map[string]any); p["mustEnrollTotp"] != true {
		t.Fatalf("verify: %s", v.body)
	}

	// Un utente normale non è obbligato.
	aliceCk, r := e.login("alice", pw)
	if aliceCk == nil || r.json()["mustEnrollTotp"] == true {
		t.Fatalf("login utente: %s", r.body)
	}
	status(t, e.do("GET", "/users", nil, aliceCk), 200)

	secretRaw, _ := e.enrollOverHTTP(ck)
	status(t, e.do("GET", "/users", nil, ck), 200)
	v = e.do("POST", "/internal/verify", map[string]any{"credential": ck.Value}, nil, bearer(secret)...)
	if p, _ := v.json()["principal"].(map[string]any); p["mustEnrollTotp"] != false {
		t.Fatalf("verify dopo l'attivazione: %s", v.body)
	}

	// Un admin non può disattivarlo.
	e.clock.advance(totp.Period * time.Second)
	code := totp.Code(secretRaw, totp.Step(e.clock.now()))
	errCode(t, e.do("POST", "/auth/totp/disable", map[string]string{"password": pw, "code": code}, ck), 409, "totp_required")

	// L'admin entra solo con il secondo fattore.
	_, r = e.login("root", pw)
	errCode(t, r, 401, "second_factor_required")
}

func TestTotpUnavailableWithoutKeys(t *testing.T) {
	e := newTotpEnv(t, false, true)
	e.mk("alice", false)
	ck := e.mustLogin("alice")
	errCode(t, e.do("POST", "/auth/totp/enroll", nil, ck), 503, "totp_unavailable")
	if j := e.do("GET", "/auth/totp", nil, ck).json(); j["available"] != false || j["enabled"] != false {
		t.Fatalf("stato: %v", j)
	}
}

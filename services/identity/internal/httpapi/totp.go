package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/fathorMB/GitStack/services/identity/internal/password"
	"github.com/fathorMB/GitStack/services/identity/internal/secondfactor"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
)

// WithSecondFactor abilita il secondo fattore TOTP (tag auth /auth/totp*,
// DELETE /users/{username}/totp). Senza, gli endpoint rispondono 503.
func WithSecondFactor(m *secondfactor.Service) Option { return func(s *server) { s.mfa = m } }

// CodeTotpEnrollmentRequired è il codice di errore (403) di ogni chiamata
// autenticata di un amministratore che deve ancora attivare il TOTP.
const CodeTotpEnrollmentRequired = "totp_enrollment_required"

func totpEnrollmentRequired(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, CodeTotpEnrollmentRequired, "L'installazione richiede il secondo fattore: attiva il TOTP prima di continuare.")
}

var totpCodeRe = regexp.MustCompile(`^[0-9]{6}$`)

// totpSession risolve il chiamante per gli endpoint /auth/totp*: solo
// sessione (i token personali non hanno secondo fattore) e servizio attivo.
// Passa da currentPending: chi deve cambiare la password o attivare il TOTP
// può comunque usarli.
func (s *server) totpSession(w http.ResponseWriter, r *http.Request) (auth.Current, bool) {
	cur, ok := s.currentPending(w, r)
	if !ok {
		return auth.Current{}, false
	}
	if cur.Session.AuthMethod == "token" {
		forbidden(w)
		return auth.Current{}, false
	}
	if cur.MustChange {
		passwordChangeRequired(w)
		return auth.Current{}, false
	}
	return cur, true
}

func (s *server) totpUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "totp_unavailable", "Il secondo fattore non è configurato in questa installazione.")
}

func (s *server) GetTotpStatus(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.totpSession(w, r)
	if !ok {
		return
	}
	out := openapi.TotpStatus{RequiredForAdmins: s.auth.RequireAdminTOTP}
	if s.mfa != nil {
		st, err := s.mfa.Status(r.Context(), cur.User.ID)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		out.Available = s.mfa.Available()
		out.Enabled, out.RecoveryCodesRemaining = st.Enabled, st.RecoveryCodesRemaining
		if st.Pending {
			p := true
			out.Pending = &p
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) EnrollTotp(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.totpSession(w, r)
	if !ok {
		return
	}
	if s.mfa == nil || !s.mfa.Available() {
		s.totpUnavailable(w)
		return
	}
	// Solo account con password: agenti e utenti solo-OIDC non hanno un
	// secondo fattore locale (l'MFA di OIDC è del provider).
	acc, err := s.users.FindForLogin(r.Context(), cur.User.Username)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if cur.User.Kind != users.KindHuman || acc.PasswordHash == "" {
		writeError(w, http.StatusConflict, "not_local", "Il secondo fattore vale solo per gli account locali con password.")
		return
	}
	label := cur.User.Username
	if cur.User.Email != nil && *cur.User.Email != "" {
		label = *cur.User.Email
	}
	en, err := s.mfa.Enroll(r.Context(), cur.User.ID, label)
	switch {
	case errors.Is(err, secondfactor.ErrAlreadyEnabled):
		writeError(w, http.StatusConflict, "already_enabled", "Il TOTP è già attivo.")
		return
	case errors.Is(err, secondfactor.ErrUnavailable):
		s.totpUnavailable(w)
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	s.secInfo(r, "security.totp.enroll_started", &cur)
	writeJSON(w, http.StatusOK, openapi.TotpEnrollment{Secret: en.Secret, OtpauthUri: en.URI})
}

func (s *server) VerifyTotp(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.totpSession(w, r)
	if !ok {
		return
	}
	var in openapi.TotpCodeInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	if !totpCodeRe.MatchString(in.Code) {
		validationFailed(w, map[string]string{"code": "6 cifre"})
		return
	}
	if s.mfa == nil || !s.mfa.Available() {
		s.totpUnavailable(w)
		return
	}
	ip := s.clientIP(r)
	if ok, retry := s.auth.Limiter.Check(cur.User.Username, ip); !ok {
		s.secWarn(r, "security.totp.rate_limited", &cur)
		s.tooMany(w, retry)
		return
	}
	codes, err := s.mfa.Confirm(r.Context(), cur.User.ID, in.Code)
	switch {
	case errors.Is(err, secondfactor.ErrNotPending):
		writeError(w, http.StatusConflict, "not_pending", "Nessuna attivazione del TOTP in corso.")
		return
	case errors.Is(err, secondfactor.ErrInvalidCode):
		s.auth.Limiter.Fail(cur.User.Username, ip)
		s.secWarn(r, "security.totp.enroll_failed", &cur, "reason", "invalid_code")
		validationFailed(w, map[string]string{"code": "codice non valido"})
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	s.secInfo(r, "security.totp.enabled", &cur)
	writeJSON(w, http.StatusOK, openapi.TotpRecoveryCodes{RecoveryCodes: codes})
}

func (s *server) DisableTotp(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.totpSession(w, r)
	if !ok {
		return
	}
	var in openapi.TotpDisableInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	if in.Password == nil || *in.Password == "" || utf8.RuneCountInString(*in.Password) > 1024 {
		validationFailed(w, map[string]string{"password": "da 1 a 1024 caratteri"})
		return
	}
	var code, recovery string
	if in.Code != nil {
		code = *in.Code
	}
	if in.RecoveryCode != nil {
		recovery = *in.RecoveryCode
	}
	if code != "" && !totpCodeRe.MatchString(code) {
		validationFailed(w, map[string]string{"code": "6 cifre"})
		return
	}
	if s.mfa == nil {
		s.totpUnavailable(w)
		return
	}
	st, err := s.mfa.Status(r.Context(), cur.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !st.Enabled && !st.Pending {
		writeError(w, http.StatusConflict, "not_enabled", "Il TOTP non è attivo.")
		return
	}
	if cur.User.IsAdmin && s.auth.RequireAdminTOTP && st.Enabled {
		writeError(w, http.StatusConflict, "totp_required", "L'installazione impone il TOTP agli amministratori.")
		return
	}
	ip := s.clientIP(r)
	if ok, retry := s.auth.Limiter.Check(cur.User.Username, ip); !ok {
		s.secWarn(r, "security.totp.rate_limited", &cur)
		s.tooMany(w, retry)
		return
	}
	deny := func(reason string) {
		s.auth.Limiter.Fail(cur.User.Username, ip)
		s.secWarn(r, "security.totp.disable_failed", &cur, "reason", reason)
		writeError(w, http.StatusForbidden, "forbidden", "Password o codice errati.")
	}
	acc, err := s.users.FindForLogin(r.Context(), cur.User.Username)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if good, verr := password.Verify(*in.Password, acc.PasswordHash); verr != nil || !good {
		deny("wrong_password")
		return
	}
	if st.Enabled {
		good, err := s.mfa.Verify(r.Context(), cur.User.ID, code, recovery)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if !good {
			deny("wrong_code")
			return
		}
	}
	if err := s.mfa.Remove(r.Context(), cur.User.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	s.auth.Limiter.Success(cur.User.Username)
	if cur.Session.ID != uuid.Nil {
		id := cur.Session.ID
		if _, err := s.auth.Sessions.RevokeAllForUser(r.Context(), cur.User.ID, &id); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	s.secInfo(r, "security.totp.disabled", &cur)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) ResetUserTotp(w http.ResponseWriter, r *http.Request, username openapi.UsernameParam) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	if !cur.User.IsAdmin {
		forbidden(w)
		return
	}
	u, err := s.users.Get(r.Context(), username)
	if !s.userError(w, r, err) {
		return
	}
	if s.mfa == nil {
		s.totpUnavailable(w)
		return
	}
	if err := s.mfa.Remove(r.Context(), u.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	var keep *uuid.UUID
	if u.ID == cur.User.ID && cur.Session.ID != uuid.Nil {
		id := cur.Session.ID
		keep = &id
	}
	if _, err := s.auth.Sessions.RevokeAllForUser(r.Context(), u.ID, keep); err != nil {
		s.internal(w, r, err)
		return
	}
	s.secInfo(r, "security.totp.reset", &cur, "target_id", u.ID.String(), "target", u.Username)
	w.WriteHeader(http.StatusNoContent)
}

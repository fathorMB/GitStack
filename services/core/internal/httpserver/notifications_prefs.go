package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// Preferenze email delle notifiche (M-06/F, GIT-134, regola C5). Una riga in
// core.notification_preferences per utente e tipo; nessuna riga = default:
// email per `mentioned` e `assigned`, non per gli altri tipi. Le preferenze si
// leggono e si salvano anche senza SMTP e per gli agenti, ma `emailAvailable`
// dice che le email non partono (la UI nasconde le opzioni).

// WithEmail dice a core se l'installazione ha un SMTP configurato (le
// preferenze lo riportano in `emailAvailable`).
func WithEmail(enabled bool) Option {
	return func(o *routerOptions) { o.emailEnabled = enabled }
}

// defaultEmail: i tipi con l'email attiva senza preferenza esplicita (C5).
var defaultEmail = map[string]bool{"mentioned": true, "assigned": true}

// emailPrefs ritorna le preferenze dell'utente con tutti i tipi presenti.
func (s *apiServer) emailPrefs(r *http.Request, userID uuid.UUID) (map[string]bool, error) {
	out := make(map[string]bool, len(notificationReasons))
	for reason := range notificationReasons {
		out[reason] = defaultEmail[reason]
	}
	rows, err := s.pool.Query(r.Context(), `SELECT reason, email_enabled FROM core.notification_preferences WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var reason string
		var on bool
		if err := rows.Scan(&reason, &on); err != nil {
			return nil, err
		}
		out[reason] = on
	}
	return out, rows.Err()
}

// emailAvailable: l'SMTP c'è e il chiamante non è un agente (C4, C5). Ritorna
// ok=false dopo aver risposto 503 se serve identity e non c'è.
func (s *apiServer) emailAvailable(w http.ResponseWriter, r *http.Request, userID uuid.UUID) (available, ok bool) {
	if !s.emailEnabled {
		return false, true
	}
	look, has := s.repoIdentity.(identityclient.UserLookup)
	if !has {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile stabilire se il chiamante è un agente.")
		return false, false
	}
	found, err := look.LookupUsers(r.Context(), []uuid.UUID{userID})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non raggiungibile.")
		return false, false
	}
	u, exists := found[userID]
	return exists && u.Kind != "agent", true
}

func (s *apiServer) respondPrefs(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	avail, ok := s.emailAvailable(w, r, userID)
	if !ok {
		return
	}
	prefs, err := s.emailPrefs(r, userID)
	if err != nil {
		writeIssueFailure(w, "lettura delle preferenze non riuscita", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.NotificationPreferences{EmailAvailable: avail, Email: prefs})
}

// GetNotificationPreferences implementa GET /user/notification-preferences.
func (s *apiServer) GetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	s.respondPrefs(w, r, userID)
}

// UpdateNotificationPreferences implementa PUT /user/notification-preferences:
// imposta i tipi indicati, gli omessi restano; un tipo sconosciuto è 422
// (details.fields.email) e non cambia nulla.
func (s *apiServer) UpdateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	_, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	var body openapi.UpdateNotificationPreferencesInput
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.Email == nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Corpo della richiesta non valido: serve {\"email\": {tipo: true|false}}.")
		return
	}
	reasons := make([]string, 0, len(body.Email))
	for reason := range body.Email {
		if !notificationReasons[reason] {
			writeFieldError(w, "email", fmt.Sprintf("tipo di notifica sconosciuto «%s».", reason))
			return
		}
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		writeIssueFailure(w, "salvataggio delle preferenze non riuscito", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	for _, reason := range reasons {
		if _, err := tx.Exec(r.Context(), `INSERT INTO core.notification_preferences (user_id, reason, email_enabled, updated_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (user_id, reason) DO UPDATE SET email_enabled = EXCLUDED.email_enabled, updated_at = now()`,
			userID, reason, body.Email[reason]); err != nil {
			writeIssueFailure(w, "salvataggio delle preferenze non riuscito", err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeIssueFailure(w, "salvataggio delle preferenze non riuscito", err)
		return
	}
	s.respondPrefs(w, r, userID)
}

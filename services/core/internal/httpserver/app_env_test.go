package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// GIT-378 (H14): il prefisso GITSTACK_ è riservato alle variabili di sistema; variabili e segreti
// dell'app che lo usano ricevono 422 con l'errore sul campo name, in qualunque caso.
func TestAppEnvNameOK_PrefissoGitStackRiservato(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"GITSTACK_X", false},
		{"GITSTACK_APP_VERSION", false},
		{"gitstack_x", false},
		{"GitStack_Token", false},
		{"MY_GITSTACK_X", true},
		{"GITSTACK", true},
		{"DATABASE_URL", true},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		if got := appEnvNameOK(rec, tc.name); got != tc.ok {
			t.Errorf("appEnvNameOK(%q) = %v, atteso %v", tc.name, got, tc.ok)
		}
		if tc.ok {
			continue
		}
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"fields"`) || !strings.Contains(rec.Body.String(), `"name"`) {
			t.Errorf("appEnvNameOK(%q): risposta %d %s, attesi 422 con fields.name", tc.name, rec.Code, rec.Body.String())
		}
	}
}

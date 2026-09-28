package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// Questi test coprono la validazione dell'input, che risponde prima di
// toccare lo store: apiServer.resources resta nil apposta, per verificare
// che non venga mai chiamato sugli input invalidi (altrimenti panicherebbe
// su nil pointer, facendo fallire il test). I percorsi che leggono/scrivono
// davvero su Postgres sono coperti dai test d'integrazione
// (router_integration_test.go, tag "integration").

func TestListResources_InvalidPage(t *testing.T) {
	s := &apiServer{}
	req := httptest.NewRequest(http.MethodGet, "/resources?page=0", nil)
	rec := httptest.NewRecorder()

	s.ListResources(rec, req, openapi.ListResourcesParams{Page: intPtr(0)})

	assertErrorResponse(t, rec, http.StatusBadRequest, "invalid_page")
}

func TestListResources_InvalidPerPage(t *testing.T) {
	s := &apiServer{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/resources?perPage=0", nil)

	s.ListResources(rec, req, openapi.ListResourcesParams{PerPage: intPtr(0)})
	assertErrorResponse(t, rec, http.StatusBadRequest, "invalid_per_page")

	rec = httptest.NewRecorder()
	s.ListResources(rec, req, openapi.ListResourcesParams{PerPage: intPtr(101)})
	assertErrorResponse(t, rec, http.StatusBadRequest, "invalid_per_page")
}

func TestListResources_InvalidType(t *testing.T) {
	s := &apiServer{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/resources?type=", nil)

	empty := ""
	s.ListResources(rec, req, openapi.ListResourcesParams{Type: &empty})
	assertErrorResponse(t, rec, http.StatusBadRequest, "invalid_type")
}

func TestCreateResource_InvalidBody(t *testing.T) {
	s := &apiServer{}

	cases := map[string]string{
		"json non valido":   `{`,
		"type mancante":     `{"name":"x"}`,
		"name mancante":     `{"type":"repo"}`,
		"campo sconosciuto": `{"type":"repo","name":"x","nope":true}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/resources", bytes.NewBufferString(body))
			rec := httptest.NewRecorder()
			s.CreateResource(rec, req)
			assertErrorResponse(t, rec, http.StatusBadRequest, "invalid_body")
		})
	}
}

func TestUpdateResource_InvalidBody(t *testing.T) {
	s := &apiServer{}
	id := openapi.ResourceIdParam{}

	cases := map[string]string{
		"corpo vuoto":       `{}`,
		"name vuoto":        `{"name":""}`,
		"campo sconosciuto": `{"name":"x","nope":true}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/resources/x", bytes.NewBufferString(body))
			rec := httptest.NewRecorder()
			s.UpdateResource(rec, req, id)
			assertErrorResponse(t, rec, http.StatusBadRequest, "invalid_body")
		})
	}
}

func intPtr(i int) *int { return &i }

func assertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, voluto %d (corpo: %s)", rec.Code, wantStatus, rec.Body.String())
	}
	var apiErr openapi.Error
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decodifica errore non riuscita: %v", err)
	}
	if apiErr.Error.Code != wantCode {
		t.Fatalf("error.code = %q, voluto %q", apiErr.Error.Code, wantCode)
	}
}

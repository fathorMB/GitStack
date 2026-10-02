//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// stubPublisher registra gli eventi pubblicati invece di mandarli su NATS:
// nella suite d'integrazione di core verifica solo che CreateResource
// agganci correttamente events.Publisher (nome/versione/payload
// dell'evento di prova). Il test che l'evento arrivi davvero su NATS
// JetStream reale è nel perimetro di GIT-6 (nota del CTO su GIT-5/GIT-6);
// qui, in attesa della libreria condivisa, core usa events.NoopPublisher in
// produzione (vedi main.go).
type stubPublisher struct {
	mu      sync.Mutex
	name    string
	version int
	payload any
	calls   int
}

func (p *stubPublisher) Publish(_ context.Context, name string, version int, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.name, p.version, p.payload = name, version, payload
	p.calls++
	return nil
}

func (p *stubPublisher) snapshot() (string, int, any, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.name, p.version, p.payload, p.calls
}

func TestRouter_ResourceLifecycleAndHealth(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	pub := &stubPublisher{}
	router := httpserver.NewRouter(pool, pub, testSecret, httpserver.WithCreatorGranter(&fakeGranter{}))
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Come il gateway, il client firma l'identità di ogni richiesta.
	client := &http.Client{Transport: signingTransport{}}

	// Senza identità firmata le rotte di risorse rispondono 401 (con i
	// probe e /health che restano pubblici, provati sotto).
	if resp, err := http.Get(srv.URL + "/resources"); err != nil {
		t.Fatalf("GET /resources non riuscita: %v", err)
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET /resources senza identità = %d, voluto 401", resp.StatusCode)
		}
	}

	// /healthz: liveness, sempre 200.
	resp, err := client.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz non riuscita: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d, voluto 200", resp.StatusCode)
	}

	// /readyz e GET /health (contratto): 200 con Postgres raggiungibile.
	for _, path := range []string{"/readyz", "/health"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s non riuscita: %v", path, err)
		}
		var health openapi.Health
		if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
			t.Fatalf("decodifica corpo %s non riuscita: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || health.Status != openapi.Ok {
			t.Fatalf("%s = %d %q, voluto 200 ok", path, resp.StatusCode, health.Status)
		}
	}

	// POST /resources: crea la risorsa di prova e deve pubblicare l'evento.
	createBody, _ := json.Marshal(openapi.CreateResourceInput{Type: "repo", Name: "gitstack"})
	resp, err = client.Post(srv.URL+"/resources", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("POST /resources non riuscita: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /resources = %d, voluto 201", resp.StatusCode)
	}
	var created openapi.Resource
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decodifica risorsa creata non riuscita: %v", err)
	}
	_ = resp.Body.Close()
	if resp.Header.Get("Location") == "" {
		t.Fatal("POST /resources deve impostare l'header Location")
	}
	if created.Id == nil {
		t.Fatal("la risorsa creata deve avere un id")
	}

	waitForPublish(t, pub)
	name, version, payload, calls := pub.snapshot()
	if calls != 1 {
		t.Fatalf("Publish chiamato %d volte, voluto 1", calls)
	}
	if name != events.TestResourceCreatedName || version != events.TestResourceCreatedVersion {
		t.Fatalf("evento pubblicato = %q v%d, voluto %q v%d", name, version, events.TestResourceCreatedName, events.TestResourceCreatedVersion)
	}
	got, ok := payload.(events.TestResourceCreatedPayload)
	if !ok {
		t.Fatalf("payload dell'evento di tipo inatteso: %T", payload)
	}
	if got.ResourceID != created.Id.String() || got.Type != "repo" || got.Name != "gitstack" {
		t.Fatalf("payload evento = %+v, non corrisponde alla risorsa creata %+v", got, created)
	}

	id := created.Id.String()

	// GET /resources/{id}.
	resp, err = client.Get(srv.URL + "/resources/" + id)
	if err != nil {
		t.Fatalf("GET /resources/{id} non riuscita: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources/{id} = %d, voluto 200", resp.StatusCode)
	}

	// PATCH /resources/{id}.
	patchBody, _ := json.Marshal(openapi.UpdateResourceInput{Name: strPtr("gitstack-2")})
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/resources/"+id, bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("PATCH /resources/{id} non riuscita: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /resources/{id} = %d, voluto 200", resp.StatusCode)
	}

	// DELETE /resources/{id}.
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/resources/"+id, nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /resources/{id} non riuscita: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /resources/{id} = %d, voluto 204", resp.StatusCode)
	}

	// GET dopo DELETE: 404 nel formato Error del contratto.
	resp, err = client.Get(srv.URL + "/resources/" + id)
	if err != nil {
		t.Fatalf("GET /resources/{id} dopo delete non riuscita: %v", err)
	}
	var apiErr openapi.Error
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decodifica errore non riuscita: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || apiErr.Error.Code != "not_found" {
		t.Fatalf("GET dopo delete = %d %q, voluto 404 not_found", resp.StatusCode, apiErr.Error.Code)
	}
}

// waitForPublish attende che CreateResource abbia pubblicato l'evento in
// background (vedi apiServer.publishTestResourceCreated, che gira in una
// goroutine per non far dipendere la risposta HTTP dal bus eventi).
func waitForPublish(t *testing.T, pub *stubPublisher) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, _, calls := pub.snapshot(); calls > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout in attesa della pubblicazione dell'evento di prova")
}

func strPtr(s string) *string { return &s }

const testSecret = "segreto-di-servizio-di-prova"

// signingTransport aggiunge a ogni richiesta l'identità firmata, come il
// gateway.
type signingTransport struct{}

func (signingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	trust.Sign(req.Header, testSecret, trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice"}, time.Now())
	return http.DefaultTransport.RoundTrip(req)
}

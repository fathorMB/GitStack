//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-492 (v3-c): webhook app_domain dagli stessi eventi da cui GIT-461 crea la notifica. Un invio per
// guasto o scadenza, payload di WebhookAppDomainPayload, mai chiavi né contenuto dei certificati.

type domainHookSealer struct{}

func (domainHookSealer) Open(id uuid.UUID, ct, nonce []byte, keyID string) (string, error) {
	return "", nil
}

func (domainHookSealer) Seal(id uuid.UUID, plaintext string) ([]byte, []byte, string, error) {
	return []byte("ct"), []byte("nonce"), "k1", nil
}

func TestAppDomainWebhook_UnInvioPerEpisodio(t *testing.T) {
	e := newHookEnv(t)
	ctx := context.Background()
	repo := e.repo("app", false)
	e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["app_domain"]}`, hookURL))
	app := createTestApp(t, e.pool, repo, "alice", "web", "alice")
	st := store.New(e.pool)
	now := time.Now().UTC().Truncate(time.Microsecond)

	d, err := st.AddAppDomain(ctx, app.ID, "www.example.org", "www.example.org", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	// dns_failed: due controlli nello stesso stato, un solo invio.
	why := "www.example.org non risolve"
	for i := 0; i < 2; i++ {
		if _, err := st.SaveAppDomainCheck(ctx, d.ID, store.AppDomainCheck{Status: store.DomainError, StatusReason: &why, CheckedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	// renewal_failed: idem.
	msg := "Traefik non ha ottenuto il certificato"
	for i := 0; i < 2; i++ {
		if _, err := st.SaveAppDomainCert(ctx, d.ID, store.AppDomainCert{State: "error", Error: &msg, LastAttempt: now}); err != nil {
			t.Fatal(err)
		}
	}
	// expiring: certificato del cliente con una chiave che non deve uscire; tre giri, un avviso.
	const secret = "-----BEGIN PRIVATE KEY-----SEGRETISSIMO"
	until := now.Add(5 * 24 * time.Hour)
	if _, err := st.SetAppDomainCustomCert(ctx, domainHookSealer{}, d.ID, store.CustomCertInput{
		ChainPEM: "-----BEGIN CERTIFICATE-----CATENA", KeyPEM: secret, ValidFrom: now.Add(-24 * time.Hour), ValidUntil: until, Issuer: "CA",
	}, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.NoticeCustomCertsExpiring(ctx, now.Add(time.Duration(i)*time.Hour), 14*24*time.Hour); err != nil {
			t.Fatal(err)
		}
	}

	e.run()
	e.run() // un secondo giro non deve consegnare di nuovo
	reqs := e.recv.requests()
	if len(reqs) != 3 {
		t.Fatalf("consegne app_domain = %d (voluto 3: dns_failed, renewal_failed, expiring)", len(reqs))
	}
	byAction := map[string]map[string]any{}
	var actions []string
	for _, r := range reqs {
		if got := r.header.Get("X-GitStack-Event"); got != "app_domain" {
			t.Fatalf("intestazione X-GitStack-Event = %q", got)
		}
		for _, bad := range []string{secret, "SEGRETISSIMO", "CATENA", "PRIVATE KEY", "BEGIN CERTIFICATE"} {
			if strings.Contains(string(r.body), bad) {
				t.Fatalf("il payload contiene %q: %s", bad, r.body)
			}
		}
		var body map[string]any
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatal(err)
		}
		a := body["action"].(string)
		actions = append(actions, a)
		byAction[a] = body
		if body["event"] != "app_domain" || body["version"] != float64(1) {
			t.Errorf("corpo comune: %v", body)
		}
		if body["app"].(map[string]any)["name"] != "web" || body["domain"] != "www.example.org" {
			t.Errorf("app e dominio: %v", body)
		}
		if s, ok := body["sender"]; !ok || s != nil {
			t.Errorf("sender deve essere null: %v", body)
		}
		if _, ok := body["repository"]; !ok {
			t.Errorf("manca il repo: %v", body)
		}
		allowed := map[string]bool{"version": true, "event": true, "action": true, "time": true, "repository": true,
			"organization": true, "sender": true, "app": true, "domain": true, "status": true, "reason": true, "validUntil": true}
		for k := range body {
			if !allowed[k] {
				t.Errorf("campo fuori contratto: %q", k)
			}
		}
	}
	sort.Strings(actions)
	if want := []string{"certificate_expiring", "certificate_renewal_failed", "dns_failed"}; fmt.Sprint(actions) != fmt.Sprint(want) {
		t.Fatalf("azioni consegnate: %v, voluto %v", actions, want)
	}
	if b := byAction["dns_failed"]; b["status"] != "dns_failed" || b["reason"] != why {
		t.Errorf("dns_failed: %v", b)
	}
	if b := byAction["certificate_renewal_failed"]; b["status"] != "renewal_failed" || b["reason"] != msg {
		t.Errorf("renewal_failed: %v", b)
	}
	b := byAction["certificate_expiring"]
	if b["status"] != "expiring" {
		t.Errorf("expiring: %v", b)
	}
	if got, _ := b["validUntil"].(string); got != until.Format(time.RFC3339) {
		t.Errorf("validUntil = %q, voluto %q", got, until.Format(time.RFC3339))
	}
}

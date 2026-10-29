//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-461 (v3-c): avvisi su domini e certificati nell'outbox. Un evento per passaggio di stato, mai
// ripetuto finché lo stato non cambia; uno stato nuovo (dopo il rientro) riparte.

func domainEventsFixture(t *testing.T) (*store.Store, store.App, store.AppDomain, func(name string) int, time.Time) {
	t.Helper()
	ctx := context.Background()
	pool, _ := dbtest.NewPool(t)
	st := store.New(pool)
	repoID, alice := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'alice/web')`, repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name)
		VALUES ($1, 'user', $2, 'alice', 'web')`, repoID, alice); err != nil {
		t.Fatal(err)
	}
	port := 8080
	now := time.Now().UTC().Truncate(time.Microsecond)
	app, _, err := st.CreateApp(ctx, store.NewApp{
		RepoID: repoID, Owner: "alice", Name: "web", Actor: &store.AppActor{ID: alice, Username: "alice", Type: "human"},
		Image: store.AppImage{Reference: "registry.example/alice/web:1", Source: "registry"},
		Config: store.AppConfig{
			Copies: 1, Port: &port, Variables: map[string]string{},
			Guaranteed: store.AppResourceAmount{CPUMillis: 100, MemoryBytes: 128 << 20},
			Limit:      store.AppResourceAmount{CPUMillis: 500, MemoryBytes: 512 << 20},
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	d, err := st.AddAppDomain(ctx, app.ID, "www.example.org", "www.example.org", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	count := func(name string) int {
		n := 0
		for _, r := range outboxRows(t, pool) {
			if r.Name == name {
				n++
			}
		}
		return n
	}
	return st, app, d, count, now
}

func TestStore_AvvisoDominioNonVerificato_UnaVoltaPerStato(t *testing.T) {
	ctx := context.Background()
	st, _, d, count, now := domainEventsFixture(t)
	const ev = "app_domain.dns_failed"
	reason := "www.example.org non risolve"
	save := func(c store.AppDomainCheck) {
		t.Helper()
		c.CheckedAt = now
		if _, err := st.SaveAppDomainCheck(ctx, d.ID, c); err != nil {
			t.Fatal(err)
		}
	}
	// In attesa del DNS non è un guasto.
	save(store.AppDomainCheck{Status: store.DomainPendingDNS})
	if count(ev) != 0 {
		t.Fatal("pending_dns non avvisa")
	}
	// Verifica fallita: un avviso; lo stesso stato ripetuto non ne scrive altri.
	save(store.AppDomainCheck{Status: store.DomainError, StatusReason: &reason})
	save(store.AppDomainCheck{Status: store.DomainError, StatusReason: &reason})
	if got := count(ev); got != 1 {
		t.Fatalf("errore ripetuto: %d avvisi, voluto 1", got)
	}
	// Rientro e nuovo guasto: stato nuovo, nuovo avviso.
	save(store.AppDomainCheck{Status: store.DomainVerified, VerifiedAt: &now})
	if got := count(ev); got != 1 {
		t.Fatalf("il rientro non avvisa: %d", got)
	}
	save(store.AppDomainCheck{Status: store.DomainError, StatusReason: &reason})
	if got := count(ev); got != 2 {
		t.Fatalf("guasto dopo il rientro: %d avvisi, voluto 2", got)
	}
}

func TestStore_AvvisoDominioNonPuntaPiu_GraziaEPoiErrore(t *testing.T) {
	ctx := context.Background()
	st, _, d, count, now := domainEventsFixture(t)
	const ev = "app_domain.dns_failed"
	if _, err := st.SaveAppDomainCheck(ctx, d.ID, store.AppDomainCheck{Status: store.DomainVerified, VerifiedAt: &now, CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Smette di puntare all'istanza: verified con failing_since (grazia). Un avviso subito.
	if _, err := st.SaveAppDomainCheck(ctx, d.ID, store.AppDomainCheck{Status: store.DomainVerified, VerifiedAt: &now, FailingSince: &now, CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveAppDomainCheck(ctx, d.ID, store.AppDomainCheck{Status: store.DomainVerified, VerifiedAt: &now, FailingSince: &now, CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	if got := count(ev); got != 1 {
		t.Fatalf("grazia: %d avvisi, voluto 1", got)
	}
	// Scaduta la grazia diventa errore: è lo stesso guasto, nessun secondo avviso.
	why := "non risponde più"
	if _, err := st.SaveAppDomainCheck(ctx, d.ID, store.AppDomainCheck{Status: store.DomainError, StatusReason: &why, FailingSince: &now, VerifiedAt: &now, CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	if got := count(ev); got != 1 {
		t.Fatalf("grazia poi errore: %d avvisi, voluto 1", got)
	}
}

func TestStore_AvvisoRinnovoLetsEncryptFallito_UnaVoltaPerStato(t *testing.T) {
	ctx := context.Background()
	st, _, d, count, now := domainEventsFixture(t)
	const ev = "app_certificate.renewal_failed"
	msg := "Traefik non ha ottenuto il certificato"
	save := func(state string, e *string) {
		t.Helper()
		if _, err := st.SaveAppDomainCert(ctx, d.ID, store.AppDomainCert{State: state, Error: e, LastAttempt: now}); err != nil {
			t.Fatal(err)
		}
	}
	save("issuing", nil)
	if count(ev) != 0 {
		t.Fatal("issuing non avvisa")
	}
	save("error", &msg)
	save("error", &msg)
	if got := count(ev); got != 1 {
		t.Fatalf("errore ripetuto: %d avvisi, voluto 1", got)
	}
	save("valid", nil)
	save("error", &msg)
	if got := count(ev); got != 2 {
		t.Fatalf("errore dopo un certificato valido: %d avvisi, voluto 2", got)
	}
}

func TestStore_AvvisoCertificatoDelClienteInScadenza_UnaVoltaPerScadenza(t *testing.T) {
	ctx := context.Background()
	st, _, d, count, now := domainEventsFixture(t)
	const ev = "app_certificate.expiring"
	within := 14 * 24 * time.Hour
	setCustom := func(until time.Time) {
		t.Helper()
		if _, err := st.SetAppDomainCustomCert(ctx, fakeSealer{}, d.ID, store.CustomCertInput{
			ChainPEM: "pem", KeyPEM: "key", ValidFrom: now.Add(-24 * time.Hour), ValidUntil: until, Issuer: "CA",
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	notice := func(at time.Time) int {
		t.Helper()
		got, err := st.NoticeCustomCertsExpiring(ctx, at, within)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	// Scade fra 30 giorni: ancora niente.
	setCustom(now.Add(30 * 24 * time.Hour))
	if notice(now) != 0 || count(ev) != 0 {
		t.Fatal("a 30 giorni non si avvisa")
	}
	// A 10 giorni dalla scadenza: un avviso; i giri successivi non lo ripetono.
	later := now.Add(20 * 24 * time.Hour)
	if notice(later) != 1 || notice(later.Add(time.Hour)) != 0 || notice(later.Add(24*time.Hour)) != 0 {
		t.Fatal("un solo avviso per la stessa scadenza")
	}
	if got := count(ev); got != 1 {
		t.Fatalf("%d avvisi, voluto 1", got)
	}
	// Certificato nuovo (altra scadenza), di nuovo vicino alla scadenza: l'avviso riparte.
	setCustom(later.Add(5 * 24 * time.Hour))
	if notice(later) != 1 || count(ev) != 2 {
		t.Fatalf("certificato nuovo in scadenza: avvisi %d, voluto 2", count(ev))
	}
	// Già scaduto: non è «in scadenza», lo dice lo stato error del certificato.
	if notice(later.Add(30*24*time.Hour)) != 0 {
		t.Fatal("scaduto non avvisa come in scadenza")
	}
}

type fakeSealer struct{}

func (fakeSealer) Seal(id uuid.UUID, plaintext string) ([]byte, []byte, string, error) {
	return []byte("ct"), []byte("nonce"), "k1", nil
}

func (fakeSealer) Open(id uuid.UUID, ct, nonce []byte, keyID string) (string, error) {
	return "key", nil
}

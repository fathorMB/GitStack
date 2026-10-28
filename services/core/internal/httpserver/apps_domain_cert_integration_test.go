//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// GIT-460 (v3-c): certificato del cliente per un dominio proprio.

type certPKI struct {
	root, inter, leaf *x509.Certificate
	interKey, leafKey *ecdsa.PrivateKey
	rootDER, interDER []byte
	leafDER           []byte
	pool              *x509.CertPool
}

func mkTestCert(t *testing.T, cn string, dns []string, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, ca bool, from, until time.Time) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: cn}, DNSNames: dns, NotBefore: from, NotAfter: until,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: ca}
	signer, signKey := tpl, key
	if parent != nil {
		signer, signKey = parent, parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, signer, &key.PublicKey, signKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c, key, der
}

func certPEMOf(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestAppDomainCertificate_SenzaChiaveSegreti(t *testing.T) {
	e := newAppsEnv(t)
	e.id.sysAdmin[users["carol"]] = true
	e.repo("handbook", true)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/web/access", "alice", `{"mode":"public"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps/web/domains", "alice", domainBody("shop.retail.it")), http.StatusCreated, "")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/web/domains/shop.retail.it/certificate", "alice",
		`{"certificate":"certificate","privateKey":"private-key"}`), http.StatusServiceUnavailable, "app_secrets_unavailable")
}

func keyPEMOf(t *testing.T, k *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func newCertPKI(t *testing.T, leafDNS string, leafFrom, leafUntil time.Time) certPKI {
	t.Helper()
	now := time.Now()
	var p certPKI
	var rootKey *ecdsa.PrivateKey
	p.root, rootKey, p.rootDER = mkTestCert(t, "Test Root", nil, nil, nil, true, now.Add(-72*time.Hour), now.Add(72*time.Hour))
	p.inter, p.interKey, p.interDER = mkTestCert(t, "Test Intermediate", nil, p.root, rootKey, true, now.Add(-48*time.Hour), now.Add(48*time.Hour))
	p.leaf, p.leafKey, p.leafDER = mkTestCert(t, "leaf", []string{leafDNS}, p.inter, p.interKey, false, leafFrom, leafUntil)
	p.pool = x509.NewCertPool()
	p.pool.AddCert(p.root)
	return p
}

// certCtl è il controller finto: conta riallineamenti della rotta e dei Secret.
type certCtl struct {
	routeCtl
	mu2   sync.Mutex
	syncs int
}

func (c *certCtl) SyncCustomCerts(context.Context) {
	c.mu2.Lock()
	defer c.mu2.Unlock()
	c.syncs++
}

func TestAppDomainCertificate_RifiutiSostituzioneERitornoALetsEncrypt(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	now := time.Now()
	good := newCertPKI(t, "shop.retail.it", now.Add(-time.Hour), now.Add(24*time.Hour))
	expired := newCertPKI(t, "shop.retail.it", now.Add(-48*time.Hour), now.Add(-time.Hour))
	wrongName := newCertPKI(t, "altro.it", now.Add(-time.Hour), now.Add(24*time.Hour))
	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	dns := newFakeDNS()
	ctl := &certCtl{}
	e := newAppsEnv(t, httpserver.WithAppController(ctl), httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys}),
		httpserver.WithAppDomains(httpserver.AppDomainsConfig{InstanceHost: "git.example.com", Domains: apps.DomainConfig{Resolver: dns}, CertRoots: good.pool}))
	e.id.sysAdmin[users["carol"]] = true
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	base := "/repos/alice/handbook/apps/web/domains"
	certURL := base + "/shop.retail.it/certificate"
	put := func(user, cert, key string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"certificate": cert, "privateKey": key})
		return e.do(http.MethodPut, certURL, user, string(b))
	}
	goodChain := certPEMOf(good.leafDER) + certPEMOf(good.interDER)
	goodKey := keyPEMOf(t, good.leafKey)

	// Non pubblica: 409. Poi pubblica e dominio aggiunto.
	e.want(e.do(http.MethodPut, "/admin/apps/settings", "carol", publicSettings(true)), http.StatusOK, "")
	e.want(e.do(http.MethodPut, "/repos/alice/handbook/apps/web/access", "alice", `{"mode":"public"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, base, "alice", domainBody("shop.retail.it")), http.StatusCreated, "")

	// Permessi: serve admin sull'app.
	e.want(put("bob", goodChain, goodKey), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodDelete, certURL, "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, base+"/nessuno.retail.it/certificate", "alice", `{"certificate":"x","privateKey":"y"}`), http.StatusNotFound, "not_found")

	// Rifiuti: dominio non coperto, scaduto, chiave diversa, catena incompleta. Nulla viene salvato.
	e.want(put("alice", certPEMOf(wrongName.leafDER)+certPEMOf(wrongName.interDER), keyPEMOf(t, wrongName.leafKey)), http.StatusUnprocessableEntity, "certificate_invalid")
	e.want(put("alice", certPEMOf(expired.leafDER)+certPEMOf(expired.interDER), keyPEMOf(t, expired.leafKey)), http.StatusUnprocessableEntity, "certificate_invalid")
	rec := put("alice", goodChain, keyPEMOf(t, otherKey))
	e.want(rec, http.StatusUnprocessableEntity, "certificate_invalid")
	if !strings.Contains(rec.Body.String(), "non corrisponde") {
		t.Fatalf("chiave diversa: %s", rec.Body.String())
	}
	e.want(put("alice", certPEMOf(good.leafDER), goodKey), http.StatusUnprocessableEntity, "certificate_invalid")
	if d := e.domain(e.do(http.MethodGet, base+"/shop.retail.it", "alice", "").Body); d.Certificate.Mode != "letsencrypt" {
		t.Fatalf("i rifiuti non cambiano il modo: %+v", d.Certificate)
	}
	e.want(e.do(http.MethodDelete, certURL, "alice", ""), http.StatusConflict, "not_custom")

	// Caricamento valido: modo custom, valid, scadenza ed emittente del cliente; la chiave non torna.
	rec = put("alice", goodChain, goodKey)
	e.want(rec, http.StatusOK, "")
	d := e.domain(rec.Body)
	if d.Certificate.Mode != "custom" || d.Certificate.State != "valid" || d.Certificate.Issuer == nil || *d.Certificate.Issuer != "Test Intermediate" ||
		d.Certificate.ValidUntil == nil || d.Certificate.Error != nil {
		t.Fatalf("custom: %+v", d.Certificate)
	}
	assertNoKey := func(where, body string) {
		t.Helper()
		for _, needle := range []string{goodKey, "PRIVATE KEY", keyBody64(goodKey)} {
			if strings.Contains(body, needle) {
				t.Fatalf("%s riporta la chiave privata: %q", where, needle)
			}
		}
	}
	assertNoKey("risposta del PUT", rec.Body.String())
	assertNoKey("GET", e.do(http.MethodGet, base+"/shop.retail.it", "alice", "").Body.String())
	assertNoKey("lista", e.do(http.MethodGet, base, "alice", "").Body.String())
	ctl.mu2.Lock()
	n := ctl.syncs
	ctl.mu2.Unlock()
	if n != 1 || ctl.count() == 0 {
		t.Fatalf("il caricamento riallinea Secret (%d) e rotta (%d)", n, ctl.count())
	}

	// A riposo la chiave è cifrata: nessuna colonna ha il PEM in chiaro, ma lo store la decifra per il controller.
	var plain int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_domains WHERE cert_pem LIKE '%PRIVATE KEY%'
		OR encode(cert_key_ciphertext, 'escape') LIKE '%PRIVATE KEY%' OR to_jsonb(app_domains)::text LIKE '%`+keyBody64(goodKey)+`%'`).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("la chiave non deve stare in chiaro nel database: %d %v", plain, err)
	}
	cs, err := store.New(e.pool).CustomCertificates(context.Background(), keys)
	if err != nil || len(cs) != 1 || cs[0].KeyPEM != goodKey || cs[0].Domain != "shop.retail.it" {
		t.Fatalf("il controller legge la chiave decifrata: %v %v", cs, err)
	}

	// Sostituzione: lo stesso dominio accetta un nuovo caricamento valido.
	e.want(put("alice", goodChain, goodKey), http.StatusOK, "")

	// Nessuna traccia della chiave nei log del server e negli eventi di dominio.
	assertNoKey("log", logs.String())
	rows, err := e.pool.Query(context.Background(), `SELECT table_name FROM information_schema.tables WHERE table_schema = 'core' AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	for _, tb := range tables {
		var hits int
		q := `SELECT count(*) FROM core."` + tb + `" x WHERE x::text LIKE '%' || $1 || '%'`
		if err := e.pool.QueryRow(context.Background(), q, keyBody64(goodKey)).Scan(&hits); err != nil || hits != 0 {
			t.Fatalf("la chiave è nella tabella %s: %d %v", tb, hits, err)
		}
	}

	// Ritorno a Let's Encrypt: certificato e chiave cancellati, modo letsencrypt, emissione da rifare.
	rec = e.do(http.MethodDelete, certURL, "alice", "")
	e.want(rec, http.StatusOK, "")
	d = e.domain(rec.Body)
	if d.Certificate.Mode != "letsencrypt" || d.Certificate.State != "none" || d.Certificate.ValidUntil != nil || d.Certificate.Issuer != nil {
		t.Fatalf("letsencrypt: %+v", d.Certificate)
	}
	var left int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_domains WHERE cert_pem IS NOT NULL OR cert_key_ciphertext IS NOT NULL`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("cancellati con il ritorno a letsencrypt: %d %v", left, err)
	}
	e.want(e.do(http.MethodDelete, certURL, "alice", ""), http.StatusConflict, "not_custom")
}

func keyBody64(pemKey string) string { return strings.Split(pemKey, "\n")[1] }

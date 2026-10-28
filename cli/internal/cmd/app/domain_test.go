package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GIT-466 (v3-c): gs app access --mode e gs app domain contro un gateway finto.

const (
	dBase = "/repos/acme/web/apps/sito/domains"
	dPath = dBase + "/shop.retail.it"
	aPath = "/repos/acme/web/apps/sito/access"

	domPending = `{"id":"00000000-0000-0000-0000-0000000000d1","domain":"shop.retail.it","status":"pending_dns",` +
		`"dns":{"type":"CNAME","name":"shop.retail.it","value":"sito-web-acme.apps.example.org","alternatives":[{"type":"A","value":"203.0.113.7"}]},` +
		`"certificate":{"mode":"letsencrypt","state":"none"},"createdAt":"2026-10-28T10:00:00Z"}`
	domVerified = `{"id":"00000000-0000-0000-0000-0000000000d1","domain":"shop.retail.it","status":"verified","lastCheckedAt":"2026-10-28T10:05:00Z",` +
		`"dns":{"type":"CNAME","name":"shop.retail.it","value":"sito-web-acme.apps.example.org","alternatives":[]},` +
		`"certificate":{"mode":"letsencrypt","state":"issuing"},"createdAt":"2026-10-28T10:00:00Z"}`
	domError = `{"id":"00000000-0000-0000-0000-0000000000d1","domain":"shop.retail.it","status":"error","statusReason":"il DNS risponde 198.51.100.9",` +
		`"dns":{"type":"A","name":"shop.retail.it","value":"203.0.113.7","alternatives":[]},` +
		`"certificate":{"mode":"letsencrypt","state":"none"},"createdAt":"2026-10-28T10:00:00Z"}`
	domCustom = `{"id":"00000000-0000-0000-0000-0000000000d1","domain":"shop.retail.it","status":"verified",` +
		`"dns":{"type":"A","name":"shop.retail.it","value":"203.0.113.7","alternatives":[]},` +
		`"certificate":{"mode":"custom","state":"valid","validUntil":"2027-05-01T00:00:00Z","issuer":"ACME CA"},"createdAt":"2026-10-28T10:00:00Z"}`
	accPublic = `{"mode":"public","publicAppsEnabled":true,"address":"https://sito-web-acme.apps.example.org"}`
)

func TestAccessModePublicChiedeConfermaEImpostaLAccesso(t *testing.T) {
	fk := newFake(t, map[string]reply{"PUT " + aPath: {200, accPublic}})

	// Senza terminale e senza --yes: exit 2, nessuna chiamata di scrittura.
	if r := run(t, fk, "app", "access", "sito", "--mode", "public"); r.code != 2 || !strings.Contains(r.errOut, "--yes") {
		t.Fatalf("senza --yes: %+v", r)
	}
	if n := len(fk.find("PUT", aPath)); n != 0 {
		t.Fatalf("PUT senza conferma: %d", n)
	}
	// Con il terminale, un nome sbagliato annulla.
	if r := runWith(t, fk, true, "altro\n", "app", "access", "sito", "--mode", "public"); r.code == 0 {
		t.Fatalf("nome sbagliato accettato: %+v", r)
	}
	if n := len(fk.find("PUT", aPath)); n != 0 {
		t.Fatalf("PUT dopo conferma sbagliata: %d", n)
	}
	// Riscrivendo il nome parte la chiamata.
	r := runWith(t, fk, true, "sito\n", "app", "access", "sito", "--mode", "public")
	if r.code != 0 || !strings.Contains(r.out, "accesso di sito: public") || !strings.Contains(r.out, "https://sito-web-acme.apps.example.org") {
		t.Fatalf("conferma: %+v", r)
	}
	// --yes e --json.
	r = run(t, fk, "app", "access", "sito", "--mode", "public", "--yes", "--json", "mode,address")
	if r.code != 0 {
		t.Fatalf("--yes --json: %+v", r)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || got["mode"] != "public" || got["address"] == nil {
		t.Fatalf("json: %q %v", r.out, err)
	}
	calls := fk.find("PUT", aPath)
	if len(calls) != 2 || calls[0].Body["mode"] != "public" {
		t.Fatalf("PUT: %+v", calls)
	}
}

func TestAccessModeLoginNonChiedeConfermaEValida(t *testing.T) {
	fk := newFake(t, map[string]reply{"PUT " + aPath: {200, `{"mode":"login","publicAppsEnabled":true}`}})
	if r := run(t, fk, "app", "access", "sito", "--mode", "login"); r.code != 0 || !strings.Contains(r.out, "accesso di sito: login") {
		t.Fatalf("login: %+v", r)
	}
	for _, args := range [][]string{{"app", "access", "sito", "--mode", "tutti"}, {"app", "access", "--mode", "public"}} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2", args, r.code)
		}
	}
}

func TestAccessModePublicErroreLeggibile(t *testing.T) {
	fk := newFake(t, map[string]reply{"PUT " + aPath: {422, `{"error":{"code":"public_apps_disabled","message":"Le app pubbliche non sono abilitate su questa istanza."}}`}})
	r := run(t, fk, "app", "access", "sito", "--mode", "public", "--yes")
	if r.code == 0 || !strings.Contains(r.errOut, "app pubbliche non sono abilitate") {
		t.Fatalf("%+v", r)
	}
}

func TestDomainList(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET " + dBase: {200, `{"items":[` + domPending + `,` + domCustom + `]}`}})
	r := run(t, fk, "app", "domain", "list", "sito")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 2 {
		t.Fatalf("righe: %q", r.out)
	}
	if f := strings.Split(lines[0], "\t"); len(f) != 4 || f[0] != "shop.retail.it" || f[1] != "pending_dns" ||
		!strings.Contains(f[2], "CNAME shop.retail.it -> sito-web-acme.apps.example.org") || !strings.Contains(f[2], "oppure A 203.0.113.7") || f[3] != "letsencrypt/none" {
		t.Errorf("riga 1: %q", lines[0])
	}
	if f := strings.Split(lines[1], "\t"); len(f) != 4 || f[3] != "custom/valid fino al 2027-05-01" {
		t.Errorf("riga 2: %q", lines[1])
	}
	r = run(t, fk, "app", "domain", "ls", "sito", "--json", "domain,status")
	var items []map[string]any
	if err := json.Unmarshal([]byte(r.out), &items); err != nil || len(items) != 2 || items[0]["domain"] != "shop.retail.it" || items[1]["status"] != "verified" {
		t.Fatalf("json: %q %v", r.out, err)
	}
	if _, ok := items[0]["dns"]; ok {
		t.Errorf("--json domain,status ha anche dns")
	}
	if r = run(t, fk, "app", "domain", "list"); r.code != 2 {
		t.Errorf("senza app: %+v", r)
	}
}

func TestDomainAdd(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST " + dBase: {201, domPending}})
	r := run(t, fk, "app", "domain", "add", "sito", " Shop.Retail.IT ")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"aggiunto shop.retail.it", "pending_dns", "CNAME shop.retail.it -> sito-web-acme.apps.example.org", "letsencrypt/none"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("manca %q in %q", want, r.out)
		}
	}
	if c := fk.find("POST", dBase); len(c) != 1 || c[0].Body["domain"] != "shop.retail.it" {
		t.Fatalf("corpo: %+v", c)
	}
	r = run(t, fk, "app", "domain", "add", "sito", "shop.retail.it", "--json", "domain,status,certificate")
	var d map[string]any
	if err := json.Unmarshal([]byte(r.out), &d); err != nil || d["status"] != "pending_dns" {
		t.Fatalf("json: %q %v", r.out, err)
	}
	if cert, _ := d["certificate"].(map[string]any); cert["mode"] != "letsencrypt" || cert["state"] != "none" {
		t.Errorf("certificate: %v", d["certificate"])
	}
	if r = run(t, fk, "app", "domain", "add", "sito"); r.code != 2 {
		t.Errorf("senza dominio: %+v", r)
	}
}

func TestDomainAddErroriLeggibili(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"409 domain_taken":     {409, `{"error":{"code":"domain_taken","message":"Il dominio è già di un'altra app."}}`, "già di un'altra app"},
		"409 app_not_public":   {409, `{"error":{"code":"app_not_public","message":"L'app non è pubblica."}}`, "non è pubblica"},
		"422 domain_reserved":  {422, `{"error":{"code":"domain_reserved","message":"Dominio riservato a GitStack."}}`, "riservato"},
		"422 invalid_domain":   {422, `{"error":{"code":"invalid_domain","message":"Nome DNS non valido."}}`, "non valido"},
		"501 non implementato": {501, `{"error":{"code":"not_implemented","message":"I domini propri non sono ancora disponibili."}}`, "non sono ancora disponibili"},
	} {
		t.Run(name, func(t *testing.T) {
			fk := newFake(t, map[string]reply{"POST " + dBase: {tc.status, tc.body}})
			r := run(t, fk, "app", "domain", "add", "sito", "shop.retail.it")
			if r.code == 0 || !strings.Contains(r.errOut, tc.want) || r.out != "" {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestDomainVerify(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"verified", domVerified, "stato:       verified"},
		{"pending", domPending, "stato:       pending_dns"},
		{"error", domError, "error (il DNS risponde 198.51.100.9)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fk := newFake(t, map[string]reply{"POST " + dPath + "/recheck": {200, tc.body}})
			r := run(t, fk, "app", "domain", "verify", "sito", "shop.retail.it")
			// Un esito negativo non è un errore: exit 0, lo stato è nell'output.
			if r.code != 0 || !strings.Contains(r.out, tc.want) || !strings.Contains(r.out, "ricontrollato shop.retail.it") {
				t.Fatalf("%+v", r)
			}
			r = run(t, fk, "app", "domain", "verify", "sito", "shop.retail.it", "--json", "status,statusReason")
			var d map[string]any
			if err := json.Unmarshal([]byte(r.out), &d); err != nil || d["status"] == nil {
				t.Fatalf("json: %q %v", r.out, err)
			}
		})
	}
	fk := newFake(t, map[string]reply{"POST " + dPath + "/recheck": {404, `{"error":{"code":"not_found","message":"Dominio non trovato."}}`}})
	if r := run(t, fk, "app", "domain", "verify", "sito", "shop.retail.it"); r.code != 6 || !strings.Contains(r.errOut, "Dominio non trovato") {
		t.Errorf("404: %+v", r)
	}
}

func TestDomainRemoveChiedeConferma(t *testing.T) {
	fk := newFake(t, map[string]reply{"DELETE " + dPath: {204, ``}})
	if r := run(t, fk, "app", "domain", "rm", "sito", "shop.retail.it"); r.code != 2 || !strings.Contains(r.errOut, "--yes") {
		t.Fatalf("senza --yes: %+v", r)
	}
	if r := runWith(t, fk, true, "no\n", "app", "domain", "rm", "sito", "shop.retail.it"); r.code == 0 {
		t.Fatalf("conferma sbagliata accettata: %+v", r)
	}
	if n := len(fk.find("DELETE", dPath)); n != 0 {
		t.Fatalf("DELETE senza conferma: %d", n)
	}
	if r := runWith(t, fk, true, "shop.retail.it\n", "app", "domain", "rm", "sito", "shop.retail.it"); r.code != 0 || !strings.Contains(r.errOut, "Tolto il dominio shop.retail.it") {
		t.Fatalf("conferma: %+v", r)
	}
	if r := run(t, fk, "app", "domain", "remove", "sito", "shop.retail.it", "--yes"); r.code != 0 {
		t.Fatalf("--yes: %+v", r)
	}
	if n := len(fk.find("DELETE", dPath)); n != 2 {
		t.Fatalf("DELETE: %d", n)
	}
	fk = newFake(t, map[string]reply{"DELETE " + dPath: {409, `{"error":{"code":"archived","message":"Il repo è archiviato."}}`}})
	if r := run(t, fk, "app", "domain", "rm", "sito", "shop.retail.it", "--yes"); r.code == 0 || !strings.Contains(r.errOut, "archiviato") {
		t.Errorf("409: %+v", r)
	}
}

func TestDomainCertUpload(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	if err := os.WriteFile(cert, []byte("-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("-----BEGIN PRIVATE KEY-----\nSEGRETO\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fk := newFake(t, map[string]reply{"PUT " + dPath + "/certificate": {200, domCustom}})
	r := run(t, fk, "app", "domain", "cert", "upload", "sito", "shop.retail.it", "--cert", cert, "--key", key)
	if r.code != 0 || !strings.Contains(r.out, "custom/valid fino al 2027-05-01") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.out+r.errOut, "SEGRETO") {
		t.Fatalf("la chiave è stata stampata: %+v", r)
	}
	c := fk.find("PUT", dPath+"/certificate")
	if len(c) != 1 || !strings.Contains(c[0].Body["certificate"].(string), "BEGIN CERTIFICATE") || !strings.Contains(c[0].Body["privateKey"].(string), "SEGRETO") {
		t.Fatalf("corpo: %+v", c)
	}
	r = run(t, fk, "app", "domain", "cert", "upload", "sito", "shop.retail.it", "--cert", cert, "--key", key, "--json", "domain,certificate")
	var d map[string]any
	if err := json.Unmarshal([]byte(r.out), &d); err != nil || d["certificate"].(map[string]any)["mode"] != "custom" || strings.Contains(r.out, "SEGRETO") {
		t.Fatalf("json: %q %v", r.out, err)
	}
	// Chiave da stdin.
	if r = runWith(t, fk, false, "-----BEGIN PRIVATE KEY-----\nDA-STDIN\n-----END PRIVATE KEY-----\n", "app", "domain", "cert", "upload", "sito", "shop.retail.it", "--cert", cert, "--key", "-"); r.code != 0 {
		t.Fatalf("stdin: %+v", r)
	}
	// Usi errati: exit 2 senza chiamate.
	before := len(fk.find("PUT", dPath+"/certificate"))
	for _, args := range [][]string{
		{"app", "domain", "cert", "upload", "sito", "shop.retail.it", "--cert", cert},
		{"app", "domain", "cert", "upload", "sito", "shop.retail.it", "--key", key},
		{"app", "domain", "cert", "upload", "sito", "shop.retail.it", "--cert", "-", "--key", "-"},
	} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2", args, r.code)
		}
	}
	if r := run(t, fk, "app", "domain", "cert", "upload", "sito", "shop.retail.it", "--cert", filepath.Join(dir, "manca.pem"), "--key", key); r.code == 0 {
		t.Errorf("file mancante accettato")
	}
	if n := len(fk.find("PUT", dPath+"/certificate")); n != before {
		t.Errorf("chiamate dopo usi errati: %d", n-before)
	}
}

func TestDomainCertUploadErrore422(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.pem")
	_ = os.WriteFile(f, []byte("PEM"), 0o600)
	fk := newFake(t, map[string]reply{"PUT " + dPath + "/certificate": {422, `{"error":{"code":"certificate_invalid","message":"La chiave privata non corrisponde al certificato."}}`}})
	r := run(t, fk, "app", "domain", "cert", "upload", "sito", "shop.retail.it", "--cert", f, "--key", f)
	if r.code == 0 || !strings.Contains(r.errOut, "non corrisponde al certificato") {
		t.Fatalf("%+v", r)
	}
}

func TestDomainCertRemove(t *testing.T) {
	fk := newFake(t, map[string]reply{"DELETE " + dPath + "/certificate": {200, domVerified}})
	if r := run(t, fk, "app", "domain", "cert", "rm", "sito", "shop.retail.it"); r.code != 2 {
		t.Fatalf("senza --yes: %+v", r)
	}
	r := run(t, fk, "app", "domain", "cert", "rm", "sito", "shop.retail.it", "--yes")
	if r.code != 0 || !strings.Contains(r.out, "letsencrypt/issuing") {
		t.Fatalf("%+v", r)
	}
	r = run(t, fk, "app", "domain", "cert", "remove", "sito", "shop.retail.it", "--yes", "--json", "certificate")
	var d map[string]any
	if err := json.Unmarshal([]byte(r.out), &d); err != nil || d["certificate"].(map[string]any)["mode"] != "letsencrypt" {
		t.Fatalf("json: %q %v", r.out, err)
	}
	fk = newFake(t, map[string]reply{"DELETE " + dPath + "/certificate": {409, `{"error":{"code":"not_custom","message":"Il dominio usa già Let's Encrypt."}}`}})
	if r := run(t, fk, "app", "domain", "cert", "rm", "sito", "shop.retail.it", "--yes"); r.code == 0 || !strings.Contains(r.errOut, "usa già Let's Encrypt") {
		t.Errorf("409: %+v", r)
	}
}

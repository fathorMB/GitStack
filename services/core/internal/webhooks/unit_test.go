package webhooks

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// L'esempio di docs/webhooks.md: segreto e corpo noti, firma nota.
func TestSign_EsempioDellaDocumentazione(t *testing.T) {
	got := Sign("s3cr3t-di-prova", []byte(`{"version":1,"event":"issues","action":"closed"}`))
	want := "sha256=445f7173dfa8f83e67455aa9b74776b5eee2fc18442148d3de09fafad8bfce56"
	if got != want {
		t.Fatalf("firma = %s, voluta %s", got, want)
	}
}

// C7: 8 tentativi, attesa crescente, 23 h 36 min dopo il primo.
func TestRetryDelay_AttesaCrescenteInCirca24Ore(t *testing.T) {
	var total, prev time.Duration
	n := 0
	for i := 1; ; i++ {
		d, ok := RetryDelay(i)
		if !ok {
			n = i
			break
		}
		if d <= prev {
			t.Fatalf("l'attesa dopo il tentativo %d (%v) non cresce rispetto a %v", i, d, prev)
		}
		prev = d
		total += d
	}
	if n != MaxAttempts {
		t.Fatalf("l'ultimo tentativo è il %d, voluto %d", n, MaxAttempts)
	}
	if total != 23*time.Hour+36*time.Minute {
		t.Fatalf("durata totale %v, voluta 23h36m", total)
	}
	if _, ok := RetryDelay(0); ok {
		t.Fatal("RetryDelay(0) non ha senso")
	}
}

func TestKeyring_CifraDecifraERotazione(t *testing.T) {
	k1 := strings.Repeat("11", 32)
	k2 := strings.Repeat("22", 32)
	old, err := NewKeyring("k1", k1, "")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	ct, nonce, keyID, err := old.Seal(id, "segreto")
	if err != nil || keyID != "k1" {
		t.Fatalf("Seal: %v %q", err, keyID)
	}
	if strings.Contains(string(ct), "segreto") {
		t.Fatal("il segreto è in chiaro")
	}
	// Dopo la rotazione la chiave nuova è la primaria e la vecchia serve a decifrare.
	rot, err := NewKeyring("k2", k2, "k1:"+k1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rot.Open(id, ct, nonce, keyID)
	if err != nil || got != "segreto" {
		t.Fatalf("Open dopo la rotazione: %q %v", got, err)
	}
	_, _, nk, _ := rot.Seal(id, "x")
	if nk != "k2" {
		t.Fatalf("cifra con %q, voluta la chiave primaria k2", nk)
	}
	// Senza la chiave vecchia la riga non si legge.
	if _, err := mustKeyring(t, "k2", k2).Open(id, ct, nonce, "k1"); err == nil {
		t.Fatal("una chiave sconosciuta deve fallire")
	}
	// Il segreto è legato al webhook (dato autenticato).
	if _, err := old.Open(uuid.New(), ct, nonce, "k1"); err == nil {
		t.Fatal("un segreto copiato in un'altra riga non deve decifrarsi")
	}
	// Chiavi malformate.
	if _, err := NewKeyring("k1", "corta", ""); err == nil {
		t.Fatal("chiave corta accettata")
	}
	var nilKeys *Keyring
	if _, _, _, err := nilKeys.Seal(id, "x"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("senza chiave: %v", err)
	}
	if k, err := NewKeyring("k1", "", ""); k != nil || err != nil {
		t.Fatalf("chiave vuota: %v %v", k, err)
	}
	// Anche base64.
	if _, err := NewKeyring("k1", "ERERERERERERERERERERERERERERERERERERERERERE=", ""); err != nil {
		t.Fatalf("base64: %v", err)
	}
}

func mustKeyring(t *testing.T, id, key string) *Keyring {
	t.Helper()
	k, err := NewKeyring(id, key, "")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestClipUTF8(t *testing.T) {
	long := strings.Repeat("è", 3000) // 2 byte per carattere
	got := clipUTF8(long, 4096)
	if len(got) > 4096 || !utf8.ValidString(got) {
		t.Fatalf("len %d valido=%v", len(got), utf8.ValidString(got))
	}
	if got := clipUTF8("a\x00b\xffc", 100); got != "ab�c" {
		t.Fatalf("NUL e byte non validi: %q", got)
	}
	if got := clipUTF8(strings.Repeat("\xff", 5000), 4096); len(got) > 4096 {
		t.Fatalf("i byte non validi gonfiano il testo: %d", len(got))
	}
}

func TestCheckURL(t *testing.T) {
	check := CheckURL(nil)
	for _, bad := range []string{"ftp://x.test/a", "http:///a", "javascript:alert(1)", "http://localhost/a", "http://app.localhost:8080/", "http://user:pw@x.test/"} {
		if err := check(bad); !errors.Is(err, ErrURLNotAllowed) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	for _, ok := range []string{"https://hooks.example.com/in", "http://10.1.2.3:8080/x", "http://ci.internal/hook"} {
		if err := check(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	_, chk, err := NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"http://127.0.0.1/x", "http://[::1]/x", "http://169.254.169.254/latest", "http://10.43.0.5:8080/", "http://10.42.1.1/"} {
		if err := chk(bad); !errors.Is(err, ErrURLNotAllowed) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	if err := chk("http://192.168.1.10/x"); err != nil {
		t.Errorf("la rete aziendale è ammessa: %v", err)
	}
}

// docs/webhooks.md: al massimo 3 redirect; il quarto non si segue.
func TestNewEgress_TreRedirectAlMassimo(t *testing.T) {
	d, _, err := NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := d.(*http.Client)
	if !ok {
		t.Fatalf("il Doer è %T, atteso il client di pkg/egress", d)
	}
	if MaxRedirects != 3 {
		t.Fatalf("MaxRedirects = %d", MaxRedirects)
	}
	req, _ := http.NewRequest(http.MethodPost, "https://hooks.example.com/b", nil)
	via := func(n int) []*http.Request {
		out := make([]*http.Request, n)
		for i := range out {
			out[i] = req
		}
		return out
	}
	if err := c.CheckRedirect(req, via(3)); err != nil {
		t.Fatalf("il terzo redirect si segue: %v", err)
	}
	if err := c.CheckRedirect(req, via(4)); err == nil {
		t.Fatal("il quarto redirect non deve seguirsi")
	}
}

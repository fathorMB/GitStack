package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, p, c string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
		t.Fatal(err)
	}
}

// tlsBackup fa un backup di un'installazione con CA "CA-ORIGINALE" e tls.conf
// della vecchia macchina, e ritorna l'archivio e un'installazione nuova con
// un'altra CA e un altro tls.conf.
func tlsBackup(t *testing.T) (string, *env) {
	t.Helper()
	e := newEnv(t)
	writeFile(t, filepath.Join(e.confDir, "tls", "ca.crt"), "CA-ORIGINALE")
	writeFile(t, filepath.Join(e.confDir, "tls", "ca.key"), "KEY-ORIGINALE")
	writeFile(t, filepath.Join(e.confDir, "tls", "tls.conf"), "TLS_IPS=10.0.0.1\n")
	writeFile(t, filepath.Join(e.confDir, "tls", "install.conf"), "HOSTS=vecchio\n")
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	r := newEnv(t)
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	writeFile(t, filepath.Join(r.confDir, "tls", "ca.crt"), "CA-NUOVA")
	writeFile(t, filepath.Join(r.confDir, "tls", "tls.conf"), "TLS_IPS=10.0.0.2\n")
	writeFile(t, filepath.Join(r.confDir, "tls", "install.conf"), "HOSTS=nuovo\n")
	return res.Path, r
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRestorePublishesTLSAfterConfigAndRestartsWeb(t *testing.T) {
	archive, r := tlsBackup(t)
	calls := 0
	r.o.PublishTLS = func(context.Context) error {
		calls++
		r.f.note("publish-tls")
		// A questo punto la CA del backup è già su disco, il tls.conf della
		// nuova installazione no: ensure rivedrà i SAN dell'host di oggi.
		if got := read(t, filepath.Join(r.confDir, "tls", "ca.crt")); got != "CA-ORIGINALE" {
			t.Errorf("ca.crt al momento di ensure = %q", got)
		}
		if got := read(t, filepath.Join(r.confDir, "tls", "ca.key")); got != "KEY-ORIGINALE" {
			t.Errorf("ca.key al momento di ensure = %q", got)
		}
		return nil
	}
	if err := Restore(context.Background(), r.o, archive); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("PublishTLS chiamato %d volte", calls)
	}
	if got := read(t, filepath.Join(r.confDir, "tls", "tls.conf")); got != "TLS_IPS=10.0.0.2\n" {
		t.Errorf("tls.conf sovrascritto: %q", got)
	}
	if got := read(t, filepath.Join(r.confDir, "tls", "install.conf")); got != "HOSTS=nuovo\n" {
		t.Errorf("install.conf sovrascritto: %q", got)
	}
	idx := func(s string) int {
		for i, c := range r.f.calls {
			if c == s {
				return i
			}
		}
		return -1
	}
	pub, back, rst := idx("publish-tls"), idx("scale gs-gateway 2"), idx("restart gs-web")
	if pub < 0 || back < 0 || rst < 0 {
		t.Fatalf("chiamate: %v", r.f.calls)
	}
	// ensure prima di riaccendere i servizi, restart del web dopo ensure e
	// seguito dall'attesa del suo ready.
	if pub >= back || pub >= rst {
		t.Errorf("ordine sbagliato: %v", r.f.calls)
	}
	if r.f.calls[len(r.f.calls)-1] != "ready gs-web" || rst != len(r.f.calls)-2 {
		t.Errorf("dopo il restart del web serve l'attesa del ready: %v", r.f.calls)
	}
}

func TestRestoreWithoutPublishTLSLeavesWebAlone(t *testing.T) {
	archive, r := tlsBackup(t) // PublishTLS nil: insecure o letsencrypt
	if err := Restore(context.Background(), r.o, archive); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.f.calls {
		if strings.Contains(c, "web") {
			t.Errorf("nessuna chiamata a web attesa: %v", r.f.calls)
		}
	}
}

func TestRestoreTLSFailureStillRestartsServices(t *testing.T) {
	archive, r := tlsBackup(t)
	r.o.PublishTLS = func(context.Context) error { return errors.New("ensure: boom") }
	err := Restore(context.Background(), r.o, archive)
	if err == nil || !strings.Contains(err.Error(), "sudo gitstack-tls ensure") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("errore atteso con il comando da rilanciare, ottenuto %v", err)
	}
	var ref *RefusedError
	if errors.As(err, &ref) {
		t.Error("non è un rifiuto: deve dare un exit code di errore generico")
	}
	for _, d := range []string{"gs-gateway", "gs-git", "gs-core", "gs-identity"} {
		if r.f.replicas[d] == 0 {
			t.Errorf("%s è rimasto a 0", d)
		}
	}
	for _, c := range r.f.calls {
		if strings.HasPrefix(c, "restart") {
			t.Errorf("senza certificato pubblicato il web non va riavviato: %v", r.f.calls)
		}
	}
}

func TestRestoreWebRestartFailure(t *testing.T) {
	archive, r := tlsBackup(t)
	r.o.PublishTLS = func(context.Context) error { return nil }
	r.f.failRestart = true
	if err := Restore(context.Background(), r.o, archive); err == nil || !strings.Contains(err.Error(), "web") {
		t.Fatalf("atteso errore sul riavvio del web, ottenuto %v", err)
	}
}

func TestRestoreSkipsWebWhenAbsent(t *testing.T) {
	archive, r := tlsBackup(t)
	delete(r.f.replicas, "gs-web")
	r.o.PublishTLS = func(context.Context) error { return nil }
	if err := Restore(context.Background(), r.o, archive); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.f.calls {
		if strings.HasPrefix(c, "restart") {
			t.Errorf("web assente: nessun restart atteso: %v", r.f.calls)
		}
	}
}

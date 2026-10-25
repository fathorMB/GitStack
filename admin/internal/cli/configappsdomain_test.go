package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSetAppsDomainInternal(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	r := &recRunner{getentOut: "192.168.1.20   gitstack-dns-check.apps.azienda.it\n"}
	a, out, errb := hostApp(r, tlsDir)

	if code := a.Run(context.Background(), []string{"config", "set", "apps-domain", "Apps.Azienda.IT", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, errb)
	}
	tls := r.find("gitstack-tls")
	if tls == nil || strings.Join(tls.args, " ") != "ensure --apps-domain apps.azienda.it" {
		t.Fatalf("gitstack-tls: %+v", tls)
	}
	if r.find("helm") != nil {
		t.Error("helm non serve per il dominio delle app")
	}
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), "apps_domain: apps.azienda.it\n") || !strings.Contains(string(b), "# commento") {
		t.Errorf("config: %s", b)
	}
	ic, _ := os.ReadFile(filepath.Join(tlsDir, "install.conf"))
	if !strings.Contains(string(ic), "APPS_DOMAIN=apps.azienda.it") || !strings.Contains(string(ic), "HOSTS=192.168.1.20") {
		t.Errorf("install.conf: %s", ic)
	}
	if !strings.Contains(out.String(), "<app>-<repo>-<owner>.apps.azienda.it") {
		t.Errorf("manca lo schema dell'indirizzo: %s", out)
	}
	if strings.Contains(errb.String(), "ATTENZIONE") {
		t.Errorf("avvisi inattesi: %s", errb)
	}

	// Rilanciarlo con lo stesso dominio non rifà niente.
	r2 := &recRunner{}
	a2, out2, _ := hostApp(r2, tlsDir)
	if code := a2.Run(context.Background(), []string{"config", "set", "apps-domain", "apps.azienda.it", "--config", cfg}); code != ExitOK || len(r2.calls) != 0 {
		t.Errorf("exit %d, chiamate %v, out %s", code, r2.calls, out2)
	}

	// Cambiarlo di nuovo riscrive la riga, non la duplica.
	if code := a.Run(context.Background(), []string{"config", "set", "apps-domain", "apps2.azienda.it", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	b, _ = os.ReadFile(cfg)
	if strings.Count(string(b), "apps_domain:") != 1 || !strings.Contains(string(b), "apps_domain: apps2.azienda.it") {
		t.Errorf("config: %s", b)
	}
	ic, _ = os.ReadFile(filepath.Join(tlsDir, "install.conf"))
	if strings.Count(string(ic), "APPS_DOMAIN=") != 1 {
		t.Errorf("install.conf: %s", ic)
	}
}

func TestConfigSetAppsDomainWarnsWithoutDNS(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	r := &recRunner{getentOut: ""}
	a, _, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "apps-domain", "apps.azienda.it", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.Contains(errb.String(), "*.apps.azienda.it") || !strings.Contains(errb.String(), "non è ancora a posto") {
		t.Errorf("manca l'avviso sul DNS wildcard: %s", errb)
	}
}

func TestConfigSetAppsDomainRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"senza dominio", []string{}, "serve un solo dominio"},
		{"una sola etichetta", []string{"apps"}, "almeno due etichette"},
		{"IP", []string{"10.0.0.5"}, "non un indirizzo IP"},
		{"trattino", []string{"apps-.azienda.it"}, "non inizia né finisce"},
		{"carattere", []string{"app_s.azienda.it"}, "solo lettere minuscole"},
		{"uguale all'host", []string{"homehub.local"}, "coincide con l'host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, tlsDir := hostFixture(t, "internal")
			b, _ := os.ReadFile(cfg)
			_ = os.WriteFile(cfg, []byte(strings.Replace(string(b), "host: 192.168.1.20", "host: homehub.local", 1)), 0o600)
			r := &recRunner{}
			a, _, errb := hostApp(r, tlsDir)
			args := append([]string{"config", "set", "apps-domain"}, tc.args...)
			if code := a.Run(context.Background(), append(args, "--config", cfg)); code == ExitOK {
				t.Fatal("doveva fallire")
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Errorf("stderr senza %q: %s", tc.want, errb)
			}
			if len(r.calls) != 0 {
				t.Errorf("nessun comando doveva partire: %v", r.calls)
			}
			after, _ := os.ReadFile(cfg)
			if strings.Contains(string(after), "apps_domain") {
				t.Errorf("config modificato: %s", after)
			}
		})
	}
}

func TestConfigSetAppsDomainLetsEncryptNoCertificate(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "letsencrypt")
	r := &recRunner{getentOut: "192.168.1.20 x\n"}
	a, _, errb := hostApp(r, tlsDir)
	if code := a.Run(context.Background(), []string{"config", "set", "apps-domain", "apps.azienda.it", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if r.find("gitstack-tls") != nil {
		t.Error("con letsencrypt non si emette il certificato interno")
	}
	if !strings.Contains(errb.String(), "non viene emesso") {
		t.Errorf("manca l'avviso: %s", errb)
	}
}

// Un'installazione v2 non ha apps_domain: il config si legge e lo status lo dice.
func TestConfigWithoutAppsDomainStillLoads(t *testing.T) {
	cfg, tlsDir := hostFixture(t, "internal")
	r := &recRunner{}
	a, out, errb := hostApp(r, tlsDir)
	c, code := a.loadConfig(cfg)
	if c == nil {
		t.Fatalf("loadConfig: %d %s", code, errb)
	}
	if c.AppsDomain != "" {
		t.Errorf("AppsDomain = %q", c.AppsDomain)
	}
	_ = out
}

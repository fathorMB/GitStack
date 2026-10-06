package config

import (
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/mailer"
)

const testDB = "postgres://core:secret@localhost:5432/gitstack?sslmode=disable"

func TestLoad_SMTPSpentoDiDefault(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{envDatabaseURL: testDB}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTP.Enabled() {
		t.Fatalf("senza host l'SMTP è spento: %+v", cfg.SMTP)
	}
	// Le altre variabili SMTP si ignorano, senza errori, se manca l'host.
	cfg, err = load(lookupFrom(map[string]string{envDatabaseURL: testDB, envSMTPPort: "boh", envSMTPFrom: "x"}))
	if err != nil || cfg.SMTP.Enabled() {
		t.Fatalf("senza host nessun errore e nessun SMTP: %v %+v", err, cfg.SMTP)
	}
}

func TestLoad_SMTPCompleto(t *testing.T) {
	cfg, err := load(lookupFrom(map[string]string{
		envDatabaseURL: testDB, envSMTPHost: " smtp.example.com ", envSMTPPort: "2525", envSMTPSecurity: "TLS",
		envSMTPUser: "robot", envSMTPPassword: " pa ss ", envSMTPFrom: "GitStack <noreply@example.com>",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := mailer.Config{Host: "smtp.example.com", Port: 2525, Security: mailer.SecurityTLS, Username: "robot", Password: " pa ss ", From: "GitStack <noreply@example.com>"}
	if cfg.SMTP != want {
		t.Fatalf("SMTP = %+v, voluto %+v", cfg.SMTP, want)
	}
}

func TestLoad_SMTPDefaultDiPortaESicurezza(t *testing.T) {
	for sec, port := range map[string]int{"": 587, "starttls": 587, "tls": 465, "none": 25} {
		cfg, err := load(lookupFrom(map[string]string{envDatabaseURL: testDB, envSMTPHost: "h", envSMTPFrom: "a@b.it", envSMTPSecurity: sec}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SMTP.Port != port {
			t.Errorf("security %q: porta %d, voluta %d", sec, cfg.SMTP.Port, port)
		}
		if sec == "" && cfg.SMTP.Security != mailer.SecurityStartTLS {
			t.Errorf("default = %q, voluto starttls", cfg.SMTP.Security)
		}
	}
}

func TestLoad_SMTPNonValido(t *testing.T) {
	cases := map[string]map[string]string{
		"senza mittente":      {envSMTPHost: "h"},
		"mittente sbagliato":  {envSMTPHost: "h", envSMTPFrom: "non un indirizzo"},
		"sicurezza ignota":    {envSMTPHost: "h", envSMTPFrom: "a@b.it", envSMTPSecurity: "ssl"},
		"porta fuori range":   {envSMTPHost: "h", envSMTPFrom: "a@b.it", envSMTPPort: "70000"},
		"utente senza parola": {envSMTPHost: "h", envSMTPFrom: "a@b.it", envSMTPUser: "u"},
	}
	for name, env := range cases {
		env[envDatabaseURL] = testDB
		if _, err := load(lookupFrom(env)); err == nil || !strings.Contains(err.Error(), "GITSTACK_CORE_SMTP_") {
			t.Errorf("%s: atteso errore con il nome della variabile, ottenuto %v", name, err)
		}
	}
}

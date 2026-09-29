package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/config"
)

// TestNoDSNInLog verifica che, al fallimento della connessione Postgres,
// la password nel DSN non compaia mai nei log del servizio.
// main.go usa pgxpool.ParseConfig per estrarre host/port/database
// ed evita di loggare la password.
func TestNoDSNInLog(t *testing.T) {
	password := "super-secret-pw-12345"
	dsn := "postgres://identity:" + password + "@127.0.0.1:5999/gitstack?sslmode=disable"

	// Imposta la variabile d'ambiente prima di chiamare run.
	t.Setenv("GITSTACK_IDENTITY_DB_URL", dsn)

	var buf bytes.Buffer
	exitCode := run(nil, &buf)

	// run dovrebbe restituire 1 (connessione fallita).
	if exitCode != 1 {
		t.Fatalf("exit code = %d, voluto 1", exitCode)
	}

	logOutput := buf.String()

	// La password non deve mai comparire nel log.
	if strings.Contains(logOutput, password) {
		t.Fatalf("la password %q è comparsa nel log!\n%s", password, logOutput)
	}

	// Il log deve contenere il messaggio di errore e le info di connessione.
	if !strings.Contains(logOutput, "connessione a Postgres non riuscita") {
		t.Fatalf("log mancante di messaggio di errore:\n%s", logOutput)
	}
	// Il log dovrebbe contenere host e database (estratti da ParseConfig).
	if !strings.Contains(logOutput, `"host":"127.0.0.1"`) {
		t.Errorf("log mancante di host:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, `"database":"gitstack"`) {
		t.Errorf("log mancante di database:\n%s", logOutput)
	}
}

// TestDefaultConfig verifica che i valori di default siano corretti
// quando non sono impostate variabili d'ambiente override.
// I default sono già verificati da config_test.go; qui si conferma
// che config.Load() li applica anche con Load() (os.LookupEnv).
func TestDefaultConfig(t *testing.T) {
	t.Setenv(config.EnvDatabaseURL, "postgres://identity:secret@localhost:5432/gitstack")
	t.Setenv(config.EnvAddr, ":8080")
	t.Setenv(config.EnvDBMaxConns, "10")
	t.Setenv(config.EnvMigrationsTimeout, "30s")
	t.Setenv(config.EnvLogLevel, "info")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("errore inatteso: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, voluto :8080", cfg.Addr)
	}
	if cfg.DBMaxConns != 10 {
		t.Errorf("DBMaxConns = %d, voluto 10", cfg.DBMaxConns)
	}
	if cfg.MigrationsTimeout != 30*time.Second {
		t.Errorf("MigrationsTimeout = %v, voluto 30s", cfg.MigrationsTimeout)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, voluto info", cfg.LogLevel)
	}
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantCmd   command
		wantSteps int
		wantErr   bool
	}{
		{name: "nessun argomento", args: nil, wantCmd: cmdServe},
		{name: "serve esplicito", args: []string{"serve"}, wantCmd: cmdServe},
		{name: "migrate senza sottocomando", args: []string{"migrate"}, wantCmd: cmdMigrateUp},
		{name: "migrate up", args: []string{"migrate", "up"}, wantCmd: cmdMigrateUp},
		{name: "migrate down senza N", args: []string{"migrate", "down"}, wantCmd: cmdMigrateDown, wantSteps: 1},
		{name: "migrate down con N", args: []string{"migrate", "down", "3"}, wantCmd: cmdMigrateDown, wantSteps: 3},
		{name: "migrate down N non valido", args: []string{"migrate", "down", "abc"}, wantErr: true},
		{name: "migrate down N negativo", args: []string{"migrate", "down", "-1"}, wantErr: true},
		{name: "migrate sottocomando sconosciuto", args: []string{"migrate", "sideways"}, wantErr: true},
		{name: "comando sconosciuto", args: []string{"bogus"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, steps, err := parseCommand(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatal("attendevo un errore")
				}
				return
			}
			if err != nil {
				t.Fatalf("errore inatteso: %v", err)
			}
			if cmd != tc.wantCmd {
				t.Fatalf("cmd = %v, voluto %v", cmd, tc.wantCmd)
			}
			if tc.wantCmd == cmdMigrateDown && steps != tc.wantSteps {
				t.Fatalf("steps = %d, voluto %d", steps, tc.wantSteps)
			}
		})
	}
}

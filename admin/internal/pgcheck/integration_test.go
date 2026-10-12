//go:build integration

package pgcheck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/pgtest"
)

func prepareScript(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(file), "..", "..", "..", "deploy", "postgres-prepare.sql")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// prepare esegue lo script del DBA come superuser: database e utente gitstack.
func prepare(t *testing.T, s *pgtest.Server, pw string) {
	t.Helper()
	s.SQL(t, "postgres", pgtest.SuperPassword, "postgres", []string{"GITSTACK_DB_PASSWORD=" + pw}, prepareScript(t))
}

func check(t *testing.T, s *pgtest.Server, user, db, pw string) (*Result, error) {
	t.Helper()
	return Check(context.Background(), Params{URL: s.URL(user, db, "disable"), Password: pw})
}

func TestPreflight_Postgres15e17(t *testing.T) {
	for _, major := range []int{15, 17} {
		t.Run(strings.Join([]string{"pg", string(rune('0' + major/10)), string(rune('0' + major%10))}, ""), func(t *testing.T) {
			s := pgtest.Start(t, major)
			prepare(t, s, "gs-pw-1")
			res, err := check(t, s, "gitstack", "gitstack", "gs-pw-1")
			if err != nil {
				t.Fatalf("atteso OK, ottenuto: %v", err)
			}
			if res.Major != major || !strings.HasPrefix(res.Version, string(rune('0'+major/10))+string(rune('0'+major%10))) {
				t.Errorf("versione %q major %d, attesa %d", res.Version, res.Major, major)
			}
			if len(res.Warnings) != 0 {
				t.Errorf("avvisi inattesi: %v", res.Warnings)
			}
			// Lo script è idempotente e riallinea la password.
			prepare(t, s, "gs-pw-2")
			if _, err := check(t, s, "gitstack", "gitstack", "gs-pw-2"); err != nil {
				t.Errorf("dopo la seconda esecuzione: %v", err)
			}
			// Password errata: messaggio chiaro.
			_, err = check(t, s, "gitstack", "gitstack", "sbagliata")
			var f *Failure
			if !errors.As(err, &f) || f.Kind != "connect" || !strings.Contains(f.Msg, "password errata") {
				t.Errorf("password errata: %v", err)
			}
			// Database inesistente.
			_, err = check(t, s, "gitstack", "non_esiste", "gs-pw-2")
			if !errors.As(err, &f) || !strings.Contains(f.Msg, "non esiste") {
				t.Errorf("database inesistente: %v", err)
			}
		})
	}
}

func TestPreflight_RifiutaVersione14(t *testing.T) {
	s := pgtest.Start(t, 14)
	// Lo script del DBA rifiuta già la 14; qui si prepara a mano per
	// provare il preflight dell'installer.
	s.SQL(t, "postgres", pgtest.SuperPassword, "postgres", nil,
		`CREATE ROLE gitstack LOGIN CREATEROLE PASSWORD 'gs-pw'; CREATE DATABASE gitstack OWNER gitstack;`)
	_, err := check(t, s, "gitstack", "gitstack", "gs-pw")
	var f *Failure
	if !errors.As(err, &f) || f.Kind != "version" {
		t.Fatalf("atteso rifiuto di versione, ottenuto: %v", err)
	}
	for _, want := range []string{"PostgreSQL 14.", "non supportato", "15, 16 e 17"} {
		if !strings.Contains(f.Msg, want) {
			t.Errorf("messaggio %q senza %q", f.Msg, want)
		}
	}
	// Lo script del DBA rifiuta la 14 con un messaggio chiaro.
	out, err := s.SQLErr("postgres", pgtest.SuperPassword, "postgres", prepareScript(t))
	if err == nil {
		t.Errorf("lo script di preparazione doveva rifiutare la 14: %s", out)
	}
}

func TestPreflight_PermessiInsufficienti(t *testing.T) {
	s := pgtest.Start(t, 17)
	prepare(t, s, "gs-pw")
	// 1) può connettersi ma non creare schemi nel database.
	s.SQL(t, "postgres", pgtest.SuperPassword, "postgres", nil, `
CREATE ROLE sola_lettura LOGIN PASSWORD 'ro-pw';
GRANT CONNECT ON DATABASE gitstack TO sola_lettura;
`)
	_, err := check(t, s, "sola_lettura", "gitstack", "ro-pw")
	var f *Failure
	if !errors.As(err, &f) || f.Kind != "permission" || !strings.Contains(f.Msg, "non può creare schemi") {
		t.Fatalf("senza CREATE sul database: %v", err)
	}
	if !strings.Contains(f.Msg, "postgres-prepare.sql") {
		t.Errorf("il messaggio non rimanda allo script del DBA: %q", f.Msg)
	}
	// 2) proprietario del database ma senza CREATEROLE.
	s.SQL(t, "postgres", pgtest.SuperPassword, "postgres", nil, `
CREATE ROLE senza_ruoli LOGIN PASSWORD 'sr-pw';
CREATE DATABASE altro OWNER senza_ruoli;
`)
	_, err = check(t, s, "senza_ruoli", "altro", "sr-pw")
	if !errors.As(err, &f) || f.Kind != "permission" || !strings.Contains(f.Msg, "non può creare ruoli") || !strings.Contains(f.Msg, "CREATEROLE") {
		t.Fatalf("senza CREATEROLE: %v", err)
	}
	// La prova non lascia traccia: nessuno schema o ruolo di preflight.
	left := s.SQL(t, "postgres", pgtest.SuperPassword, "gitstack", nil,
		`SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'gitstack_preflight_%'`)
	left2 := s.SQL(t, "postgres", pgtest.SuperPassword, "postgres", nil,
		`SELECT count(*) FROM pg_roles WHERE rolname LIKE 'gitstack_preflight_%'`)
	if left != "0" || left2 != "0" {
		t.Errorf("tracce del preflight: schemi %s ruoli %s", left, left2)
	}
}

func TestPreflight_TLSeCA(t *testing.T) {
	s, ca := pgtest.StartTLS(t, 17)
	prepare(t, s, "gs-pw")
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	// verify-full con la CA: ok, e la sessione è cifrata.
	res, err := Check(context.Background(), Params{URL: s.URL("gitstack", "gitstack", "verify-full"), Password: "gs-pw", CAFile: caFile})
	if err != nil {
		t.Fatalf("verify-full con CA: %v", err)
	}
	if !res.TLS || res.SSLMode != "verify-full" {
		t.Errorf("risultato: %+v", res)
	}
	// require senza CA: ok (cifra senza verificare).
	if _, err := Check(context.Background(), Params{URL: s.URL("gitstack", "gitstack", "require"), Password: "gs-pw"}); err != nil {
		t.Errorf("require: %v", err)
	}
	// verify-full senza CA: il certificato non è fidato, e il messaggio dice cosa fare.
	_, err = Check(context.Background(), Params{URL: s.URL("gitstack", "gitstack", "verify-full"), Password: "gs-pw"})
	var f *Failure
	if !errors.As(err, &f) || f.Kind != "connect" || !strings.Contains(f.Msg, "certificato") || !strings.Contains(f.Msg, "--postgres-ca-file") {
		t.Errorf("verify-full senza CA: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "gs-pw") {
		t.Errorf("la password è nel messaggio: %v", err)
	}
}

func TestPreflight_ServerSenzaTLS(t *testing.T) {
	s := pgtest.Start(t, 15)
	prepare(t, s, "gs-pw")
	_, err := Check(context.Background(), Params{URL: s.URL("gitstack", "gitstack", "require"), Password: "gs-pw"})
	var f *Failure
	if !errors.As(err, &f) || !strings.Contains(f.Msg, "non accetta connessioni TLS") || !strings.Contains(f.Msg, "sslmode=disable") {
		t.Errorf("server senza TLS: %v", err)
	}
}

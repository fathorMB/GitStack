package pgcheck

import (
	"errors"
	"strings"
	"testing"
)

func TestParse_Rifiuti(t *testing.T) {
	cases := []struct {
		name string
		p    Params
		want string
	}{
		{"vuoto", Params{}, "manca l'URL"},
		{"schema", Params{URL: "mysql://u@h/db", Password: "x"}, "URL del database non valido"},
		{"senza host", Params{URL: "postgres:///db", Password: "x"}, "senza host"},
		{"senza database", Params{URL: "postgres://u@h:5432/", Password: "x"}, "senza nome del database"},
		{"senza utente", Params{URL: "postgres://h:5432/db", Password: "x"}, "senza utente"},
		{"senza password", Params{URL: "postgres://u@h:5432/db"}, "manca la password"},
		{"password non URL-safe", Params{URL: "postgres://u@h:5432/db", Password: "p@ss/word"}, "caratteri che GitStack non può mettere in un URL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Parse(c.p)
			var f *Failure
			if !errors.As(err, &f) || f.Kind != "params" {
				t.Fatalf("atteso Failure params, ottenuto %v", err)
			}
			if !strings.Contains(f.Msg, c.want) {
				t.Errorf("messaggio %q senza %q", f.Msg, c.want)
			}
			if strings.Contains(f.Msg, "p@ss/word") {
				t.Errorf("la password è nel messaggio: %q", f.Msg)
			}
		})
	}
}

func TestParse_DefaultSSLModeRequire(t *testing.T) {
	cfg, res, err := Parse(Params{URL: "postgresql://gs@db.example.com/gitstack", Password: "pw-1.x~"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SSLMode != "require" || res.Port != 5432 || res.Host != "db.example.com" || res.Database != "gitstack" || res.User != "gs" {
		t.Errorf("risultato inatteso: %+v", res)
	}
	if cfg.Password != "pw-1.x~" {
		t.Errorf("password non applicata")
	}
}

func TestParse_PasswordDelFileVinceSullURL(t *testing.T) {
	cfg, _, err := Parse(Params{URL: "postgres://gs:daurl@h:5433/db?sslmode=disable", Password: "dalfile"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "dalfile" || cfg.Port != 5433 {
		t.Errorf("cfg inattesa: password %q porta %d", cfg.Password, cfg.Port)
	}
}

func TestParse_DatabaseDiSistemaAvvisa(t *testing.T) {
	_, res, err := Parse(Params{URL: "postgres://gs@h/postgres", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "database dedicato") {
		t.Errorf("avvisi: %v", res.Warnings)
	}
}

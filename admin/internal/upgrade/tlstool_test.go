package upgrade

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestUpgradeInstallaGitstackTLSNuovo(t *testing.T) {
	w := newWorld(t)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := readFile(t, w.tlsBin); got != signedTLS {
		t.Errorf("gitstack-tls: %q, atteso quello del chart firmato (non quello del sorgente)", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(w.tlsBin)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("mode %v, attesa 0755", fi.Mode().Perm())
		}
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "gitstack-tls aggiornato") {
		t.Errorf("il riepilogo non lo dice: %v", res.Summary)
	}
	entries, _ := os.ReadDir(w.tlsBin + "/..")
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".gitstack-tls-") {
			t.Errorf("file temporaneo rimasto: %s", e.Name())
		}
	}
}

func TestUpgradeFirmaChartFalliscePerGitstackTLSResta(t *testing.T) {
	w := versionWorld(t)
	w.noBundle = true
	runRefused(t, w, "bundle")
	if got := readFile(t, w.tlsBin); got != oldTLS {
		t.Errorf("gitstack-tls sostituito con la verifica fallita: %q", got)
	}
}

// Firma che non torna (il chart pubblicato non è quello firmato): nessuno script installato.
func TestUpgradeChartManomessoNonInstallaGitstackTLS(t *testing.T) {
	w := versionWorld(t)
	w.chartSign = []byte("altro contenuto")
	runRefused(t, w)
	if got := readFile(t, w.tlsBin); got != oldTLS {
		t.Errorf("gitstack-tls sostituito con la firma non valida: %q", got)
	}
}

// Chart firmato di una release vecchia, senza lo script: niente ripiego sul sorgente.
func TestUpgradeChartFirmatoSenzaScriptNonAggiorna(t *testing.T) {
	w := newWorld(t)
	w.chartTgz = signedChartTgzFiles(t, "name: gitstack\nversion: 0.1.0\n", "")
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := readFile(t, w.tlsBin); got != oldTLS {
		t.Errorf("gitstack-tls cambiato (ripiego sul sorgente?): %q", got)
	}
	s := strings.Join(res.Summary, "\n")
	if !strings.Contains(s, "gitstack-tls NON aggiornato") || !strings.Contains(s, "non contiene lo script") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
}

// Eccezione N9: si usa lo script del sorgente, con l'avviso.
func TestUpgradeAllowUnsignedUsaSorgenteConAvviso(t *testing.T) {
	w := newWorld(t)
	var log strings.Builder
	w.o.Log = &log
	w.o.V5.AllowUnsigned = true
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := readFile(t, w.tlsBin); got != newTLS {
		t.Errorf("gitstack-tls: %q, atteso quello del sorgente", got)
	}
	if !strings.Contains(log.String(), "firma di gitstack-tls NON verificata (eccezione N9 attiva)") {
		t.Errorf("manca l'avviso: %s", log.String())
	}
}

func TestUpgradeSenzaScriptNelCommitLoDice(t *testing.T) {
	w := newWorld(t)
	w.tlsSrc = ""
	w.o.V5.AllowUnsigned = true // con la firma lo script viene solo dal chart firmato
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := readFile(t, w.tlsBin); got != oldTLS {
		t.Errorf("gitstack-tls cambiato: %q", got)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "gitstack-tls NON aggiornato") {
		t.Errorf("manca il messaggio: %v", res.Summary)
	}
}

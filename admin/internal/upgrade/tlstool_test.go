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
	if got := readFile(t, w.tlsBin); got != newTLS {
		t.Errorf("gitstack-tls: %q, atteso quello del commit di destinazione", got)
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

func TestUpgradeSenzaScriptNelCommitLoDice(t *testing.T) {
	w := newWorld(t)
	w.tlsSrc = ""
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

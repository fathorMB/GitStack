package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Unità scritta dallo script di k3s con il vecchio INSTALL_K3S_EXEC (GIT-228).
const oldUnit = `[Unit]
Description=Lightweight Kubernetes

[Service]
Type=notify
ExecStartPre=-/sbin/modprobe br_netfilter
ExecStart=/usr/local/bin/k3s \
    server \
    '--write-kubeconfig-mode' \
    '644' \
`

func TestHardenedUnit(t *testing.T) {
	out, mode, enc, err := HardenedUnit(oldUnit)
	if err != nil || !mode || !enc {
		t.Fatalf("mode=%v enc=%v err=%v", mode, enc, err)
	}
	for _, want := range []string{"'600' \\", "'--secrets-encryption' \\", "'--write-kubeconfig-mode' \\"} {
		if !strings.Contains(out, want) {
			t.Errorf("manca %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "644") {
		t.Errorf("resta 644:\n%s", out)
	}
	// Idempotente.
	again, mode, enc, err := HardenedUnit(out)
	if err != nil || mode || enc || again != out {
		t.Errorf("seconda passata: mode=%v enc=%v err=%v", mode, enc, err)
	}
}

func TestHardenedUnit_Varianti(t *testing.T) {
	cases := []struct {
		name, in string
		mode     bool
		contains []string
	}{
		{"uguale con =", "ExecStart=/usr/local/bin/k3s \\\n    server \\\n    '--write-kubeconfig-mode=644' \\\n", true, []string{"--write-kubeconfig-mode=600'", "--secrets-encryption"}},
		{"una riga", "ExecStart=/usr/local/bin/k3s server --write-kubeconfig-mode 644\n", true, []string{"--write-kubeconfig-mode 600", "server --write-kubeconfig-mode 600 --secrets-encryption"}},
		{"già a 600 senza cifratura", "ExecStart=/usr/local/bin/k3s \\\n    server \\\n    '--write-kubeconfig-mode' \\\n    '600' \\\n", false, []string{"'600'", "--secrets-encryption"}},
		{"cifratura esplicitamente spenta", "ExecStart=/usr/local/bin/k3s server --secrets-encryption=false\n", false, []string{"server --secrets-encryption"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, mode, enc, err := HardenedUnit(c.in)
			if err != nil || mode != c.mode || !enc {
				t.Fatalf("mode=%v enc=%v err=%v\n%s", mode, enc, err, out)
			}
			for _, w := range c.contains {
				if !strings.Contains(out, w) {
					t.Errorf("manca %q in:\n%s", w, out)
				}
			}
		})
	}
	if _, _, _, err := HardenedUnit("[Service]\nExecStart=/bin/true\n"); err == nil {
		t.Error("un'unità senza argomento server va rifiutata")
	}
}

// hardenRunner finge systemctl e `k3s secrets-encrypt`, registrando l'ordine.
type hardenRunner struct {
	w          *world
	events     []string
	failState  string // sottocomando systemctl che fallisce ("restart")
	notEnabled bool   // `status` non dichiara la cifratura attiva
	restarts   int
}

func (r *hardenRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	switch name {
	case "systemctl":
		ev := "systemctl " + strings.Join(args, " ")
		r.events = append(r.events, ev)
		if !slices.Contains(r.w.cl.calls, "pg_dump") {
			r.events = append(r.events, "ERRORE: systemctl prima del backup")
		}
		if r.w.helm.upgraded {
			r.events = append(r.events, "ERRORE: systemctl dopo helm upgrade")
		}
		if args[0] == "restart" {
			r.restarts++
			if r.failState == "restart" && r.restarts == 1 {
				return []byte("Job failed"), errors.New("exit 1")
			}
		}
		return nil, nil
	case "k3s":
		if args[0] == "secrets-encrypt" {
			r.events = append(r.events, "k3s "+strings.Join(args, " "))
			if args[1] == "status" {
				if r.notEnabled {
					return []byte("Encryption Status: Disabled\n"), nil
				}
				return []byte("Encryption Status: Enabled\nCurrent Rotation Stage: start\n"), nil
			}
			return []byte("keys rotated"), nil
		}
	}
	return r.w.helm.Run(ctx, env, name, args...)
}

func newHardenWorld(t *testing.T, unit string, kubeMode os.FileMode) (*world, *hardenRunner, string, string) {
	t.Helper()
	w := newWorld(t)
	r := &hardenRunner{w: w}
	w.o.Runner = r
	dir := t.TempDir()
	w.o.SystemdDir = dir
	unitPath := filepath.Join(dir, k3sUnitName)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil { //nolint:gosec // unità di prova
		t.Fatal(err)
	}
	kc := filepath.Join(t.TempDir(), "k3s.yaml")
	if err := os.WriteFile(kc, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(kc, kubeMode); err != nil {
		t.Fatal(err)
	}
	w.o.Cfg.Kubeconfig = kc
	return w, r, unitPath, kc
}

func assertKubeconfig600(t *testing.T, kc string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return // i modi Unix non esistono
	}
	st, err := os.Stat(kc)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("kubeconfig %v, atteso 0600", st.Mode().Perm())
	}
}

// L'upgrade di un'installazione esistente (kubeconfig 644, Secret in chiaro)
// la mette in sicurezza dopo il backup e prima del chart.
func TestUpgrade_HardeningInstallazioneEsistente(t *testing.T) {
	w, r, unitPath, kc := newHardenWorld(t, oldUnit, 0o644)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want := []string{"systemctl daemon-reload", "systemctl restart k3s", "k3s secrets-encrypt status", "k3s secrets-encrypt rotate-keys"}
	if !slices.Equal(r.events, want) {
		t.Errorf("eventi: %v", r.events)
	}
	u := readFile(t, unitPath)
	if strings.Contains(u, "644") || !strings.Contains(u, "--secrets-encryption") || !strings.Contains(u, "'600'") {
		t.Errorf("unità non sistemata:\n%s", u)
	}
	if got := readFile(t, unitPath+".pre-hardening"); got != oldUnit {
		t.Errorf("copia dell'unità precedente:\n%s", got)
	}
	assertKubeconfig600(t, kc)
	if !w.helm.upgraded {
		t.Error("il chart non è stato aggiornato")
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "cifratura dei Secret attiva") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
}

// Stessa versione di GitStack: l'hardening gira comunque, con backup.
func TestUpgrade_HardeningConStessaVersione(t *testing.T) {
	w, r, unitPath, kc := newHardenWorld(t, oldUnit, 0o644)
	w.compare = "identical"
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != NoChange {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.BackupPath == "" || !slices.Contains(w.cl.calls, "pg_dump") {
		t.Error("manca il backup preventivo")
	}
	if w.helm.upgraded {
		t.Error("helm upgrade con la stessa versione")
	}
	if !slices.Contains(r.events, "systemctl restart k3s") {
		t.Errorf("eventi: %v", r.events)
	}
	if u := readFile(t, unitPath); strings.Contains(u, "644") || !strings.Contains(u, "--secrets-encryption") {
		t.Errorf("unità non sistemata:\n%s", u)
	}
	assertKubeconfig600(t, kc)
}

func TestUpgrade_HardeningGiaFatto_NonRiavviaNulla(t *testing.T) {
	ok, _, _, _ := HardenedUnit(oldUnit)
	w, r, _, _ := newHardenWorld(t, ok, 0o600)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(r.events) != 0 {
		t.Errorf("k3s toccato senza motivo: %v", r.events)
	}
}

func TestUpgrade_HardeningDryRunNonToccaNulla(t *testing.T) {
	w, r, unitPath, kc := newHardenWorld(t, oldUnit, 0o644)
	w.o.DryRun = true
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != NoChange {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(r.events) != 0 || slices.Contains(w.cl.calls, "pg_dump") {
		t.Errorf("modifiche in dry-run: %v", r.events)
	}
	if readFile(t, unitPath) != oldUnit {
		t.Error("unità cambiata in dry-run")
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "Hardening di k3s") {
		t.Errorf("il dry-run non dice il piano: %v", res.Summary)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(kc); st.Mode().Perm() != 0o644 {
			t.Errorf("kubeconfig toccato in dry-run: %v", st.Mode().Perm())
		}
	}
}

// Riavvio fallito: l'unità torna com'era, k3s riparte, GitStack non si tocca.
func TestUpgrade_HardeningFallito_RipristinaUnita(t *testing.T) {
	w, r, unitPath, _ := newHardenWorld(t, oldUnit, 0o644)
	r.failState = "restart"
	res, err := Run(context.Background(), w.o)
	if err == nil || res.Outcome != Failed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if readFile(t, unitPath) != oldUnit {
		t.Errorf("unità non ripristinata:\n%s", readFile(t, unitPath))
	}
	if r.restarts != 2 {
		t.Errorf("restart: %d (uno fallito, uno di ripristino)", r.restarts)
	}
	if w.helm.upgraded {
		t.Error("GitStack non va aggiornato se l'hardening fallisce")
	}
	w.assertUntouched()
	if !strings.Contains(strings.Join(res.Summary, "\n"), "docs/operations.md") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
}

// Cifratura non attiva dopo il riavvio: si torna indietro.
func TestUpgrade_HardeningCifraturaNonAttiva_RipristinaUnita(t *testing.T) {
	w, r, unitPath, _ := newHardenWorld(t, oldUnit, 0o644)
	r.notEnabled = true
	res, err := Run(context.Background(), w.o)
	if err == nil || res.Outcome != Failed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if readFile(t, unitPath) != oldUnit {
		t.Error("unità non ripristinata")
	}
	if slices.Contains(r.events, "k3s secrets-encrypt rotate-keys") {
		t.Error("rotate-keys senza cifratura attiva")
	}
	w.assertUntouched()
}

func TestUpgrade_UnitaNonRiconosciuta_Rifiutata(t *testing.T) {
	w, r, unitPath, _ := newHardenWorld(t, "[Service]\nExecStart=/bin/true\n", 0o600)
	res, err := Run(context.Background(), w.o)
	var re *RefusedError
	if !errors.As(err, &re) || res.Outcome != Refused {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(r.events) != 0 || readFile(t, unitPath) != "[Service]\nExecStart=/bin/true\n" {
		t.Error("modifiche su un'unità non riconosciuta")
	}
	w.assertUntouched()
}

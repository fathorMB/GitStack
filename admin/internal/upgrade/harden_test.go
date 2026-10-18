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
	"time"
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
	// statusErrors: quante prime chiamate a `status` escono con errore
	// (k3s non ha ancora scritto le annotazioni del nodo, GIT-249).
	statusErrors int
	statusCalls  int
	restarts     int

	// Aggiornamento di k3s: lo script di installazione riscrive l'unità col
	// nuovo K3sInstallExec e k3s riparte (come fa lo script vero).
	installed  string // versione di k3s in esecuzione
	unitPath   string
	kubeconfig string
	k3sUpdated bool
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
	case "sh":
		r.events = append(r.events, "k3s-install")
		if !slices.Contains(r.w.cl.calls, "pg_dump") {
			r.events = append(r.events, "ERRORE: k3s prima del backup")
		}
		b, err := os.ReadFile(r.unitPath)
		if err != nil {
			return nil, err
		}
		out, _, _, err := HardenedUnit(string(b))
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(r.unitPath, []byte(out), 0o644); err != nil { //nolint:gosec // unità di prova
			return nil, err
		}
		if err := os.Chmod(r.kubeconfig, 0o600); err != nil {
			return nil, err
		}
		r.installed = r.w.k3sFile
		r.k3sUpdated = true
		return nil, nil
	case "k3s":
		if args[0] == "--version" {
			return []byte("k3s version " + r.installed + " (deadbeef)" + string(rune(10))), nil
		}
		if args[0] == "secrets-encrypt" {
			r.events = append(r.events, "k3s "+strings.Join(args, " "))
			if args[1] == "status" {
				r.statusCalls++
				if r.statusCalls <= r.statusErrors {
					return []byte(`level=fatal msg="Error: see server log for details: Internal error occurred: secret-encrypt error ID 48327"`), errors.New("exit status 1")
				}
				if r.notEnabled || !r.k3sUpdated && r.installed != "" {
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

// `k3s secrets-encrypt status` esce con errore finché k3s non scrive le
// annotazioni del nodo (homehub, 18/10, GIT-249): si riprova, non si torna
// indietro.
func TestUpgrade_HardeningStatusRiprovato(t *testing.T) {
	w, r, unitPath, kc := newHardenWorld(t, oldUnit, 0o644)
	r.statusErrors = 3
	w.o.EncryptionStatusWait = 5 * time.Second
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if r.statusCalls != 4 {
		t.Errorf("chiamate a status: %d, attese 4", r.statusCalls)
	}
	if !slices.Contains(r.events, "k3s secrets-encrypt rotate-keys") {
		t.Error("rotate-keys non eseguito")
	}
	if u := readFile(t, unitPath); !strings.Contains(u, "--secrets-encryption") {
		t.Errorf("unità non indurita:\n%s", u)
	}
	assertKubeconfig600(t, kc)
}

// Se `status` non si riprende entro l'attesa, l'unità torna com'era.
func TestUpgrade_HardeningStatusSempreInErrore_RipristinaUnita(t *testing.T) {
	w, r, unitPath, _ := newHardenWorld(t, oldUnit, 0o644)
	r.statusErrors = 1 << 20
	w.o.EncryptionStatusWait = 100 * time.Millisecond
	res, err := Run(context.Background(), w.o)
	if err == nil || res.Outcome != Failed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if readFile(t, unitPath) != oldUnit {
		t.Error("unità non ripristinata")
	}
	if slices.Contains(r.events, "k3s secrets-encrypt rotate-keys") {
		t.Error("rotate-keys senza cifratura verificata")
	}
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

// newHardenK3sWorld: k3s da aggiornare (patch) e unità vecchia 644 senza cifratura.
func newHardenK3sWorld(t *testing.T) (*world, *hardenRunner, string, string) {
	t.Helper()
	w, r, unitPath, kc := newHardenWorld(t, oldUnit, 0o644)
	w.k3sFile = "v1.36.5+k3s1"
	r.installed = "v1.36.4+k3s1"
	r.unitPath, r.kubeconfig = unitPath, kc
	return w, r, unitPath, kc
}

// Caso più comune: lo stesso upgrade aggiorna k3s. Lo script di k3s riscrive
// l'unità con --secrets-encryption e 600, ma i Secret già presenti restano in
// chiaro finché non si verifica e si ricifra: l'hardening non può saltarlo.
func TestUpgrade_HardeningConAggiornamentoK3s(t *testing.T) {
	w, r, unitPath, kc := newHardenK3sWorld(t)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want := []string{"k3s-install", "k3s secrets-encrypt status", "k3s secrets-encrypt rotate-keys"}
	if !slices.Equal(r.events, want) {
		t.Errorf("eventi: %v, attesi %v", r.events, want)
	}
	if slices.Contains(r.events, "systemctl restart k3s") {
		t.Error("restart in più: k3s è già ripartito con lo script")
	}
	if u := readFile(t, unitPath); strings.Contains(u, "644") || !strings.Contains(u, "--secrets-encryption") {
		t.Errorf("unità:\n%s", u)
	}
	assertKubeconfig600(t, kc)
	if !w.helm.upgraded {
		t.Error("il chart non è stato aggiornato")
	}
	if res.K3sFrom != "v1.36.4+k3s1" || res.K3sTo != "v1.36.5+k3s1" {
		t.Errorf("k3s: %q → %q", res.K3sFrom, res.K3sTo)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "cifratura dei Secret attiva") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
}

// Dopo l'aggiornamento di k3s lo status non dice Enabled: l'upgrade si ferma
// prima del chart, senza rollback automatico dell'unità.
func TestUpgrade_HardeningConAggiornamentoK3s_CifraturaNonAttiva(t *testing.T) {
	w, r, unitPath, _ := newHardenK3sWorld(t)
	r.notEnabled = true
	res, err := Run(context.Background(), w.o)
	if err == nil || res.Outcome != Failed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if w.helm.upgraded {
		t.Error("helm upgrade dopo una cifratura non attiva")
	}
	if slices.Contains(r.events, "k3s secrets-encrypt rotate-keys") {
		t.Error("rotate-keys senza cifratura attiva")
	}
	if u := readFile(t, unitPath); !strings.Contains(u, "--secrets-encryption") {
		t.Error("l'unità non va rimessa com'era dopo l'aggiornamento di k3s")
	}
	if res.BackupPath == "" || !strings.Contains(strings.Join(res.Summary, "\n"), "docs/operations.md") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
}

package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseK3sVersion(t *testing.T) {
	ok := map[string]K3sVersion{
		"v1.36.4+k3s1":                            {1, 36, 4, 1},
		"v1.36.10+k3s2\n":                         {1, 36, 10, 2},
		"k3s version v1.36.5+k3s1 (a1b2c3d4)\n":   {1, 36, 5, 1},
		"k3s version v1.36.5+k3s1 (a1b2c3d4)\ngo": {1, 36, 5, 1},
	}
	for in, want := range ok {
		got, err := ParseK3sVersion(in)
		if err != nil || got != want {
			t.Errorf("%q: %+v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "latest", "v1.36.4", "1.36.4+k3s1", "v1.36+k3s1"} {
		if _, err := ParseK3sVersion(in); err == nil {
			t.Errorf("%q: atteso errore", in)
		}
	}
	if got := (K3sVersion{1, 36, 4, 1}).String(); got != "v1.36.4+k3s1" {
		t.Errorf("String: %q", got)
	}
}

func TestPlanK3s(t *testing.T) {
	cases := []struct {
		name      string
		installed string
		declared  string
		allow     bool
		action    K3sAction
		refuse    string // sottostringa del rifiuto, "" = nessun rifiuto
	}{
		{"nessuna dichiarazione", "v1.36.4+k3s1", "", false, K3sKeep, ""},
		{"stessa versione", "v1.36.4+k3s1", "v1.36.4+k3s1", false, K3sKeep, ""},
		{"patch automatica", "v1.36.4+k3s1", "v1.36.5+k3s1", false, K3sPatch, ""},
		{"patch con --k3s", "v1.36.4+k3s1", "v1.36.5+k3s1", true, K3sPatch, ""},
		{"salto di più patch", "v1.36.2+k3s1", "v1.36.9+k3s1", false, K3sPatch, ""},
		{"revisione k3s", "v1.36.4+k3s1", "v1.36.4+k3s2", false, K3sPatch, ""},
		{"minore senza flag", "v1.36.4+k3s1", "v1.37.0+k3s1", false, K3sMinor, "gitstack upgrade --k3s"},
		{"minore con flag", "v1.36.4+k3s1", "v1.37.0+k3s1", true, K3sMinor, ""},
		{"due minori", "v1.36.4+k3s1", "v1.38.0+k3s1", true, K3sMinor, "una minore alla volta"},
		{"maggiore", "v1.36.4+k3s1", "v2.0.0+k3s1", true, K3sMinor, "una minore alla volta"},
		{"downgrade di patch", "v1.36.5+k3s1", "v1.36.4+k3s1", true, K3sKeep, "docs/operations.md"},
		{"downgrade di minore", "v1.37.0+k3s1", "v1.36.9+k3s1", true, K3sKeep, "docs/operations.md"},
		{"dichiarata non valida", "v1.36.4+k3s1", "latest", true, K3sKeep, "non valida"},
		{"installata non valida", "boh", "v1.36.4+k3s1", true, K3sKeep, "non leggibile"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := PlanK3s(c.installed, c.declared, c.allow)
			if c.refuse != "" {
				var re *RefusedError
				if !errors.As(err, &re) || !strings.Contains(err.Error(), c.refuse) {
					t.Fatalf("atteso rifiuto con %q, ottenuto %v", c.refuse, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Action != c.action || p.Needed() != (c.action != K3sKeep) {
				t.Errorf("azione %v, attesa %v", p.Action, c.action)
			}
		})
	}
}

func TestSteps_Ordine(t *testing.T) {
	if got := Steps(K3sPlan{Action: K3sPatch}); !slices.Equal(got, []Step{StepBackup, StepK3s, StepChart}) {
		t.Errorf("con k3s: %v", got)
	}
	if got := Steps(K3sPlan{}); !slices.Equal(got, []Step{StepBackup, StepChart}) {
		t.Errorf("senza k3s: %v", got)
	}
}

// k3sRunner finge `k3s --version` e lo script di installazione, e registra a
// che punto dell'aggiornamento arrivano (ordine backup → k3s → GitStack).
type k3sRunner struct {
	w         *world
	installed string
	failed    bool
	events    []string
	script    string
}

func (r *k3sRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	switch name {
	case "k3s":
		return []byte("k3s version " + r.installed + " (deadbeef)\ngo version go1.26\n"), nil
	case "sh":
		r.script = args[len(args)-1]
		r.events = append(r.events, "k3s-install")
		if !slices.Contains(r.w.cl.calls, "pg_dump") {
			r.events = append(r.events, "ERRORE: k3s prima del backup")
		}
		if r.w.helm.upgraded {
			r.events = append(r.events, "ERRORE: k3s dopo helm upgrade")
		}
		if r.failed {
			return []byte("curl: (22) boom"), errors.New("exit 22")
		}
		r.installed = r.w.k3sFile
		return nil, nil
	}
	return r.w.helm.Run(ctx, env, name, args...)
}

func newK3sWorld(t *testing.T, installed, declared string) (*world, *k3sRunner) {
	t.Helper()
	w := newWorld(t)
	w.k3sFile = declared
	r := &k3sRunner{w: w, installed: installed}
	w.o.Runner = r
	return w, r
}

func TestUpgradeK3s_PatchAutomatica(t *testing.T) {
	w, r := newK3sWorld(t, "v1.36.4+k3s1", "v1.36.5+k3s1")
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !slices.Equal(r.events, []string{"k3s-install"}) {
		t.Errorf("eventi: %v", r.events)
	}
	if !strings.Contains(r.script, "INSTALL_K3S_VERSION=v1.36.5+k3s1") || !strings.Contains(r.script, K3sInstallExec) {
		t.Errorf("script: %s", r.script)
	}
	if res.K3sFrom != "v1.36.4+k3s1" || res.K3sTo != "v1.36.5+k3s1" {
		t.Errorf("k3s: %q → %q", res.K3sFrom, res.K3sTo)
	}
	if !w.helm.upgraded || res.BackupPath == "" {
		t.Error("manca helm upgrade o backup")
	}
}

func TestUpgradeK3s_MinoreSenzaFlagRifiutata(t *testing.T) {
	w, r := newK3sWorld(t, "v1.36.4+k3s1", "v1.37.0+k3s1")
	res, err := Run(context.Background(), w.o)
	var re *RefusedError
	if !errors.As(err, &re) || res.Outcome != Refused {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(err.Error(), "--k3s") {
		t.Errorf("manca la spiegazione: %v", err)
	}
	if len(r.events) != 0 || slices.Contains(w.cl.calls, "pg_dump") || w.helm.upgraded {
		t.Errorf("modifiche fatte: %v %v", r.events, w.cl.calls)
	}
	w.assertUntouched()
}

func TestUpgradeK3s_MinoreConFlag(t *testing.T) {
	w, r := newK3sWorld(t, "v1.36.4+k3s1", "v1.37.0+k3s1")
	w.o.AllowK3sMinor = true
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !slices.Equal(r.events, []string{"k3s-install"}) || !w.helm.upgraded {
		t.Errorf("eventi %v, helm upgrade %v", r.events, w.helm.upgraded)
	}
}

func TestUpgradeK3s_DueMinoriRifiutateAncheConFlag(t *testing.T) {
	w, r := newK3sWorld(t, "v1.36.4+k3s1", "v1.38.0+k3s1")
	w.o.AllowK3sMinor = true
	_, err := Run(context.Background(), w.o)
	var re *RefusedError
	if !errors.As(err, &re) || len(r.events) != 0 {
		t.Fatalf("err=%v eventi=%v", err, r.events)
	}
	w.assertUntouched()
}

func TestUpgradeK3s_InstallazioneFallita(t *testing.T) {
	w, r := newK3sWorld(t, "v1.36.4+k3s1", "v1.36.5+k3s1")
	r.failed = true
	res, err := Run(context.Background(), w.o)
	if err == nil || res.Outcome != Failed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if w.helm.upgraded {
		t.Error("GitStack non va aggiornato se k3s fallisce")
	}
	if res.BackupPath == "" || !strings.Contains(strings.Join(res.Summary, "\n"), "docs/operations.md") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
	w.assertUntouched()
}

func TestUpgradeK3s_DryRunNonToccaNulla(t *testing.T) {
	w, r := newK3sWorld(t, "v1.36.4+k3s1", "v1.37.0+k3s1")
	w.o.AllowK3sMinor = true
	w.o.DryRun = true
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != NoChange {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(r.events) != 0 || slices.Contains(w.cl.calls, "pg_dump") {
		t.Errorf("modifiche in dry-run: %v", r.events)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "v1.37.0+k3s1") {
		t.Errorf("il dry-run non dice il piano: %v", res.Summary)
	}
}

func TestUpgradeK3s_SenzaDichiarazioneNonTocca(t *testing.T) {
	w, r := newK3sWorld(t, "v1.36.4+k3s1", "")
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded || len(r.events) != 0 {
		t.Fatalf("res=%+v err=%v eventi=%v", res, err, r.events)
	}
}

// Il file della versione provata è unico e lo legge install.sh: nessuna
// copia della versione nello script, e K3sInstallExec è lo stesso.
func TestDeclaredFile_InstallSh(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	b, err := os.ReadFile(filepath.Join(root, "deploy", "k3s-version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseK3sVersion(string(b)); err != nil {
		t.Fatalf("deploy/k3s-version: %v", err)
	}
	sh, err := os.ReadFile(filepath.Join(root, "deploy", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(sh)
	if !strings.Contains(s, "k3s-version") {
		t.Error("install.sh non legge deploy/k3s-version")
	}
	if !strings.Contains(s, "INSTALL_K3S_EXEC=\""+K3sInstallExec+"\"") {
		t.Errorf("install.sh non usa INSTALL_K3S_EXEC=%q", K3sInstallExec)
	}
	if strings.Contains(s, strings.TrimSpace(string(b))) {
		t.Error("install.sh duplica la versione di k3s")
	}
}

func TestInstallerScript(t *testing.T) {
	v, err := ParseK3sVersion("v1.36.5+k3s1")
	if err != nil {
		t.Fatal(err)
	}
	want := `curl -sfL 'https://get.k3s.io' | INSTALL_K3S_VERSION=v1.36.5+k3s1 INSTALL_K3S_EXEC='server --write-kubeconfig-mode 644' sh -`
	if got := installerScript(K3sInstallURL, v); got != want {
		t.Errorf("script:\n got %s\nwant %s", got, want)
	}
}

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/config"
)

// helmRunner risponde ai comandi helm con i values e al resto con kubectl.
type helmRunner struct{ values string }

func (h helmRunner) Run(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
	if strings.HasSuffix(name, "helm") {
		return []byte(h.values), nil
	}
	if len(args) > 0 && args[0] == "exec" {
		return []byte("16.4\n"), nil
	}
	return []byte(kubectlJSON(1)), nil
}

func TestSystemInfoPublish(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	a, out, errb := newApp(nil, srv)
	a.Runner = helmRunner{values: `{"proxy":{"http":"http://u:pw-segreto@proxy.acme.local:3128"},"corporateCA":{"configMap":"acme-ca"}}`}
	var manifest []byte
	a.ApplyManifest = func(_ context.Context, m []byte) error { manifest = m; return nil }
	cfg := writeConfig(t, hostOf(srv), t.TempDir())

	if code := a.Run(context.Background(), []string{"system-info", "publish", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errb)
	}
	if strings.Contains(string(manifest), "pw-segreto") {
		t.Fatalf("credenziali del proxy nel ConfigMap: %s", manifest)
	}
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(manifest, &cm); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Network struct {
			HTTPProxy   string `json:"httpProxy"`
			CorporateCA bool   `json:"corporateCA"`
			Configured  bool   `json:"configured"`
		} `json:"network"`
		Versions struct {
			GitStack string `json:"gitstack"`
		} `json:"versions"`
	}
	if err := json.Unmarshal([]byte(cm.Data["system.json"]), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Network.Configured || doc.Network.HTTPProxy != "http://proxy.acme.local:3128" || !doc.Network.CorporateCA {
		t.Errorf("rete: %+v", doc.Network)
	}
	if doc.Versions.GitStack != "sha-abc123" {
		t.Errorf("versione: %q", doc.Versions.GitStack)
	}
}

func TestBackupAggiornaSystemInfo(t *testing.T) {
	// con AutoPublish il backup (anche fallito) aggiorna il ConfigMap: qui
	// basta provare il comando; l'aggancio è in backup.go e upgrade.go.
	a, _, _ := newApp(nil, nil)
	a.Runner = helmRunner{values: `{}`}
	a.AutoPublish = true
	n := 0
	a.ApplyManifest = func(context.Context, []byte) error { n++; return nil }
	cfg := writeConfig(t, "git.example.com", t.TempDir())
	c, code := a.loadConfig(cfg)
	if c == nil {
		t.Fatalf("config: %d", code)
	}
	a.autoPublish(context.Background(), c, cfg)
	if n != 1 {
		t.Errorf("apply chiamato %d volte", n)
	}
}

// rolloutSim simula il cluster dopo un backup: i Deployment riscalati
// risultano pronti solo dopo WaitReady (il rollout). Registra l'ordine delle
// chiamate di Scale, WaitReady e della raccolta dello stato (kubectl get).
type rolloutSim struct {
	*stubCluster
	calls    *[]string
	ready    bool
	failWait bool
}

func (r *rolloutSim) Scale(_ context.Context, d string, n int) error {
	*r.calls = append(*r.calls, "scale "+d)
	if n == 0 {
		r.ready = false
	}
	return nil
}

func (r *rolloutSim) WaitReady(_ context.Context, d string) error {
	*r.calls = append(*r.calls, "ready "+d)
	if r.failWait {
		return errors.New("rollout status: timeout")
	}
	r.ready = true
	return nil
}

type collectRunner struct {
	sim   *rolloutSim
	calls *[]string
}

func (c collectRunner) Run(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
	if strings.HasSuffix(name, "helm") {
		return []byte("{}"), nil
	}
	if len(args) > 0 && args[0] == "exec" {
		return []byte("16.4"), nil
	}
	if len(args) > 0 && args[0] == "get" {
		*c.calls = append(*c.calls, "collect")
		n := 0
		if c.sim.ready {
			n = 1
		}
		return []byte(kubectlJSON(n)), nil
	}
	return []byte(""), nil
}

// publishedServices esegue un backup con AutoPublish e restituisce il
// documento system.json pubblicato e l'ordine delle chiamate.
func publishedAfterBackup(t *testing.T, failWait bool) (items []struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
}, calls []string) {
	t.Helper()
	srv := healthServer(200)
	t.Cleanup(srv.Close)
	a, _, errb := newApp(nil, srv)
	git := t.TempDir()
	_ = os.WriteFile(filepath.Join(git, "f"), []byte("x"), 0o600)
	sim := &rolloutSim{stubCluster: &stubCluster{git: git}, calls: &calls, failWait: failWait}
	a.NewCluster = func(*config.Config) backup.Cluster { return sim }
	a.Runner = collectRunner{sim: sim, calls: &calls}
	a.AutoPublish = true
	var manifest []byte
	a.ApplyManifest = func(_ context.Context, m []byte) error { manifest = m; return nil }
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte(strings.Join([]string{"version: 1", "host: " + hostOf(srv), "image_tag: sha-abc123", ""}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", t.TempDir()}); code != ExitOK {
		t.Fatalf("backup exit %d: %s", code, errb)
	}
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(manifest, &cm); err != nil {
		t.Fatalf("manifest non pubblicato: %v", err)
	}
	var doc struct {
		Services struct {
			Items []struct {
				Name    string `json:"name"`
				Healthy bool   `json:"healthy"`
			} `json:"items"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(cm.Data["system.json"]), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Services.Items, calls
}

func TestBackupPubblicaDopoLaProntezzaDeiServizi(t *testing.T) {
	items, calls := publishedAfterBackup(t, false)
	lastReady, collect := -1, -1
	for i, c := range calls {
		if strings.HasPrefix(c, "ready ") {
			lastReady = i
		}
		if c == "collect" && collect < 0 {
			collect = i
		}
	}
	if lastReady < 0 || collect < 0 || lastReady > collect {
		t.Fatalf("WaitReady deve precedere la raccolta dello stato: %v", calls)
	}
	found := false
	for _, it := range items {
		if it.Name == "gitstack-gateway" {
			found = true
			if !it.Healthy {
				t.Errorf("il gateway riscalato risulta non pronto nel documento: %+v", it)
			}
		}
	}
	if !found {
		t.Errorf("gateway assente: %+v", items)
	}
}

func TestBackupPubblicaComunqueSeLAttesaScade(t *testing.T) {
	items, calls := publishedAfterBackup(t, true)
	for _, it := range items {
		if it.Name == "gitstack-gateway" && it.Healthy {
			t.Errorf("con l'attesa scaduta lo stato reale è non pronto: %+v", it)
		}
	}
	if len(items) == 0 {
		t.Errorf("documento non pubblicato: %v", calls)
	}
}

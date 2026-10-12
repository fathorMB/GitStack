package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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

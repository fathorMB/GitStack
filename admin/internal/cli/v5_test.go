package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/upgrade"
)

// writeV5Config scrive un config con la sezione v5 data.
func writeV5Config(t *testing.T, host, v5 string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "version: 1\nhost: " + host + "\nimage_tag: sha-" + cliOld + "\n" + v5
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// L'eccezione N9 salvata nel config si vede in gitstack status (testo e JSON).
func TestStatusSegnalaEccezioneN9(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	cfg := writeV5Config(t, hostOf(srv), "v5:\n  allow_unsigned: true\n")

	a, out, _ := newApp(&fakeRunner{out: kubectlJSON(1)}, srv)
	if code := a.Run(context.Background(), []string{"status", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out.String(), "AVVISO catena di fornitura") || !strings.Contains(out.String(), "N9") {
		t.Errorf("manca l'avviso N9:\n%s", out)
	}

	a, out, _ = newApp(&fakeRunner{out: kubectlJSON(1)}, srv)
	if code := a.Run(context.Background(), []string{"status", "--json", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out.String(), `"supply_chain_warning"`) {
		t.Errorf("manca supply_chain_warning nel JSON:\n%s", out)
	}
}

func TestStatusSenzaEccezioneNonAvvisa(t *testing.T) {
	srv := healthServer(200)
	defer srv.Close()
	cfg := writeV5Config(t, hostOf(srv), "v5:\n  cosign_path: /usr/local/bin/gitstack-cosign\n")
	a, out, _ := newApp(&fakeRunner{out: kubectlJSON(1)}, srv)
	if code := a.Run(context.Background(), []string{"status", "--config", cfg}); code != ExitOK {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if strings.Contains(out.String(), "catena di fornitura") {
		t.Errorf("avviso N9 senza eccezione:\n%s", out)
	}
}

// gitstack upgrade verifica per default col cosign dell'installer; l'eccezione
// N9 viene solo dal config o dal flag esplicito, con avviso.
func TestUpgradeConfiguraV5(t *testing.T) {
	cases := []struct {
		name      string
		v5        string
		args      []string
		wantAllow bool
		wantPath  string
	}{
		{"default", "", nil, false, ""},
		{"cosign del config", "v5:\n  cosign_path: /opt/cosign\n", nil, false, "/opt/cosign"},
		{"eccezione nel config", "v5:\n  allow_unsigned: true\n", nil, true, ""},
		{"flag", "", []string{"--allow-unsigned-mirrors"}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _, _ := upgradeApp(t, "ahead")
			cfg := writeV5Config(t, "h", c.v5)
			var got upgrade.V5Config
			tune := a.TuneUpgrade
			a.TuneUpgrade = func(o *upgrade.Options) { tune(o); got = o.V5 }
			var stderr strings.Builder
			a.Stderr = &stderr
			args := append([]string{"upgrade", "--config", cfg, "--dest", t.TempDir(), "--dry-run"}, c.args...)
			a.Run(context.Background(), args)
			if got.AllowUnsigned != c.wantAllow || got.CosignPath != c.wantPath {
				t.Errorf("V5 = %+v, atteso allow=%v cosign=%q", got, c.wantAllow, c.wantPath)
			}
			if warned := strings.Contains(stderr.String(), "AVVISO"); warned != c.wantAllow {
				t.Errorf("avviso visibile=%v, atteso %v:\n%s", warned, c.wantAllow, stderr.String())
			}
		})
	}
}

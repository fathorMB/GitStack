package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetNetworkRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	base := "# commento\nversion: 1\nhost: 10.0.0.5\nbackup:\n  destination: /srv/backup\n  retention: 3\n"
	if err := os.WriteFile(path, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	n := Network{
		HTTPProxy: "http://proxy.corp:3128", NoProxy: "a.corp,b.corp",
		RegistryMirror: "https://reg.corp:5000", RegistryUsername: `u"x`, RegistryPassword: `p\w"d`,
		DownloadMirror: "http://dl.corp", CABundle: "/etc/gitstack/network/ca.crt",
	}
	if err := SetNetwork(path, n); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Network != n {
		t.Fatalf("network riletta: %+v, scritta: %+v", c.Network, n)
	}
	if c.Backup.Retention != 3 || c.Host != "10.0.0.5" {
		t.Fatalf("il resto del file è cambiato: %+v", c)
	}
	// Riscrivere sostituisce la sezione, non la duplica; vuoto la toglie.
	n.NoProxy = ""
	if err := SetNetwork(path, n); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "network:") != 1 || strings.Contains(string(b), "no_proxy") || !strings.Contains(string(b), "# commento") {
		t.Fatalf("file:\n%s", b)
	}
	if err := SetNetwork(path, Network{}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "network") || !strings.Contains(string(b), "retention: 3") {
		t.Fatalf("file senza rete:\n%s", b)
	}
}

func TestNetworkValidate(t *testing.T) {
	bad := map[string]Network{
		"proxy senza schema":     {HTTPProxy: "proxy.corp:3128"},
		"mirror ftp":             {RegistryMirror: "ftp://x"},
		"no_proxy con spazi":     {NoProxy: "a, b"},
		"utente senza password":  {RegistryMirror: "http://r", RegistryUsername: "u"},
		"utente senza mirror":    {RegistryUsername: "u", RegistryPassword: "p"},
		"CA con percorso a caso": {CABundle: "ca.crt"},
	}
	for name, n := range bad {
		if err := n.Validate(); err == nil {
			t.Errorf("%s: atteso un errore", name)
		}
	}
	if err := (Network{HTTPProxy: "http://u:p@proxy:3128", RegistryMirror: "https://r:5000"}).Validate(); err != nil {
		t.Errorf("rete valida rifiutata: %v", err)
	}
	// La password dell'URL del proxy non finisce nei messaggi.
	err := (Network{HTTPProxy: "http://u:segreto@:3128"}).Validate()
	if err != nil && strings.Contains(err.Error(), "segreto") {
		t.Errorf("password nel messaggio: %v", err)
	}
}

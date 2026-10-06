package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newCfg(t *testing.T) *Config {
	t.Helper()
	// La cartella di gs non esiste ancora: la crea la prima scrittura.
	return New(filepath.Join(t.TempDir(), "gs"))
}

func TestDir(t *testing.T) {
	got, err := Dir(func(k string) string {
		if k == EnvConfigDir {
			return "/x/y"
		}
		return ""
	})
	if err != nil || got != "/x/y" {
		t.Fatalf("GS_CONFIG_DIR: %q %v", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	got, err = Dir(func(string) string { return "" })
	if err != nil || filepath.Base(got) != "gs" {
		t.Fatalf("predefinita: %q %v", got, err)
	}
}

func TestSaveHostEDefault(t *testing.T) {
	c := newCfg(t)
	if d, err := c.DefaultHost(); err != nil || d != "" {
		t.Fatalf("default iniziale: %q %v", d, err)
	}
	if _, ok, err := c.Host("a.test"); ok || err != nil {
		t.Fatalf("host assente: %v %v", ok, err)
	}
	if err := c.SaveHost("a.test", HostConfig{User: "alice", GitProtocol: "https"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveHost("b.test:8443", HostConfig{User: "bob", Token: "gst_x"}); err != nil {
		t.Fatal(err)
	}
	if d, _ := c.DefaultHost(); d != "b.test:8443" {
		t.Errorf("default_host = ultima istanza configurata, ho %q", d)
	}
	hc, ok, err := c.Host("b.test:8443")
	if err != nil || !ok || hc.User != "bob" || hc.Token != "gst_x" {
		t.Fatalf("lettura: %+v %v %v", hc, ok, err)
	}
	hs, _ := c.Hosts()
	if len(hs) != 2 || hs[0] != "a.test" {
		t.Errorf("Hosts = %v", hs)
	}
	if filepath.Base(c.HostPath("b.test:8443")) != "b.test_8443.yaml" {
		t.Errorf("nome file: %s", c.HostPath("b.test:8443"))
	}
	// Cancellando la predefinita, passa a un'altra.
	if err := c.DeleteHost("b.test:8443"); err != nil {
		t.Fatal(err)
	}
	if d, _ := c.DefaultHost(); d != "a.test" {
		t.Errorf("dopo la cancellazione default = %q", d)
	}
}

func TestTokenSource(t *testing.T) {
	c := newCfg(t)
	ts := WithEnv(FileTokens{Cfg: c}, func(k string) string { return "" })
	if _, _, err := ts.Token("a.test"); !errors.Is(err, ErrNoToken) {
		t.Fatalf("atteso ErrNoToken, ho %v", err)
	}
	if err := ts.SetToken("a.test", "gst_file"); err != nil {
		t.Fatal(err)
	}
	tok, src, err := ts.Token("a.test")
	if err != nil || tok != "gst_file" || src != SourceFile {
		t.Fatalf("file: %q %q %v", tok, src, err)
	}
	// GS_TOKEN vince.
	env := WithEnv(FileTokens{Cfg: c}, func(k string) string {
		if k == EnvToken {
			return "gst_env"
		}
		return ""
	})
	tok, src, err = env.Token("a.test")
	if err != nil || tok != "gst_env" || src != SourceEnv {
		t.Fatalf("env: %q %q %v", tok, src, err)
	}
	if err := ts.DeleteToken("a.test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ts.Token("a.test"); !errors.Is(err, ErrNoToken) {
		t.Fatalf("dopo DeleteToken: %v", err)
	}
	if err := ts.DeleteToken("mai-visto.test"); err != nil {
		t.Errorf("DeleteToken su host ignoto: %v", err)
	}
}

// Il file con il token e le cartelle che lo contengono sono solo-utente
// (0600/0700 su Unix, DACL dell'utente su Windows).
func TestPermessiSoloUtente(t *testing.T) {
	c := newCfg(t)
	if err := c.SaveHost("a.test", HostConfig{User: "alice", Token: "gst_secret"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		path string
		dir  bool
	}{
		{c.Path(), true},
		{filepath.Join(c.Path(), "hosts"), true},
		{filepath.Join(c.Path(), "config.yaml"), false},
		{c.HostPath("a.test"), false},
	} {
		assertPrivate(t, p.path, p.dir)
	}
	// Una riscrittura non allarga i permessi e non lascia temporanei.
	if err := c.SaveHost("a.test", HostConfig{User: "alice", Token: "gst_new"}); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, c.HostPath("a.test"), false)
	ents, _ := os.ReadDir(filepath.Join(c.Path(), "hosts"))
	if len(ents) != 1 {
		t.Errorf("file temporanei rimasti: %v", ents)
	}
}

package config

import "testing"

// Nomi e default delle variabili di GIT-67 (le usa anche il chart, GIT-74).
func TestLoad_RepoEnv(t *testing.T) {
	base := map[string]string{envDatabaseURL: "postgres://x"}
	cfg, err := load(lookupFrom(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SSHPort != 2222 || cfg.GitURL != "" || cfg.PublicURL != "" || cfg.SSHHost != "" {
		t.Fatalf("default inattesi: %+v", cfg)
	}

	if envGitURL != "GITSTACK_GIT_URL" || envPublicURL != "GITSTACK_CORE_PUBLIC_URL" ||
		envSSHHost != "GITSTACK_CORE_SSH_HOST" || envSSHPort != "GITSTACK_CORE_SSH_PORT" {
		t.Fatal("nomi delle variabili cambiati: li usa GIT-74 nel chart")
	}
	base[envGitURL] = " http://git:8080 "
	base[envPublicURL] = "https://git.example.com/"
	base[envSSHHost] = "ssh.example.com"
	base[envSSHPort] = "22"
	cfg, err = load(lookupFrom(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitURL != "http://git:8080" || cfg.PublicURL != "https://git.example.com" || cfg.SSHHost != "ssh.example.com" || cfg.SSHPort != 22 {
		t.Fatalf("configurazione: %+v", cfg)
	}

	for _, bad := range []string{"0", "70000", "abc", "-1"} {
		base[envSSHPort] = bad
		if _, err := load(lookupFrom(base)); err == nil {
			t.Fatalf("porta %q accettata", bad)
		}
	}
}

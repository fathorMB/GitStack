package config

import (
	"testing"
	"time"
)

// Nomi e default delle variabili di GIT-67 (le usa anche il chart, GIT-74).
func TestLoad_RepoEnv(t *testing.T) {
	base := map[string]string{envDatabaseURL: "postgres://x"}
	cfg, err := load(lookupFrom(base))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SSHEnabled || cfg.SSHPort != 2222 || cfg.GitURL != "" || cfg.PublicURL != "" || cfg.SSHHost != "" {
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

	for _, off := range []string{"off", " OFF "} {
		base[envSSHPort] = off
		cfg, err = load(lookupFrom(base))
		if err != nil {
			t.Fatalf("%q: %v", off, err)
		}
		if cfg.SSHEnabled || cfg.SSHPort != 0 {
			t.Fatalf("%q: SSH non spento: %+v", off, cfg)
		}
	}

	for _, bad := range []string{"0", "70000", "abc", "-1"} {
		base[envSSHPort] = bad
		if _, err := load(lookupFrom(base)); err == nil {
			t.Fatalf("porta %q accettata", bad)
		}
	}
}

// Allegati (I9, GIT-107): default e nomi delle variabili (le usa anche il chart).
func TestLoad_AttachmentsEnv(t *testing.T) {
	base := map[string]string{envDatabaseURL: "postgres://x"}
	cfg, err := load(lookupFrom(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AttachmentsDir != "" || cfg.AttachmentMaxBytes != 10<<20 || cfg.AttachmentOrphanTTL != 24*time.Hour {
		t.Fatalf("default inattesi: %+v", cfg)
	}
	if envAttachmentsDir != "GITSTACK_CORE_ATTACHMENTS_DIR" || envAttachmentMax != "GITSTACK_CORE_ATTACHMENTS_MAX_BYTES" ||
		envAttachmentOrphan != "GITSTACK_CORE_ATTACHMENTS_ORPHAN_TTL" {
		t.Fatal("nomi delle variabili cambiati: li usa il chart")
	}
	base[envAttachmentsDir] = " /var/lib/gitstack/attachments "
	base[envAttachmentMax] = "1048576"
	base[envAttachmentOrphan] = "2h"
	cfg, err = load(lookupFrom(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AttachmentsDir != "/var/lib/gitstack/attachments" || cfg.AttachmentMaxBytes != 1<<20 || cfg.AttachmentOrphanTTL != 2*time.Hour {
		t.Fatalf("configurazione: %+v", cfg)
	}
	for _, bad := range []struct{ k, v string }{{envAttachmentMax, "0"}, {envAttachmentMax, "10MB"}, {envAttachmentOrphan, "-1h"}, {envAttachmentOrphan, "un giorno"}} {
		b := map[string]string{envDatabaseURL: "postgres://x", bad.k: bad.v}
		if _, err := load(lookupFrom(b)); err == nil {
			t.Errorf("%s=%q accettato", bad.k, bad.v)
		}
	}
}

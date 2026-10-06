package config

import (
	"slices"
	"testing"
)

// Nomi e default delle variabili dei webhook (M-06/G): le usa il chart.
func TestLoad_WebhookEEgressEnv(t *testing.T) {
	base := map[string]string{envDatabaseURL: "postgres://x"}
	cfg, err := load(lookupFrom(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookSecretKey != "" || cfg.WebhookSecretKeyID != "k1" || cfg.WebhookSecretOldKeys != "" ||
		cfg.EgressAllow != nil || cfg.EgressDeny != nil || cfg.EgressClusterCIDRs != nil {
		t.Fatalf("default inattesi: %+v", cfg)
	}
	if envWebhookSecretKey != "GITSTACK_WEBHOOK_SECRET_KEY" || envWebhookSecretKeyID != "GITSTACK_WEBHOOK_SECRET_KEY_ID" ||
		envWebhookSecretOldKeys != "GITSTACK_WEBHOOK_SECRET_OLD_KEYS" || envEgressAllow != "GITSTACK_EGRESS_ALLOW" ||
		envEgressDeny != "GITSTACK_EGRESS_DENY" || envEgressClusterCIDRs != "GITSTACK_EGRESS_CLUSTER_CIDRS" {
		t.Fatal("nomi delle variabili cambiati: li usa il chart")
	}
	base[envWebhookSecretKey] = " abc "
	base[envWebhookSecretKeyID] = "k2"
	base[envWebhookSecretOldKeys] = "k1:def"
	base[envEgressAllow] = "10.0.0.0/8, hooks.example.com,,"
	base[envEgressDeny] = "*.evil.test"
	base[envEgressClusterCIDRs] = "10.1.0.0/16"
	cfg, err = load(lookupFrom(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookSecretKey != "abc" || cfg.WebhookSecretKeyID != "k2" || cfg.WebhookSecretOldKeys != "k1:def" ||
		!slices.Equal(cfg.EgressAllow, []string{"10.0.0.0/8", "hooks.example.com"}) ||
		!slices.Equal(cfg.EgressDeny, []string{"*.evil.test"}) || !slices.Equal(cfg.EgressClusterCIDRs, []string{"10.1.0.0/16"}) {
		t.Fatalf("configurazione: %+v", cfg)
	}
}

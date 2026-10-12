package sysinfo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backupstate"
	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
)

const secretValue = "SUPERSECRET-do-not-leak"

func cfgFrom(t *testing.T, y string) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte(y), "test")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var now = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

func TestBuildDatabaseInternoEBackupLocale(t *testing.T) {
	cfg := cfgFrom(t, "version: 1\nhost: git.example.com\nimage_tag: v1.0.0\n")
	rep := &status.Report{ServerVersion: "v1.0.1", K3sVersion: "v1.36.4+k3s1", K3sDeclared: "v1.36.7+k3s1", K3sUpdate: "patch",
		Database: status.Database{Mode: "bundle", Version: "16.4", VersionLive: true}}
	info := Build(Input{Cfg: cfg, Report: rep, Now: now,
		Backup: backupstate.State{Success: true, Path: "/var/backups/gitstack/b.tar.gz", At: now.Add(-time.Hour)}})

	if info.Database.Mode != "internal" || info.Database.Host != "" || info.Database.ServerVersion != "16.4" {
		t.Errorf("database: %+v", info.Database)
	}
	if info.Backup.Last.State != "success" || info.Backup.Last.Name != "/var/backups/gitstack/b.tar.gz" {
		t.Errorf("backup: %+v", info.Backup.Last)
	}
	if info.Backup.S3.Configured || info.Backup.S3.Upload.State != "none" {
		t.Errorf("s3: %+v", info.Backup.S3)
	}
	v := info.Versions
	if v.GitStack != "v1.0.1" || v.K3s != "v1.36.4+k3s1" || v.K3sDeclared != "v1.36.7+k3s1" || v.K3sUpdate != "patch" {
		t.Errorf("versioni: %+v", v)
	}
	if info.Network.Configured {
		t.Errorf("rete: %+v", info.Network)
	}
}

func TestBuildDatabaseEsternoS3Fallito(t *testing.T) {
	cfg := cfgFrom(t, `version: 1
host: git.example.com
image_tag: v1.0.0
postgres:
  external: true
  host: pg.acme.local
  port: 5432
  database: gitstack
  user: gitstack
  ssl_mode: verify-full
  version: "16.4"
  password_secret: pg-pass
  ca_secret: pg-ca
backup:
  destination: /var/backups/gitstack
  retention: 7
  s3:
    endpoint: https://backup-user:`+secretValue+`@s3.acme.local
    bucket: acme-backups
    prefix: gitstack
    access_key: AKIAEXAMPLE`+secretValue+`
    secret_key: `+secretValue+`
    key_file: /etc/gitstack/backup.key
    notify_command: "echo `+secretValue+`"
`)
	info := Build(Input{Cfg: cfg, Now: now, Backup: backupstate.State{
		Success: true, Path: "/var/backups/gitstack/b.tar.gz", At: now,
		Upload: &backupstate.Upload{Success: false, Error: "timeout", At: now},
	}})
	if info.Database.Mode != "external" || info.Database.Host != "pg.acme.local" || info.Database.Name != "gitstack" ||
		info.Database.SSLMode != "verify-full" || info.Database.ServerVersion != "16.4" {
		t.Errorf("database: %+v", info.Database)
	}
	s3 := info.Backup.S3
	if !s3.Configured || s3.Bucket != "acme-backups" || s3.Upload.State != "failed" || s3.Upload.Error != "timeout" {
		t.Errorf("s3: %+v", s3)
	}
	if info.Backup.Last.State != "success" {
		t.Errorf("l'ultimo backup locale è valido: %+v", info.Backup.Last)
	}

	// Nessun segreto nel documento, né nel ConfigMap che lo contiene.
	m, err := Manifest(cfg, info)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{secretValue, "AKIAEXAMPLE", "pg-pass", "pg-ca", "backup.key", "password", "secret_key", "access_key", "notify_command", "backup-user"} {
		if strings.Contains(string(m), leak) {
			t.Errorf("il documento contiene %q: %s", leak, m)
		}
	}
	if !strings.Contains(string(m), "s3.acme.local") {
		t.Errorf("l'endpoint (senza credenziali) deve esserci: %s", m)
	}
}

func TestNetworkFromValues(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(`{
	  "proxy": {"http": "http://u:`+secretValue+`@proxy.acme.local:3128", "https": "", "noProxy": ".acme.local,10.0.0.0/8"},
	  "corporateCA": {"configMap": "acme-ca"},
	  "git": {"mirror": {"caConfigMap": ""}}
	}`), &v); err != nil {
		t.Fatal(err)
	}
	n := networkFromValues(v)
	if !n.Configured || n.HTTPProxy != "http://proxy.acme.local:3128" || !n.ProxyHasCredentials || !n.CorporateCA || n.MirrorCA {
		t.Errorf("rete: %+v", n)
	}
	if n.NoProxy != ".acme.local,10.0.0.0/8" {
		t.Errorf("noProxy: %q", n.NoProxy)
	}
	b, _ := json.Marshal(n)
	if strings.Contains(string(b), secretValue) {
		t.Errorf("credenziali nel JSON: %s", b)
	}
	if got := networkFromValues(nil); got.Configured {
		t.Errorf("senza values: %+v", got)
	}
}

func TestPublishApplicaConfigMap(t *testing.T) {
	cfg := cfgFrom(t, "version: 1\nhost: git.example.com\nrelease: gs\nnamespace: prod\n")
	var got []byte
	err := Publish(context.Background(), cfg, Build(Input{Cfg: cfg, Now: now}), func(_ context.Context, m []byte) error {
		got = m
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var cm struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(got, &cm); err != nil {
		t.Fatal(err)
	}
	if cm.Kind != "ConfigMap" || cm.Metadata.Name != "gs-system-info" || cm.Metadata.Namespace != "prod" {
		t.Errorf("manifest: %s", got)
	}
	var doc Info
	if err := json.Unmarshal([]byte(cm.Data[Key]), &doc); err != nil || doc.Schema != 1 {
		t.Errorf("documento: %v %s", err, cm.Data[Key])
	}
}

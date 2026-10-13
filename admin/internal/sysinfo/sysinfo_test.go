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

func TestBuildReteDaConfigSenzaSegreti(t *testing.T) {
	cfg := cfgFrom(t, `version: 1
host: git.example.com
network:
  http_proxy: http://proxyuser:`+secretValue+`@proxy.acme.local:3128
  no_proxy: .acme.local
  registry_mirror: https://registry.acme.local
  registry_username: USER-`+secretValue+`
  registry_password: PW-`+secretValue+`
  download_mirror: https://dl-user:`+secretValue+`@mirror.acme.local/gitstack
  ca_bundle: /etc/gitstack/network/ca.crt
`)
	// Senza values di helm: ripiego su config.yaml.
	info := Build(Input{Cfg: cfg, Now: now})
	n := info.Network
	if !n.Configured || n.RegistryMirror != "https://registry.acme.local" || n.DownloadMirror != "https://mirror.acme.local/gitstack" {
		t.Errorf("mirror: %+v", n)
	}
	if !n.RegistryHasCredentials || !n.CorporateCA || n.HTTPProxy != "http://proxy.acme.local:3128" || !n.ProxyHasCredentials || n.NoProxy != ".acme.local" {
		t.Errorf("rete: %+v", n)
	}
	m, err := Manifest(cfg, info)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{secretValue, "registry_password", "registry_username", "dl-user", "proxyuser"} {
		if strings.Contains(string(m), leak) {
			t.Errorf("il documento contiene %q: %s", leak, m)
		}
	}
	// Solo mirror: network.configured lo conta.
	only := cfgFrom(t, "version: 1\nhost: g.example.com\nnetwork:\n  download_mirror: https://mirror.acme.local\n")
	if !Build(Input{Cfg: only, Now: now}).Network.Configured {
		t.Error("un mirror da solo deve rendere la rete configurata")
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

func TestBuildAccessServicesNodeSupplyChain(t *testing.T) {
	cfg := cfgFrom(t, "version: 1\nhost: git.example.com\nssh_port: 2222\ntls: internal\nimage_tag: v1.0.0\nv5:\n  allow_unsigned: true\n")
	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	rep := &status.Report{Services: []status.Service{
		{Name: "gs-core", Kind: "Deployment", Desired: 2, Ready: 2, Image: "x/core:v1"},
		{Name: "gs-git", Kind: "StatefulSet", Desired: 1, Ready: 0, Image: "x/git:v1"},
	}}
	info := Build(Input{Cfg: cfg, Report: rep, Now: now, CertExpiry: &exp, Node: NodeProbe{
		CPUCores: 8, MemoryBytes: 12 * gb, DiskPath: "/var/lib/rancher", DiskTotalBytes: 250 * gb, DiskFreeBytes: 10 * gb}})

	a := info.Access
	if a.Host != "git.example.com" || a.TLSMode != "internal" || a.SSHPort != 2222 || a.TLSExpiresAt == nil || !a.TLSExpiresAt.Equal(exp) {
		t.Errorf("access: %+v", a)
	}
	if !info.Services.Available || len(info.Services.Items) != 2 || !info.Services.Items[0].Healthy || info.Services.Items[1].Healthy {
		t.Errorf("services: %+v", info.Services)
	}
	n := info.Node
	if !n.Available || n.CPU.Level != "recommended" || n.Memory.Level != "below_recommended" || n.Disk.Level != "below_minimum" {
		t.Errorf("node: %+v", n)
	}
	if info.SupplyChainWarning == "" {
		t.Error("avviso della catena di fornitura assente con allow_unsigned")
	}
	b, _ := json.Marshal(info)
	if strings.Contains(string(b), `"tlsExpiresAt":"2027-01-02T03:04:05Z"`) == false {
		t.Errorf("tlsExpiresAt non serializzata: %s", b)
	}
}

func TestBuildHttpSenzaAvvisoEHostNonDisponibile(t *testing.T) {
	cfg := cfgFrom(t, "version: 1\nhost: 10.0.0.5\nssh_port: 2222\n")
	exp := now
	info := Build(Input{Cfg: cfg, Now: now, CertExpiry: &exp})
	if info.Access.TLSMode != "insecure" || info.Access.TLSExpiresAt != nil {
		t.Errorf("access: %+v", info.Access)
	}
	if info.Services.Available || info.Services.Error == "" || info.Services.Items == nil {
		t.Errorf("services: %+v", info.Services)
	}
	if info.Node.Available || info.Node.CPU.Level != "unknown" || info.Node.Disk.Level != "unknown" {
		t.Errorf("node: %+v", info.Node)
	}
	if info.SupplyChainWarning != "" {
		t.Errorf("avviso inatteso: %q", info.SupplyChainWarning)
	}
	b, _ := json.Marshal(info)
	if strings.Contains(string(b), "supplyChainWarning") {
		t.Errorf("chiave presente senza avviso: %s", b)
	}
}

func TestBuildClusterNonRaggiungibile(t *testing.T) {
	cfg := cfgFrom(t, "version: 1\nhost: h\n")
	info := Build(Input{Cfg: cfg, Now: now, Report: &status.Report{ClusterError: "cluster non interrogabile: x"}})
	if info.Services.Available || !strings.Contains(info.Services.Error, "cluster") {
		t.Errorf("services: %+v", info.Services)
	}
}

func TestBuildNodeSoglieEParseMemTotal(t *testing.T) {
	cases := []struct {
		p              NodeProbe
		cpu, mem, disk string
	}{
		{NodeProbe{CPUCores: 4, MemoryBytes: 8 * gb, DiskTotalBytes: 60 * gb, DiskFreeBytes: 20 * gb}, "below_recommended", "below_recommended", "below_recommended"},
		{NodeProbe{CPUCores: 2, MemoryBytes: 4 * gb, DiskTotalBytes: 30 * gb, DiskFreeBytes: 30 * gb}, "below_minimum", "below_minimum", "below_minimum"},
		{NodeProbe{CPUCores: 16, MemoryBytes: 32 * gb, DiskTotalBytes: 500 * gb, DiskFreeBytes: 400 * gb}, "recommended", "recommended", "recommended"},
	}
	for i, c := range cases {
		n := BuildNode(c.p)
		if n.CPU.Level != c.cpu || n.Memory.Level != c.mem || n.Disk.Level != c.disk {
			t.Errorf("caso %d: %+v", i, n)
		}
	}
	if got := parseMemTotal("MemTotal:       16384000 kB\nMemFree: 1 kB\n"); got != 16384000*1024 {
		t.Errorf("parseMemTotal = %d", got)
	}
	if parseMemTotal("niente") != 0 {
		t.Error("parseMemTotal senza riga deve dare 0")
	}
}

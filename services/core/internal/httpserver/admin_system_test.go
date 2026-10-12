package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

const leakMarker = "SEGRETO-DA-NON-ESPORRE"

func TestBuildAdminSystem_NonPubblicato(t *testing.T) {
	out := buildAdminSystem(nil)
	if out.Published {
		t.Error("published deve essere falso senza file")
	}
	if out.Database.Mode != "unknown" || out.Backup.Last.State != "none" || out.Backup.S3.Upload.State != "none" ||
		out.Versions.K3sUpdate != "none" || out.Network.Configured {
		t.Errorf("default sbagliati: %+v", out)
	}
}

func TestBuildAdminSystem_FileCompletoSenzaSegreti(t *testing.T) {
	// Un system.json con campi in più (un host mal configurato, un domani)
	// non deve far uscire niente che il contratto non dichiara.
	raw := `{
	  "schema": 1,
	  "updatedAt": "2026-10-12T10:00:00Z",
	  "database": {"mode": "external", "host": "pg.acme.local", "port": 5432, "name": "gitstack",
	               "sslmode": "verify-full", "serverVersion": "16.4",
	               "password": "` + leakMarker + `", "url": "postgres://u:` + leakMarker + `@pg/db"},
	  "backup": {"destination": "/var/backups/gitstack", "retention": 7,
	             "last": {"state": "success", "at": "2026-10-12T02:00:00Z", "name": "/var/backups/gitstack/b.tar.gz"},
	             "s3": {"configured": true, "endpoint": "https://s3.acme.local", "bucket": "acme-backups",
	                    "access_key": "` + leakMarker + `", "secret_key": "` + leakMarker + `",
	                    "upload": {"state": "failed", "at": "2026-10-12T02:03:00Z", "error": "timeout"}}},
	  "versions": {"gitstack": "v1.0.0", "k3s": "v1.36.4+k3s1", "k3sDeclared": "v1.37.0+k3s1", "k3sUpdate": "minor"},
	  "network": {"httpProxy": "http://u:` + leakMarker + `@proxy.acme.local:3128", "noProxy": ".acme.local",
	              "corporateCA": true, "mirrorCA": false,
	              "registryMirror": "https://u:` + leakMarker + `@registry.acme.local", "downloadMirror": "https://mirror.acme.local/gitstack",
	              "registry_password": "` + leakMarker + `"}
	}`
	out := buildAdminSystem([]byte(raw))
	if !out.Published || out.Database.Mode != "external" || *out.Database.Host != "pg.acme.local" ||
		out.Backup.S3.Upload.State != "failed" || *out.Backup.S3.Upload.Error != "timeout" ||
		out.Versions.K3sUpdate != "minor" || !out.Network.Configured || !out.Network.CorporateCA {
		t.Errorf("campi sbagliati: %+v", out)
	}
	if !out.Network.ProxyHasCredentials || *out.Network.HttpProxy != "http://proxy.acme.local:3128" {
		t.Errorf("proxy: %+v", out.Network)
	}
	if *out.Network.RegistryMirror != "https://registry.acme.local" || !out.Network.RegistryHasCredentials ||
		*out.Network.DownloadMirror != "https://mirror.acme.local/gitstack" {
		t.Errorf("mirror: %+v", out.Network)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), leakMarker) {
		t.Errorf("segreto nella risposta: %s", b)
	}
}

func TestBuildAdminSystem_JsonNonValido(t *testing.T) {
	out := buildAdminSystem([]byte("{non json"))
	if out.Published || out.Database.Mode != "unknown" {
		t.Errorf("%+v", out)
	}
}

func TestBuildAdminSystem_AccessServicesNodeEAvviso(t *testing.T) {
	raw := `{
	  "schema": 1,
	  "database": {"mode": "internal"},
	  "backup": {"last": {"state": "none"}, "s3": {"configured": false, "upload": {"state": "none"}}},
	  "versions": {"gitstack": "v1.0.0", "k3sUpdate": "none"},
	  "network": {"configured": false},
	  "access": {"host": "git.acme.local", "tlsMode": "internal", "tlsExpiresAt": "2027-01-02T03:04:05Z", "sshPort": 2222,
	             "tlsKey": "` + leakMarker + `"},
	  "services": {"available": true, "items": [
	    {"name": "gs-core", "kind": "Deployment", "desired": 2, "ready": 1, "image": "x/core:v1", "healthy": false}]},
	  "node": {"available": true,
	           "cpu": {"available": true, "value": 4, "minimum": 4, "recommended": 8, "level": "below_recommended"},
	           "memory": {"available": true, "value": 16000000000, "minimum": 8000000000, "recommended": 16000000000, "level": "recommended"},
	           "disk": {"available": true, "value": 90000000000, "minimum": 60000000000, "recommended": 200000000000,
	                    "level": "below_minimum", "path": "/var/lib/rancher", "freeBytes": 5000000000, "minFreeBytes": 20000000000}},
	  "supplyChainWarning": "firme non verificate"
	}`
	out := buildAdminSystem([]byte(raw))
	if out.Access.Host != "git.acme.local" || out.Access.TlsMode != "internal" || out.Access.SshPort != 2222 || out.Access.TlsExpiresAt == nil {
		t.Errorf("access: %+v", out.Access)
	}
	if !out.Services.Available || len(out.Services.Items) != 1 || out.Services.Items[0].Ready != 1 || out.Services.Items[0].Healthy {
		t.Errorf("services: %+v", out.Services)
	}
	if out.Node.Cpu.Level != "below_recommended" || out.Node.Disk.Level != "below_minimum" || out.Node.Disk.FreeBytes != 5000000000 {
		t.Errorf("node: %+v", out.Node)
	}
	if out.SupplyChainWarning == nil || *out.SupplyChainWarning != "firme non verificate" {
		t.Errorf("avviso: %v", out.SupplyChainWarning)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), leakMarker) {
		t.Errorf("segreto nella risposta: %s", b)
	}
}

func TestBuildAdminSystem_SezioniNuoveAssentiNonDisponibili(t *testing.T) {
	// system.json di un gitstack precedente a GIT-218.
	out := buildAdminSystem([]byte(`{"schema": 1, "database": {"mode": "internal"}}`))
	if out.Access.TlsMode != "unknown" || out.Services.Available || out.Services.Items == nil || len(out.Services.Items) != 0 {
		t.Errorf("access/services: %+v %+v", out.Access, out.Services)
	}
	if out.Node.Available || out.Node.Cpu.Level != "unknown" || out.Node.Memory.Level != "unknown" || out.Node.Disk.Level != "unknown" {
		t.Errorf("node: %+v", out.Node)
	}
	if out.SupplyChainWarning != nil {
		t.Errorf("avviso inatteso: %v", *out.SupplyChainWarning)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "supplyChainWarning") {
		t.Errorf("chiave presente senza avviso: %s", b)
	}
}

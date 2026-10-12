package status

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

type execRunner struct {
	out  string
	err  error
	args []string
}

func (r *execRunner) Run(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
	r.args = append([]string{name}, args...)
	return []byte(r.out), r.err
}

func extCfg() *config.Config {
	return &config.Config{Release: "gs", Namespace: "ns", Postgres: config.Postgres{
		External: true, Host: "db.example.com", Port: 5432, Database: "gitstack", User: "gitstack", SSLMode: "verify-full", Version: "15.3"}}
}

func TestDatabase_EsternoLeggeLaVersioneDalPodClient(t *testing.T) {
	r := &execRunner{out: "16.4\n"}
	c := &Collector{Runner: r, LookPath: func(string) (string, error) { return "/usr/bin/kubectl", nil }}
	d := c.database(context.Background(), extCfg(), true)
	if d.Mode != "external" || d.Version != "16.4" || !d.VersionLive || d.Error != "" {
		t.Fatalf("database: %+v", d)
	}
	if d.Host != "db.example.com" || d.Port != 5432 || d.Name != "gitstack" || d.SSLMode != "verify-full" {
		t.Errorf("dati del database: %+v", d)
	}
	joined := strings.Join(r.args, " ")
	if !strings.Contains(joined, "exec -n ns deployment/gs-pgclient") {
		t.Errorf("deve eseguire nel pod client: %s", joined)
	}
}

func TestDatabase_BundleUsaLoStatefulSet(t *testing.T) {
	r := &execRunner{out: "16.2"}
	c := &Collector{Runner: r, LookPath: func(string) (string, error) { return "/usr/bin/kubectl", nil }}
	d := c.database(context.Background(), &config.Config{Release: "gs", Namespace: "ns"}, true)
	if d.Mode != "bundle" || d.Version != "16.2" {
		t.Fatalf("database: %+v", d)
	}
	if !strings.Contains(strings.Join(r.args, " "), "statefulset/gs-postgres") {
		t.Errorf("args: %v", r.args)
	}
}

func TestDatabase_EsternoNonRaggiungibileEMalsano(t *testing.T) {
	c := &Collector{Runner: &execRunner{err: errors.New("connection refused")},
		LookPath: func(string) (string, error) { return "/usr/bin/kubectl", nil }}
	d := c.database(context.Background(), extCfg(), true)
	if d.Error == "" || d.Version != "15.3" || d.VersionLive {
		t.Fatalf("database: %+v", d)
	}
	r := &Report{APIHealthy: true, Services: []Service{{Name: "gs-gateway", Desired: 1, Ready: 1}}, Database: d}
	if r.Healthy() {
		t.Errorf("con il database esterno non raggiungibile il sistema non è sano")
	}
	r.Database = Database{Mode: "bundle", Error: ""}
	if !r.Healthy() {
		t.Errorf("senza errori deve essere sano")
	}
}

func TestDatabase_AvvisaSeIlClientHaUnaVersioneDiversa(t *testing.T) {
	d := Database{Mode: "external", Version: "15.4", VersionLive: true}
	d.warnClientMismatch([]Service{{Name: "gs-pgclient", Image: "docker.io/postgres:17-alpine"}})
	if !strings.Contains(d.Warning, "client ha PostgreSQL 17") || !strings.Contains(d.Warning, "15-alpine") {
		t.Errorf("avviso: %q", d.Warning)
	}
	d = Database{Mode: "external", Version: "16.1", VersionLive: true}
	d.warnClientMismatch([]Service{{Name: "gs-pgclient", Image: "docker.io/postgres:16-alpine"}})
	if d.Warning != "" {
		t.Errorf("stessa versione: nessun avviso, ottenuto %q", d.Warning)
	}
}

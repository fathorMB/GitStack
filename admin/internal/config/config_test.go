package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const valid = `version: 1
host: 10.0.0.5
ssh_port: 2222
release: gitstack
namespace: gs
image_tag: sha-abc
kubeconfig: /etc/rancher/k3s/k3s.yaml
backup:
  destination: /srv/backup
  retention: 3
`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(valid), "t")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "10.0.0.5" || c.Namespace != "gs" || c.Backup.Retention != 3 || c.Backup.Destination != "/srv/backup" || c.ImageTag != "sha-abc" {
		t.Fatalf("config inattesa: %+v", c)
	}
}

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte("host: gs.example.org\n"), "t")
	if err != nil {
		t.Fatal(err)
	}
	if c.Release != "gitstack" || c.Namespace != "default" || c.SSHPort != 2222 || c.Backup.Retention != 7 || c.Backup.Destination != "/var/backups/gitstack" {
		t.Fatalf("default inattesi: %+v", c)
	}
}

func TestParseInvalid(t *testing.T) {
	cases := map[string]string{
		"senza host":        "version: 1\n",
		"host con schema":   "host: http://x/\n",
		"porta fuori range": "host: x\nssh_port: 70000\n",
		"retention":         "host: x\nbackup:\n  retention: -1\n",
		"destinazione":      "host: x\nbackup:\n  destination: rel/path\n",
		"versione":          "host: x\nversion: 9\n",
		"campo sconosciuto": "host: x\nhots: y\n",
		"yaml rotto":        "host: [\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(in), "t"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("atteso ErrInvalid, ottenuto %v", err)
			}
		})
	}
}

func TestLoadNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("atteso ErrNotFound, ottenuto %v", err)
	}
}

func TestSetFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("# nota\nversion: 1\nhost: h\nimage_tag: sha-a\nbackup:\n  retention: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetFields(p, map[string]string{"image_tag": "sha-b", "chart_dir": "/x/chart"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	for _, want := range []string{"# nota", "image_tag: sha-b", "chart_dir: /x/chart", "  retention: 3"} {
		if !strings.Contains(s, want) {
			t.Errorf("manca %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "sha-a") {
		t.Error("valore vecchio rimasto")
	}
	c, err := Parse(b, "t")
	if err != nil || c.ImageTag != "sha-b" || c.ChartDir != "/x/chart" {
		t.Errorf("riletto: %+v %v", c, err)
	}
}

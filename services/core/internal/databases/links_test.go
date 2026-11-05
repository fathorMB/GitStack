package databases

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// DB10: le variabili iniettate sono esattamente quelle che lo store tratta come gestite, e DATABASE_URL e le
// PG* dicono la stessa cosa (host interno nei SAN, verify-full, CA montata).
func TestLinkCredentials_Env(t *testing.T) {
	d := store.ManagedDB{ID: uuid.New(), Name: "shop"}
	link := uuid.New()
	c := NewLinkCredentials(d, link, "0123456789abcdef0123456789abcdef")
	env := c.Env()
	var got []string
	for k := range env {
		got = append(got, k)
	}
	sort.Strings(got)
	want := append([]string(nil), store.ManagedDBEnvNames...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("variabili = %v, attese %v", got, want)
	}
	u, err := url.Parse(env["DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	if u.Scheme != "postgresql" || u.User.Username() != LinkRole(link) || pw != "0123456789abcdef0123456789abcdef" {
		t.Errorf("DATABASE_URL: %s", env["DATABASE_URL"])
	}
	if u.Host != ServiceHost(d.ID)+":5432" || u.Path != "/shop" {
		t.Errorf("host/db: %s %s", u.Host, u.Path)
	}
	if q := u.Query(); q.Get("sslmode") != "verify-full" || q.Get("sslrootcert") != "/etc/gitstack/db/ca.crt" {
		t.Errorf("query: %v", q)
	}
	for k, v := range map[string]string{"PGHOST": ServiceHost(d.ID), "PGPORT": "5432", "PGDATABASE": "shop", "PGUSER": LinkRole(link),
		"PGPASSWORD": "0123456789abcdef0123456789abcdef", "PGSSLMODE": "verify-full", "PGSSLROOTCERT": "/etc/gitstack/db/ca.crt"} {
		if env[k] != v {
			t.Errorf("%s = %q, atteso %q", k, env[k], v)
		}
	}
}

func TestRootCA_SenzaFileErrorePulito(t *testing.T) {
	dir := t.TempDir()
	if _, err := RootCA(dir); err != ErrNoRootCA {
		t.Fatalf("senza ca.crt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RootCA(dir); err != ErrNoRootCA {
		t.Fatalf("file vuoto: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := RootCA(dir); err != nil || len(b) == 0 {
		t.Fatalf("con il file: %v", err)
	}
}

// DB11: la label che apre l'ingresso del database e' quella che la NetworkPolicy del database seleziona.
func TestLinkLabel_ECoerenteConLaPolicyDelDatabase(t *testing.T) {
	d := store.ManagedDB{ID: uuid.New()}
	np := BuildNetworkPolicy(d)
	sel := np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels
	if sel[LinkLabel(d.ID)] != "true" || len(sel) != 1 {
		t.Fatalf("selettore dell'ingresso %v, label %s", sel, LinkLabel(d.ID))
	}
	pl := PodLabels(d.ID)
	sts := BuildStatefulSet(d, StatefulSetOptions{Image: Image{Repository: "r", Digest: "sha256:" + strings.Repeat("a", 64)}})
	for k, v := range pl {
		if sts.Spec.Template.Labels[k] != v {
			t.Errorf("i pod del database non portano %s=%s", k, v)
		}
	}
}

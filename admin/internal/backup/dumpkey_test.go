package backup

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// GIT-565 (ADR 0003, punto 5): la chiave di dump dei database è un Secret `<release>-db-dump-key` con
// label part-of=gitstack e helm.sh/resource-policy: keep (lo crea admin/internal/dbpki). Entra in
// secrets.json di `gitstack backup` con la stessa lista di tutti i Secret di GitStack e `gitstack restore`
// lo rimette identico: i dump già scritti restano leggibili.
func TestDumpKeyInBackupAndRestore(t *testing.T) {
	const name = "gs-db-dump-key"
	raw := []byte(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"gs-db-dump-key",` +
		`"labels":{"app.kubernetes.io/part-of":"gitstack"},"annotations":{"helm.sh/resource-policy":"keep"}},` +
		`"type":"Opaque","data":{"key":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="}}`)
	e := newEnv(t)
	e.f.secrets = append(e.f.secrets, Secret{Name: name, Raw: raw})
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Manifest.Files, func(f FileSum) bool { return f.Name == SecretsName }) {
		t.Fatalf("secrets.json non è nel manifest: %+v", res.Manifest.Files)
	}

	// Restore su un'installazione pulita, dove la chiave non c'è.
	r := newEnv(t)
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	r.f.secrets = nil
	if err := Restore(context.Background(), r.o, res.Path); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.f.applied, name) {
		t.Fatalf("la chiave di dump non è tornata con il restore: Secret applicati %v", r.f.applied)
	}
	if got := r.f.appliedRaw[name]; string(got) != string(raw) {
		t.Errorf("il Secret ripristinato è diverso dall'originale:\n%s\natteso:\n%s", got, raw)
	}
}

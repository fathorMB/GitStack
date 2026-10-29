package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Con il database esterno, i Secret con la password e la CA appartengono al
// Postgres di questa installazione: un restore non li sovrascrive.
func TestRestoreSecrets_NonRipristinaQuelliDelDatabaseEsterno(t *testing.T) {
	e := newEnv(t)
	e.o.Cfg.Postgres.External = true
	e.o.Cfg.Postgres.PasswordSecret = "gs-postgres-external"
	e.o.Cfg.Postgres.CASecret = "gs-postgres-ca"
	p := filepath.Join(t.TempDir(), "secrets.json")
	body := `[{"name":"gs-postgres","object":{}},{"name":"gs-postgres-external","object":{}},` +
		`{"name":"gs-postgres-ca","object":{}},{"name":"gs-identity-admin","object":{}}]`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.o.restoreSecrets(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(e.f.applied) != 1 || e.f.applied[0] != "gs-identity-admin" {
		t.Errorf("Secret applicati: %v", e.f.applied)
	}
}

func TestKubectlCluster_PGExecEsternoUsaIlPodClient(t *testing.T) {
	var got []string
	k := &KubectlCluster{Bin: "kubectl", Namespace: "ns", Release: "gs", ExternalPG: true,
		Exec: func(_ context.Context, _ []string, _ io.Reader, _ io.Writer, name string, args ...string) error {
			got = append([]string{name}, args...)
			return nil
		}}
	if err := k.PGExec(context.Background(), nil, nil, "sh", "-c", "true"); err != nil {
		t.Fatal(err)
	}
	want := "kubectl -n ns exec -i deployment/gs-pgclient -- sh -c true"
	if s := join(got); s != want {
		t.Errorf("comando: %q, atteso %q", s, want)
	}
	k.ExternalPG = false
	_ = k.PGExec(context.Background(), nil, nil, "sh", "-c", "true")
	if s := join(got); s != "kubectl -n ns exec -i statefulset/gs-postgres -- sh -c true" {
		t.Errorf("bundle: %q", s)
	}
}

func join(a []string) string { return strings.Join(a, " ") }

// GIT-494: i Secret dei certificati del cliente stanno in <release>-certs, che il backup non legge né
// riscrive: Secrets() guarda solo il namespace del release (label part-of=gitstack) e ApplySecret scrive
// lì. Dopo un restore è core a ricreare i Secret nel namespace dei certificati dal database.
func TestKubectlCluster_SecretsSoloNelNamespaceDelRelease(t *testing.T) {
	var got [][]string
	k := &KubectlCluster{Bin: "kubectl", Namespace: "ns", Release: "gs",
		Exec: func(_ context.Context, _ []string, _ io.Reader, out io.Writer, name string, args ...string) error {
			got = append(got, append([]string{name}, args...))
			if out != nil {
				_, _ = out.Write([]byte(`{"items":[]}`))
			}
			return nil
		}}
	if _, err := k.Secrets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := k.ApplySecret(context.Background(), Secret{Name: "x", Raw: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("chiamate attese 2, trovate %v", got)
	}
	for _, c := range got {
		s := join(c)
		if !strings.HasPrefix(s, "kubectl -n ns ") || strings.Contains(s, "certs") || strings.Contains(s, "--all-namespaces") || strings.Contains(s, " -A") {
			t.Errorf("il backup deve restare nel namespace del release: %q", s)
		}
	}
}

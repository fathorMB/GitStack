package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Prove con Helm VERO (GIT-565, rework 2): abilitazione e tabella dei database vengono da ciò che
// `helm template` rende con i valori dell'upgrade, quindi maiuscole di `true`, stringhe truthy e
// precedenza di -f e --set sono quelle di Helm. Il test salta senza helm:
//   - GITSTACK_HELM_BIN=<percorso di helm>, oppure
//   - GITSTACK_HELM_DOCKER_IMAGE=alpine/helm:3.16.2 (helm in un container, cartella di lavoro montata).
//
// La CI lo lancia da deploy/tests/chart_hardening_test.sh.

func realChartDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs(filepath.Join("..", "..", "..", "deploy", "gitstack"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// helmExec ritorna la funzione che lancia helm (nativo o in Docker) per i percorsi sotto root.
func helmExec(t *testing.T, root string) func(args []string) ([]byte, error) {
	t.Helper()
	if bin := os.Getenv("GITSTACK_HELM_BIN"); bin != "" {
		return func(args []string) ([]byte, error) {
			var stderr bytes.Buffer
			c := exec.Command(bin, args...)
			c.Stderr = &stderr
			out, err := c.Output()
			if err != nil {
				return append(out, stderr.Bytes()...), err
			}
			return out, nil
		}
	}
	if img := os.Getenv("GITSTACK_HELM_DOCKER_IMAGE"); img != "" {
		return func(args []string) ([]byte, error) {
			slashRoot := filepath.ToSlash(root)
			tr := make([]string, len(args))
			for i, a := range args {
				if s := filepath.ToSlash(a); strings.HasPrefix(s, slashRoot) {
					a = "/w" + strings.TrimPrefix(s, slashRoot)
				}
				tr[i] = a
			}
			full := append([]string{"run", "--rm", "-v", root + ":/w:ro", img}, tr...)
			var stderr bytes.Buffer
			c := exec.Command("docker", full...)
			c.Stderr = &stderr
			out, err := c.Output()
			if err != nil {
				return append(out, stderr.Bytes()...), err
			}
			return out, nil
		}
	}
	t.Skip("helm non disponibile (GITSTACK_HELM_BIN o GITSTACK_HELM_DOCKER_IMAGE): la prova gira da chart_hardening_test.sh in CI")
	return nil
}

func chartTgzFromDir(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if err := tw.WriteHeader(&tar.Header{Name: "gitstack/" + filepath.ToSlash(rel), Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Lo script di TLS arriva dal chart firmato (files/gitstack-tls.sh): quello di prova.
	if err := tw.WriteHeader(&tar.Header{Name: "gitstack/files/gitstack-tls.sh", Mode: 0o644, Size: int64(len(signedTLS)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(signedTLS))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// defaultRefs sono i repository@digest della tabella di default del chart vero.
func defaultRefs(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(realChartDir(t), "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Databases struct {
			Postgres struct {
				Images map[string]pgEntry `yaml:"images"`
			} `yaml:"postgres"`
		} `yaml:"databases"`
	}
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for m, e := range v.Databases.Postgres.Images {
		out[m] = e.Repository + "@" + e.Digest
	}
	if len(out) != 2 {
		t.Fatalf("tabella di default inattesa: %v", out)
	}
	return out
}

// helmWorld: chart attuale e chart nuovo sono quello vero; i database dipendono solo dai valori.
func helmWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	root := filepath.Dir(filepath.Dir(w.chart))
	run := helmExec(t, root)
	chart := realChartDir(t)
	if err := os.RemoveAll(w.chart); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(chart, w.chart); err != nil {
		t.Fatal(err)
	}
	w.chartTgz = chartTgzFromDir(t, chart)
	w.helm.real = run
	w.o.TLSDir = t.TempDir()
	writeRoot(t, w.o.TLSDir)
	return w
}

func extraValues(t *testing.T, w *world, body string) string {
	t.Helper()
	f := filepath.Join(filepath.Dir(filepath.Dir(w.chart)), "extra.yaml")
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// Release prima spenta, nessuna firma: --set databases.enabled=<qualunque maiuscola di true> ferma l'upgrade.
func TestHelm_EnabledSetQualunqueMaiuscolaVerificaLeFirme(t *testing.T) {
	for _, v := range []string{"TRUE", "True", "true"} {
		t.Run(v, func(t *testing.T) {
			w := helmWorld(t)
			w.o.Sets = []string{"databases.enabled=" + v}
			refusedNoUpgrade(t, w, "immagine postgres")
			// Tutti i riferimenti effettivi firmati: procede e crea i Secret.
			w2 := helmWorld(t)
			w2.o.Sets = []string{"databases.enabled=" + v}
			refs := defaultRefs(t)
			signImages(t, w2, validIdentity, refs["16"], refs["17"])
			res, err := Run(context.Background(), w2.o)
			if err != nil || res.Outcome != Upgraded {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			if len(w2.cl.applied) != 2 {
				t.Errorf("Secret creati: %v", w2.cl.applied)
			}
		})
	}
}

// -f con enabled: "false" (stringa, truthy per i template) lo rifiuta lo schema prima dell'apply.
func TestHelm_FileConEnabledStringaRifiutatoDalloSchema(t *testing.T) {
	for _, body := range []string{"databases:\n  enabled: \"false\"\n", "databases:\n  enabled: \"TRUE\"\n"} {
		w := helmWorld(t)
		w.o.ValueFiles = []string{extraValues(t, w, body)}
		refusedNoUpgrade(t, w, "helm template")
	}
}

// -f con TRUE senza apici è un booleano per YAML: acceso, e le firme si verificano.
func TestHelm_FileConTRUEBooleanoVerificaLeFirme(t *testing.T) {
	w := helmWorld(t)
	w.o.ValueFiles = []string{extraValues(t, w, "databases:\n  enabled: TRUE\n")}
	refusedNoUpgrade(t, w, "immagine postgres")
}

// bool false: nessun oggetto dei database, nessuna verifica, nessun Secret.
func TestHelm_BoolFalseNonVerificaNulla(t *testing.T) {
	w := helmWorld(t)
	w.o.ValueFiles = []string{extraValues(t, w, "databases:\n  enabled: false\n")}
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, "postgres@") || len(w.cl.applied) != 0 {
		t.Errorf("database spenti: cosign=%v secret=%v", cosignCalls(t), w.cl.applied)
	}
}

// Precedenza di Helm sulla tabella: valori conservati < -f < --set. Si verifica il riferimento effettivo.
func TestHelm_TabellaEffettivaConPrecedenzaDiHelm(t *testing.T) {
	refs := defaultRefs(t)
	d16 := strings.TrimPrefix(refs["16"][strings.Index(refs["16"], "@")+1:], "")
	conserved := "sha256:" + strings.Repeat("b", 64)
	fileDigest := "sha256:" + strings.Repeat("c", 64)
	repo := refs["16"][:strings.Index(refs["16"], "@")]

	w := helmWorld(t)
	w.helm.values = `{"databases":{"enabled":true,"postgres":{"images":{"16":{"digest":"` + conserved + `"}}}}}`
	w.o.ValueFiles = []string{extraValues(t, w, "databases:\n  postgres:\n    images:\n      \"16\":\n        digest: "+fileDigest+"\n")}
	w.o.Sets = []string{"databases.postgres.images.17.repository=altro.test/pg"}
	// Sono firmati i riferimenti SCAVALCATI (conservato, default) ma non gli effettivi.
	signImages(t, w, validIdentity, repo+"@"+conserved, refs["16"], refs["17"])
	refusedNoUpgrade(t, w, repo+"@"+fileDigest)

	// Firmando gli effettivi (16 dal file, 17 dal --set) procede, e gli scavalcati non servono.
	w2 := helmWorld(t)
	w2.helm.values = w.helm.values
	w2.o.ValueFiles = []string{extraValues(t, w2, "databases:\n  postgres:\n    images:\n      \"16\":\n        digest: "+fileDigest+"\n")}
	w2.o.Sets = w.o.Sets
	eff17 := "altro.test/pg@" + refs["17"][strings.Index(refs["17"], "@")+1:]
	signImages(t, w2, validIdentity, repo+"@"+fileDigest, eff17)
	res, err := Run(context.Background(), w2.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, conserved) || calledWith(t, refs["17"]) || calledWith(t, d16) {
		t.Errorf("riferimenti scavalcati verificati: %v", cosignCalls(t))
	}
}

// Riferimento effettivo invariato rispetto alla release: riuso, nessuna chiamata a cosign.
func TestHelm_RiferimentoInvariatoRiuso(t *testing.T) {
	w := helmWorld(t)
	w.helm.values = `{"databases":{"enabled":true}}`
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calledWith(t, "postgres@") {
		t.Errorf("tabella invariata: non si riverifica: %v", cosignCalls(t))
	}
}

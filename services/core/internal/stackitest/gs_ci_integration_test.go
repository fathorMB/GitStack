//go:build integration

package stackitest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/google/uuid"
)

// GIT-291 (v2): il mockup B8 (terminale) contro gli handler CI veri di core, sullo stack
// completo (gateway, identity e git veri, core in-process, Postgres). Il workflow sta nel
// repo, `gs workflow run` lo avvia con un input (CI3), il motore legge il file da git; i job
// li prende e li chiude il test, come farebbe il ci-gateway (ClaimNext, CompleteJob), perché
// sullo stack non c'è un cluster per i pod. Provati qui:
//   - gs workflow run con input (-f) e senza l'input obbligatorio (errore);
//   - gs run watch --exit-status: uscita 0 se il run riesce, non zero se fallisce;
//   - gs run view --log-failed: stampa il log del solo job fallito;
//   - gs release create/list/upload/download su un tag spinto dal repo (REL1).

const ciBuildYML = `name: Build
on:
  workflow_dispatch:
    inputs:
      env:
        required: true
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo build
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: echo lint
  test:
    runs-on: ubuntu-latest
    needs: build
    steps:
      - run: echo test
`

// ciFinish prende i job in coda e li chiude con l'esito di conclFor(nome); i log di logFor
// (se non vuoti) li scrive nel LogStore del job, come il runner.
func (g *gsEnv) ciFinish(logs *ci.LogStore, conclFor func(job string) string, logFor func(job string) []string) {
	g.t.Helper()
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		job, _, ok, err := ci.ClaimNext(ctx, g.pool, time.Now().UTC(), 3, "pod-prova")
		if err != nil {
			g.t.Fatalf("claim: %v", err)
		}
		if !ok {
			return
		}
		var rows []ci.LogLine
		for _, l := range logFor(job.Name) {
			rows = append(rows, ci.LogLine{Time: time.Now().UTC(), Content: l})
		}
		if len(rows) > 0 {
			if _, err := logs.Append(ctx, g.pool, time.Now().UTC(), job.ID, 0, rows); err != nil {
				g.t.Fatalf("log di %s: %v", job.Name, err)
			}
		}
		if err := ci.CompleteJob(ctx, g.pool, time.Now().UTC(), job.ID, conclFor(job.Name)); err != nil {
			g.t.Fatalf("chiusura di %s: %v", job.Name, err)
		}
	}
	g.t.Fatal("la coda non si svuota")
}

func TestGsCI_RunWatchViewWorkflowReleaseSulloStack(t *testing.T) {
	logs := &ci.LogStore{Dir: t.TempDir()}
	assets := &attachments.Disk{Dir: t.TempDir()}
	g := newGsEnv(t,
		httpserver.WithCILogs(logs),
		httpserver.WithAttachments(httpserver.AttachmentsConfig{Disk: &attachments.Disk{Dir: t.TempDir()}, ReleaseAssets: assets, MaxBytes: 1 << 20}))
	const repo = "ci-terminale"
	id := g.createRepo("alice", repo, "private")
	_ = id
	seed := g.initWork(g.httpsURL("alice", g.tokens["alice"], "/alice/"+repo+".git"),
		map[string]string{"README.md": "# ci\n", ".github/workflows/build.yml": ciBuildYML})
	g.mustGit(seed, "", "push", "-q", "origin", "main")
	alice := func(args ...string) gsRun { return g.ok("alice", repo, "", args...) }

	// gs workflow list: il workflow letto da git.
	wl := alice("workflow", "list")
	if !strings.Contains(wl.out, "Build") || !strings.Contains(wl.out, "build.yml") {
		t.Fatalf("workflow list: %q", wl.out)
	}

	// Senza l'input obbligatorio il motore rifiuta l'avvio: uscita non zero.
	if r := g.gs("alice", repo, "", "workflow", "run", "build.yml", "--ref", "main"); r.code == 0 {
		t.Fatalf("workflow run senza input obbligatorio doveva fallire: %q", r.out)
	}
	// Senza --ref è un errore d'uso, prima di ogni chiamata.
	if r := g.gs("alice", repo, "", "workflow", "run", "build.yml", "-f", "env=prod"); r.code == 0 {
		t.Fatal("workflow run senza --ref doveva fallire")
	}

	// --- run riuscito: watch --exit-status esce 0 -------------------------------------
	okRun := jsonMap(t, alice("workflow", "run", "build.yml", "--ref", "main", "-f", "env=prod", "--json", "id,status").out)
	okID, _ := okRun["id"].(string)
	if _, err := uuid.Parse(okID); err != nil || okRun["status"] == "completed" {
		t.Fatalf("workflow run: %v", okRun)
	}
	g.ciFinish(logs, func(string) string { return "success" }, func(string) []string { return nil })
	w := alice("run", "watch", okID, "--exit-status", "--interval", "100ms")
	if !strings.Contains(w.out, okID) || !strings.Contains(w.out, "completed") || !strings.Contains(w.out, "success") {
		t.Fatalf("run watch (riuscito): %q", w.out)
	}
	if v := alice("run", "view", okID); !strings.Contains(v.out, "success") {
		t.Fatalf("run view: %q", v.out)
	}

	// --- run fallito: watch --exit-status non zero, view --log-failed col log del job -
	badRun := jsonMap(t, alice("workflow", "run", "build.yml", "--ref", "main", "-f", "env=staging", "--json", "id").out)
	badID, _ := badRun["id"].(string)
	g.ciFinish(logs,
		func(job string) string {
			if job == "build" {
				return "failure"
			}
			return "success"
		},
		func(job string) []string {
			if job == "build" {
				return []string{"compilo", "errore: manca il pacchetto xyz"}
			}
			return []string{"riga del job " + job}
		})
	// Senza --exit-status il watch descrive l'esito e esce 0; con --exit-status esce non zero.
	if w := alice("run", "watch", badID, "--interval", "100ms"); !strings.Contains(w.out, "failure") {
		t.Fatalf("run watch (fallito): %q", w.out)
	}
	wf := g.fails(1, "alice", repo, "run", "watch", badID, "--exit-status", "--interval", "100ms")
	if !strings.Contains(wf.out, "failure") {
		t.Fatalf("run watch --exit-status (fallito): out %q err %q", wf.out, wf.errOut)
	}
	vf := alice("run", "view", badID, "--log-failed")
	if !strings.Contains(vf.out, "--- build ---") || !strings.Contains(vf.out, "errore: manca il pacchetto xyz") {
		t.Fatalf("run view --log-failed: %q", vf.out)
	}
	if strings.Contains(vf.out, "riga del job lint") || strings.Contains(vf.out, "riga del job test") {
		t.Fatalf("run view --log-failed stampa anche i job riusciti: %q", vf.out)
	}
	// run list --json vede i due run.
	if l := alice("run", "list", "--json", "id"); !strings.Contains(l.out, okID) || !strings.Contains(l.out, badID) {
		t.Fatalf("run list: %q", l.out)
	}

	// --- gs release: tag spinto, release con note generate, asset con SHA-256 ----------
	g.mustGit(seed, "", "tag", "v0.0.0-test.1")
	g.mustGit(seed, "", "push", "-q", "origin", "v0.0.0-test.1")
	relID := strings.TrimSpace(alice("release", "create", "--tag", "v0.0.0-test.1", "--name", "Prova 1", "--generate-notes", "--prerelease").out)
	if _, err := uuid.Parse(relID); err != nil {
		t.Fatalf("release create: %q", relID)
	}
	if rl := alice("release", "list"); !strings.Contains(rl.out, "v0.0.0-test.1") {
		t.Fatalf("release list: %q", rl.out)
	}
	payload := filepath.Join(t.TempDir(), "gs-linux-amd64")
	if err := os.WriteFile(payload, []byte("binario di prova"), 0o600); err != nil {
		t.Fatal(err)
	}
	up := strings.Fields(alice("release", "upload", relID, payload).out)
	if len(up) != 2 || !strings.HasPrefix(up[1], "sha256:") {
		t.Fatalf("release upload: %v", up)
	}
	dst := filepath.Join(t.TempDir(), "scaricato")
	alice("release", "download", relID, up[0], "-o", dst)
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "binario di prova" {
		t.Fatalf("release download: %q %v", got, err)
	}
	if v := alice("release", "view", relID); !strings.Contains(v.out, "v0.0.0-test.1") {
		t.Fatalf("release view: %q", v.out)
	}
}

//go:build windows

package cirunner_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"

	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
)

// TestWindowsE2E (GIT-526) è la prova reale del runner esterno su Windows: compila
// gitstack-runner.exe, lo fa girare con l'act vero contro un ci-gateway finto (lo stesso
// fakeGateway degli altri test, col masker vero del gateway applicato ai log) ed esegue il
// workflow di deploy/ci/windows-tauri-prova: setup di Node e Rust, shell pwsh/powershell/bash,
// GITHUB_PATH/ENV/OUTPUT, segreto su più righe e `npx tauri build --bundles nsis` firmato.
// Costa 10-30 minuti: gira solo con GITSTACK_WINDOWS_E2E_DIR (la cartella dell'app di prova).
//
//	GITSTACK_WINDOWS_E2E_DIR   cartella con l'app (deploy/ci/windows-tauri-prova)
//	GITSTACK_WINDOWS_E2E_ACT   percorso di act.exe (default: act nel PATH)
//	GITSTACK_WINDOWS_E2E_KEY   file della chiave di firma di Tauri (tauri signer generate)
//	GITSTACK_WINDOWS_E2E_WORK  cartella di lavoro del runner (default %TEMP%\gsr)
//	GITSTACK_WINDOWS_E2E_TARGET CARGO_TARGET_DIR del job (default %TEMP%\gsr-target)
//	GITSTACK_WINDOWS_E2E_LOG   dove scrivere il log del job (con i segreti mascherati)
func TestWindowsE2E(t *testing.T) {
	appDir := os.Getenv("GITSTACK_WINDOWS_E2E_DIR")
	if appDir == "" {
		t.Skip("GITSTACK_WINDOWS_E2E_DIR non impostata")
	}
	keyRaw, err := os.ReadFile(os.Getenv("GITSTACK_WINDOWS_E2E_KEY"))
	if err != nil {
		t.Fatalf("chiave di firma: %v", err)
	}
	act := os.Getenv("GITSTACK_WINDOWS_E2E_ACT")
	if act == "" {
		act = "act"
	}
	work := os.Getenv("GITSTACK_WINDOWS_E2E_WORK")
	if work == "" {
		work = filepath.Join(os.TempDir(), "gsr")
	}
	target := os.Getenv("GITSTACK_WINDOWS_E2E_TARGET")
	if target == "" {
		target = filepath.Join(os.TempDir(), "gsr-target")
	}
	wf, err := os.ReadFile(filepath.Join(appDir, ".github", "workflows", "windows.yml"))
	if err != nil {
		t.Fatal(err)
	}

	// Segreto su più righe (come una chiave minisign) e la chiave di Tauri.
	multi := "untrusted comment: gitstack prova\nRWQ-riga-due-ABCDEF1234567890\r\nriga-tre-$HOME-\"q\"\n"
	secrets := map[string]string{"TAURI_SIGNING_PRIVATE_KEY": strings.TrimSpace(string(keyRaw)), "MULTI_SECRET": multi}
	masker := ci.NewMasker(jobToken, secrets["TAURI_SIGNING_PRIVATE_KEY"], multi)

	g := &fakeGateway{
		workflow: string(wf),
		archive:  appTarball(t, appDir),
		extra:    map[string]any{"job": "tauri", "event_name": "push", "ref": "refs/heads/main", "repository": "alice/tauri-prova"},
		secrets:  secrets,
		vars:     map[string]string{"CARGO_TARGET_DIR": target},
		masker:   masker,
	}
	url := start(t, g)

	exe := filepath.Join(t.TempDir(), "gitstack-runner.exe")
	if out, err := exec.Command("go", "build", "-o", exe, "../../cmd/gitstack-runner").CombinedOutput(); err != nil {
		t.Fatalf("build di gitstack-runner: %v\n%s", err, out)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfgJSON, _ := json.Marshal(map[string]any{"url": url, "runnerToken": secret, "name": "pc-windows", "labels": []string{"self-hosted", "windows"}})
	if err := os.WriteFile(cfgPath, cfgJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(work)

	var runnerLog bytes.Buffer
	cmd := exec.Command(exe, "run", "--config", cfgPath, "--workdir", work, "--act", act, "--action-mirror", "https://github.com")
	cmd.Stdout, cmd.Stderr = &runnerLog, &runnerLog
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	deadline := time.After(90 * time.Minute)
	for done := false; !done; {
		select {
		case <-deadline:
			t.Fatal("il job non è finito in 90 minuti")
		case <-time.After(2 * time.Second):
			g.mu.Lock()
			done = g.result != runnerv1.Result_RESULT_UNSPECIFIED
			g.mu.Unlock()
		}
	}
	elapsed := time.Since(start).Round(time.Second)

	g.mu.Lock()
	log := strings.Join(g.rows, "\n")
	result := g.result
	g.mu.Unlock()
	header := "# Log del job windows-tauri, secondo il ci-gateway finto con il masker vero (ci.Masker)\n" +
		"# durata: " + elapsed.String() + ", esito: " + result.String() + "\n"
	if p := os.Getenv("GITSTACK_WINDOWS_E2E_LOG"); p != "" {
		if err := os.WriteFile(p, []byte(header+log+"\n"), 0o644); err != nil {
			t.Error(err)
		}
	}
	t.Logf("durata %s, esito %s, righe di log %d\n--- log del runner:\n%s", elapsed, result, len(g.rows), runnerLog.String())
	if result != runnerv1.Result_RESULT_SUCCESS {
		tail := log
		if len(tail) > 6000 {
			tail = tail[len(tail)-6000:]
		}
		t.Fatalf("esito %s; coda del log:\n%s", result, tail)
	}
	for _, s := range append([]string{"riga-due-ABCDEF1234567890", "untrusted comment: gitstack prova", `riga-tre-$HOME-"q"`}, strings.Split(secrets["TAURI_SIGNING_PRIVATE_KEY"], "\n")...) {
		if s != "" && strings.Contains(log, s) {
			t.Errorf("segreto in chiaro nel log: %q", s)
		}
	}
	for _, want := range []string{"pwsh 7.", "powershell 5.1", "bash 5.", "da-pwsh da-powershell da-bash", "dal PATH di bash", "durata build:", "-setup.exe"} {
		if !strings.Contains(log, want) {
			t.Errorf("manca %q nel log", want)
		}
	}
}

// appTarball impacchetta la cartella dell'app sotto «workspace/», come l'archivio del gateway,
// senza node_modules, dist e target.
func appTarball(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "workspace/", Typeflag: tar.TypeDir, Mode: 0o755})
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "dist", "target":
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: "workspace/" + filepath.ToSlash(rel), Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(b))}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

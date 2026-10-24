package cirunner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// usesRe riconosce `uses: owner/repo[/percorso]@ref` di un passo. Restano fuori `./locale`
// e `docker://...` (il primo carattere dopo `uses:` non e' un nome di owner seguito da `/`
// e `@`), e i workflow riusabili di job (`.github/workflows/...`) si scartano dopo.
var usesRe = regexp.MustCompile(`(?m)^(\s*(?:-\s+)?uses:\s*)(["']?)([A-Za-z0-9][A-Za-z0-9_.-]*)/([A-Za-z0-9_.-]+)((?:/[^@\s"'#]+)?)@([^\s"'#]+)(["']?)`)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

// MirrorActions scarica le action pubbliche del workflow da base: il mirror amministrato o,
// se non c'e', https://github.com. Lo fa il runner e non act, per due motivi: act 0.2.x clona
// sempre da https://<istanza>/<owner>/<repo> e non accetta `uses:` con URL; e act manda
// GITHUB_TOKEN, qui il token del job (gsj_), come credenziale del clone, cioe' a github.com.
// Ogni `uses: owner/repo[/percorso]@ref` si scarica qui dal mirror
// (`git fetch --depth 1 <mirror>/<owner>/<repo> <ref>`, funziona con http e https) sotto dest, e
// nel workflow diventa un'action locale (`./.gitstack-actions/...`): dest sta dentro il workspace perche' act risolve le action locali nella sua copia.
// Un clone che fallisce e' un errore: non si ricade mai su github.com di nascosto.
// Con base vuota il workflow resta com'e'. actions/checkout non si scarica (vedi sotto). Le action richiamate dentro un'action composita
// non si riscrivono: le risolve act come sempre.
func MirrorActions(ctx context.Context, workflow []byte, base, gitBin, dest string, env []string) ([]byte, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return workflow, nil
	}
	if gitBin == "" {
		gitBin = "git"
	}
	fetched := map[string]string{} // owner/repo@ref -> cartella
	var firstErr error
	out := usesRe.ReplaceAllFunc(workflow, func(match []byte) []byte {
		if firstErr != nil {
			return match
		}
		m := usesRe.FindSubmatch(match)
		lead, owner, repo, sub, ref := string(m[1]), string(m[3]), string(m[4]), string(m[5]), string(m[6])
		if strings.HasPrefix(sub, "/.github/workflows/") {
			return match
		}
		// Il codice del repo e' gia' nel workspace (archivio del gateway): act salta
		// actions/checkout, mentre quello vero cercherebbe il repo su github.com.
		if owner == "actions" && repo == "checkout" && sub == "" {
			return match
		}
		key := owner + "/" + repo + "@" + ref
		dir, ok := fetched[key]
		if !ok {
			dir = filepath.Join(dest, owner+"__"+repo+"__"+unsafeName.ReplaceAllString(ref, "_"))
			if err := fetchAction(ctx, gitBin, base+"/"+owner+"/"+repo, ref, dir, env); err != nil {
				firstErr = fmt.Errorf("action %s da %s: %w", key, base, err)
				return match
			}
			fetched[key] = dir
		}
		// act risolve le action locali dentro la copia del workspace: dest ne e' una sottocartella.
		rel, err := filepath.Rel(filepath.Dir(dest), dir)
		if err != nil {
			firstErr = err
			return match
		}
		return []byte(lead + string(m[2]) + "./" + filepath.ToSlash(rel) + sub + string(m[7]))
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func fetchAction(ctx context.Context, gitBin, url, ref, dir string, env []string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(append([]string{}, env...), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(buf.String())
			if len(msg) > 300 {
				msg = msg[:300]
			}
			return fmt.Errorf("git %s: %w: %s", args[0], err, msg)
		}
		return nil
	}
	if err := run("init", "-q"); err != nil {
		return err
	}
	if err := run("fetch", "-q", "--depth", "1", url, ref); err != nil {
		return err
	}
	return run("-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD")
}

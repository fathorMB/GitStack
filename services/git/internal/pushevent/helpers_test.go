package pushevent_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

func runner(t *testing.T) *gitrun.Runner {
	t.Helper()
	r, err := gitrun.New()
	if err != nil {
		t.Skip("git non installato")
	}
	return r
}

func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Ada Lovelace", "GIT_AUTHOR_EMAIL=ada@example.com",
		"GIT_COMMITTER_NAME=Grace Hopper", "GIT_COMMITTER_EMAIL=grace@example.com")
}

// git lancia il git vero in dir e ritorna l'output senza spazi finali.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// bareAndWork crea un repo bare e un clone di lavoro con origin = il bare.
func bareAndWork(t *testing.T) (bare, work string) {
	t.Helper()
	runner(t)
	root := t.TempDir()
	bare = root + "/bare.git"
	work = root + "/work"
	git(t, root, "init", "--bare", "-b", "main", bare)
	git(t, root, "init", "-b", "main", work)
	git(t, work, "remote", "add", "origin", bare)
	return bare, work
}

// commit crea un commit vuoto con il messaggio dato e ritorna il suo sha.
func commit(t *testing.T, work, msg string) string {
	t.Helper()
	git(t, work, "commit", "--allow-empty", "-m", msg)
	return git(t, work, "rev-parse", "HEAD")
}

// fastImport scrive n commit vuoti c1..cn su un ref nuovo del repo bare,
// in un colpo solo: molto più veloce di n processi git. La data dipende dal
// nome del ref, così due ref non producono gli stessi sha.
func fastImport(t *testing.T, bare, ref string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		msg := fmt.Sprintf("c%d", i)
		fmt.Fprintf(&b, "commit %s\nmark :%d\ncommitter T <t@example.com> %d +0000\ndata %d\n%s\n",
			ref, i, 1700000000+len(ref)*100000+i, len(msg), msg)
		if i > 1 {
			fmt.Fprintf(&b, "from :%d\n", i-1)
		}
		b.WriteString("\n")
	}
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir = bare
	cmd.Env = gitEnv()
	cmd.Stdin = strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fast-import: %v\n%s", err, out)
	}
}

package cirunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCleanJobDirs_Ritenta: su Windows un file può restare aperto un attimo; la pulizia ritenta
// e, se non ci riesce, ritorna l'errore invece di lasciare il workspace al job successivo (GIT-526).
func TestCleanJobDirs_Ritenta(t *testing.T) {
	wd := t.TempDir()
	for _, d := range []string{"workspace", "workflow", "actenv", "outputs"} {
		if err := os.MkdirAll(filepath.Join(wd, d, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig, origWait := removeAll, cleanRetryWait
	cleanRetryWait = time.Millisecond
	defer func() { removeAll, cleanRetryWait = orig, origWait }()
	fails := 2
	removeAll = func(p string) error {
		if strings.HasSuffix(p, "workspace") && fails > 0 {
			fails--
			return errors.New("file in uso")
		}
		return orig(p)
	}
	if err := cleanJobDirs(context.Background(), wd); err != nil {
		t.Fatalf("dopo due errori passeggeri deve riuscire: %v", err)
	}
	for _, d := range []string{"workspace", "workflow", "actenv", "outputs"} {
		if _, err := os.Stat(filepath.Join(wd, d)); !os.IsNotExist(err) {
			t.Errorf("%s non rimossa: %v", d, err)
		}
	}

	removeAll = func(p string) error { return errors.New("file in uso") }
	err := cleanJobDirs(context.Background(), wd)
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Errorf("errore persistente: voglio un errore che nomina workspace, ho %v", err)
	}
}

// TestWithoutCIEnv_MaiuscoleEMinuscole: su Windows «gitstack_ci_runner_secret» è la stessa variabile.
func TestWithoutCIEnv_MaiuscoleEMinuscole(t *testing.T) {
	got := withoutCIEnv([]string{"PATH=x", "GITSTACK_CI_RUNNER_SECRET=a", "gitstack_ci_x=b", "Gitstack_Ci_Y=c", "GITSTACK_CORE=d"})
	if strings.Join(got, ",") != "PATH=x,GITSTACK_CORE=d" {
		t.Errorf("ambiente: %v", got)
	}
}

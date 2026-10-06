//go:build !windows

package upgrade

import (
	"context"
	"errors"
	"os"
	"testing"
)

func assertConfigMode(t *testing.T, w *world) {
	t.Helper()
	st, err := os.Stat(w.cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("permessi di config.yaml: %o, attesi 600", got)
	}
}

// Dopo un upgrade riuscito config.yaml resta root-only, altrimenti il
// Load successivo (controllo di mode_unix.go) lo rifiuterebbe.
func TestConfigStays0600AfterUpgrade(t *testing.T) {
	w := newWorld(t)
	w.helm.newSchem = "identity|6|f\ncore|12|f"
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	assertConfigMode(t, w)
}

// Dopo un rollback con restore dal backup config.yaml è ancora 0600.
func TestConfigStays0600AfterRollback(t *testing.T) {
	w := newWorld(t)
	w.helm.newSchem = "identity|5|f\ncore|12|t"
	w.helm.onUp = func() error { return errors.New("exit 1") }
	w.healthy = func() bool { return !w.helm.upgraded || w.helm.rolled && w.cl.restored }
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != RolledBack {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	assertConfigMode(t, w)
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/upgrade"
)

// GIT-490: `upgrade --reexec-protocol` dichiara il protocollo, senza config e senza toccare niente.
func TestUpgradeReexecProtocol(t *testing.T) {
	a, cfg, h := upgradeApp(t, "ahead")
	var out strings.Builder
	a.Stdout = &out
	if code := a.Run(context.Background(), []string{"upgrade", "--config", cfg, "--reexec-protocol"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if strings.TrimSpace(out.String()) != upgrade.ReexecProtocol || len(h.calls) != 0 {
		t.Errorf("stdout=%q chiamate=%v", out.String(), h.calls)
	}
}

// Il lato del processo nuovo esegue il passo chiesto e scrive la risposta.
func TestUpgradeReexecChildWritesReply(t *testing.T) {
	a, cfg, _ := upgradeApp(t, "ahead")
	dir := t.TempDir()
	req, reply := filepath.Join(dir, "req.json"), filepath.Join(dir, "reply.json")
	if err := upgrade.WriteJSON(req, &upgrade.ReexecRequest{Mode: upgrade.ReexecPost, From: "a", To: "b"}); err != nil {
		t.Fatal(err)
	}
	args := []string{"upgrade", "--config", cfg, "--reexec-request", req, "--reexec-reply", reply}
	if code := a.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	r, err := upgrade.ReadReexecReply(reply)
	if err != nil || r.Outcome != upgrade.Upgraded {
		t.Fatalf("reply=%+v err=%v", r, err)
	}
	// senza --reexec-reply: uso errato
	if code := a.Run(context.Background(), args[:5]); code != ExitUsage {
		t.Errorf("senza reply: exit %d", code)
	}
}

// Il processo vecchio: sonda il protocollo, poi lancia il binario con i flag giusti e legge la risposta.
func TestReexecFuncRunsStagedBinary(t *testing.T) {
	a, _, _ := upgradeApp(t, "ahead")
	work := t.TempDir()
	staged := filepath.Join(work, "gitstack")
	var calls [][]string
	a.ExecBinary = func(_ context.Context, bin string, args []string, out io.Writer) error {
		calls = append(calls, args)
		if bin != staged {
			t.Errorf("binario: %s", bin)
		}
		if len(args) == 2 && args[1] == "--reexec-protocol" {
			_, _ = io.WriteString(out, upgrade.ReexecProtocol+"\n")
			return nil
		}
		m := map[string]string{}
		for i := 1; i+1 < len(args); i += 2 {
			m[args[i]] = args[i+1]
		}
		r, err := upgrade.ReadReexecRequest(m["--reexec-request"])
		if err != nil || r.Mode != upgrade.ReexecRollback || r.PrevRev != 4 {
			t.Errorf("richiesta: %+v %v", r, err)
		}
		return upgrade.WriteJSON(m["--reexec-reply"], &upgrade.ReexecReply{Outcome: upgrade.RolledBack, RolledBackChart: true, Summary: []string{"ok"}})
	}
	f := a.reexecFunc("/etc/gitstack/config.yaml", "/bk", "/k", 5*time.Minute)
	got, err := f(context.Background(), staged, &upgrade.ReexecRequest{Mode: upgrade.ReexecRollback, PrevRev: 4})
	if err != nil || got.Outcome != upgrade.RolledBack || !got.RolledBackChart {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	joined := strings.Join(calls[1], " ")
	for _, want := range []string{"upgrade --config /etc/gitstack/config.yaml", "--timeout 5m0s", "--dest /bk", "--key-file /k"} {
		if !strings.Contains(joined, want) {
			t.Errorf("manca %q in %s", want, joined)
		}
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Errorf("file temporanei rimasti: %v", entries)
	}
}

// Un binario di destinazione che non conosce il protocollo (o non parte): ErrNoReexec, si ripiega.
func TestReexecFuncNoProtocol(t *testing.T) {
	a, _, _ := upgradeApp(t, "ahead")
	for name, exec := range map[string]func(io.Writer) error{
		"errore d'uso":    func(out io.Writer) error { _, _ = io.WriteString(out, "flag provided but not defined"); return errors.New("exit 2") },
		"non parte":       func(io.Writer) error { return errors.New("exec format error") },
		"risposta strana": func(out io.Writer) error { _, _ = io.WriteString(out, "9\n"); return nil },
	} {
		a.ExecBinary = func(_ context.Context, _ string, _ []string, out io.Writer) error { return exec(out) }
		var buf bytes.Buffer
		a.Stdout = &buf
		f := a.reexecFunc("c", "", "", time.Minute)
		if _, err := f(context.Background(), filepath.Join(t.TempDir(), "gitstack"), &upgrade.ReexecRequest{Mode: upgrade.ReexecRollback}); !errors.Is(err, upgrade.ErrNoReexec) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

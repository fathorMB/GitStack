package gitrun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEnvStripsGit(t *testing.T) {
	t.Setenv("GIT_DIR", "/altrove")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	n := 0
	for _, kv := range Env() {
		if strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") {
			t.Fatalf("variabile ereditata: %s", kv)
		}
		if kv == "GIT_TERMINAL_PROMPT=0" || kv == "GIT_CONFIG_NOSYSTEM=1" || kv == "GIT_CONFIG_GLOBAL="+os.DevNull {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("variabili di isolamento mancanti: %v", Env())
	}
}

func TestOutputCapAndTimeoutAndStream(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Skip("git non trovato nel PATH")
	}
	dir := t.TempDir()
	if _, err := r.Output(context.Background(), dir, nil, "init", "--bare", "--quiet"); err != nil {
		t.Fatal(err)
	}
	// Tetto sull'output.
	small := &Runner{Bin: r.Bin, MaxOutput: 10}
	if out, err := small.Output(context.Background(), dir, nil, "rev-parse", "--is-bare-repository"); err != nil || len(out) == 0 {
		t.Fatalf("output corto: %v", err)
	}
	if _, err := small.Output(context.Background(), dir, nil, "config", "--list"); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("tetto: %v", err)
	}
	// Timeout.
	fast := &Runner{Bin: r.Bin, Timeout: time.Nanosecond}
	if _, err := fast.Output(context.Background(), dir, nil, "config", "--list"); err == nil {
		t.Fatal("timeout atteso")
	}
	// Errore con stderr e codice.
	_, err = r.Output(context.Background(), dir, nil, "rev-parse", "--verify", "--quiet", "nonesiste")
	var ge *Error
	if !errors.As(err, &ge) || ge.ExitCode != 1 {
		t.Fatalf("errore: %v", err)
	}
	// Streaming.
	var buf bytes.Buffer
	if err := r.Stream(context.Background(), dir, &buf, "config", "--list"); err != nil || buf.Len() == 0 {
		t.Fatalf("stream: %v %q", err, buf.String())
	}
}

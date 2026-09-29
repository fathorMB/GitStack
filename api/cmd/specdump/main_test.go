package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/fathorMB/GitStack/api"
)

func TestRunWritesSpec(t *testing.T) {
	out := filepath.Join(t.TempDir(), "spec.yaml")
	var stderr bytes.Buffer
	if code := run([]string{out}, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, api.Spec) {
		t.Fatal("il file scritto differisce da api.Spec")
	}
}

func TestRunWithoutArgument(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(nil, &stderr); code == 0 {
		t.Fatal("atteso exit != 0 senza argomento")
	}
	if stderr.Len() == 0 {
		t.Fatal("atteso un messaggio d'errore")
	}
}

func TestRunWriteError(t *testing.T) {
	out := filepath.Join(t.TempDir(), "manca", "spec.yaml")
	var stderr bytes.Buffer
	if code := run([]string{out}, &stderr); code == 0 {
		t.Fatal("atteso exit != 0 se la scrittura fallisce")
	}
}

package api

import (
	"bytes"
	"os"
	"testing"
)

func TestSpecMatchesFile(t *testing.T) {
	if len(Spec) == 0 {
		t.Fatal("Spec è vuota")
	}
	raw, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Spec, raw) {
		t.Fatal("Spec differisce da openapi.yaml")
	}
}

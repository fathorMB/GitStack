package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestDefaultWorkDir (GIT-526): su Windows la cartella di lavoro predefinita sta in LocalAppData,
// non nel profilo Roaming della configurazione; altrove accanto alla configurazione.
func TestDefaultWorkDir(t *testing.T) {
	cfg := filepath.Join("base", "gitstack-runner", "config.json")
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", filepath.Join("C:", "Users", "gsr", "AppData", "Local"))
		want := filepath.Join("C:", "Users", "gsr", "AppData", "Local", "gitstack-runner", "work")
		if got := defaultWorkDir(cfg); got != want {
			t.Errorf("defaultWorkDir = %s, voglio %s", got, want)
		}
		t.Setenv("LocalAppData", "")
	}
	if got, want := defaultWorkDir(cfg), filepath.Join("base", "gitstack-runner", "work"); got != want {
		t.Errorf("senza LocalAppData: %s, voglio %s", got, want)
	}
}

//go:build ignore

// check-standalone-build compila ogni servizio con un Dockerfile fuori dal
// workspace (GOWORK=off, -mod=readonly), come fa la build dell'immagine: nel
// workspace (go.work) un go.sum incompleto non si vede, nel Dockerfile sì
// (GIT-123). La lista dei servizi si ricava da services/*/Dockerfile. Si
// lancia dalla radice del repo:
//
//	go run scripts/check-standalone-build.go
//
// Per correggere: GOWORK=off go mod tidy nel servizio che fallisce.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	dockerfiles, err := filepath.Glob(filepath.Join("services", "*", "Dockerfile"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-standalone-build:", err)
		os.Exit(2)
	}
	if len(dockerfiles) == 0 {
		fmt.Fprintln(os.Stderr, "check-standalone-build: nessun services/*/Dockerfile trovato (si lancia dalla radice del repo)")
		os.Exit(2)
	}
	failed := 0
	for _, df := range dockerfiles {
		dir := filepath.Dir(df)
		cmd := exec.Command("go", "build", "./...")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
		out, err := cmd.CombinedOutput()
		if err != nil {
			failed++
			fmt.Printf("FAIL %s: %v\n%s\n", dir, err, out)
			continue
		}
		fmt.Printf("ok   %s\n", dir)
	}
	if failed > 0 {
		fmt.Printf("check-standalone-build: %d servizi non compilano fuori dal workspace\n", failed)
		os.Exit(1)
	}
}

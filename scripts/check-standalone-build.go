//go:build ignore

// check-standalone-build compila ogni servizio con un Dockerfile fuori dal
// workspace (GOWORK=off, -mod=readonly), come fa la build dell'immagine: nel
// workspace (go.work) un go.sum incompleto non si vede, nel Dockerfile si vede
// (GIT-123).
//
// Dal GIT-184 i Dockerfile hanno per contesto la radice del repo e usano le
// sorgenti locali dei moduli interni (scripts/docker-local-replace.sh). Qui si
// riproduce la stessa cosa: si copiano in una cartella temporanea SOLO i
// percorsi che il Dockerfile copia (righe COPY senza --from), si lancia lo
// stesso script dei replace dalla cartella del servizio e si compila con
// GOWORK=off e -mod=readonly. Se il Dockerfile non copia un modulo interno che
// serve, la build qui fallisce come nell'immagine.
//
// La lista dei servizi si ricava da services/*/Dockerfile. Si lancia dalla
// radice del repo:
//
//	go run scripts/check-standalone-build.go
//
// Per correggere un go.sum incompleto: GOWORK=off go mod tidy nel servizio
// (con i replace locali, vedi lo script) oppure go work sync.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// copySources ricava dal Dockerfile i percorsi sorgente delle righe COPY che
// vengono dal contesto (senza --from), con le continuazioni di riga unite.
func copySources(dockerfile string) ([]string, error) {
	f, err := os.Open(dockerfile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	var cur string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if strings.HasSuffix(l, "\\") {
			cur += strings.TrimSuffix(l, "\\") + " "
			continue
		}
		lines = append(lines, cur+l)
		cur = ""
	}
	var srcs []string
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) < 3 || !strings.EqualFold(fields[0], "COPY") {
			continue
		}
		var args []string
		fromStage := false
		for _, a := range fields[1:] {
			if strings.HasPrefix(a, "--from") {
				fromStage = true
			}
			if !strings.HasPrefix(a, "--") {
				args = append(args, a)
			}
		}
		if fromStage || len(args) < 2 {
			continue
		}
		srcs = append(srcs, args[:len(args)-1]...)
	}
	return srcs, sc.Err()
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		return copyFile(p, filepath.Join(dst, rel))
	})
}

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
		if err := checkOne(df, dir); err != nil {
			failed++
			fmt.Printf("FAIL %s: %v\n", dir, err)
			continue
		}
		fmt.Printf("ok   %s\n", dir)
	}
	if failed > 0 {
		fmt.Printf("check-standalone-build: %d servizi non compilano come nel Dockerfile\n", failed)
		os.Exit(1)
	}
}

func checkOne(df, dir string) error {
	srcs, err := copySources(df)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "standalone-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, s := range srcs {
		s = filepath.FromSlash(strings.TrimSuffix(strings.TrimPrefix(s, "./"), "/"))
		if s == "." || s == "" || strings.Contains(s, "..") {
			return fmt.Errorf("COPY %q non supportato dal controllo", s)
		}
		st, err := os.Stat(s)
		if err != nil {
			return fmt.Errorf("COPY %s: %w", s, err)
		}
		if st.IsDir() {
			err = copyTree(s, filepath.Join(tmp, s))
		} else {
			err = copyFile(s, filepath.Join(tmp, s))
		}
		if err != nil {
			return err
		}
	}
	svc := filepath.Join(tmp, dir)
	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = svc
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return nil
	}
	// go mod edit modifica go.mod: per -mod=readonly e' il comportamento del
	// Dockerfile (il replace si scrive prima del download/build).
	if err := run("sh", filepath.ToSlash(filepath.Join(tmp, "scripts", "docker-local-replace.sh")), filepath.ToSlash(tmp)); err != nil {
		return err
	}
	// Ogni require interno deve essere sostituito da un replace locale: se il
	// Dockerfile non copia il modulo, lo script non lo trova e la build
	// scaricherebbe la pseudo-versione (codice vecchio) senza errori.
	out, err := exec.Command("go", "mod", "edit", "-json", filepath.Join(svc, "go.mod")).Output()
	if err != nil {
		return err
	}
	var gm struct {
		Require []struct{ Path string }
		Replace []struct{ Old struct{ Path string } }
	}
	if err := json.Unmarshal(out, &gm); err != nil {
		return err
	}
	replaced := map[string]bool{}
	for _, r := range gm.Replace {
		replaced[r.Old.Path] = true
	}
	for _, r := range gm.Require {
		if strings.HasPrefix(r.Path, "github.com/fathorMB/GitStack/") && !replaced[r.Path] {
			return fmt.Errorf("il require interno %s non ha un replace verso le sorgenti locali (il Dockerfile non le copia?)", r.Path)
		}
	}
	return run("go", "build", "./...")
}

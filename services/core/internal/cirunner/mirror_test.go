package cirunner_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/fathorMB/GitStack/services/core/internal/cirunner"
)

// git esegue git in dir con un'identita' fissa e senza configurazione dell'utente.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestMirrorActions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non disponibile")
	}
	mirror := t.TempDir()
	repo := filepath.Join(mirror, "actions", "github-script")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "action.yml"), []byte("name: finto\nruns:\n  using: composite\n  steps: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "action.yml")
	gitIn(t, repo, "commit", "-q", "-m", "m")
	gitIn(t, repo, "tag", "v5")

	work := t.TempDir()
	dest := filepath.Join(work, "workspace", ".gitstack-actions")
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull)
	workflow := []byte("jobs:\n  j:\n    steps:\n      - uses: actions/github-script@v5\n      - uses: ./locale\n      - uses: docker://alpine:3\n" +
		"      - uses: 'actions/github-script@v5'\n      - run: echo uses: actions/github-script@v5\n      - uses: actions/checkout@v4\n")

	got, err := cirunner.MirrorActions(context.Background(), workflow, mirror, "git", dest, env)
	if err != nil {
		t.Fatalf("MirrorActions: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "- uses: actions/github-script@v5") || strings.Contains(s, "'actions/github-script@v5'") {
		t.Fatalf("uses non riscritti:\n%s", s)
	}
	for _, want := range []string{"- uses: ./.gitstack-actions/actions__github-script__v5\n", "- uses: './.gitstack-actions/actions__github-script__v5'", "uses: ./locale", "uses: docker://alpine:3", "- run: echo uses: actions/github-script@v5", "- uses: actions/checkout@v4\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("manca %q in:\n%s", want, s)
		}
	}
	// L'action e' stata scaricata nel posto che il workflow indica, relativo al workspace.
	if _, err := os.Stat(filepath.Join(dest, "actions__github-script__v5", "action.yml")); err != nil {
		t.Errorf("action non scaricata: %v", err)
	}

	// Senza mirror il workflow resta com'e'.
	same, err := cirunner.MirrorActions(context.Background(), workflow, "", "git", dest, env)
	if err != nil || string(same) != string(workflow) {
		t.Fatalf("senza mirror: %v %q", err, same)
	}

	// Un'action che il mirror non ha e' un errore, mai un ripiego su github.com.
	if _, err := cirunner.MirrorActions(context.Background(), []byte("      - uses: actions/mancante@v1\n"), mirror, "git", dest, env); err == nil {
		t.Fatal("un'action assente dal mirror deve far fallire la preparazione")
	}
}

// Un ref che inizia con '-' non diventa un'opzione di git: la preparazione fallisce, senza scaricare nulla.
func TestMirrorActionsRifiutaRefConTrattino(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non disponibile")
	}
	mirror := t.TempDir()
	dest := filepath.Join(t.TempDir(), "workspace", ".gitstack-actions")
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull)
	workflow := []byte("      - uses: actions/x@--upload-pack=touch-pwned\n")
	_, err := cirunner.MirrorActions(context.Background(), workflow, mirror, "git", dest, env)
	if err == nil {
		t.Fatal("un ref con '-' iniziale deve far fallire la preparazione")
	}
	if !strings.Contains(err.Error(), "non puo' iniziare con '-'") {
		t.Errorf("errore inatteso: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "actions__x__--upload-pack_touch-pwned")); statErr == nil {
		t.Error("la cartella dell'action non doveva essere creata")
	}
}

// Senza actions/checkout act non copia il workspace e le action riscritte in locale non esisterebbero
// (GIT-393): il runner antepone il passo ai job che ne usano una, e solo a quelli.
func TestMirrorActionsAnteponeIlCheckoutSeServe(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non disponibile")
	}
	mirror := t.TempDir()
	repo := filepath.Join(mirror, "actions", "cache")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "action.yml"), []byte("name: finto\nruns:\n  using: composite\n  steps: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "action.yml")
	gitIn(t, repo, "commit", "-q", "-m", "m")
	gitIn(t, repo, "tag", "v4")

	dest := filepath.Join(t.TempDir(), "workspace", ".gitstack-actions")
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull)
	workflow := []byte("jobs:\n  senza:\n    steps:\n      - uses: actions/cache@v4\n      - run: echo ciao\n" +
		"  con:\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/cache@v4\n" +
		"  solorun:\n    steps:\n      - run: echo ciao\n")
	got, err := cirunner.MirrorActions(context.Background(), workflow, mirror, "git", dest, env)
	if err != nil {
		t.Fatalf("MirrorActions: %v", err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string `yaml:"uses"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(got, &wf); err != nil {
		t.Fatalf("il risultato non e' YAML valido: %v\n%s", err, got)
	}
	if s := wf.Jobs["senza"].Steps; len(s) != 3 || s[0].Uses != "actions/checkout@v4" || s[1].Uses != "./.gitstack-actions/actions__cache__v4" || s[2].Run != "echo ciao" {
		t.Errorf("job senza checkout: %+v\n%s", s, got)
	}
	if s := wf.Jobs["con"].Steps; len(s) != 2 || s[0].Uses != "actions/checkout@v4" {
		t.Errorf("job con checkout modificato: %+v", s)
	}
	if s := wf.Jobs["solorun"].Steps; len(s) != 1 {
		t.Errorf("job senza action riscritte modificato: %+v", s)
	}
}

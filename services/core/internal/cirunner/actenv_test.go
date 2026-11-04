package cirunner

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDotenvQuote(t *testing.T) {
	got := dotenvQuote("a\\b \"q\" $HOME ${X}\r\nz")
	want := `"a\\b \"q\" \$HOME \${X}\r\nz"`
	if got != want {
		t.Errorf("dotenvQuote = %s, voglio %s", got, want)
	}
}

func TestWriteDotenv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "s.env")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("VECCHIO=1\n"), 0o644); err != nil { // file di un job interrotto, aperto a tutti
		t.Fatal(err)
	}
	dropped, err := writeDotenv(p, map[string]string{"B": "2", "A": "1", "GITHUB_TOKEN": "x", "a-b": "y", "1x": "z"}, "GITHUB_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "A=\"1\"\nB=\"2\"\n" {
		t.Errorf("contenuto: %q", b)
	}
	if strings.Join(dropped, ",") != "1x,a-b" {
		t.Errorf("scartati: %v", dropped)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Errorf("permessi %v, voglio 0600", st.Mode().Perm())
		}
	}
}

// TestDotenvQuote_ConActVero: il valore scritto da dotenvQuote arriva ad act byte per byte, anche
// con a-capo CRLF, barre, virgolette, «$» e caratteri non ASCII. Gira solo se `act` è nel PATH
// (sul PC Windows del runner, o in locale): act legge il file con godotenv, che espande ${VAR} e
// toglie le barre in un valore non protetto, per questo l'escape.
func TestDotenvQuote_ConActVero(t *testing.T) {
	actBin, err := exec.LookPath("act")
	if err != nil {
		t.Skip("act non è nel PATH")
	}
	val := "a\\b\\\\c\\n \"q\" $HOME ${X} 's' #h \r\nriga2\ttab = é € `bt` %PATH%"
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	for _, d := range []string{filepath.Join(ws, ".git", "objects"), filepath.Join(ws, ".git", "refs", "heads")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for f, c := range map[string]string{
		filepath.Join(ws, ".git", "HEAD"):                 "ref: refs/heads/main\n",
		filepath.Join(ws, ".git", "refs", "heads", "main"): strings.Repeat("a", 40) + "\n",
		filepath.Join(ws, ".git", "config"):                "[core]\n\trepositoryformatversion = 0\n",
		filepath.Join(ws, "wf.yml"): "name: t\non: push\njobs:\n  j:\n    runs-on: self-hosted\n    steps:\n" +
			"      - run: printf '%s' \"$S\" | od -An -v -tx1 | tr -d ' \\n'; echo\n        shell: bash\n        env:\n          S: ${{ secrets.S }}\n",
	} {
		if err := os.WriteFile(f, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sf := filepath.Join(dir, "s.env")
	if _, err := writeDotenv(sf, map[string]string{"S": val}); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "e.env")
	_ = os.WriteFile(empty, nil, 0o600)
	cmd := exec.Command(actBin, "push", "-W", "wf.yml", "-j", "j", "-P", "self-hosted=-self-hosted", "--secret-file", sf, "--env-file", empty, "--pull=false")
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("act non esegue qui (serve bash): %v\n%s", err, out) // il banco Windows senza Git Bash
	}
	if want := hex.EncodeToString([]byte(val)); !bytes.Contains(out, []byte(want)) {
		t.Errorf("il segreto non è arrivato intatto.\nvoglio %s\n%s", want, out)
	}
}

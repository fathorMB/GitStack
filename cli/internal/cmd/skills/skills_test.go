package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/gsrepo"
)

type noGit struct{}

func (noGit) RemoteURL(context.Context, string) (string, error) { return "", gsrepo.ErrNoRemote }

var _ gsrepo.Git = noGit{}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// instance è un'istanza finta che serve index.json e lo zip.
type instance struct {
	srv    *httptest.Server
	zip    atomic.Value // []byte
	sha    atomic.Value // string da mettere in index.json
	zipHit atomic.Int32
}

func newInstance(t *testing.T, files map[string]string) *instance {
	t.Helper()
	in := &instance{}
	in.set(makeZip(t, files))
	mux := http.NewServeMux()
	mux.HandleFunc("/downloads/index.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"version":"sha-test","binaries":[],"skills":{"file":"gs-skills.zip","url":"/downloads/gs-skills.zip","sha256":%q}}`, in.sha.Load().(string))
	})
	mux.HandleFunc("/downloads/gs-skills.zip", func(w http.ResponseWriter, _ *http.Request) {
		in.zipHit.Add(1)
		_, _ = w.Write(in.zip.Load().([]byte))
	})
	in.srv = httptest.NewServer(mux)
	t.Cleanup(in.srv.Close)
	return in
}

func (in *instance) set(z []byte) {
	in.zip.Store(z)
	in.sha.Store(sumHex(z))
}

var baseFiles = map[string]string{
	"README.md":                       "root",
	"LICENSE":                         "apache",
	"gitstack-setup/SKILL.md":         "---\nname: gitstack-setup\ndescription: x\n---\nv1\n",
	"gitstack-setup/extra/a.txt":      "a",
	"gitstack-notifications/SKILL.md": "---\nname: gitstack-notifications\ndescription: y\n---\nv1\n",
}

type env struct {
	home, project string
	in            *instance
}

// setup crea home e progetto temporanei (con .git) e si sposta nel progetto.
func setup(t *testing.T, files map[string]string) *env {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	return &env{home: home, project: project, in: newInstance(t, files)}
}

func (e *env) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	io_, _, out, _ := cmdutil.Test()
	f := cmdutil.New("test", io_)
	f.Getenv = func(k string) string {
		if k == "HOME" || k == "USERPROFILE" {
			return e.home
		}
		return ""
	}
	f.Git = noGit{}
	f.HostnameFlag = e.in.srv.URL
	cmd := NewCmd(f)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallProgettoRilevaClaude(t *testing.T) {
	e := setup(t, baseFiles)
	if err := os.Mkdir(filepath.Join(e.project, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := e.run(t, "install")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	d := filepath.Join(e.project, ".claude", "skills")
	for _, p := range []string{"gitstack-setup/SKILL.md", "gitstack-setup/extra/a.txt", "gitstack-notifications/SKILL.md", "gitstack-setup/" + VersionFile} {
		if !exists(filepath.Join(d, filepath.FromSlash(p))) {
			t.Errorf("manca %s", p)
		}
	}
	if exists(filepath.Join(d, "README.md")) || exists(filepath.Join(e.project, ".agents")) {
		t.Error("ha scritto file di radice o per un agente non presente")
	}
	m, ok := readMeta(filepath.Join(d, "gitstack-setup"))
	if !ok || m.SHA256 != e.in.sha.Load().(string) || m.Version != "sha-test" {
		t.Errorf("meta: %+v %v", m, ok)
	}
}

func TestInstallRilevaCodexDaHomeEPerUtente(t *testing.T) {
	e := setup(t, baseFiles)
	if err := os.Mkdir(filepath.Join(e.home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	// progetto: l'agente presente in home basta, destinazione nel progetto
	if out, err := e.run(t, "install"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !exists(filepath.Join(e.project, ".agents", "skills", "gitstack-setup", "SKILL.md")) {
		t.Error("manca l'installazione di Codex nel progetto")
	}
	if exists(filepath.Join(e.project, ".claude")) {
		t.Error("Claude Code non è presente e non deve essere installato")
	}
	// utente
	if out, err := e.run(t, "install", "--user"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !exists(filepath.Join(e.home, ".agents", "skills", "gitstack-notifications", "SKILL.md")) {
		t.Error("manca l'installazione a livello utente")
	}
}

func TestInstallAgentEsplicitoEDue(t *testing.T) {
	e := setup(t, baseFiles)
	if out, err := e.run(t, "install", "--agent", "claude,codex"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, d := range []string{".claude/skills", ".agents/skills"} {
		if !exists(filepath.Join(e.project, filepath.FromSlash(d), "gitstack-setup", "SKILL.md")) {
			t.Errorf("manca %s", d)
		}
	}
}

func TestInstallSenzaAgentiOFuoriDalRepo(t *testing.T) {
	e := setup(t, baseFiles)
	_, err := e.run(t, "install")
	var ue *cmdutil.UsageError
	if !asUsage(err, &ue) || !strings.Contains(err.Error(), "--agent") {
		t.Errorf("senza agenti: %v", err)
	}
	if _, err := e.run(t, "install", "--agent", "vim"); !asUsage(err, &ue) {
		t.Errorf("agente sconosciuto: %v", err)
	}
	t.Chdir(t.TempDir())
	if _, err := e.run(t, "install", "--agent", "claude"); !asUsage(err, &ue) || !strings.Contains(err.Error(), "repo git") {
		t.Errorf("fuori dal repo: %v", err)
	}
	// --user non richiede il repo
	if out, err := e.run(t, "install", "--agent", "claude", "--user"); err != nil {
		t.Errorf("--user fuori dal repo: %v\n%s", err, out)
	}
}

func asUsage(err error, target **cmdutil.UsageError) bool {
	for err != nil {
		if ue, ok := err.(*cmdutil.UsageError); ok {
			*target = ue
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestInstallChecksumSbagliatoNonScriveNiente(t *testing.T) {
	e := setup(t, baseFiles)
	e.in.sha.Store(strings.Repeat("0", 64))
	out, err := e.run(t, "install", "--agent", "claude", "--agents-md")
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("atteso errore di sha256: %v\n%s", err, out)
	}
	for _, d := range []string{".claude", ".agents", "AGENTS.md"} {
		if exists(filepath.Join(e.project, d)) {
			t.Errorf("%s scritto nonostante il checksum sbagliato", d)
		}
	}
}

func TestInstallZipMalformato(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"percorso con ..": {"../evil/SKILL.md": "x"},
		"senza SKILL.md":  {"gitstack-x/readme.md": "x"},
		"nome non valido": {"Brutto Nome/SKILL.md": "x"},
		"nessuna skill":   {"README.md": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			e := setup(t, files)
			if _, err := e.run(t, "install", "--agent", "claude"); err == nil {
				t.Fatal("atteso errore")
			}
			if exists(filepath.Join(e.project, ".claude")) {
				t.Error("scritto qualcosa")
			}
		})
	}
}

func TestInstallNonSovrascriveCartelleAltrui(t *testing.T) {
	e := setup(t, baseFiles)
	own := filepath.Join(e.project, ".claude", "skills", "gitstack-setup")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "SKILL.md"), []byte("mia"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run(t, "install", "--agent", "claude"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("atteso rifiuto: %v", err)
	}
	if read(t, filepath.Join(own, "SKILL.md")) != "mia" {
		t.Error("file sovrascritto senza --force")
	}
	if exists(filepath.Join(e.project, ".claude", "skills", "gitstack-notifications")) {
		t.Error("installazione parziale")
	}
	if out, err := e.run(t, "install", "--agent", "claude", "--force"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(read(t, filepath.Join(own, "SKILL.md")), "v1") {
		t.Error("--force non ha sovrascritto")
	}
}

func TestUpdate(t *testing.T) {
	e := setup(t, baseFiles)
	if _, err := e.run(t, "update"); err == nil || !strings.Contains(err.Error(), "gs skills install") {
		t.Fatalf("update senza installazione: %v", err)
	}
	if out, err := e.run(t, "install", "--agent", "claude"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	hits := e.in.zipHit.Load()
	out, err := e.run(t, "update")
	if err != nil || !strings.Contains(out, "già aggiornato") {
		t.Fatalf("update a pari versione: %v\n%s", err, out)
	}
	_ = hits

	// l'istanza ha una versione nuova: SKILL.md cambia, una skill nuova, una file in meno
	next := map[string]string{
		"gitstack-setup/SKILL.md":         "---\nname: gitstack-setup\ndescription: x\n---\nv2\n",
		"gitstack-notifications/SKILL.md": "---\nname: gitstack-notifications\ndescription: y\n---\nv2\n",
		"gitstack-new/SKILL.md":           "---\nname: gitstack-new\ndescription: z\n---\nv2\n",
	}
	e.in.set(makeZip(t, next))
	out, err = e.run(t, "update")
	if err != nil || !strings.Contains(out, "aggiornate") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	d := filepath.Join(e.project, ".claude", "skills")
	if !strings.Contains(read(t, filepath.Join(d, "gitstack-setup", "SKILL.md")), "v2") {
		t.Error("SKILL.md non aggiornato")
	}
	if exists(filepath.Join(d, "gitstack-setup", "extra", "a.txt")) {
		t.Error("il file tolto dal pacchetto è rimasto")
	}
	if !exists(filepath.Join(d, "gitstack-new", "SKILL.md")) {
		t.Error("skill nuova non installata")
	}
	if m, _ := readMeta(filepath.Join(d, "gitstack-setup")); m.SHA256 != e.in.sha.Load().(string) {
		t.Error("versione non aggiornata")
	}
	// e adesso è allineato
	if out, err := e.run(t, "update"); err != nil || !strings.Contains(out, "già aggiornato") {
		t.Fatalf("secondo update: %v\n%s", err, out)
	}
}

func TestUpdateUtenteNonTocca_Progetto(t *testing.T) {
	e := setup(t, baseFiles)
	if _, err := e.run(t, "install", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	e.in.set(makeZip(t, map[string]string{"gitstack-setup/SKILL.md": "---\nname: gitstack-setup\ndescription: x\n---\nv9\n"}))
	if _, err := e.run(t, "update", "--user"); err == nil {
		t.Fatal("nessuna skill a livello utente: atteso errore")
	}
	if strings.Contains(read(t, filepath.Join(e.project, ".agents", "skills", "gitstack-setup", "SKILL.md")), "v9") {
		t.Error("update --user ha toccato il progetto")
	}
}

func TestAgentsMDIdempotenteLFeCRLF(t *testing.T) {
	names := []string{"gitstack-setup", "gitstack-notifications"}
	cases := map[string]string{
		"vuoto":         "",
		"lf":            "# Progetto\n\nRegole.\n",
		"crlf":          "# Progetto\r\n\r\nRegole.\r\n",
		"senza eol":     "# Progetto",
		"crlf senza eo": "# P\r\nx",
	}
	for name, orig := range cases {
		t.Run(name, func(t *testing.T) {
			once, err := UpdateAgentsMD([]byte(orig), names)
			if err != nil {
				t.Fatal(err)
			}
			twice, err := UpdateAgentsMD(once, names)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(once, twice) {
				t.Errorf("non idempotente:\n%q\n%q", once, twice)
			}
			if c := strings.Count(string(once), MarkerStart); c != 1 {
				t.Errorf("sezioni: %d", c)
			}
			if !strings.HasPrefix(string(once), orig) {
				t.Errorf("il contenuto originale non è conservato:\n%q", once)
			}
			if strings.Contains(orig, "\r\n") {
				if strings.Contains(strings.ReplaceAll(string(once), "\r\n", ""), "\n") {
					t.Error("LF dentro un file CRLF")
				}
			} else if strings.Contains(string(once), "\r") {
				t.Error("CRLF dentro un file LF")
			}
		})
	}
}

func TestAgentsMDSostituisceLaSezioneEConservaIlResto(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		before := "# Titolo" + eol + eol + "testo con  spazi finali  " + eol + eol
		after := eol + eol + "## Altro" + eol + "- a" + eol + "ultima riga senza eol"
		old := before + MarkerStart + eol + "vecchio" + eol + MarkerEnd + after
		got, err := UpdateAgentsMD([]byte(old), []string{"gitstack-setup"})
		if err != nil {
			t.Fatal(err)
		}
		s := string(got)
		if !strings.HasPrefix(s, before+MarkerStart) || !strings.HasSuffix(s, MarkerEnd+after) {
			t.Errorf("il resto del file è cambiato (eol %q):\n%q", eol, s)
		}
		if strings.Contains(s, "vecchio") || !strings.Contains(s, "`gitstack-setup`") {
			t.Errorf("sezione non sostituita:\n%q", s)
		}
		again, _ := UpdateAgentsMD(got, []string{"gitstack-setup"})
		if !bytes.Equal(got, again) {
			t.Error("non idempotente")
		}
	}
}

func TestAgentsMDDelimitatoriRotti(t *testing.T) {
	for name, in := range map[string]string{
		"solo inizio": MarkerStart + "\nx\n",
		"solo fine":   "x\n" + MarkerEnd + "\n",
		"invertiti":   MarkerEnd + "\n" + MarkerStart + "\n",
		"doppia":      MarkerStart + "\n" + MarkerEnd + "\n" + MarkerStart + "\n" + MarkerEnd + "\n",
	} {
		if _, err := UpdateAgentsMD([]byte(in), nil); err == nil {
			t.Errorf("%s: atteso errore", name)
		}
	}
}

func TestAgentsMDDaComando(t *testing.T) {
	e := setup(t, baseFiles)
	p := filepath.Join(e.project, "AGENTS.md")
	if err := os.WriteFile(p, []byte("# Mio\r\ntesto\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := e.run(t, "install", "--agent", "claude", "--agents-md"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	first := read(t, p)
	out, err := e.run(t, "install", "--agent", "claude", "--agents-md")
	if err != nil || !strings.Contains(out, "AGENTS.md già aggiornato") {
		t.Fatalf("%v\n%s", err, out)
	}
	if read(t, p) != first || strings.Count(first, MarkerStart) != 1 || !strings.HasPrefix(first, "# Mio\r\ntesto\r\n") {
		t.Errorf("AGENTS.md:\n%q", first)
	}
	if _, err := e.run(t, "install", "--agent", "claude", "--user", "--agents-md"); err == nil {
		t.Error("--agents-md con --user dovrebbe essere un errore")
	}
}

func TestRilevamentoAgenti(t *testing.T) {
	base := t.TempDir()
	if got := detectAgents(base); len(got) != 0 {
		t.Errorf("nessun marcatore: %v", got)
	}
	if err := os.Mkdir(filepath.Join(base, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := detectAgents(base)
	if len(got) != 1 || got[0].Name != "codex" {
		t.Errorf(".agents: %v", got)
	}
	if err := os.Mkdir(filepath.Join(base, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectAgents(base); len(got) != 2 {
		t.Errorf("entrambi: %v", got)
	}
	// un file con quel nome non è un agente
	b2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(b2, ".claude"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectAgents(b2); len(got) != 0 {
		t.Errorf("file: %v", got)
	}
}

// TestPacchettoVero comprime la vera cartella skills/ come fa
// scripts/build-gs-dist.sh (percorsi relativi, README e LICENSE in radice) e
// controlla che il pacchetto si legga: 5 skills, ognuna con SKILL.md.
func TestPacchettoVero(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "skills")
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := unzipSkills(makeZip(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Names) != 5 {
		t.Errorf("skills: %v", b.Names)
	}
}

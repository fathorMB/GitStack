package gitread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// Limiti del diff (B6). Sono costanti documentate nel README del servizio.
const (
	// CollapseLines: un file con più righe cambiate (aggiunte + tolte) è
	// «chiuso di default».
	CollapseLines = 500
	// MaxFiles: oltre, il commit porta solo l'elenco dei file.
	MaxFiles = 300
	// MaxLines: righe cambiate in totale oltre le quali il commit porta solo
	// l'elenco dei file.
	MaxLines = 20000
	// MaxFilePatchBytes: un patch più lungo è troncato.
	MaxFilePatchBytes = 1 << 20
)

// Motivi per cui un file è chiuso di default.
const (
	ReasonLarge     = "large"     // più di CollapseLines righe cambiate
	ReasonLock      = "lock"      // file di lock delle dipendenze
	ReasonGenerated = "generated" // file generato o minificato
)

// lockFiles sono i nomi (ultimo segmento del percorso) dei file di lock.
var lockFiles = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "pnpm-lock.yaml": true,
	"yarn.lock": true, "bun.lock": true, "bun.lockb": true,
	"go.sum": true, "go.work.sum": true,
	"cargo.lock": true, "composer.lock": true, "gemfile.lock": true,
	"poetry.lock": true, "pipfile.lock": true, "uv.lock": true,
	"packages.lock.json": true, "gradle.lockfile": true, "pubspec.lock": true,
	"mix.lock": true, "podfile.lock": true, "flake.lock": true, "pdm.lock": true,
	"deno.lock": true,
}

// generatedSuffixes sono i suffissi (minuscoli) dei file generati o minificati.
var generatedSuffixes = []string{
	".min.js", ".min.css", ".min.mjs", ".js.map", ".css.map",
	".pb.go", ".pb.gw.go", "_pb2.py", "_pb2_grpc.py", ".pb.cc", ".pb.h",
	".designer.cs", ".g.cs", ".g.dart", ".freezed.dart",
}

// CollapseReason dice perché un file è chiuso di default ("" se non lo è).
// Lock e generati hanno la precedenza sulla dimensione.
func CollapseReason(filePath string, changedLines int) string {
	base := strings.ToLower(path.Base(filePath))
	if lockFiles[base] {
		return ReasonLock
	}
	for _, suf := range generatedSuffixes {
		if strings.HasSuffix(base, suf) {
			return ReasonGenerated
		}
	}
	if changedLines > CollapseLines {
		return ReasonLarge
	}
	return ""
}

// FileDiff è un FileDiff del contratto, più i campi di B6.
type FileDiff struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
	// Truncated: il patch è omesso o parziale per un limite (1 MB per file,
	// 300 file, 20 000 righe).
	Truncated bool `json:"truncated"`
	// Collapsed: «chiuso di default» nella UI; CollapseReason dice perché
	// (large, lock, generated).
	Collapsed      bool   `json:"collapsed"`
	CollapseReason string `json:"collapseReason,omitempty"`
	Patch          string `json:"patch,omitempty"`
}

// CommitDetail è un CommitDetail del contratto, più i campi di B6.
type CommitDetail struct {
	Commit       Commit     `json:"commit"`
	Files        []FileDiff `json:"files"`
	FilesChanged int        `json:"filesChanged"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	// Truncated: i file sono oltre 300 (ne sono elencati 300) o i patch sono
	// stati omessi per i limiti.
	Truncated bool `json:"truncated"`
	// ListOnly: oltre 300 file o 20 000 righe, solo l'elenco dei file con
	// le righe aggiunte e tolte, senza patch.
	ListOnly bool `json:"listOnly"`
	// IgnoreWhitespace: il diff è stato calcolato ignorando gli spazi.
	IgnoreWhitespace bool `json:"ignoreWhitespace"`
	// Tags sono i tag che puntano al commit (anche annotati), in ordine
	// alfabetico.
	Tags []string `json:"tags"`
}

// Commit torna il dettaglio del commit con il diff per file. Il diff è
// contro il primo genitore (merge compresi) o contro l'albero vuoto per il
// commit iniziale.
func (s *Service) Commit(ctx context.Context, repoID, sha string, ignoreWhitespace bool) (*CommitDetail, error) {
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	full, err := gitref.ResolveCommit(ctx, s.run, dir, sha)
	if err != nil {
		return nil, err
	}
	cm, err := s.commitsByID(ctx, dir, []string{full})
	if err != nil {
		return nil, err
	}
	c, ok := cm[full]
	if !ok {
		return nil, gitref.ErrRefNotFound
	}
	base, err := s.baseTree(ctx, dir, c)
	if err != nil {
		return nil, err
	}

	diffArgs := func(extra ...string) []string {
		a := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M"}
		if ignoreWhitespace {
			a = append(a, "-w")
		}
		a = append(a, extra...)
		return append(a, base, full, "--")
	}
	ns, err := s.run.Output(ctx, dir, nil, diffArgs("--name-status", "-z")...)
	if err != nil {
		return nil, err
	}
	nu, err := s.run.Output(ctx, dir, nil, diffArgs("--numstat", "-z")...)
	if err != nil {
		return nil, err
	}
	files, err := parseFileList(ns, nu)
	if err != nil {
		return nil, err
	}

	d := &CommitDetail{Commit: c, FilesChanged: len(files), IgnoreWhitespace: ignoreWhitespace, Files: []FileDiff{}}
	for _, f := range files {
		d.Additions += f.Additions
		d.Deletions += f.Deletions
	}
	d.ListOnly = len(files) > MaxFiles || d.Additions+d.Deletions > MaxLines

	if !d.ListOnly {
		out, err := s.run.Output(ctx, dir, nil, diffArgs("-p")...)
		switch {
		case err == nil:
			patches := splitPatches(out)
			if len(patches) == len(files) {
				for i := range files {
					files[i].Patch, files[i].Truncated = clipPatch(patches[i], files[i].Binary)
				}
			} else {
				d.ListOnly = true
			}
		case errors.Is(err, gitrun.ErrOutputTooLarge):
			d.ListOnly = true
		default:
			return nil, err
		}
	}

	if len(files) > MaxFiles {
		files = files[:MaxFiles]
		d.Truncated = true
	}
	for i := range files {
		f := &files[i]
		if d.ListOnly {
			f.Patch = ""
			f.Truncated = !f.Binary
			d.Truncated = true
		}
		if r := CollapseReason(f.Path, f.Additions+f.Deletions); r != "" {
			f.Collapsed, f.CollapseReason = true, r
		}
	}
	d.Files = files

	tags, err := s.run.Output(ctx, dir, nil, "for-each-ref", "--points-at="+full, "--sort=refname", "--format=%(refname:lstrip=2)", "refs/tags")
	if err != nil {
		return nil, err
	}
	d.Tags = []string{}
	for _, l := range strings.Split(string(tags), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			d.Tags = append(d.Tags, l)
		}
	}
	return d, nil
}

// baseTree è l'oggetto contro cui si calcola il diff: il primo genitore, o
// l'albero vuoto per il commit iniziale.
func (s *Service) baseTree(ctx context.Context, dir string, c Commit) (string, error) {
	if len(c.Parents) > 0 {
		return c.Parents[0], nil
	}
	out, err := s.run.Output(ctx, dir, bytes.NewReader(nil), "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func statusName(code string) string {
	switch code[0] {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	default: // M, T (cambio di tipo)
		return "modified"
	}
}

// parseFileList unisce `--name-status -z` e `--numstat -z` della stessa
// differenza (stesso ordine delle voci).
func parseFileList(nameStatus, numstat []byte) ([]FileDiff, error) {
	var files []FileDiff
	tok := splitNUL(nameStatus)
	for i := 0; i < len(tok); {
		code := tok[i]
		if code == "" {
			return nil, fmt.Errorf("gitread: name-status inatteso")
		}
		n := 1
		if code[0] == 'R' || code[0] == 'C' {
			n = 2
		}
		if i+1+n > len(tok) {
			return nil, fmt.Errorf("gitread: name-status troncato")
		}
		f := FileDiff{Status: statusName(code)}
		if n == 2 {
			f.OldPath, f.Path = tok[i+1], tok[i+2]
		} else {
			f.Path = tok[i+1]
		}
		files = append(files, f)
		i += 1 + n
	}

	type stat struct {
		path   string
		a, d   int
		binary bool
	}
	var stats []stat
	tok = splitNUL(numstat)
	for i := 0; i < len(tok); {
		parts := strings.SplitN(tok[i], "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("gitread: numstat inatteso %q", tok[i])
		}
		i++
		st := stat{path: parts[2]}
		if parts[2] == "" { // rinomina: seguono vecchio e nuovo percorso
			if i+2 > len(tok) {
				return nil, fmt.Errorf("gitread: numstat troncato")
			}
			st.path = tok[i+1]
			i += 2
		}
		if parts[0] == "-" && parts[1] == "-" {
			st.binary = true
		} else {
			a, err1 := strconv.Atoi(parts[0])
			d, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("gitread: numstat inatteso %q", tok[i-1])
			}
			st.a, st.d = a, d
		}
		stats = append(stats, st)
	}
	// Con «ignora spazi» name-status elenca anche i file che cambiano solo
	// negli spazi, numstat e patch no: si tengono solo i file con statistiche.
	var res []FileDiff
	si := 0
	for _, f := range files {
		if si < len(stats) && stats[si].path == f.Path {
			f.Additions, f.Deletions, f.Binary = stats[si].a, stats[si].d, stats[si].binary
			res = append(res, f)
			si++
		}
	}
	if si != len(stats) {
		return nil, fmt.Errorf("gitread: numstat (%d) e name-status (%d) non combaciano", len(stats), len(files))
	}
	return res, nil
}

func splitNUL(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// splitPatches divide l'output di `git diff -p` in un blocco per file, nello
// stesso ordine, tenendo solo gli hunk (dal primo `@@`). I file senza hunk
// (binari, solo rinomina o cambio di modo) danno stringa vuota.
func splitPatches(out []byte) []string {
	var blocks []string
	var cur []string
	started := false
	flush := func() {
		if !started {
			return
		}
		blocks = append(blocks, hunks(cur))
	}
	for _, line := range strings.SplitAfter(string(out), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			cur, started = nil, true
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return blocks
}

func hunks(lines []string) string {
	for i, l := range lines {
		if strings.HasPrefix(l, "@@ ") {
			return strings.Join(lines[i:], "")
		}
	}
	return ""
}

// clipPatch porta il patch al massimo di MaxFilePatchBytes, tagliando a fine
// riga; torna se ha dovuto troncare.
func clipPatch(p string, binary bool) (string, bool) {
	if binary {
		return "", false
	}
	if len(p) <= MaxFilePatchBytes {
		return p, false
	}
	cut := strings.LastIndexByte(p[:MaxFilePatchBytes], '\n') + 1
	return p[:cut], true
}

// Download è il diff completo di un commit, da scaricare in streaming.
type Download struct {
	// SHA è lo sha completo del commit.
	SHA string
	// Filename è il nome suggerito, es. `a1b2c3d.diff`.
	Filename string

	s    *Service
	dir  string
	args []string
}

// Formati di download.
const (
	FormatDiff  = "diff"
	FormatPatch = "patch"
)

// PrepareDownload risolve lo sha e prepara il download in formato diff (git
// diff --binary, accettato da `git apply`) o patch (git format-patch, accettato
// da `git apply` e `git am`). Gli errori (sha non valido, commit mancante)
// escono qui, prima che si scriva una sola riga di risposta.
func (s *Service) PrepareDownload(ctx context.Context, repoID, sha, format string, ignoreWhitespace bool) (*Download, error) {
	if format != FormatDiff && format != FormatPatch {
		return nil, fmt.Errorf("%w: formato", ErrInvalidInput)
	}
	dir, err := s.repoDir(repoID)
	if err != nil {
		return nil, err
	}
	full, err := gitref.ResolveCommit(ctx, s.run, dir, sha)
	if err != nil {
		return nil, err
	}
	cm, err := s.commitsByID(ctx, dir, []string{full})
	if err != nil {
		return nil, err
	}
	c, ok := cm[full]
	if !ok {
		return nil, gitref.ErrRefNotFound
	}
	d := &Download{SHA: full, Filename: full[:12] + "." + format, s: s, dir: dir}
	ws := []string{}
	if ignoreWhitespace {
		ws = []string{"-w"}
	}
	switch {
	case format == FormatDiff:
		base, err := s.baseTree(ctx, dir, c)
		if err != nil {
			return nil, err
		}
		d.args = append(append([]string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--binary", "-M"}, ws...), base, full, "--")
	case len(c.Parents) > 1:
		// format-patch salta i merge: messaggio in forma di email e diff
		// contro il primo genitore.
		d.args = append(append([]string{"log", "-1", "-m", "--first-parent", "--no-color", "--no-ext-diff", "--no-textconv", "--format=email", "--binary", "-p", "-M"}, ws...), full, "--")
	default:
		d.args = append(append([]string{"format-patch", "--stdout", "--root", "-1", "--binary", "--no-color", "--no-ext-diff", "--no-textconv", "-M"}, ws...), full, "--")
	}
	return d, nil
}

// WriteTo scrive il contenuto su w man mano che git lo produce (nessun buffer
// in memoria), con il timeout dei comandi in streaming.
func (d *Download) WriteTo(ctx context.Context, w io.Writer) error {
	return d.s.run.Stream(ctx, d.dir, w, d.args...)
}

// Package backup realizza `gitstack backup` e `gitstack restore` (D19): un
// solo archivio con il dump di Postgres (schemi identity e core), i repo
// bare di git-data, gli allegati, i Secret e la configurazione
// dell'installazione.
//
// Coerenza (decisione del CTO su GIT-145): una breve finestra di sola
// lettura. Il backup porta a 0 le repliche degli ingressi che scrivono,
// gateway (API e UI) e git (push HTTP e SSH), aspetta che i pod siano
// terminati, copia database e volumi e riporta le repliche ai valori di
// prima, sempre (anche su errore o interruzione).
package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// FormatVersion è la versione del formato dell'archivio.
const FormatVersion = 1

// Nomi dei file dentro l'archivio.
const (
	ManifestName    = "manifest.json"
	DatabaseName    = "database.sql"
	GitDataName     = "git-data.tar"
	AttachmentsName = "attachments.tar"
	SecretsName     = "secrets.json"
	ConfigPrefix    = "config/"
	// ArchivePrefix inizia il nome di ogni archivio prodotto.
	ArchivePrefix = "gitstack-backup-"
)

// Componenti che scrivono: si fermano durante il backup.
var writers = []string{"gateway", "git"}

// Componenti da fermare in restore (tutto ciò che usa database o volumi).
var restoreStops = []string{"gateway", "git", "core", "identity"}

// FileSum è una voce del manifest.
type FileSum struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest descrive l'archivio. È la prima voce del tar.
type Manifest struct {
	FormatVersion int       `json:"format_version"`
	Version       string    `json:"version"` // tag immagine del server (image_tag)
	Commit        string    `json:"commit"`  // commit, ricavato da sha-<commit>
	BinaryVersion string    `json:"binary_version"`
	CreatedAt     time.Time `json:"created_at"`
	Release       string    `json:"release"`
	Namespace     string    `json:"namespace"`
	Files         []FileSum `json:"files"`
}

// RefusedError è un rifiuto motivato (versione diversa, archivio corrotto,
// chiave mancante): il chiamante lo traduce in un codice di uscita dedicato.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

func refused(format string, a ...any) error { return &RefusedError{fmt.Sprintf(format, a...)} }

// ServingError: i dati sono ripristinati e i Deployment sono pronti, ma
// l'istanza non risponde attraverso l'Ingress entro il timeout. Il chiamante
// lo traduce in un codice di uscita dedicato.
type ServingError struct{ Err error }

func (e *ServingError) Error() string {
	return "i dati sono ripristinati ma l'istanza non risponde attraverso l'Ingress: " + e.Err.Error() +
		"\nControlla con: gitstack status"
}

func (e *ServingError) Unwrap() error { return e.Err }

// Options sono gli ingressi di Backup e Restore.
type Options struct {
	Cfg     *config.Config
	Cluster Cluster
	// BinaryVersion è la versione del binario gitstack.
	BinaryVersion string
	// ConfigDir è la cartella della configurazione dell'installazione
	// (default /etc/gitstack): vi si cerca anche la chiave della CA.
	ConfigDir string
	// ConfigFile è il file di configurazione in uso; non si sovrascrive in
	// restore (l'installer lo ha già scritto per la nuova installazione).
	ConfigFile string
	// PublishTLS, in restore con TLS internal o custom, riallinea al
	// certificato e alla CA ripristinati in ConfigDir/tls il Secret
	// <release>-tls e il ConfigMap <release>-ca (gitstack-tls ensure).
	// Impostato da internal/cli; nil = niente da fare.
	PublishTLS func(ctx context.Context) error
	// WaitServing, in restore, è l'ultimo passo prima del completamento:
	// aspetta che l'istanza risponda attraverso l'Ingress (/api/healthz e
	// /downloads/ca.crt) o scade con un errore. nil = nessuna attesa.
	WaitServing func(ctx context.Context) error
	// DestDir sostituisce cfg.Backup.Destination.
	DestDir string
	// Key è la chiave di cifratura (backup, opzionale) o di decifratura.
	Key []byte
	// Log riceve i messaggi di avanzamento: mai dati degli utenti.
	Log io.Writer
	// Now è l'orologio (test).
	Now func() time.Time
	// Retention sovrascrive cfg.Backup.Retention quando > 0.
	Retention int
}

func (o *Options) logf(format string, a ...any) {
	if o.Log != nil {
		_, _ = fmt.Fprintf(o.Log, format+"\n", a...)
	}
}

func (o *Options) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

func (o *Options) dest() string {
	if o.DestDir != "" {
		return o.DestDir
	}
	return o.Cfg.Backup.Destination
}

func (o *Options) deployment(component string) string { return o.Cfg.Release + "-" + component }

// Result è l'esito di un backup.
type Result struct {
	Path     string
	SHA256   string
	Manifest *Manifest
	// Window è la durata della finestra di sola lettura.
	Window time.Duration
}

const (
	dumpCmd = `exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --schema=identity --schema=core --no-comments`
	psqlCmd = `exec psql -q -v ON_ERROR_STOP=1 --single-transaction -U "$POSTGRES_USER" -d "$POSTGRES_DB"`
)

// stager scrive i file dell'archivio in una cartella di lavoro, calcolando
// dimensione e SHA-256 mentre scrive.
type stager struct {
	dir   string
	files []FileSum
}

type countHash struct {
	w io.Writer
	h interface {
		io.Writer
		Sum([]byte) []byte
	}
	n int64
}

func (c *countHash) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	_, _ = c.h.Write(p[:n])
	c.n += int64(n)
	return n, err
}

func (s *stager) write(name string, fill func(w io.Writer) error) error {
	p := filepath.Join(s.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ch := &countHash{w: f, h: sha256.New()}
	ferr := fill(ch)
	if cerr := f.Close(); ferr == nil {
		ferr = cerr
	}
	if ferr != nil {
		return ferr
	}
	s.files = append(s.files, FileSum{Name: name, Size: ch.n, SHA256: hex.EncodeToString(ch.h.Sum(nil))})
	return nil
}

// Backup produce l'archivio. In caso di errore o interruzione le repliche
// dei componenti fermati sono comunque riportate ai valori di prima.
func Backup(ctx context.Context, o *Options) (res *Result, err error) {
	if o.Cfg.ImageTag == "" {
		return nil, errors.New("image_tag assente dal config: la versione del server non è nota")
	}
	dest := o.dest()
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return nil, fmt.Errorf("cartella dei backup: %w", err)
	}
	if err := os.Chmod(dest, 0o700); err != nil {
		return nil, fmt.Errorf("cartella dei backup: %w", err)
	}
	stage, err := os.MkdirTemp(dest, ".staging-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	st := &stager{dir: stage}

	// Percorsi dei volumi e Secret si leggono prima di fermare qualsiasi cosa:
	// un errore qui non costa nemmeno un secondo di indisponibilità.
	gitPath, err := o.Cluster.VolumePath(ctx, o.Cfg.Release+"-git-data")
	if err != nil {
		return nil, fmt.Errorf("volume dei repo (git-data): %w", err)
	}
	attPath, err := o.Cluster.VolumePath(ctx, o.Cfg.Release+"-attachments-data")
	hasAtt := true
	if errors.Is(err, ErrNotFound) {
		hasAtt = false
		o.logf("volume degli allegati assente: non incluso")
	} else if err != nil {
		return nil, fmt.Errorf("volume degli allegati: %w", err)
	}
	secrets, err := o.Cluster.Secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("lettura dei Secret: %w", err)
	}

	// Finestra di sola lettura.
	saved := map[string]int{}
	restored := false
	restoreReplicas := func() error {
		if restored {
			return nil
		}
		// Contesto nuovo: l'interruzione (Ctrl-C) non deve impedire di
		// riaccendere gli ingressi.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer cancel()
		var errs []error
		for _, c := range writers {
			n, ok := saved[c]
			if !ok {
				continue
			}
			if e := o.Cluster.Scale(rctx, o.deployment(c), n); e != nil {
				errs = append(errs, fmt.Errorf("ripristino delle repliche di %s: %w", c, e))
				continue
			}
			delete(saved, c)
		}
		if len(errs) == 0 {
			restored = true
		}
		return errors.Join(errs...)
	}
	defer func() {
		if rerr := restoreReplicas(); rerr != nil {
			err = errors.Join(err, fmt.Errorf("ATTENZIONE: gli ingressi potrebbero essere ancora fermi, riportali a mano con kubectl scale: %w", rerr))
		}
	}()

	var windowStart time.Time
	for _, c := range writers {
		n, found, e := o.Cluster.Replicas(ctx, o.deployment(c))
		if e != nil {
			return nil, fmt.Errorf("repliche di %s: %w", c, e)
		}
		if !found || n == 0 {
			continue
		}
		saved[c] = n
	}
	o.logf("finestra di sola lettura: fermo %s", strings.Join(sortedKeys(saved), ", "))
	windowStart = time.Now()
	for _, c := range writers {
		if _, ok := saved[c]; !ok {
			continue
		}
		if e := o.Cluster.Scale(ctx, o.deployment(c), 0); e != nil {
			return nil, fmt.Errorf("arresto di %s: %w", c, e)
		}
	}
	for _, c := range writers {
		if _, ok := saved[c]; !ok {
			continue
		}
		if e := o.Cluster.WaitStopped(ctx, c); e != nil {
			return nil, fmt.Errorf("attesa dell'arresto di %s: %w", c, e)
		}
	}

	o.logf("dump di Postgres (schemi identity e core)")
	if e := st.write(DatabaseName, func(w io.Writer) error {
		return o.Cluster.PGExec(ctx, nil, w, "sh", "-c", dumpCmd)
	}); e != nil {
		return nil, fmt.Errorf("dump del database: %w", e)
	}
	o.logf("copia dei repo (git-data)")
	if e := st.write(GitDataName, func(w io.Writer) error { return tarDir(w, gitPath) }); e != nil {
		return nil, fmt.Errorf("copia di git-data: %w", e)
	}
	if hasAtt {
		o.logf("copia degli allegati")
		if e := st.write(AttachmentsName, func(w io.Writer) error { return tarDir(w, attPath) }); e != nil {
			return nil, fmt.Errorf("copia degli allegati: %w", e)
		}
	}

	if e := restoreReplicas(); e != nil {
		return nil, e
	}
	window := time.Since(windowStart)
	o.logf("finestra di sola lettura chiusa dopo %.1f s: ingressi riavviati", window.Seconds())

	// Da qui in poi non serve più fermare niente.
	sb, err := json.Marshal(secrets)
	if err != nil {
		return nil, err
	}
	if e := st.write(SecretsName, func(w io.Writer) error { _, e := w.Write(sb); return e }); e != nil {
		return nil, e
	}
	nconf, err := o.stageConfig(st)
	if err != nil {
		return nil, fmt.Errorf("configurazione: %w", err)
	}
	o.logf("incluse %d Secret e %d file di configurazione", len(secrets), nconf)

	m := &Manifest{
		FormatVersion: FormatVersion,
		Version:       o.Cfg.ImageTag,
		Commit:        strings.TrimPrefix(o.Cfg.ImageTag, "sha-"),
		BinaryVersion: o.BinaryVersion,
		CreatedAt:     o.now(),
		Release:       o.Cfg.Release,
		Namespace:     o.Cfg.Namespace,
		Files:         st.files,
	}
	path, sum, err := writeArchive(dest, o.archiveName(m), st, m, o.Key)
	if err != nil {
		return nil, err
	}
	o.prune(dest)
	return &Result{Path: path, SHA256: sum, Manifest: m, Window: window}, nil
}

func sortedKeys(m map[string]int) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

func (o *Options) archiveName(m *Manifest) string {
	name := ArchivePrefix + m.CreatedAt.Format("20060102T150405Z") + "-" + sanitize(m.Version) + ".tar.gz"
	if len(o.Key) > 0 {
		name += ".enc"
	}
	return name
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

// stageConfig copia i file di ConfigDir (config.yaml e, se esiste, la
// chiave della CA e quanto altro vi sta) sotto config/.
func (o *Options) stageConfig(st *stager) (int, error) {
	if o.ConfigDir == "" {
		return 0, nil
	}
	n := 0
	err := filepath.WalkDir(o.ConfigDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == o.ConfigDir {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(o.ConfigDir, p)
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }()
		n++
		return st.write(ConfigPrefix+filepath.ToSlash(rel), func(w io.Writer) error {
			_, e := io.Copy(w, src)
			return e
		})
	})
	return n, err
}

// writeArchive impacchetta lo staging in <dest>/<name> (0600) con il
// manifest per primo, e scrive <name>.sha256 accanto.
func writeArchive(dest, name string, st *stager, m *Manifest, key []byte) (string, string, error) {
	final := filepath.Join(dest, name)
	part := final + ".part"
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", "", err
	}
	cleanup := func() { _ = os.Remove(part) }
	ch := &countHash{w: f, h: sha256.New()}
	var sink io.Writer = ch
	var enc io.WriteCloser
	if len(key) > 0 {
		if enc, err = NewEncryptWriter(ch, key); err != nil {
			_ = f.Close()
			cleanup()
			return "", "", err
		}
		sink = enc
	}
	gz := gzip.NewWriter(sink)
	tw := tar.NewWriter(gz)
	werr := func() error {
		mb, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: ManifestName, Mode: 0o600, Size: int64(len(mb)), ModTime: m.CreatedAt}); err != nil {
			return err
		}
		if _, err := tw.Write(mb); err != nil {
			return err
		}
		for _, sf := range st.files {
			p := filepath.Join(st.dir, filepath.FromSlash(sf.Name))
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			e := tw.WriteHeader(&tar.Header{Name: sf.Name, Mode: 0o600, Size: sf.Size, ModTime: m.CreatedAt})
			if e == nil {
				_, e = io.Copy(tw, in)
			}
			_ = in.Close()
			if e != nil {
				return e
			}
		}
		if err := tw.Close(); err != nil {
			return err
		}
		if err := gz.Close(); err != nil {
			return err
		}
		if enc != nil {
			return enc.Close()
		}
		return nil
	}()
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		cleanup()
		return "", "", werr
	}
	if err := os.Chmod(part, 0o600); err != nil {
		cleanup()
		return "", "", err
	}
	if err := os.Rename(part, final); err != nil {
		cleanup()
		return "", "", err
	}
	sum := hex.EncodeToString(ch.h.Sum(nil))
	if err := os.WriteFile(final+".sha256", []byte(sum+"  "+name+"\n"), 0o600); err != nil {
		return "", "", err
	}
	return final, sum, nil
}

// prune tiene gli ultimi N archivi, dove N è cfg.Backup.Retention o
// o.Retention se maggiore di zero.
func (o *Options) prune(dest string) {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ArchivePrefix) && (strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tar.gz.enc")) {
			names = append(names, n)
		}
	}
	sort.Strings(names) // il nome contiene la data UTC: ordine cronologico
	keep := o.Cfg.Backup.Retention
	if o.Retention > 0 {
		keep = o.Retention
	}
	keep = max(keep, 1)
	for len(names) > keep {
		_ = os.Remove(filepath.Join(dest, names[0]))
		_ = os.Remove(filepath.Join(dest, names[0]) + ".sha256")
		o.logf("rimosso il backup più vecchio: %s", names[0])
		names = names[1:]
	}
}

// openArchive apre l'archivio (decifrandolo se serve) e restituisce il
// lettore del tar.
func openArchive(path string, key []byte) (*tar.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	br := bufio.NewReader(f)
	var src io.Reader = br
	if IsEncrypted(br) {
		if len(key) == 0 {
			_ = f.Close()
			return nil, nil, refused("l'archivio è cifrato: serve la chiave (--key-file)")
		}
		if src, err = NewDecryptReader(br, key); err != nil {
			_ = f.Close()
			return nil, nil, err
		}
	}
	gz, err := gzip.NewReader(src)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, ErrWrongKey) {
			return nil, nil, refused("%v", err)
		}
		if len(key) > 0 {
			return nil, nil, refused("archivio non leggibile (non è un archivio di gitstack backup, oppure la chiave non serve): %v", err)
		}
		return nil, nil, refused("archivio non leggibile (non è un archivio di gitstack backup): %v", err)
	}
	return tar.NewReader(gz), func() { _ = gz.Close(); _ = f.Close() }, nil
}

// Package upgrade realizza `gitstack upgrade` (D18): verifica della
// destinazione, backup preventivo (D19), aggiornamento di chart e binario,
// attesa della salute e, se qualcosa va storto, rollback automatico.
//
// Ordine, pensato perché ogni passo si possa disfare:
//  1. controlli (nessuna modifica): non è un downgrade, il sistema è sano,
//     binario con checksum, chart e immagini esistono;
//  2. backup con backup.Backup;
//  3. helm upgrade (le migrazioni girano all'avvio dei servizi) e attesa
//     che tutti i servizi siano sani;
//  4. sostituzione del binario, della copia locale del chart e di
//     image_tag nel config, nell'ordine in cui si disfano.
//
// Rollback: helm rollback alla revisione di prima; se le migrazioni hanno
// toccato il database (versioni di schema cambiate, o non leggibili),
// restore del backup appena fatto.
package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
)

// Valori di default.
const (
	DefaultRepo    = "fathorMB/GitStack"
	DefaultRef     = "main"
	DefaultTimeout = 10 * time.Minute
	// DefaultChartDir è dove install.sh copia il chart.
	DefaultChartDir = "/usr/local/share/gitstack/chart"
)

// Outcome è l'esito dell'aggiornamento.
type Outcome int

// Valori di Outcome.
const (
	Upgraded   Outcome = iota // aggiornato e sano
	NoChange                  // già alla versione richiesta, o --dry-run riuscito
	Refused                   // rifiutato dai controlli: niente è cambiato
	RolledBack                // fallito, tornato com'era
	Broken                    // fallito e rollback non riuscito: serve un intervento
	Failed                    // fallito prima di toccare il sistema (es. backup)
)

// RefusedError è un rifiuto motivato dei controlli.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

func refused(format string, a ...any) error { return &RefusedError{fmt.Sprintf(format, a...)} }

// Options sono gli ingressi di Run.
type Options struct {
	Cfg        *config.Config
	ConfigFile string
	Cluster    backup.Cluster
	Runner     status.Runner
	// Health restituisce lo stato dei servizi (status.Collector.Collect).
	Health func(ctx context.Context) *status.Report
	HTTP   *http.Client
	// Backup è il modello delle opzioni di backup (Cfg, ConfigDir, Key, Log,
	// DestDir): Run vi aggiunge il Cluster.
	Backup backup.Options

	To          string // --to
	DryRun      bool
	Timeout     time.Duration
	Sets        []string // --set di helm
	ValueFiles  []string // -f di helm
	HelmBin     string
	ExePath     string // binario gitstack da sostituire
	Repo        string
	Ref         string
	APIBase     string
	CodeloadURL string
	ReleaseURL  string
	// ImageCheck sostituisce la verifica sul registry (test).
	ImageCheck func(ctx context.Context, image string) error
	// Poll è l'intervallo fra due controlli di salute (default 5 s).
	Poll time.Duration
	Log  io.Writer
}

func (o *Options) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (o *Options) repo() string {
	if o.Repo != "" {
		return o.Repo
	}
	return DefaultRepo
}

func (o *Options) ref() string {
	if o.Ref != "" {
		return o.Ref
	}
	return DefaultRef
}

func (o *Options) apiBase() string {
	if o.APIBase != "" {
		return strings.TrimRight(o.APIBase, "/")
	}
	return "https://api.github.com"
}

func (o *Options) codeloadBase() string {
	if o.CodeloadURL != "" {
		return strings.TrimRight(o.CodeloadURL, "/")
	}
	return "https://codeload.github.com"
}

func (o *Options) releaseBase() string {
	if o.ReleaseURL != "" {
		return strings.TrimRight(o.ReleaseURL, "/")
	}
	return "https://github.com"
}

func (o *Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultTimeout
}

func (o *Options) poll() time.Duration {
	if o.Poll > 0 {
		return o.Poll
	}
	return 5 * time.Second
}

func (o *Options) helm() string {
	if o.HelmBin != "" {
		return o.HelmBin
	}
	return "helm"
}

func (o *Options) logf(format string, a ...any) {
	if o.Log != nil {
		_, _ = fmt.Fprintf(o.Log, format+"\n", a...)
	}
}

// Result è il resoconto, sempre compilato.
type Result struct {
	Outcome    Outcome
	From, To   string // tag immagine
	BackupPath string
	// RolledBackChart e Restored dicono cosa ha fatto il rollback.
	RolledBackChart bool
	Restored        bool
	Reason          string // perché è fallito
	RollbackErr     string // perché il rollback non è riuscito
	Summary         []string
}

func (r *Result) add(format string, a ...any) {
	r.Summary = append(r.Summary, fmt.Sprintf(format, a...))
}

func (o *Options) helmRun(ctx context.Context, args ...string) ([]byte, error) {
	return o.Runner.Run(ctx, []string{"KUBECONFIG=" + o.Cfg.Kubeconfig}, o.helm(), args...)
}

func (o *Options) health(ctx context.Context) *status.Report { return o.Health(ctx) }

// waitHealthy aspetta che tutti i servizi siano sani, al massimo d.
func (o *Options) waitHealthy(ctx context.Context, d time.Duration) error {
	deadline := time.Now().Add(d)
	var last string
	for {
		rep := o.health(ctx)
		if rep.Healthy() {
			return nil
		}
		last = describeUnhealthy(rep)
		if time.Now().After(deadline) {
			return fmt.Errorf("i servizi non sono sani dopo %s: %s", d.Round(time.Second), last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.poll()):
		}
	}
}

func describeUnhealthy(r *status.Report) string {
	if r.ClusterError != "" {
		return "cluster: " + r.ClusterError
	}
	var bad []string
	for _, s := range r.Services {
		if !s.Healthy() {
			bad = append(bad, fmt.Sprintf("%s %d/%d pronti", s.Name, s.Ready, s.Desired))
		}
	}
	if len(r.Services) == 0 {
		bad = append(bad, "nessun servizio trovato")
	}
	if !r.APIHealthy {
		bad = append(bad, "API: "+r.APIDetail)
	}
	return strings.Join(bad, "; ")
}

// migrationState legge le versioni di schema di identity e core. Una riga
// per schema: "schema|versione|dirty".
const migrationQuery = `exec psql -tA -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c ` +
	`"SELECT 'identity', version, dirty FROM identity.schema_migrations UNION ALL SELECT 'core', version, dirty FROM core.schema_migrations"`

func (o *Options) migrationState(ctx context.Context) (string, error) {
	var b bytes.Buffer
	if err := o.Cluster.PGExec(ctx, nil, &b, "sh", "-c", migrationQuery); err != nil {
		return "", err
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "", errors.New("nessuna versione di schema letta")
	}
	return s, nil
}

// helmRevision è la revisione "deployed" della release.
func (o *Options) helmRevision(ctx context.Context) (int, error) {
	out, err := o.helmRun(ctx, "history", o.Cfg.Release, "-n", o.Cfg.Namespace, "-o", "json", "--max", "50")
	if err != nil {
		return 0, fmt.Errorf("helm history: %w", err)
	}
	var h []struct {
		Revision int    `json:"revision"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(out, &h); err != nil {
		return 0, fmt.Errorf("risposta di helm history non valida: %w", err)
	}
	best := 0
	for _, e := range h {
		if e.Status == "deployed" && e.Revision > best {
			best = e.Revision
		}
	}
	if best == 0 {
		return 0, errors.New("nessuna revisione 'deployed' della release: il rollback non saprebbe a cosa tornare")
	}
	return best, nil
}

// helmValues scrive i valori scelti dall'utente alla prima installazione
// (e agli upgrade precedenti) in un file: si riapplicano sui default del
// chart nuovo. `--reuse-values` riusa anche i default vecchi e farebbe
// mancare le chiavi aggiunte dal chart nuovo.
func (o *Options) helmValues(ctx context.Context, dest string) error {
	out, err := o.helmRun(ctx, "get", "values", o.Cfg.Release, "-n", o.Cfg.Namespace, "-o", "json")
	if err != nil {
		return fmt.Errorf("helm get values: %w", err)
	}
	s := bytes.TrimSpace(out)
	if len(s) == 0 || string(s) == "null" {
		s = []byte("{}")
	}
	var probe map[string]any
	if err := json.Unmarshal(s, &probe); err != nil {
		return fmt.Errorf("valori della release non leggibili: %w", err)
	}
	return os.WriteFile(dest, s, 0o600)
}

func (o *Options) valueArgs(valuesFile string, tag string) []string {
	args := []string{"-f", valuesFile}
	for _, f := range o.ValueFiles {
		args = append(args, "-f", f)
	}
	args = append(args, "--set", "global.image.tag="+tag)
	for _, s := range o.Sets {
		args = append(args, "--set", s)
	}
	return args
}

// Run esegue l'aggiornamento. L'errore è nil quando il resoconto basta
// (aggiornato, rollback riuscito...): leggi Result.Outcome. È non nil per
// i rifiuti (RefusedError) e per gli errori prima di toccare il sistema.
func Run(ctx context.Context, o *Options) (*Result, error) {
	res := &Result{From: o.Cfg.ImageTag}
	if o.Cfg.ImageTag == "" {
		res.Outcome = Refused
		return res, refused("image_tag assente dal config: la versione installata non è nota. Rilancia deploy/install.sh")
	}
	chartDir := o.Cfg.ChartDir
	if chartDir == "" {
		chartDir = DefaultChartDir
	}

	// 1. Controlli, senza modificare nulla.
	o.logf("==> Controllo la destinazione")
	t, err := o.ResolveTarget(ctx, o.To)
	if err != nil {
		res.Outcome = Refused
		return res, refused("%v", err)
	}
	res.To = t.Tag
	dir, err := o.Compare(ctx, t)
	if err != nil {
		res.Outcome = Refused
		return res, refused("%v", err)
	}
	switch dir {
	case Identical:
		res.Outcome = NoChange
		res.add("GitStack è già alla versione %s: niente da fare.", t.Tag)
		return res, nil
	case Behind:
		res.Outcome = Refused
		return res, refused("downgrade non supportato: la destinazione %s è più vecchia della versione installata (%s). "+
			"Le migrazioni del database non si disfano: per tornare indietro serve un restore da un backup fatto prima dell'aggiornamento (gitstack restore)", t.Tag, o.Cfg.ImageTag)
	case Diverged:
		res.Outcome = Refused
		return res, refused("la destinazione %s non discende dalla versione installata (%s): non è un aggiornamento (downgrade non supportato)", t.Tag, o.Cfg.ImageTag)
	case Ahead:
	}

	o.logf("==> Controllo lo stato attuale")
	if rep := o.health(ctx); !rep.Healthy() {
		res.Outcome = Refused
		return res, refused("GitStack non è sano prima dell'aggiornamento (%s): il rollback non avrebbe uno stato buono a cui tornare. Sistema il problema (gitstack status) e riprova", describeUnhealthy(rep))
	}

	base := o.Backup.DestDir
	if base == "" {
		base = o.Cfg.Backup.Destination
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		res.Outcome = Failed
		return res, fmt.Errorf("cartella di lavoro: %w", err)
	}
	work, err := os.MkdirTemp(base, ".upgrade-")
	if err != nil {
		res.Outcome = Failed
		return res, fmt.Errorf("cartella di lavoro: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	o.logf("==> Scarico e verifico il binario %s", t.Tag)
	staged := filepath.Join(work, "gitstack")
	sum, err := o.FetchBinary(ctx, t, staged)
	if err != nil {
		res.Outcome = Refused
		return res, refused("%v", err)
	}
	o.logf("    SHA-256 %s", sum)

	newChart := chartDir + ".new"
	_ = os.RemoveAll(newChart)
	if err := os.MkdirAll(filepath.Dir(newChart), 0o755); err != nil { //nolint:gosec // cartella condivisa del chart
		res.Outcome = Failed
		return res, fmt.Errorf("cartella del chart: %w", err)
	}
	chartKept := false
	defer func() {
		if !chartKept {
			_ = os.RemoveAll(newChart)
		}
	}()
	o.logf("==> Scarico il chart del commit %s", t.SHA[:12])
	if err := o.FetchChart(ctx, t, newChart); err != nil {
		res.Outcome = Refused
		return res, refused("%v", err)
	}

	valuesFile := filepath.Join(work, "values.json")
	if err := o.helmValues(ctx, valuesFile); err != nil {
		res.Outcome = Refused
		return res, refused("%v", err)
	}
	prevRev, err := o.helmRevision(ctx)
	if err != nil {
		res.Outcome = Refused
		return res, refused("%v", err)
	}

	o.logf("==> Controllo che le immagini esistano")
	tmpl := append([]string{"template", o.Cfg.Release, newChart, "-n", o.Cfg.Namespace}, o.valueArgs(valuesFile, t.Tag)...)
	out, err := o.helmRun(ctx, tmpl...)
	if err != nil {
		res.Outcome = Refused
		return res, refused("il chart di %s non si applica ai valori attuali (helm template): %v", t.Tag, err)
	}
	images := imagesFromManifests(string(out))
	if len(images) == 0 {
		res.Outcome = Refused
		return res, refused("helm template non produce nessuna immagine: chart inatteso")
	}
	for _, img := range images {
		if err := o.CheckImage(ctx, img); err != nil {
			res.Outcome = Refused
			return res, refused("%v", err)
		}
		o.logf("    ok %s", img)
	}

	if o.DryRun {
		res.Outcome = NoChange
		res.add("Controlli superati: %s → %s si può installare (%d immagini, binario con checksum, chart). Nessuna modifica fatta (--dry-run).", o.Cfg.ImageTag, t.Tag, len(images))
		return res, nil
	}

	// 2. Backup preventivo.
	o.logf("==> Backup preventivo")
	bo := o.Backup
	bo.Cfg, bo.Cluster = o.Cfg, o.Cluster
	if bo.Log == nil {
		bo.Log = o.Log
	}
	br, err := backup.Backup(ctx, &bo)
	if err != nil {
		res.Outcome = Failed
		res.Reason = "backup preventivo fallito: " + err.Error()
		res.add("Aggiornamento NON eseguito: il backup preventivo è fallito, GitStack è com'era.")
		return res, fmt.Errorf("backup preventivo: %w", err)
	}
	res.BackupPath = br.Path
	o.logf("    %s", br.Path)

	before, beforeErr := o.migrationState(ctx)
	if beforeErr != nil {
		o.logf("    versioni di schema non leggibili prima (%v): se qualcosa fallisce ripristino comunque dal backup", beforeErr)
	}

	// 3. Chart e salute.
	st := &state{o: o, res: res, prevRev: prevRev, before: before, beforeErr: beforeErr, backupPath: br.Path}
	if err := o.apply(ctx, t, st, newChart, chartDir, staged, valuesFile, &chartKept); err != nil {
		res.Reason = err.Error()
		o.logf("!! Aggiornamento fallito: %v", err)
		o.logf("==> Rollback automatico")
		o.rollback(st)
		return res, nil
	}
	res.Outcome = Upgraded
	res.add("Aggiornato da %s a %s: tutti i servizi sono sani.", o.Cfg.ImageTag, t.Tag)
	res.add("Backup preventivo conservato: %s", br.Path)
	return res, nil
}

// state è quello che serve al rollback.
type state struct {
	o          *Options
	res        *Result
	prevRev    int
	before     string
	beforeErr  error
	backupPath string
	helmRan    bool
	undo       []func() error
}

func (o *Options) apply(ctx context.Context, t *Target, st *state, newChart, chartDir, staged, valuesFile string, chartKept *bool) error {
	o.logf("==> Aggiorno il chart (le migrazioni girano all'avvio dei servizi)")
	args := append([]string{"upgrade", o.Cfg.Release, newChart, "-n", o.Cfg.Namespace}, o.valueArgs(valuesFile, t.Tag)...)
	args = append(args, "--wait", "--timeout", strconv.Itoa(int(o.timeout().Seconds()))+"s")
	st.helmRan = true
	if out, err := o.helmRun(ctx, args...); err != nil {
		return fmt.Errorf("helm upgrade: %w: %s", err, tail(string(out)))
	}
	o.logf("==> Aspetto che tutti i servizi siano sani (al massimo %s)", o.timeout())
	if err := o.waitHealthy(ctx, o.timeout()); err != nil {
		return err
	}

	// Sostituzioni locali, nell'ordine in cui si disfano.
	exe := o.ExePath
	if exe == "" {
		p, err := os.Executable()
		if err != nil {
			return fmt.Errorf("percorso del binario gitstack: %w", err)
		}
		exe = p
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	o.logf("==> Installo il binario in %s", exe)
	prevExe := exe + ".prev"
	if err := copyFile(exe, prevExe, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("copia del binario attuale: %w", err)
	}
	if err := atomicCopy(staged, exe, 0o755); err != nil {
		return fmt.Errorf("sostituzione del binario: %w", err)
	}
	st.undo = append(st.undo, func() error {
		if _, err := os.Stat(prevExe); err != nil {
			return nil
		}
		return os.Rename(prevExe, exe)
	})

	o.logf("==> Aggiorno la copia locale del chart in %s", chartDir)
	oldChart := chartDir + ".prev"
	_ = os.RemoveAll(oldChart)
	hadChart := false
	if _, err := os.Stat(chartDir); err == nil {
		hadChart = true
		if err := os.Rename(chartDir, oldChart); err != nil {
			return fmt.Errorf("copia locale del chart: %w", err)
		}
	}
	if err := os.Rename(newChart, chartDir); err != nil {
		if hadChart {
			_ = os.Rename(oldChart, chartDir)
		}
		return fmt.Errorf("copia locale del chart: %w", err)
	}
	*chartKept = true
	st.undo = append(st.undo, func() error {
		if !hadChart {
			return os.RemoveAll(chartDir)
		}
		if err := os.RemoveAll(chartDir); err != nil {
			return err
		}
		return os.Rename(oldChart, chartDir)
	})

	o.logf("==> Aggiorno il config (image_tag %s)", t.Tag)
	fields := map[string]string{"image_tag": t.Tag}
	if o.Cfg.ChartDir == "" {
		fields["chart_dir"] = chartDir
	}
	if err := config.SetFields(o.ConfigFile, fields); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	// Tutto fatto: i file di appoggio non servono più.
	_ = os.Remove(prevExe)
	_ = os.RemoveAll(oldChart)
	return nil
}

func tail(s string) string {
	s = dropHelmNoise(s)
	if len(s) > 1500 {
		return "…" + s[len(s)-1500:]
	}
	return s
}

// rollback riporta il sistema com'era. Usa un contesto nuovo: anche con
// Ctrl-C a metà l'aggiornamento deve tornare indietro.
func (o *Options) rollback(st *state) {
	res := st.res
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*o.timeout()+10*time.Minute)
	defer cancel()
	var errs []string
	fail := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		o.logf("!! %s", msg)
		errs = append(errs, msg)
	}

	for i := len(st.undo) - 1; i >= 0; i-- {
		if err := st.undo[i](); err != nil {
			fail("ripristino di un file locale: %v", err)
		}
	}

	touched := false
	if st.helmRan {
		after, err := o.migrationState(ctx)
		switch {
		case err != nil:
			touched = true
			o.logf("    versioni di schema non leggibili (%v): considero il database toccato", err)
		case st.beforeErr != nil:
			touched = true
		case after != st.before || strings.Contains(after, "|t"):
			touched = true
			o.logf("    le migrazioni hanno cambiato il database (prima: %s; dopo: %s)", oneLine(st.before), oneLine(after))
		}

		args := []string{"rollback", o.Cfg.Release, strconv.Itoa(st.prevRev), "-n", o.Cfg.Namespace, "--timeout", strconv.Itoa(int(o.timeout().Seconds())) + "s"}
		if !touched {
			// Con il database toccato, i servizi vecchi non partono: li
			// riavvia il restore, che li ferma e li riaccende.
			args = append(args, "--wait")
		}
		o.logf("==> helm rollback alla revisione %d", st.prevRev)
		if out, err := o.helmRun(ctx, args...); err != nil {
			fail("helm rollback: %v: %s", err, tail(string(out)))
		} else {
			res.RolledBackChart = true
		}
	}

	if touched {
		o.logf("==> Ripristino dal backup %s", st.backupPath)
		ro := o.Backup
		ro.Cfg, ro.Cluster = o.Cfg, o.Cluster // Cfg.ImageTag è ancora la versione di prima
		if ro.Log == nil {
			ro.Log = o.Log
		}
		if err := backup.Restore(ctx, &ro, st.backupPath); err != nil {
			fail("restore dal backup: %v", err)
		} else {
			res.Restored = true
		}
	}

	if len(errs) == 0 {
		o.logf("==> Controllo la salute dopo il rollback")
		if err := o.waitHealthy(ctx, o.timeout()); err != nil {
			fail("dopo il rollback: %v", err)
		}
	}

	if len(errs) > 0 {
		res.Outcome = Broken
		res.RollbackErr = strings.Join(errs, "; ")
		res.add("Aggiornamento a %s FALLITO e rollback NON riuscito: GitStack può essere in uno stato incoerente.", res.To)
		res.add("Il backup preventivo è intatto: %s", st.backupPath)
		res.add("Per tornare com'era: helm rollback %s %d -n %s e, se il database è stato toccato, gitstack restore %s", o.Cfg.Release, st.prevRev, o.Cfg.Namespace, st.backupPath)
		return
	}
	res.Outcome = RolledBack
	res.add("Aggiornamento a %s FALLITO: %s", res.To, res.Reason)
	switch {
	case res.Restored:
		res.add("Rollback riuscito: chart tornato alla versione %s e database, repo e allegati ripristinati dal backup %s. I dati scritti dopo il backup, durante l'aggiornamento, non ci sono più.", res.From, st.backupPath)
	default:
		res.add("Rollback riuscito: chart tornato alla versione %s. Il database non era stato toccato dalle migrazioni: nessun restore, dati intatti.", res.From)
	}
	res.add("Tutti i servizi sono sani. Backup preventivo conservato: %s", st.backupPath)
}

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", ", ") }

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return writeFile(dst, in, mode)
}

func writeFile(dst string, r io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// atomicCopy copia src su dst con una rename: un binario in esecuzione si
// può sostituire senza scriverci dentro.
func atomicCopy(src, dst string, mode os.FileMode) error {
	tmp := dst + ".new"
	if err := copyFile(src, tmp, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// dropHelmNoise toglie gli avvisi sul permesso del kubeconfig che helm
// stampa a ogni comando e che coprono il vero errore.
func dropHelmNoise(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "WARNING: Kubernetes configuration file is") {
			keep = append(keep, l)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

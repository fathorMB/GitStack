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
//     Se la destinazione dichiara un'altra versione di k3s (N10, K3sVersionFile)
//     l'ordine è backup → k3s → GitStack (vedi Steps): la patch si applica
//     da sola, la minore solo con --k3s; il ritorno indietro di k3s non è
//     automatico.
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
	// CheckCA verifica che /downloads/ca.crt risponda attraverso l'Ingress
	// (un solo tentativo); waitHealthy lo ripete dopo che i servizi sono
	// sani. nil = non si controlla (insecure e letsencrypt non la servono).
	CheckCA func(ctx context.Context) error
	// Backup è il modello delle opzioni di backup (Cfg, ConfigDir, Key, Log,
	// DestDir): Run vi aggiunge il Cluster.
	Backup backup.Options

	To     string // --to
	DryRun bool
	// AllowK3sMinor è --k3s: consente il cambio di minore di k3s (N10).
	AllowK3sMinor bool
	// K3sInstallURL sostituisce lo script https://get.k3s.io (test, reti chiuse).
	K3sInstallURL string
	// K3sDownloadMirror è network.download_mirror (N9a): con un valore lo script
	// di k3s e il binario vengono da <mirror>/k3s/install.sh e
	// GITHUB_URL=<mirror>/k3s/releases, come in deploy/install.sh.
	K3sDownloadMirror string
	// DownloadMirror è network.download_mirror: binario, chart, bundle e
	// sorgente si scaricano da <mirror>/gitstack/... e si verificano uguale.
	DownloadMirror string
	// SystemdDir è la cartella delle unità systemd (default /etc/systemd/system):
	// serve a togliere le variabili *_PROXY che lo script di k3s ricopia in
	// k3s.service.env, perché il drop-in resti l'unica fonte.
	SystemdDir string
	Timeout    time.Duration
	// ExtraConfig sono chiavi di primo livello di config.yaml scritte insieme a image_tag quando
	// l'aggiornamento riesce (es. apps_enabled con --enable-apps).
	ExtraConfig map[string]string
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
	// EncryptionStatusWait è quanto si riprova `k3s secrets-encrypt status`
	// dopo il riavvio di k3s (default 2 min).
	EncryptionStatusWait time.Duration
	Log                  io.Writer
	// V5 è la verifica delle firme cosign (binario, chart, immagini). Il
	// valore zero verifica: si salta solo con V5.AllowUnsigned (N9).
	V5 V5Config
}

func (o *Options) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// v5 è la configurazione di verifica con il repository di default: l'identità
// accettata è sempre quella del repository da cui si scarica.
func (o *Options) v5() V5Config {
	v := o.V5
	if v.Repo == "" {
		v.Repo = o.repo()
	}
	return v
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
	// K3sFrom e K3sTo: versioni di k3s prima e dopo, se è cambiata.
	K3sFrom, K3sTo string
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
			if o.CheckCA == nil {
				return nil
			}
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := o.CheckCA(cctx)
			cancel()
			if err == nil {
				return nil
			}
			last = "CA: " + err.Error()
		} else {
			last = describeUnhealthy(rep)
		}
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
//
// Toglie identity.serviceSecret (F9, GIT-235): il chart nuovo genera un segreto
// per chiamante e rifiuta il vecchio valore. userSecret è il Secret che
// l'utente aveva dato con identity.serviceSecret.existingSecret (vuoto se non
// c'era): non viene cancellato.
func (o *Options) helmValues(ctx context.Context, dest string) (userSecret string, err error) {
	out, err := o.helmRun(ctx, "get", "values", o.Cfg.Release, "-n", o.Cfg.Namespace, "-o", "json")
	if err != nil {
		return "", fmt.Errorf("helm get values: %w", err)
	}
	s := bytes.TrimSpace(out)
	if len(s) == 0 || string(s) == "null" {
		s = []byte("{}")
	}
	var probe map[string]any
	if err := json.Unmarshal(s, &probe); err != nil {
		return "", fmt.Errorf("valori della release non leggibili: %w", err)
	}
	cleaned, existing, changed, err := dropLegacyServiceSecretValues(s)
	if err != nil {
		return "", fmt.Errorf("valori della release non leggibili: %w", err)
	}
	if changed {
		o.logf("==> Segreti di servizio (F9): tolgo %s dai valori, il chart genera un segreto per chiamante", legacyServiceSecretValue)
		s = cleaned
	}
	return existing, os.WriteFile(dest, s, 0o600)
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
		return o.hardenOnly(ctx, res, t.Tag)
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

	var k3s K3sPlan
	if t.K3sVersion != "" {
		inst, err := o.installedK3s(ctx)
		if err != nil {
			o.logf("    k3s: versione installata non leggibile (%v): non lo aggiorno", err)
		} else {
			if k3s, err = PlanK3s(inst, t.K3sVersion, o.AllowK3sMinor); err != nil {
				res.Outcome = Refused
				return res, err
			}
		}
	}
	if k3s.Needed() {
		res.K3sFrom, res.K3sTo = k3s.From.String(), k3s.To.String()
	}

	valuesFile := filepath.Join(work, "values.json")
	userServiceSecret, err := o.helmValues(ctx, valuesFile)
	if err != nil {
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
		switch {
		case o.v5().AllowUnsigned:
			o.logf("    ok %s (firma NON verificata: eccezione N9)", img)
		case !IsGitstackImage(img):
			o.logf("    ok %s (immagine di terzi: non firmata da GitStack)", img)
		default:
			if err := o.v5().VerifyImage(ctx, img); err != nil {
				res.Outcome = Refused
				return res, refused("immagine %s: %v. Per un mirror interno non firmato serve --allow-unsigned-mirrors (N9)", img, err)
			}
			o.logf("    ok %s (firma cosign verificata)", img)
		}
	}

	hp, err := o.planHarden()
	if err != nil {
		res.Outcome = Refused
		return res, err
	}
	if o.DryRun {
		res.Outcome = NoChange
		res.add("Controlli superati: %s → %s si può installare (%d immagini, binario con checksum, chart). Nessuna modifica fatta (--dry-run).", o.Cfg.ImageTag, t.Tag, len(images))
		if hp.needed() {
			res.add("Hardening di k3s (GIT-228): %s.", hp.describe())
		}
		if k3s.Needed() {
			res.add("k3s: %s → %s (%s): passi nell'ordine %v.", k3s.From, k3s.To, k3s.Action, Steps(k3s))
		}
		return res, nil
	}

	// 2. Backup preventivo.
	br, err := o.preventiveBackup(ctx, res)
	if err != nil {
		return res, err
	}

	// 2b. k3s, dopo il backup e prima di GitStack (Steps).
	if k3s.Needed() {
		o.logf("==> Aggiorno k3s da %s a %s (%s): interruzione breve, i servizi si fermano e ripartono", k3s.From, k3s.To, k3s.Action)
		if err := o.updateK3s(ctx, k3s); err != nil {
			res.Outcome = Failed
			res.Reason = err.Error()
			res.add("Aggiornamento di k3s NON riuscito: GitStack non è stato aggiornato (il chart è com'era).")
			res.add("Il backup preventivo è intatto: %s", br.Path)
			res.add("Il ritorno indietro di k3s non è automatico: segui «Rolling back k3s» in docs/operations.md")
			return res, fmt.Errorf("aggiornamento di k3s: %w", err)
		}
		res.add("k3s aggiornato da %s a %s.", k3s.From, k3s.To)
	}

	// 2c. Hardening di k3s (GIT-228): kubeconfig a 600 e Secret cifrati. Dopo
	// k3s (l'installer riscrive l'unità) e prima di GitStack.
	if hp.needed() {
		if k3s.Needed() {
			// L'unità è stata riscritta dallo script di k3s (K3sInstallExec):
			// si ripianifica sul file com'è ora, ma l'esigenza di cifratura è
			// quella del piano iniziale. Se la cifratura mancava prima, va
			// comunque verificata e i Secret in chiaro ricifrati.
			needEnc := hp.encEnable
			re, perr := o.planHarden()
			if perr != nil {
				res.Outcome = Failed
				res.Reason = perr.Error()
				return res, perr
			}
			if re == nil {
				re = &hardenPlan{unitPath: hp.unitPath, kubeconfig: hp.kubeconfig}
			}
			re.encEnable = re.encEnable || needEnc
			hp = re
		}
		if hp.needed() {
			if err := o.hardenK3s(ctx, hp, res); err != nil {
				res.Outcome = Failed
				res.Reason = err.Error()
				res.add("Hardening di k3s NON riuscito: GitStack non è stato aggiornato (il chart è com'era).")
				res.add("Il backup preventivo è intatto: %s. Procedura manuale: «Hardening di k3s» in docs/operations.md", br.Path)
				return res, fmt.Errorf("hardening di k3s: %w", err)
			}
		}
	}

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
	o.removeLegacyServiceSecret(ctx, res, userServiceSecret)
	res.add("Backup preventivo conservato: %s", br.Path)
	return res, nil
}

// preventiveBackup fa il backup preventivo e ne registra il percorso in res.
// In caso di errore compila res (Failed) e lo restituisce.
func (o *Options) preventiveBackup(ctx context.Context, res *Result) (*backup.Result, error) {
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
		return nil, fmt.Errorf("backup preventivo: %w", err)
	}
	res.BackupPath = br.Path
	o.logf("    %s", br.Path)
	return br, nil
}

// hardenOnly è il caso «GitStack è già alla versione richiesta»: niente da
// aggiornare, ma un'installazione con kubeconfig 644 o Secret in chiaro va
// sistemata comunque (GIT-228). Stesse garanzie dell'upgrade: sani prima,
// backup preventivo, unità ripristinata se qualcosa fallisce.
func (o *Options) hardenOnly(ctx context.Context, res *Result, tag string) (*Result, error) {
	res.Outcome = NoChange
	hp, err := o.planHarden()
	if err != nil {
		res.Outcome = Refused
		return res, err
	}
	if !hp.needed() {
		res.add("GitStack è già alla versione %s: niente da fare.", tag)
		// Una prima installazione di F9 fatta da un binario precedente non
		// ha cancellato il Secret condiviso: lo si toglie ora (idempotente).
		if !o.DryRun {
			o.removeLegacyServiceSecret(ctx, res, "")
		}
		return res, nil
	}
	res.add("GitStack è già alla versione %s, ma k3s va messo in sicurezza (GIT-228): %s.", tag, hp.describe())
	if o.DryRun {
		res.add("Nessuna modifica fatta (--dry-run).")
		return res, nil
	}
	o.logf("==> Controllo lo stato attuale")
	if rep := o.health(ctx); !rep.Healthy() {
		res.Outcome = Refused
		return res, refused("GitStack non è sano (%s): non riavvio k3s su un sistema già in difficoltà. Sistema il problema (gitstack status) e riprova", describeUnhealthy(rep))
	}
	base := o.Backup.DestDir
	if base == "" {
		base = o.Cfg.Backup.Destination
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		res.Outcome = Failed
		return res, fmt.Errorf("cartella di lavoro: %w", err)
	}
	br, err := o.preventiveBackup(ctx, res)
	if err != nil {
		return res, err
	}
	if err := o.hardenK3s(ctx, hp, res); err != nil {
		res.Outcome = Failed
		res.Reason = err.Error()
		res.add("Hardening di k3s NON riuscito. Il backup preventivo è intatto: %s. Procedura manuale: «Hardening di k3s» in docs/operations.md", br.Path)
		return res, fmt.Errorf("hardening di k3s: %w", err)
	}
	res.add("k3s messo in sicurezza. Backup preventivo conservato: %s", br.Path)
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
	for k, v := range o.ExtraConfig {
		fields[k] = v
	}
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

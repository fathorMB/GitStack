package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Volumi delle app nel backup e nel restore (v3-b, GIT-357, H3, D19). Ogni volume persistente di
// un'app è un PVC `vol-<nome>` nel namespace `gitstack-app-<id>`. Di default entrano tutti, anche
// verso S3; ogni volume si può escludere (core.app_volumes.include_in_backup, API
// PATCH .../volumes/{volume}). La copia si fa con l'app accesa: per un database dentro un container
// la coerenza non è garantita (la UI avvisa e docs/operations.md spiega il dump). In restore la
// cartella del volume torna al suo posto e l'app, riapplicata da core, riparte con i suoi dati.

// AppVolumesPrefix inizia il nome delle voci dei volumi delle app nell'archivio:
// app-volumes/<app-id>/<volume>.tar.
const AppVolumesPrefix = "app-volumes/"

const (
	appNamespacePrefix = "gitstack-app-"
	appPVCPrefix       = "vol-"
)

var volumeNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,28}[a-z0-9])?$`)

// ErrNotBound: il PVC esiste ma nessun pod l'ha ancora usato, quindi non ha una cartella sull'host
// (local-path crea il volume al primo pod). Non c'è niente da copiare.
var ErrNotBound = errors.New("pvc non ancora associato a un volume")

// AppVolumeRef è un volume di app incluso nel backup: serve a trovarlo nel cluster e a ricrearlo in restore.
type AppVolumeRef struct {
	AppID     string `json:"app_id"`
	RepoID    string `json:"repo_id"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	App       string `json:"app"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	// Entry è il nome della voce nell'archivio.
	Entry string `json:"entry"`
}

// Namespace è il namespace Kubernetes dell'app.
func (v AppVolumeRef) Namespace() string { return appNamespacePrefix + v.AppID }

// PVC è il nome del PersistentVolumeClaim del volume.
func (v AppVolumeRef) PVC() string { return appPVCPrefix + v.Name }

func (v AppVolumeRef) entryName() string { return AppVolumesPrefix + v.AppID + "/" + v.Name + ".tar" }

// validate rifiuta riferimenti che non siano un id e un nome di volume ben formati: finiscono in
// percorsi e comandi kubectl.
func (v AppVolumeRef) validate() error {
	if !repoIDRe.MatchString(v.AppID) {
		return fmt.Errorf("volume di app: id dell'app non valido %q", v.AppID)
	}
	if v.RepoID != "" && !repoIDRe.MatchString(v.RepoID) {
		return fmt.Errorf("volume %q: id del repo non valido %q", v.Name, v.RepoID)
	}
	if !volumeNameRe.MatchString(v.Name) {
		return fmt.Errorf("volume di app: nome non valido %q", v.Name)
	}
	if v.SizeBytes < 1 {
		return fmt.Errorf("volume %q: dimensione non valida", v.Name)
	}
	return nil
}

// AppVolumeCluster è la parte di Cluster che serve ai volumi delle app. KubectlCluster la realizza;
// un Cluster che non la realizza non fa né backup né restore dei volumi delle app.
type AppVolumeCluster interface {
	// AppVolumePath è la cartella sull'host del volume (ErrNotFound se il PVC non c'è, ErrNotBound
	// se non è mai stato usato da un pod).
	AppVolumePath(ctx context.Context, v AppVolumeRef) (string, error)
	// EnsureAppVolume dà la cartella del volume in restore: se il PVC c'è già e ha una cartella la
	// ritorna; altrimenti ricrea namespace, volume e PVC dell'app (stessa label e dimensione di
	// quelli del controller) in una cartella nuova sotto base e la ritorna.
	EnsureAppVolume(ctx context.Context, v AppVolumeRef, base string) (string, error)
	// StopApp porta a zero le repliche del Deployment dell'app e aspetta che i pod spariscano, così
	// i dati si scrivono con il volume libero. Core, ripartendo, riporta le copie volute.
	StopApp(ctx context.Context, v AppVolumeRef) error
}

// appVolumesCmd legge i volumi da mettere nel backup: solo quelli con include_in_backup. Una riga per
// volume: app_id|repo_id|owner|repo|app|volume|size_bytes. Se la tabella non esiste (database di una
// versione precedente, per esempio il backup prima di un aggiornamento) l'elenco è vuoto; se manca
// solo la colonna, nessuna esclusione era possibile e entrano tutti.
const appVolumesCmd = `exec_psql() { psql -q -At -F '|' -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"; }
if [ "$(exec_psql "SELECT to_regclass('core.app_volumes') IS NOT NULL")" = t ]; then
  if [ "$(exec_psql "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'app_volumes' AND column_name = 'include_in_backup'")" = 1 ]; then
    where='WHERE v.include_in_backup'
  else
    where=''
  fi
  exec_psql "SELECT a.id, a.repo_id, r.owner_name, r.name, a.name, v.name, v.size_bytes FROM core.app_volumes v JOIN core.apps a ON a.id = v.app_id JOIN core.repositories r ON r.resource_id = a.repo_id $where ORDER BY a.id, v.name"
fi`

// parseAppVolumeRow legge una riga di appVolumesCmd.
func parseAppVolumeRow(line string) (AppVolumeRef, error) {
	f := strings.Split(line, "|")
	if len(f) != 7 {
		return AppVolumeRef{}, fmt.Errorf("riga dei volumi delle app non valida (%d campi)", len(f))
	}
	size, err := strconv.ParseInt(f[6], 10, 64)
	if err != nil {
		return AppVolumeRef{}, fmt.Errorf("riga dei volumi delle app non valida: dimensione %q", f[6])
	}
	v := AppVolumeRef{AppID: strings.ToLower(f[0]), RepoID: strings.ToLower(f[1]), Owner: f[2], Repo: f[3], App: f[4], Name: f[5], SizeBytes: size}
	if err := v.validate(); err != nil {
		return AppVolumeRef{}, err
	}
	v.Entry = v.entryName()
	return v, nil
}

// appVolumes ritorna i volumi delle app da includere: dal hook AppVolumes se c'è (test), altrimenti dal database.
func (o *Options) appVolumes(ctx context.Context) ([]AppVolumeRef, error) {
	if o.AppVolumes != nil {
		return o.AppVolumes(ctx)
	}
	var out bytes.Buffer
	if err := o.Cluster.PGExec(ctx, nil, &out, "sh", "-c", appVolumesCmd); err != nil {
		return nil, fmt.Errorf("volumi delle app: %w", err)
	}
	var vols []AppVolumeRef
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, err := parseAppVolumeRow(line)
		if err != nil {
			return nil, err
		}
		vols = append(vols, v)
	}
	return vols, nil
}

// stagedAppVolume è un volume trovato nel cluster, pronto per essere copiato.
type stagedAppVolume struct {
	ref  AppVolumeRef
	path string
}

// locateAppVolumes trova la cartella di ogni volume incluso. Un volume senza PVC o mai usato da un pod
// non ha dati: si salta con un avviso (l'app ripartirà con il volume vuoto, che core ricrea). Va chiamato
// prima di fermare qualsiasi cosa.
func (o *Options) locateAppVolumes(ctx context.Context, vols []AppVolumeRef) ([]stagedAppVolume, error) {
	if len(vols) == 0 {
		return nil, nil
	}
	ac, ok := o.Cluster.(AppVolumeCluster)
	if !ok {
		return nil, errors.New("questo cluster non sa leggere i volumi delle app")
	}
	var out []stagedAppVolume
	for _, v := range vols {
		p, err := ac.AppVolumePath(ctx, v)
		switch {
		case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotBound):
			o.logf("volume %s dell'app %s/%s/%s: nessun dato nel cluster, non incluso", v.Name, v.Owner, v.Repo, v.App)
			continue
		case err != nil:
			return nil, fmt.Errorf("volume %s dell'app %s/%s/%s: %w", v.Name, v.Owner, v.Repo, v.App, err)
		}
		out = append(out, stagedAppVolume{ref: v, path: p})
	}
	return out, nil
}

// dirSize somma i byte dei file regolari sotto root; un errore di lettura non interrompe la stima.
func dirSize(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			n += info.Size()
		}
		return nil
	})
	return n
}

// estimate è la stima dello spazio che serve al backup: i byte da copiare dei volumi, comprese le
// cartelle dei volumi delle app. È un massimo: l'archivio è compresso e le immagini del registry non
// scelte restano fuori. Serve due volte (la cartella di lavoro e l'archivio).
type estimate struct {
	CoreBytes      int64 // git-data, allegati, log della CI
	AppVolumeBytes int64 // dati nei volumi delle app
}

func (e estimate) total() int64 { return e.CoreBytes + e.AppVolumeBytes }

// checkSpace confronta la stima con lo spazio libero della cartella dei backup e avvisa nel log se non
// basta. Non ferma il backup: la stima è un massimo e lo spazio libero può non essere noto.
func (o *Options) checkSpace(dest string, e estimate) {
	o.logf("stima dello spazio: %s di dati (di cui %s nei volumi delle app); servono fino a %s liberi in %s",
		humanBytes(e.total()), humanBytes(e.AppVolumeBytes), humanBytes(2*e.total()), dest)
	free, ok := o.freeBytes(dest)
	if !ok {
		return
	}
	if free < 2*e.total() {
		o.logf("ATTENZIONE: in %s ci sono %s liberi, meno della stima (%s): il backup potrebbe non entrare", dest, humanBytes(free), humanBytes(2*e.total()))
	}
}

func (o *Options) freeBytes(dir string) (int64, bool) {
	if o.FreeBytes != nil {
		return o.FreeBytes(dir)
	}
	return diskFree(dir)
}

func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f GiB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1f MiB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1f KiB", float64(n)/k)
	}
	return fmt.Sprintf("%d B", n)
}

// restoreAppVolumes rimette nei volumi delle app i dati dell'archivio. Per ogni volume ricrea
// (se serve) namespace, volume e PVC, ferma il Deployment dell'app se gira e scrive la cartella.
func (o *Options) restoreAppVolumes(ctx context.Context, stage string, vols []AppVolumeRef, has map[string]bool, base string) error {
	if len(vols) == 0 {
		return nil
	}
	ac, ok := o.Cluster.(AppVolumeCluster)
	if !ok {
		return errors.New("questo cluster non sa ripristinare i volumi delle app")
	}
	sorted := append([]AppVolumeRef(nil), vols...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Entry < sorted[j].Entry })
	stopped := map[string]bool{}
	for _, v := range sorted {
		if err := v.validate(); err != nil {
			return refused("archivio non valido: %v", err)
		}
		if v.Entry != v.entryName() || !has[v.Entry] {
			return refused("archivio incompleto: manca %s", v.entryName())
		}
		o.logf("ripristino del volume %s dell'app %s/%s/%s", v.Name, v.Owner, v.Repo, v.App)
		if !stopped[v.AppID] {
			if err := ac.StopApp(ctx, v); err != nil {
				return fmt.Errorf("arresto dell'app %s: %w", v.App, err)
			}
			stopped[v.AppID] = true
		}
		p, err := ac.EnsureAppVolume(ctx, v, base)
		if err != nil {
			return fmt.Errorf("volume %s dell'app %s: %w", v.Name, v.App, err)
		}
		if err := restoreVolume(filepath.Join(stage, filepath.FromSlash(v.Entry)), p); err != nil {
			return fmt.Errorf("ripristino del volume %s dell'app %s: %w", v.Name, v.App, err)
		}
	}
	return nil
}

// ensureDir crea la cartella di un volume ripristinato con i permessi che dà local-path (0777): i
// container girano con uid qualsiasi e i file mantengono il proprietario dell'archivio.
func ensureDir(p string) error {
	if err := os.MkdirAll(p, 0o777); err != nil {
		return err
	}
	return os.Chmod(p, 0o777)
}

package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Volumi delle app nel backup e nel restore (GIT-357, H3, D19).

const (
	avApp   = "aaaaaaaa-0000-4000-8000-000000000001"
	avRepo  = "11111111-2222-4333-8444-555555555555"
	avOther = "aaaaaaaa-0000-4000-8000-000000000002"
)

// appVolCluster è fakeCluster più i volumi delle app: dirs è la cartella di ogni PVC (namespace/pvc).
type appVolCluster struct {
	*fakeCluster
	dirs    map[string]string
	order   []string
	ensured map[string]string // namespace/pvc -> cartella di destinazione del restore
}

func (c *appVolCluster) AppVolumePath(_ context.Context, v AppVolumeRef) (string, error) {
	p, ok := c.dirs[v.Namespace()+"/"+v.PVC()]
	if !ok {
		return "", ErrNotFound
	}
	return p, nil
}

func (c *appVolCluster) StopApp(_ context.Context, v AppVolumeRef) error {
	c.order = append(c.order, "stop "+v.AppID)
	return nil
}

func (c *appVolCluster) EnsureAppVolume(_ context.Context, v AppVolumeRef, base string) (string, error) {
	c.order = append(c.order, "ensure "+v.Namespace()+"/"+v.PVC())
	p, ok := c.ensured[v.Namespace()+"/"+v.PVC()]
	if !ok {
		return "", errors.New("volume inatteso")
	}
	return p, nil
}

func avRow(app, name string, size int64) string {
	return app + "|" + avRepo + "|alice|wiki|wiki|" + name + "|" + itoa(size) + "\n"
}

func itoa(n int64) string {
	const d = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{d[n%10]}, b...)
	}
	return string(b)
}

func newAppVolEnv(t *testing.T) (*env, *appVolCluster, string) {
	t.Helper()
	e := newEnv(t)
	vol := filepath.Join(t.TempDir(), "pvc-data")
	writeFile(t, filepath.Join(vol, "wiki", "pagina.md"), "DATI-SCRITTI-PRIMA-DEL-BACKUP")
	writeFile(t, filepath.Join(vol, "indice.db"), "INDICE")
	c := &appVolCluster{fakeCluster: e.f, dirs: map[string]string{"gitstack-app-" + avApp + "/vol-data": vol}, ensured: map[string]string{}}
	e.o.Cluster = c
	return e, c, vol
}

// Di default i volumi delle app sono nel backup, con l'app accesa, e tornano con il restore su
// un'installazione pulita: il volume nuovo (che il restore ricrea) riceve i dati e l'app va fermata prima.
func TestBackupVolumiAppDiDefaultEPoiRestore(t *testing.T) {
	e, _, _ := newAppVolEnv(t)
	e.f.appVolumes = avRow(avApp, "data", 4096)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	entry := AppVolumesPrefix + avApp + "/data.tar"
	if len(res.Manifest.AppVolumes) != 1 || res.Manifest.AppVolumes[0].Entry != entry {
		t.Fatalf("manifest: %+v", res.Manifest.AppVolumes)
	}
	names := tarNames(t, res.Path, entry)
	if strings.Join(names, ",") != "indice.db,wiki/pagina.md" {
		t.Fatalf("contenuto del volume nell'archivio: %v", names)
	}
	// L'app resta accesa: il backup ferma solo gateway e git.
	for _, call := range e.f.calls {
		if strings.Contains(call, "app") && strings.Contains(call, "scale") && !strings.Contains(call, "gs-") {
			t.Errorf("chiamata inattesa sull'app: %q", call)
		}
	}
	if strings.Contains(e.log.String(), "DATI-SCRITTI") || strings.Contains(e.log.String(), "pagina.md") {
		t.Errorf("dati del volume nei log: %s", e.log.String())
	}

	// Restore su una VM pulita: la cartella del volume è nuova e vuota (con un file vecchio).
	r, rc, _ := newAppVolEnv(t)
	dst := filepath.Join(t.TempDir(), "pvc-restore")
	writeFile(t, filepath.Join(dst, "vecchio"), "x")
	rc.ensured["gitstack-app-"+avApp+"/vol-data"] = dst
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	if err := Restore(context.Background(), r.o, res.Path); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "wiki", "pagina.md")); err != nil || string(b) != "DATI-SCRITTI-PRIMA-DEL-BACKUP" {
		t.Errorf("dato ripristinato = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "vecchio")); err == nil {
		t.Error("il contenuto precedente del volume non è stato tolto")
	}
	if got := strings.Join(rc.order, "|"); got != "stop "+avApp+"|ensure gitstack-app-"+avApp+"/vol-data" {
		t.Errorf("ordine: %s", got)
	}
}

// Un volume escluso dall'app non è nell'archivio: la query ne lascia fuori le righe (include_in_backup),
// quindi qui l'elenco è vuoto, il backup non ha voci di volumi e il restore non li cerca.
func TestBackupVolumiAppEsclusiNonEntrano(t *testing.T) {
	e, _, _ := newAppVolEnv(t)
	e.f.appVolumes = "" // l'unico volume dell'app ha include_in_backup = false
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Manifest.AppVolumes) != 0 {
		t.Errorf("manifest: %+v", res.Manifest.AppVolumes)
	}
	for _, f := range res.Manifest.Files {
		if strings.HasPrefix(f.Name, AppVolumesPrefix) {
			t.Errorf("voce di volume in un backup senza volumi inclusi: %s", f.Name)
		}
	}
	if !strings.Contains(appVolumesCmd, "WHERE v.include_in_backup") {
		t.Error("la query dei volumi deve leggere solo quelli con include_in_backup")
	}
	if err := Restore(context.Background(), e.o, res.Path); err != nil {
		t.Fatalf("restore senza volumi di app: %v", err)
	}
}

// Più app, più volumi: ognuno ha la sua voce; un volume senza PVC (app mai avviata) si salta senza fermare il backup.
func TestBackupVolumiAppSenzaPVCSaltato(t *testing.T) {
	e, _, _ := newAppVolEnv(t)
	e.f.appVolumes = avRow(avApp, "data", 4096) + avRow(avOther, "cache", 1<<20)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Manifest.AppVolumes) != 1 || res.Manifest.AppVolumes[0].AppID != avApp {
		t.Fatalf("manifest: %+v", res.Manifest.AppVolumes)
	}
	if !strings.Contains(e.log.String(), "nessun dato nel cluster") {
		t.Errorf("manca l'avviso del volume saltato: %s", e.log.String())
	}
}

// Le dimensioni dei volumi entrano nella stima dello spazio, che avvisa se lo spazio libero non basta.
func TestBackupStimaSpazioConVolumiApp(t *testing.T) {
	e, _, vol := newAppVolEnv(t)
	e.f.appVolumes = avRow(avApp, "data", 4096)
	writeFile(t, filepath.Join(vol, "grande.bin"), strings.Repeat("x", 100000))
	e.o.FreeBytes = func(string) (int64, bool) { return 1000, true }
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if res.EstimatedBytes < 100000 {
		t.Errorf("la stima (%d) deve contenere i %d byte del volume", res.EstimatedBytes, 100000)
	}
	log := e.log.String()
	if !strings.Contains(log, "stima dello spazio") || !strings.Contains(log, "nei volumi delle app") {
		t.Errorf("manca la stima nel log: %s", log)
	}
	if !strings.Contains(log, "ATTENZIONE: in ") {
		t.Errorf("manca l'avviso di spazio insufficiente: %s", log)
	}
}

func TestParseAppVolumeRow(t *testing.T) {
	if v, err := parseAppVolumeRow(strings.TrimSpace(avRow(avApp, "data", 4096))); err != nil || v.Entry != AppVolumesPrefix+avApp+"/data.tar" || v.SizeBytes != 4096 || v.PVC() != "vol-data" {
		t.Errorf("= %+v, %v", v, err)
	}
	for _, bad := range []string{
		"x",
		strings.TrimSpace(avRow("../etc", "data", 1)),
		strings.TrimSpace(avRow(avApp, "../data", 1)),
		strings.TrimSpace(avRow(avApp, "Data", 1)),
		strings.TrimSpace(avRow(avApp, "data", 0)),
		avApp + "|" + avRepo + "|alice|wiki|wiki|data|molti",
	} {
		if _, err := parseAppVolumeRow(bad); err == nil {
			t.Errorf("%q doveva essere rifiutata", bad)
		}
	}
}

// Un archivio con volumi di app non si ripristina su un cluster che non li sa gestire: meglio fermarsi che perdere dati in silenzio.
func TestRestoreVolumiAppSenzaSupportoDelCluster(t *testing.T) {
	e, _, _ := newAppVolEnv(t)
	e.f.appVolumes = avRow(avApp, "data", 4096)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	r := newEnv(t) // fakeCluster semplice
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	err = Restore(context.Background(), r.o, res.Path)
	if err == nil || !strings.Contains(err.Error(), "volumi di app") {
		t.Fatalf("err = %v", err)
	}
	if len(r.f.calls) != 0 {
		t.Errorf("il restore ha toccato il cluster prima di rifiutare: %v", r.f.calls)
	}
}

//go:build integration

package apps

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/repopurge"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// GIT-358 (H5, v3-b): volumi e ciclo di vita del repo. Cluster finto e orologio finto: nessun k3d.

type lifecycleGit struct{ gitclient.Git }

func (lifecycleGit) Trash(context.Context, trust.Identity, uuid.UUID) error  { return nil }
func (lifecycleGit) Delete(context.Context, trust.Identity, uuid.UUID) error { return nil }

type lifecycleAccess struct{}

func (lifecycleAccess) PurgeResource(context.Context, uuid.UUID) error { return nil }

type lifecycle struct {
	t    *testing.T
	pool *pgxpool.Pool
	st   *store.Store
	kube *kubernetesfake.Clientset
	ctl  *Controller
	repo uuid.UUID
	app  store.App
	ver  store.AppVersion
	t0   time.Time
}

// newLifecycle crea un repo di alice con un'app a una copia, un volume da 1 GiB e la applica al cluster finto.
func newLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	ctx := context.Background()
	pool, _ := dbtest.NewPool(t)
	st := store.New(pool)
	l := &lifecycle{t: t, pool: pool, st: st, kube: kubernetesfake.NewSimpleClientset(), repo: uuid.New(),
		t0: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'alice/wiki')`, l.repo); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name)
		VALUES ($1, 'user', $2, 'alice', 'wiki')`, l.repo, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repo_counters (repo_id) VALUES ($1)`, l.repo); err != nil {
		t.Fatal(err)
	}
	port := 8080
	app, _, err := st.CreateApp(ctx, store.NewApp{RepoID: l.repo, Owner: "alice", Name: "wiki",
		Image: store.AppImage{Reference: "registry.example/alice/wiki:v1", Source: "registry"},
		Config: store.AppConfig{Copies: 1, Port: &port, Variables: map[string]string{},
			Guaranteed: store.AppResourceAmount{CPUMillis: 100, MemoryBytes: 128 << 20},
			Limit:      store.AppResourceAmount{CPUMillis: 500, MemoryBytes: 512 << 20}},
	}, l.t0)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := st.AddAppVolume(ctx, app.ID, store.AppVolumeSpec{Name: "data", MountPath: "/var/lib/data", SizeBytes: 1 << 30}, nil, l.t0)
	if err != nil {
		t.Fatal(err)
	}
	l.ver = ch.Version
	l.ctl = New(st, l.kube, fakeDynamic(), Config{GateImage: testGateImage})
	l.app = l.reload()
	if err := l.ctl.apply(ctx, l.app, l.ver); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *lifecycle) reload() store.App {
	l.t.Helper()
	a, err := l.st.GetApp(context.Background(), l.repo, "wiki")
	if err != nil {
		l.t.Fatal(err)
	}
	return a
}

func (l *lifecycle) setRepo(column string, at *time.Time) {
	l.t.Helper()
	if _, err := l.pool.Exec(context.Background(), `UPDATE core.repositories SET `+column+` = $2 WHERE resource_id = $1`, l.repo, at); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lifecycle) replicas() int32 {
	l.t.Helper()
	d, err := l.kube.AppsV1().Deployments(Namespace(l.app.ID)).Get(context.Background(), WorkloadName, metav1.GetOptions{})
	if err != nil {
		l.t.Fatal(err)
	}
	return *d.Spec.Replicas
}

func (l *lifecycle) pvc() bool {
	l.t.Helper()
	_, err := l.kube.CoreV1().PersistentVolumeClaims(Namespace(l.app.ID)).Get(context.Background(), PVCName("data"), metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		l.t.Fatal(err)
	}
	return err == nil
}

// mark scrive un'annotazione sul PVC: se dopo il ripristino c'è ancora, il PVC è lo stesso oggetto
// (non ricreato), cioè i dati sono quelli di prima.
func (l *lifecycle) mark() {
	l.t.Helper()
	pvcs := l.kube.CoreV1().PersistentVolumeClaims(Namespace(l.app.ID))
	p, err := pvcs.Get(context.Background(), PVCName("data"), metav1.GetOptions{})
	if err != nil {
		l.t.Fatal(err)
	}
	p.Annotations = map[string]string{"test/dati": "presenti"}
	if _, err := pvcs.Update(context.Background(), p, metav1.UpdateOptions{}); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lifecycle) marked() bool {
	p, err := l.kube.CoreV1().PersistentVolumeClaims(Namespace(l.app.ID)).Get(context.Background(), PVCName("data"), metav1.GetOptions{})
	return err == nil && p.Annotations["test/dati"] == "presenti"
}

func (l *lifecycle) volumeRows() int {
	l.t.Helper()
	vs, err := l.st.ListAppVolumes(context.Background(), l.app.ID)
	if err != nil {
		l.t.Fatal(err)
	}
	return len(vs)
}

func (l *lifecycle) diskUsed() int64 {
	l.t.Helper()
	_, used, _, err := l.st.OwnerAppQuota(context.Background(), "alice")
	if err != nil {
		l.t.Fatal(err)
	}
	return used.DiskBytes
}

func (l *lifecycle) purgeJob(now time.Time) *repopurge.Job {
	return &repopurge.Job{Store: l.st, Git: lifecycleGit{}, Identity: lifecycleAccess{}, Apps: l.ctl, Now: func() time.Time { return now }}
}

// Criterio 1: ripristino entro 7 giorni riporta i dati; dopo 7 giorni PVC eliminati e quota liberata.
func TestRepoEliminato_VolumiTornanoEntroSetteGiorniEvannoDopo(t *testing.T) {
	ctx := context.Background()
	l := newLifecycle(t)
	if !l.pvc() || l.replicas() != 1 || l.diskUsed() != 1<<30 {
		t.Fatalf("partenza: pvc=%v repliche=%d disco=%d", l.pvc(), l.replicas(), l.diskUsed())
	}

	// Il repo viene eliminato: l'app si ferma subito, volumi e quota restano.
	deletedAt := l.t0
	l.setRepo("deleted_at", &deletedAt)
	app := l.reload()
	if err := l.ctl.Scale(ctx, app); err != nil {
		t.Fatal(err)
	}
	if got := l.replicas(); got != 0 {
		t.Fatalf("repo eliminato: repliche %d, volute 0", got)
	}
	l.ctl.pruneVolumes(ctx, app)
	if !l.pvc() || l.volumeRows() != 1 || l.diskUsed() != 1<<30 {
		t.Fatalf("dentro i 7 giorni i volumi restano: pvc=%v righe=%d disco=%d", l.pvc(), l.volumeRows(), l.diskUsed())
	}

	// Una corsa del purge a 6 giorni e 23 ore non tocca niente.
	if n, err := l.purgeJob(deletedAt.Add(store.Retention - time.Hour)).RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("purge troppo presto: n=%d err=%v", n, err)
	}
	if !l.pvc() {
		t.Fatal("il purge anticipato ha toccato il PVC")
	}

	// Ripristino a 6 giorni: l'app riparte e il PVC (con i dati) è lo stesso.
	l.mark()
	l.setRepo("deleted_at", nil)
	app = l.reload()
	if err := l.ctl.Scale(ctx, app); err != nil {
		t.Fatal(err)
	}
	if l.replicas() != 1 || !l.marked() {
		t.Fatalf("ripristino: repliche=%d, PVC cambiato", l.replicas())
	}
	if l.volumeRows() != 1 || l.diskUsed() != 1<<30 {
		t.Fatalf("ripristino: righe=%d disco=%d", l.volumeRows(), l.diskUsed())
	}

	// Eliminato di nuovo e questa volta lasciato scadere: dopo 7 giorni app, PVC e quota spariscono.
	l.setRepo("deleted_at", &deletedAt)
	n, err := l.purgeJob(deletedAt.Add(store.Retention + time.Minute)).RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purge dopo 7 giorni: n=%d err=%v", n, err)
	}
	if l.pvc() {
		t.Fatal("dopo 7 giorni il PVC deve essere eliminato")
	}
	if _, err := l.kube.CoreV1().Namespaces().Get(ctx, Namespace(app.ID), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("il namespace deve sparire: %v", err)
	}
	if got := l.diskUsed(); got != 0 {
		t.Fatalf("quota disco dopo il purge = %d, voluta 0", got)
	}
	if _, err := l.st.GetAppByID(ctx, app.ID); err == nil {
		t.Fatal("l'app deve sparire dal modello")
	}
}

// Criterio 2: archiviazione e riattivazione senza perdita di dati.
func TestRepoArchiviato_AppFermaDatiRestano(t *testing.T) {
	ctx := context.Background()
	l := newLifecycle(t)
	l.mark()

	at := l.t0
	l.setRepo("archived_at", &at)
	app := l.reload()
	if err := l.ctl.Scale(ctx, app); err != nil {
		t.Fatal(err)
	}
	l.ctl.pruneVolumes(ctx, app)
	if l.replicas() != 0 || !l.pvc() || l.volumeRows() != 1 || l.diskUsed() != 1<<30 {
		t.Fatalf("archiviato: repliche=%d pvc=%v righe=%d disco=%d", l.replicas(), l.pvc(), l.volumeRows(), l.diskUsed())
	}
	// Il purge dei repo eliminati non riguarda un repo solo archiviato, nemmeno dopo anni.
	if n, err := l.purgeJob(l.t0.AddDate(1, 0, 0)).RunOnce(ctx); err != nil || n != 0 || !l.pvc() {
		t.Fatalf("purge su archiviato: n=%d err=%v pvc=%v", n, err, l.pvc())
	}

	l.setRepo("archived_at", nil)
	app = l.reload()
	if err := l.ctl.Scale(ctx, app); err != nil {
		t.Fatal(err)
	}
	if l.replicas() != 1 || !l.marked() {
		t.Fatalf("riattivato: repliche=%d, PVC cambiato", l.replicas())
	}
}

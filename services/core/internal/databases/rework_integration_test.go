//go:build integration

package databases

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// DB22: se un giro del reconciler passa fra il commit della riga e Provision, Provision ritorna la password
// gia' persistita (che puo' essere applicata ai ruoli) e non la sovrascrive: l'API mostra quella che funziona.
func TestDatabases_PasswordCoerenteConUnGiroDelReconciler(t *testing.T) {
	e := newCtlEnv(t, nil)
	ctx := context.Background()
	d, err := e.st.CreateManagedDB(ctx, store.NewManagedDB{ID: uuid.New(), OwnerName: "alice", OwnerType: "user", OwnerID: uuid.New(),
		Name: "shop", PgMajor: 17, SizeBytes: 1 << 30, CPUMillis: 100, MemoryBytes: 256 << 20}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	// Il reconciler vede la riga prima di Provision: crea lui il Secret con una password sua.
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	sec, err := e.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretAuth, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	applied := string(sec.Data["owner-password"])
	if applied == "" || applied == "scelta-dall-api" {
		t.Fatalf("password del reconciler: %q", applied)
	}
	got, err := e.ctl.Provision(ctx, d, "scelta-dall-api")
	if err != nil {
		t.Fatal(err)
	}
	if got != applied {
		t.Fatalf("Provision ha ritornato %q, ma nel Secret c'e' %q", got, applied)
	}
	sec, _ = e.kube.CoreV1().Secrets(Namespace(d.ID)).Get(ctx, SecretAuth, metav1.GetOptions{})
	if string(sec.Data["owner-password"]) != applied {
		t.Fatal("il Secret e' stato sovrascritto")
	}
	// I ruoli nascono con la password mostrata all'utente.
	e.markReady(d)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	all := strings.Join(e.sql.stmts, "\n")
	if !strings.Contains(all, "PASSWORD '"+got+"'") || strings.Contains(all, "scelta-dall-api") {
		t.Fatalf("ruoli creati con un'altra password:\n%s", all)
	}
	// Ordine normale: Provision prima del reconciler.
	e2 := newCtlEnv(t, nil)
	d2 := e2.create("alice", "ordine", 17)
	sec, _ = e2.kube.CoreV1().Secrets(Namespace(d2.ID)).Get(ctx, SecretAuth, metav1.GetOptions{})
	if string(sec.Data["owner-password"]) != "owner-pw-ordine" {
		t.Fatalf("ordine normale: %q", sec.Data["owner-password"])
	}
}

type fakeDumper struct {
	err   error
	calls int
}

func (f *fakeDumper) SafetyDump(context.Context, store.ManagedDB) error { f.calls++; return f.err }

// DB6: l'immagine e' del singolo database. Cambiare la tabella dell'istanza non aggiorna i database gia'
// creati (ne' con la riconciliazione ne' col resize); le nuove creazioni usano la tabella corrente; la minor
// si aggiorna solo su richiesta esplicita e dopo un dump di sicurezza riuscito.
func TestDatabases_ImmagineFissataPerDatabase(t *testing.T) {
	e := newCtlEnv(t, nil)
	ctx := context.Background()
	stsImage := func(d store.ManagedDB) string {
		sts, err := e.kube.AppsV1().StatefulSets(Namespace(d.ID)).Get(ctx, WorkloadName, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return sts.Spec.Template.Spec.Containers[0].Image
	}
	a := e.create("alice", "shop", 17)
	e.ctl.Reconcile(ctx, e.reload(a.ID))
	e.markReady(a)
	e.ctl.Reconcile(ctx, e.reload(a.ID))
	wantA := "docker.io/library/postgres@" + testImage.Digest
	if stsImage(a) != wantA || e.reload(a.ID).Status != store.DBRunning {
		t.Fatalf("immagine iniziale: %s", stsImage(a))
	}

	// La release cambia la tabella (digest B): stesso store e stesso cluster, controller nuovo.
	digestB := "sha256:" + strings.Repeat("c", 64)
	dumper := &fakeDumper{}
	cfg := e.ctl.cfg
	cfg.Images = map[int]Image{17: {Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: digestB}}
	cfg.Dumper = dumper
	ctl2 := New(e.st, e.kube, cfg)
	ctl2.Reconcile(ctx, e.reload(a.ID))
	if stsImage(a) != wantA {
		t.Fatalf("la tabella cambiata ha aggiornato un database esistente: %s", stsImage(a))
	}
	// Il resize riavvia per CPU/RAM ma non cambia immagine.
	mem := int64(512 << 20)
	if _, err := e.st.UpdateManagedDB(ctx, a.ID, store.ManagedDBChange{MemoryBytes: &mem}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	ctl2.Reconcile(ctx, e.reload(a.ID))
	if stsImage(a) != wantA {
		t.Fatalf("il resize ha cambiato immagine: %s", stsImage(a))
	}
	// Una creazione nuova usa la tabella corrente.
	b, _ := e.st.CreateManagedDB(ctx, store.NewManagedDB{ID: uuid.New(), OwnerName: "bob", OwnerType: "user", OwnerID: uuid.New(),
		Name: "nuovo", PgMajor: 17, SizeBytes: 1 << 30, CPUMillis: 100, MemoryBytes: 256 << 20}, time.Now().UTC())
	if _, err := ctl2.Provision(ctx, b, "pw"); err != nil {
		t.Fatal(err)
	}
	ctl2.Reconcile(ctx, e.reload(b.ID))
	if stsImage(b) != "docker.io/library/postgres@"+digestB {
		t.Fatalf("la nuova creazione deve usare la tabella corrente: %s", stsImage(b))
	}

	// Aggiornamento esplicito: senza dumper non procede; con dump fallito non procede; con dump riuscito si'.
	noDumpCfg := cfg
	noDumpCfg.Dumper = nil
	noDump := New(e.st, e.kube, noDumpCfg)
	if err := noDump.UpgradeMinor(ctx, e.reload(a.ID), nil); !errors.Is(err, ErrDumpUnavailable) {
		t.Fatalf("senza dumper: %v", err)
	}
	dumper.err = errors.New("disco pieno")
	if err := ctl2.UpgradeMinor(ctx, e.reload(a.ID), nil); err == nil || !strings.Contains(err.Error(), "disco pieno") {
		t.Fatalf("dump fallito: %v", err)
	}
	ctl2.Reconcile(ctx, e.reload(a.ID))
	if stsImage(a) != wantA || *e.reload(a.ID).ImageDigest != testImage.Digest {
		t.Fatal("un dump fallito non deve cambiare l'immagine")
	}
	dumper.err = nil
	if err := ctl2.UpgradeMinor(ctx, e.reload(a.ID), nil); err != nil {
		t.Fatal(err)
	}
	if dumper.calls != 2 {
		t.Fatalf("dump eseguiti: %d", dumper.calls)
	}
	ctl2.Reconcile(ctx, e.reload(a.ID))
	if stsImage(a) != "docker.io/library/postgres@"+digestB {
		t.Fatalf("dopo l'aggiornamento esplicito: %s", stsImage(a))
	}
	if err := ctl2.UpgradeMinor(ctx, e.reload(a.ID), nil); !errors.Is(err, ErrUpToDate) {
		t.Fatalf("gia' aggiornato: %v", err)
	}
}

// DB21: i log si conservano nel database di core e si recuperano dopo la sostituzione del pod; niente
// righe duplicate fra un giro e l'altro. CPU/RAM entrano nei campioni solo se metrics-server risponde.
func TestDatabases_LogEMetricheConservati(t *testing.T) {
	var feed string
	var gone, withMetrics bool
	e := newCtlEnv(t, func(c *Config) {
		c.PodLogs = func(_ context.Context, _, _ string, _ *corev1.PodLogOptions) (io.ReadCloser, error) {
			if gone {
				return nil, errors.New("pod non trovato")
			}
			return io.NopCloser(strings.NewReader(feed)), nil
		}
		c.PodMetrics = func(context.Context, string) (int, int64, error) {
			if !withMetrics {
				return 0, 0, errors.New("metrics-server assente")
			}
			return 42, 100 << 20, nil
		}
	})
	ctx := context.Background()
	d := e.create("alice", "shop", 17)
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	e.markReady(d)
	feed = "2026-11-05T10:00:00.000001Z LOG:  avvio\n2026-11-05T10:00:01.000000Z LOG:  pronto\n"
	if err := e.ctl.CollectLogs(ctx, d); err != nil {
		t.Fatal(err)
	}
	feed += "2026-11-05T10:00:02.000000Z LOG:  checkpoint\n"
	if err := e.ctl.CollectLogs(ctx, d); err != nil {
		t.Fatal(err)
	}
	// Un giro che rilegge le stesse righe non le duplica; il pod sostituito non toglie la cronologia.
	if err := e.ctl.CollectLogs(ctx, d); err != nil {
		t.Fatal(err)
	}
	gone = true
	if err := e.ctl.CollectLogs(ctx, d); err == nil {
		t.Fatal("pod sparito: l'errore va riportato")
	}
	lines, err := e.st.ListManagedDBLogs(ctx, d.ID, store.ManagedDBLogFilter{})
	if err != nil || len(lines) != 3 || lines[0].Message != "LOG:  avvio" || lines[2].Message != "LOG:  checkpoint" {
		t.Fatalf("log conservati: %+v %v", lines, err)
	}
	if got, _ := e.st.ListManagedDBLogs(ctx, d.ID, store.ManagedDBLogFilter{Grep: "PRONTO"}); len(got) != 1 {
		t.Fatalf("grep: %+v", got)
	}

	// Campioni: senza metrics-server cpu/memoria restano NULL; con metrics-server entrano.
	e.sql.size = 5 << 20
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	withMetrics = true
	time.Sleep(1100 * time.Millisecond) // un campione per secondo
	e.ctl.Reconcile(ctx, e.reload(d.ID))
	bs, err := e.st.ManagedDBBuckets(ctx, d.ID, time.Now().Add(-time.Hour), 1)
	if err != nil || len(bs) < 2 {
		t.Fatalf("campioni: %+v %v", bs, err)
	}
	var noCPU, withCPU int
	for _, b := range bs {
		if b.DBBytes == nil || int64(*b.DBBytes) != 5<<20 {
			t.Fatalf("dimensione del campione: %+v", b)
		}
		if b.CPUMillis == nil {
			noCPU++
		} else if int(*b.CPUMillis) == 42 && int64(*b.MemoryBytes) == 100<<20 {
			withCPU++
		}
	}
	if noCPU == 0 || withCPU == 0 {
		t.Fatalf("senza metrics-server=%d con metrics-server=%d", noCPU, withCPU)
	}
}

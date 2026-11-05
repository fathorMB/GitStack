//go:build integration

package databases

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
	cfg := e.ctl.cfg
	cfg.Images = map[int]Image{17: {Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: digestB}}
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

	// Anche un giro completo (Once) del controller con la tabella B lascia A al database esistente e prende B
	// per il nuovo; il digest salvato resta quello della creazione.
	ctl2.Once(ctx)
	if stsImage(a) != wantA || stsImage(b) != "docker.io/library/postgres@"+digestB {
		t.Fatalf("dopo Once: a=%s b=%s", stsImage(a), stsImage(b))
	}
	if got := e.reload(a.ID); *got.ImageDigest != testImage.Digest || *e.reload(b.ID).ImageDigest != digestB {
		t.Fatalf("digest salvati: a=%s b=%s", *got.ImageDigest, *e.reload(b.ID).ImageDigest)
	}
}

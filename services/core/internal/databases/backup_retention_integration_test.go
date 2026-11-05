//go:build integration

package databases

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

func completedByKind(t *testing.T, e *ctlEnv, id uuid.UUID, kind string) map[uuid.UUID]store.ManagedDBBackup {
	t.Helper()
	rows, err := e.st.ListManagedDBBackups(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[uuid.UUID]store.ManagedDBBackup{}
	for _, b := range rows {
		if b.Kind == kind && b.Status == store.BackupCompleted {
			out[b.ID] = b
		}
	}
	return out
}

// Rework 1: il dump scelto per il ripristino sul posto e' il PIU' VECCHIO dei 3 di sicurezza (KeepSafety=3). Il dump
// di sicurezza preliminare porta i completati a 4 e la retention toglierebbe proprio quello: la prenotazione lo
// protegge per tutta la durata del ripristino, i dati finali sono quelli scelti e, a ripristino concluso, il dump non
// piu' prenotato torna potabile (righe e file spariscono, restano 3 dump di sicurezza).
func TestDatabaseRestore_SorgenteProtettaDallaRetention(t *testing.T) {
	e := newDumpEnv(t, func(c *Config) { c.KeepSafety = 3 })
	ctx := context.Background()
	d := e.running("alice", "p"+strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	e.seed(d) // 1:uno,2:due,3:tre
	var first store.ManagedDBBackup
	for i, mut := range []string{"", `UPDATE negozio SET nome = 'versione-A'`, `UPDATE negozio SET nome = 'versione-B'`} {
		if mut != "" {
			e.execAs(d, mut)
		}
		rec, err := e.ctl.Backup(ctx, d, store.BackupSafety, nil)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = rec
		}
	}
	e.execAs(d, `UPDATE negozio SET nome = 'versione-C'`)
	if n := len(completedByKind(t, e.ctlEnv, d.ID, store.BackupSafety)); n != 3 {
		t.Fatalf("dump di sicurezza prima del ripristino: %d", n)
	}

	if err := e.ctl.RestoreBackup(ctx, d, first, store.RestoreSame, nil); err != nil {
		t.Fatal(err)
	}
	// Un dump prenotato non si pota nemmeno se un altro dump supera il limite PRIMA che il controller parta.
	if _, err := e.ctl.Backup(ctx, e.reload(d.ID), store.BackupSafety, nil); err != nil {
		t.Fatal(err)
	}
	if got := completedByKind(t, e.ctlEnv, d.ID, store.BackupSafety); len(got) != 4 {
		t.Fatalf("il dump prenotato doveva restare oltre il limite: %d completati, voluti 4", len(got))
	}
	if !e.ctl.DumpExists(*first.File) {
		t.Fatal("il file del dump prenotato e' sparito prima del ripristino")
	}

	e.ctl.Reconcile(ctx, e.reload(d.ID))
	got := e.reload(d.ID)
	ops, _ := e.st.ManagedDBOperations(ctx, d.ID)
	if got.Status != store.DBRunning || ops.RestoreError != nil || ops.Restore != nil {
		t.Fatalf("stato %s, errore %v, pendente %v", got.Status, ops.RestoreError, ops.Restore)
	}
	if r := e.rows(got); r != knownRows {
		t.Fatalf("dati dopo il ripristino del dump piu' vecchio: %q, voluti %q", r, knownRows)
	}
	// Concluso il ripristino il dump non e' piu' prenotato: la retention rimasta in sospeso si applica.
	after := completedByKind(t, e.ctlEnv, d.ID, store.BackupSafety)
	if len(after) != 3 {
		t.Fatalf("dopo il ripristino i dump di sicurezza sono %d, voluti 3", len(after))
	}
	if _, ok := after[first.ID]; ok {
		t.Fatal("il dump non piu' prenotato doveva tornare potabile")
	}
	if e.ctl.DumpExists(*first.File) {
		t.Fatal("il file del dump potato e' rimasto sul disco")
	}
}

// Rework 1: sorgente prenotata da target=new. Il dump on_demand scelto sta in un database; mentre il database di
// destinazione aspetta il provisioning altri dump di quel database superano KeepOnDemand: il sorgente prenotato
// sopravvive, il ripristino riempie il database nuovo con i dati scelti e solo dopo il sorgente torna potabile.
func TestDatabaseRestore_SorgenteDiDatabaseNuovoProtettaDallaRetention(t *testing.T) {
	e := newDumpEnv(t, func(c *Config) { c.KeepOnDemand = 2 })
	ctx := context.Background()
	src := e.running("alice", "q"+strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	e.seed(src)
	chosen, err := e.ctl.Backup(ctx, src, store.BackupOnDemand, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.execAs(src, `UPDATE negozio SET nome = 'dopo'`)
	if _, err := e.ctl.Backup(ctx, src, store.BackupOnDemand, nil); err != nil {
		t.Fatal(err)
	}

	newName := "n" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	nd, err := e.st.CreateManagedDB(ctx, store.NewManagedDB{ID: uuid.New(), OwnerName: "alice", OwnerType: "user", OwnerID: src.OwnerID,
		Name: newName, PgMajor: 17, SizeBytes: 1 << 30, CPUMillis: 100, MemoryBytes: 256 << 20}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	e.track(newName)
	if err := e.ctl.RestoreBackup(ctx, nd, chosen, store.RestoreNew, nil); err != nil {
		t.Fatal(err)
	}
	// Il database di destinazione aspetta il provisioning; intanto il sorgente produce altri 3 dump on_demand.
	for i := 0; i < 3; i++ {
		if _, err := e.ctl.Backup(ctx, src, store.BackupOnDemand, nil); err != nil {
			t.Fatal(err)
		}
	}
	rows := completedByKind(t, e.ctlEnv, src.ID, store.BackupOnDemand)
	if _, ok := rows[chosen.ID]; !ok || !e.ctl.DumpExists(*chosen.File) {
		t.Fatalf("il dump prenotato da target=new e' stato potato (%d completati)", len(rows))
	}
	if len(rows) != 3 { // i 2 piu' recenti + quello prenotato
		t.Fatalf("on_demand tenuti %d, voluti 3 (keep 2 + il prenotato)", len(rows))
	}
	if _, err := e.ctl.Provision(ctx, nd, "owner-pw-"+newName); err != nil {
		t.Fatal(err)
	}
	e.ctl.Reconcile(ctx, e.reload(nd.ID))
	e.markReady(nd)
	e.ctl.Reconcile(ctx, e.reload(nd.ID))
	got := e.reload(nd.ID)
	ops, _ := e.st.ManagedDBOperations(ctx, nd.ID)
	if got.Status != store.DBRunning || ops.RestoreError != nil || ops.Restore != nil {
		t.Fatalf("stato %s, errore %v, pendente %v", got.Status, ops.RestoreError, ops.Restore)
	}
	if r := e.rows(got); r != knownRows {
		t.Fatalf("dati del database nuovo %q, voluti %q", r, knownRows)
	}
	// Concluso il ripristino il sorgente non e' piu' prenotato e la retention del database sorgente si applica.
	after := completedByKind(t, e.ctlEnv, src.ID, store.BackupOnDemand)
	if len(after) != 2 {
		t.Fatalf("dopo il ripristino gli on_demand del sorgente sono %d, voluti 2", len(after))
	}
	if _, ok := after[chosen.ID]; ok || e.ctl.DumpExists(*chosen.File) {
		t.Fatal("il dump non piu' prenotato doveva tornare potabile")
	}
	// Una richiesta su un dump gia' potato e' rifiutata subito.
	if err := e.ctl.RestoreBackup(ctx, e.reload(nd.ID), chosen, store.RestoreNew, nil); err == nil {
		t.Fatal("la prenotazione di un dump potato doveva fallire")
	}
}

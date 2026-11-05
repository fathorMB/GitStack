//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// apiDumper e' un Dumper finto: dump con la magia PGDMP, o l'errore richiesto.
type apiDumper struct {
	mu  sync.Mutex
	err error
}

func (f *apiDumper) Dump(_ context.Context, _ store.ManagedDB, w io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	_, err := w.Write([]byte("PGDMP dati di prova"))
	return err
}

func (f *apiDumper) Restore(context.Context, store.ManagedDB, io.Reader, databases.RestoreOptions) error {
	return nil
}

// withBackups sostituisce il controller del router con uno che ha chiave, cartella (t.TempDir()) e dump finto.
func (e *dbEnv) withBackups(t *testing.T, d databases.Dumper, images map[int]databases.Image) {
	t.Helper()
	e.proxy.set(databases.New(store.New(e.pool), fake.NewSimpleClientset(), databases.Config{SQL: e.sql, Images: images, Dumper: d,
		DumpKey: []byte("0123456789abcdef0123456789abcdef"), DataDir: t.TempDir(), Verifier: okVerifier{}}))
}

func (e *dbEnv) setRunning(t *testing.T, owner, name string) openapi.Database {
	t.Helper()
	d, err := store.New(e.pool).GetManagedDB(context.Background(), owner, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.New(e.pool).SetManagedDBStatus(context.Background(), d.ID, store.DBRunning, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.New(e.pool).SetManagedDBImage(context.Background(), d.ID, "sha256:"+strings.Repeat("a", 64), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodGet, "/databases/"+owner+"/"+name, "alice", "")
	e.want(rec, http.StatusOK, "")
	var out openapi.Database
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func (e *dbEnv) backups(t *testing.T, owner, name string) []openapi.DatabaseBackup {
	t.Helper()
	rec := e.do(http.MethodGet, "/databases/"+owner+"/"+name+"/backups", "alice", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.DatabaseBackupList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l.Items
}

// DB13, DB15, DB2: elenco e creazione dei backup, esito e dimensione, errore visibile nel database, permessi (404),
// ripristino con conferma `owner/nome` (sul posto) e in un database nuovo (name_conflict), mai 501.
func TestDatabaseBackups_API(t *testing.T) {
	e := newDBEnv(t)
	e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)

	// Senza chiave di dump e volume i backup non sono configurati: errore stabile, mai un dump in chiaro.
	e.setRunning(t, "alice", "shop")
	e.want(e.do(http.MethodPost, "/databases/alice/shop/backups", "alice", ""), http.StatusUnprocessableEntity, "backup_unavailable")

	dumper := &apiDumper{}
	e.withBackups(t, dumper, nil)
	// Chi non e' admin dell'owner: 404 su tutto (DB2).
	e.want(e.do(http.MethodGet, "/databases/alice/shop/backups", "bob", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPost, "/databases/alice/shop/backups", "bob", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPost, "/databases/alice/shop/upgrade-minor", "bob", ""), http.StatusNotFound, "not_found")

	rec := e.do(http.MethodPost, "/databases/alice/shop/backups", "alice", "")
	e.want(rec, http.StatusAccepted, "")
	var started openapi.DatabaseBackup
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if started.Kind != openapi.OnDemand || started.Status != openapi.DatabaseBackupStatusInProgress {
		t.Fatalf("avvio: %+v", started)
	}
	var done openapi.DatabaseBackup
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if l := e.backups(t, "alice", "shop"); len(l) == 1 && l[0].Status == openapi.DatabaseBackupStatusCompleted {
			done = l[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if done.SizeBytes == nil || *done.SizeBytes <= 0 || done.CompletedAt == nil || done.Id != started.Id {
		t.Fatalf("backup completato: %+v", done)
	}

	// Un dump che fallisce e' visibile nell'elenco e nello stato del database (operations.backupError).
	dumper.err = errors.New("pg_dump: server chiuso")
	e.want(e.do(http.MethodPost, "/databases/alice/shop/backups", "alice", ""), http.StatusAccepted, "")
	for time.Now().Before(deadline) {
		l := e.backups(t, "alice", "shop")
		if len(l) == 2 && l[0].Status == openapi.DatabaseBackupStatusFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	l := e.backups(t, "alice", "shop")
	if len(l) != 2 || l[0].Status != openapi.DatabaseBackupStatusFailed || l[0].Error == nil || !strings.Contains(*l[0].Error, "server chiuso") {
		t.Fatalf("elenco dopo il fallimento: %+v", l)
	}
	rec = e.do(http.MethodGet, "/databases/alice/shop", "alice", "")
	var db openapi.Database
	_ = json.Unmarshal(rec.Body.Bytes(), &db)
	if db.Operations == nil || db.Operations.BackupError == nil || !strings.Contains(*db.Operations.BackupError, "server chiuso") || db.Operations.LastBackupAt == nil {
		t.Fatalf("operations: %+v", db.Operations)
	}

	// Ripristino sul posto: conferma obbligatoria e uguale a owner/nome (DB19, G8).
	path := "/databases/alice/shop/backups/" + done.Id.String() + "/restore"
	e.want(e.do(http.MethodPost, path, "alice", `{"target":"same"}`), http.StatusUnprocessableEntity, "confirmation_required")
	e.want(e.do(http.MethodPost, path, "alice", `{"target":"same","confirm":"alice/altro"}`), http.StatusUnprocessableEntity, "confirmation_mismatch")
	e.want(e.do(http.MethodPost, path, "bob", `{"target":"same","confirm":"alice/shop"}`), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPost, "/databases/alice/shop/backups/"+uuid.NewString()+"/restore", "alice", `{"target":"same","confirm":"alice/shop"}`), http.StatusNotFound, "not_found")
	// Il dump fallito non si ripristina.
	e.want(e.do(http.MethodPost, "/databases/alice/shop/backups/"+l[0].Id.String()+"/restore", "alice", `{"target":"same","confirm":"alice/shop"}`), http.StatusUnprocessableEntity, "backup_not_completed")
	rec = e.do(http.MethodPost, path, "alice", `{"target":"same","confirm":"alice/shop"}`)
	e.want(rec, http.StatusAccepted, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &db)
	if db.Status != openapi.DatabaseStatusRestoring || db.Operations == nil || db.Operations.RestoreInProgress == nil || !*db.Operations.RestoreInProgress {
		t.Fatalf("dopo la richiesta: stato %s operations %+v", db.Status, db.Operations)
	}
	// Un secondo ripristino mentre il database non e' `running`: rifiutato.
	e.want(e.do(http.MethodPost, path, "alice", `{"target":"same","confirm":"alice/shop"}`), http.StatusUnprocessableEntity, "database_not_ready")

	// In un database nuovo: il nome della destinazione e' libero o no (name_conflict).
	e.want(e.do(http.MethodPost, path, "alice", `{"target":"new"}`), http.StatusConflict, "name_conflict")
	rec = e.do(http.MethodPost, path, "alice", `{"target":"new","name":"shop-copia"}`)
	e.want(rec, http.StatusAccepted, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &db)
	if db.Name != "shop-copia" || db.Status != openapi.DatabaseStatusCreating || db.Operations == nil || !*db.Operations.RestoreInProgress {
		t.Fatalf("database nuovo: %+v", db)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("la risposta non deve rivelare password: %s", rec.Body.String())
	}
	e.want(e.do(http.MethodPost, path, "alice", `{"target":"new","name":"Nome Non Valido"}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.do(http.MethodPost, path, "alice", `{"target":"altro"}`), http.StatusUnprocessableEntity, "validation_failed")
}

// DB6: la richiesta di aggiornamento minor del singolo database (admin dell'owner) e quella per tutti
// (amministratore d'istanza); 409 se non c'e' niente da aggiornare, 403 per chi non e' amministratore.
func TestDatabaseBackups_AggiornamentoMinorePermessi(t *testing.T) {
	e := newDBEnv(t)
	e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	e.setRunning(t, "alice", "shop") // digest fissato: aaaa...
	same := map[int]databases.Image{17: {Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: "sha256:" + strings.Repeat("a", 64)}}
	e.withBackups(t, &apiDumper{}, same)
	e.want(e.do(http.MethodPost, "/databases/alice/shop/upgrade-minor", "alice", ""), http.StatusConflict, "no_update")

	newer := map[int]databases.Image{17: {Repository: "docker.io/library/postgres", Tag: "17-alpine", Digest: "sha256:" + strings.Repeat("b", 64)}}
	e.withBackups(t, &apiDumper{}, newer)
	rec := e.do(http.MethodPost, "/databases/alice/shop/upgrade-minor", "alice", "")
	e.want(rec, http.StatusAccepted, "")
	var db openapi.Database
	_ = json.Unmarshal(rec.Body.Bytes(), &db)
	if db.Operations == nil || !db.Operations.MinorUpdateAvailable || db.Operations.UpgradeRequested == nil || !*db.Operations.UpgradeRequested {
		t.Fatalf("operations: %+v", db.Operations)
	}
	// Una seconda richiesta mentre la prima e' pendente non ne crea un'altra (idempotente).
	e.want(e.do(http.MethodPost, "/databases/alice/shop/upgrade-minor", "alice", ""), http.StatusAccepted, "")

	// Per tutti: solo l'amministratore d'istanza.
	e.want(e.do(http.MethodPost, "/admin/databases/upgrade-minor", "bob", ""), http.StatusForbidden, "forbidden")
	e.id.sysAdmin[users["carol"]] = true
	rec = e.do(http.MethodPost, "/admin/databases/upgrade-minor", "carol", "")
	e.want(rec, http.StatusAccepted, "")
	var out openapi.DatabaseMinorUpgradeRequested
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Requested != 0 { // l'unico database ha gia' la richiesta pendente
		t.Fatalf("richieste nuove: %d", out.Requested)
	}
}

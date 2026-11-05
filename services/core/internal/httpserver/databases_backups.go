package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Backup, ripristino e aggiornamento minor dei database gestiti (v3-d, GIT-553, ADR 0003, DB6, DB13, DB15).
// Permessi come le altre operazioni (DB2): `admin` sull'owner, 404 per chi non lo e'. I contenuti dei dump non
// escono mai dall'API.

func backupToAPI(b store.ManagedDBBackup) openapi.DatabaseBackup {
	out := openapi.DatabaseBackup{Id: openapi_types.UUID(b.ID), Kind: openapi.DatabaseBackupKind(b.Kind), Status: openapi.DatabaseBackupStatus(b.Status),
		SizeBytes: b.SizeBytes, Error: b.Error, CreatedAt: b.CreatedAt, CompletedAt: b.CompletedAt}
	if b.CreatedBy != nil {
		out.CreatedBy = &openapi.AppActor{Id: openapi_types.UUID(b.CreatedBy.ID), Username: b.CreatedBy.Username, Type: openapi.AppActorType(b.CreatedBy.Type)}
	}
	return out
}

// apiDatabase e' databaseToAPI con lo stato di backup, ripristino e aggiornamento (operations).
func (s *apiServer) apiDatabase(ctx context.Context, d store.ManagedDB) openapi.Database {
	out := databaseToAPI(d)
	ops, err := s.resources.ManagedDBOperations(ctx, d.ID)
	if err != nil {
		slog.Default().Warn("stato delle operazioni del database non letto", "db", d.ID, "err", err)
		return out
	}
	o := openapi.DatabaseOperations{LastBackupAt: ops.LastBackupAt, BackupError: ops.BackupError, RestoreError: ops.RestoreError, UpgradeError: ops.UpgradeError}
	restoring, upgrading := ops.Restore != nil, ops.UpgradeRequest != nil
	o.RestoreInProgress, o.UpgradeRequested = &restoring, &upgrading
	if s.databasesOn() {
		o.MinorUpdateAvailable = s.databases.Controller.MinorUpdateAvailable(d)
	}
	out.Operations = &o
	return out
}

// ListDatabaseBackups implementa GET /databases/{owner}/{name}/backups.
func (s *apiServer) ListDatabaseBackups(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, _, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	rows, err := s.resources.ListManagedDBBackups(r.Context(), d.ID)
	if err != nil {
		appInternalError(w, "elenco dei backup del database", err)
		return
	}
	out := openapi.DatabaseBackupList{Items: make([]openapi.DatabaseBackup, 0, len(rows))}
	for _, b := range rows {
		out.Items = append(out.Items, backupToAPI(b))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateDatabaseBackup implementa POST /databases/{owner}/{name}/backups: 202 col dump `in_progress`.
func (s *apiServer) CreateDatabaseBackup(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, u, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	rec, err := s.databases.Controller.StartBackup(r.Context(), d, u.actor())
	if s.writeBackupError(w, err) {
		return
	}
	writeJSON(w, http.StatusAccepted, backupToAPI(rec))
}

// writeBackupError traduce gli errori comuni di backup e ripristino; ritorna true se ha risposto.
func (s *apiServer) writeBackupError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrBackupInProgress):
		writeError(w, http.StatusConflict, "backup_in_progress", "Un backup di questo database e' gia' in corso.")
	case errors.Is(err, store.ErrDBBusy):
		writeError(w, http.StatusConflict, "operation_in_progress", "Il database ha gia' un ripristino o un aggiornamento in attesa.")
	case errors.Is(err, databases.ErrNotReady):
		writeError(w, http.StatusUnprocessableEntity, "database_not_ready", "Il database non e' ancora pronto: riprova quando e' `running`.")
	case errors.Is(err, databases.ErrBackupUnavailable):
		writeError(w, http.StatusUnprocessableEntity, "backup_unavailable", "I backup dei database non sono configurati in questa installazione (manca la chiave di dump o il volume databases-data).")
	case errors.Is(err, databases.ErrBackupFileMissing), errors.Is(err, store.ErrBackupNotFound):
		writeError(w, http.StatusUnprocessableEntity, "backup_file_missing", "Il file di questo backup non c'e' piu': scegline un altro.")
	case errors.Is(err, databases.ErrNoMinorUpdate):
		writeError(w, http.StatusConflict, "no_update", "Il database e' gia' sull'immagine corrente dell'istanza.")
	default:
		appInternalError(w, "operazione di backup del database", err)
	}
	return true
}

// RestoreDatabaseBackup implementa POST /databases/{owner}/{name}/backups/{backupId}/restore (DB15).
func (s *apiServer) RestoreDatabaseBackup(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, backupId openapi.DatabaseBackupIdParam) {
	d, u, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	var in openapi.DatabaseRestoreInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	b, err := s.resources.GetManagedDBBackup(r.Context(), d.ID, uuid.UUID(backupId))
	if errors.Is(err, store.ErrBackupNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
		return
	}
	if err != nil {
		appInternalError(w, "lettura del backup", err)
		return
	}
	if b.Status != store.BackupCompleted {
		writeError(w, http.StatusUnprocessableEntity, "backup_not_completed", "Si ripristina solo un backup completato.")
		return
	}
	ctl := s.databases.Controller
	switch in.Target {
	case openapi.DatabaseRestoreTargetSame:
		want := d.OwnerName + "/" + d.Name
		switch {
		case in.Confirm == nil || *in.Confirm == "":
			writeError(w, http.StatusUnprocessableEntity, "confirmation_required", "Conferma il ripristino con confirm="+want+": il database viene sostituito con il contenuto del backup (prima si fa un dump di sicurezza).")
			return
		case *in.Confirm != want:
			writeError(w, http.StatusUnprocessableEntity, "confirmation_mismatch", "La conferma non corrisponde: scrivi "+want+".")
			return
		}
		if d.Status != store.DBRunning {
			writeError(w, http.StatusUnprocessableEntity, "database_not_ready", "Il database non e' ancora pronto: riprova quando e' `running`.")
			return
		}
		if err := ctl.RestoreBackup(r.Context(), d, b, store.RestoreSame, u.actor()); s.writeBackupError(w, err) {
			return
		}
		updated, err := s.resources.GetManagedDBByID(r.Context(), d.ID)
		if err != nil {
			appInternalError(w, "lettura del database", err)
			return
		}
		writeJSON(w, http.StatusAccepted, s.apiDatabase(r.Context(), updated))
	case openapi.DatabaseRestoreTargetNew:
		target := d.Name
		if in.Name != nil && *in.Name != "" {
			target = *in.Name
		}
		if !databaseNameRE.MatchString(target) {
			writeFieldError(w, "name", "Il nome e' una label DNS: minuscole, cifre e '-', da 1 a 30 caratteri, senza '-' ai bordi.")
			return
		}
		if b.File == nil || !ctl.DumpExists(*b.File) {
			s.writeBackupError(w, databases.ErrBackupFileMissing)
			return
		}
		id, err := uuid.NewV7()
		if err != nil {
			appInternalError(w, "id del database", err)
			return
		}
		pw, err := databases.NewPassword()
		if err != nil {
			appInternalError(w, "generazione della password", err)
			return
		}
		nd, err := s.resources.CreateManagedDB(r.Context(), store.NewManagedDB{ID: id, OwnerName: d.OwnerName, OwnerType: d.OwnerType, OwnerID: d.OwnerID,
			Name: target, PgMajor: d.PgMajor, SizeBytes: d.SizeBytes, CPUMillis: d.CPUMillis, MemoryBytes: d.MemoryBytes, Actor: u.actor()}, s.now())
		if err != nil {
			if s.writeDatabaseConflict(w, err) {
				return
			}
			appInternalError(w, "creazione del database di destinazione", err)
			return
		}
		// Il ripristino e' registrato PRIMA del Provision: il controller lo vede al primo giro utile.
		if err := ctl.RestoreBackup(r.Context(), nd, b, store.RestoreNew, u.actor()); err != nil {
			_ = s.resources.RemoveManagedDB(context.WithoutCancel(r.Context()), nd.ID, u.actor(), s.now())
			s.writeBackupError(w, err)
			return
		}
		if _, err := ctl.Provision(r.Context(), nd, pw); err != nil {
			slog.Default().Error("preparazione del database di destinazione non riuscita", "db", nd.ID, "err", err)
			_ = s.resources.RemoveManagedDB(context.WithoutCancel(r.Context()), nd.ID, u.actor(), s.now())
			writeError(w, http.StatusServiceUnavailable, "cluster_unavailable", "Il cluster non ha preparato il database: riprova.")
			return
		}
		created, err := s.resources.GetManagedDBByID(r.Context(), nd.ID)
		if err != nil {
			appInternalError(w, "lettura del database", err)
			return
		}
		writeJSON(w, http.StatusAccepted, s.apiDatabase(r.Context(), created))
	default:
		writeFieldError(w, "target", "target: same o new.")
	}
}

// UpgradeDatabaseMinor implementa POST /databases/{owner}/{name}/upgrade-minor (DB6).
func (s *apiServer) UpgradeDatabaseMinor(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, u, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	if err := s.databases.Controller.RequestMinorUpgrade(r.Context(), d, u.actor()); s.writeBackupError(w, err) {
		return
	}
	updated, err := s.resources.GetManagedDBByID(r.Context(), d.ID)
	if err != nil {
		appInternalError(w, "lettura del database", err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.apiDatabase(r.Context(), updated))
}

// UpgradeAllDatabasesMinor implementa POST /admin/databases/upgrade-minor (solo amministratore d'istanza).
func (s *apiServer) UpgradeAllDatabasesMinor(w http.ResponseWriter, r *http.Request) {
	if !s.databasesOn() {
		databaseDisabled(w)
		return
	}
	callerID, ok := s.requireInstanceAdmin(w, r, "chiedere l'aggiornamento minor di tutti i database")
	if !ok {
		return
	}
	caller, _, _ := callerIdentity(w, r)
	n, err := s.databases.Controller.RequestMinorUpgradeAll(r.Context(), &store.AppActor{ID: callerID, Username: caller.Username, Type: "human"})
	if err != nil {
		appInternalError(w, "richiesta di aggiornamento minor", err)
		return
	}
	writeJSON(w, http.StatusAccepted, openapi.DatabaseMinorUpgradeRequested{Requested: n})
}

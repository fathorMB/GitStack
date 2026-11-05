package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Database gestiti (v3-d, GIT-551, ADR 0003): elenco, creazione, lettura, ridimensionamento, eliminazione,
// rotazione delle credenziali, log e metriche. Permessi (DB2): serve `admin` sull'owner; chi non lo e'
// riceve 404 come per i repo privati, e l'amministratore dell'istanza vede tutto.

// DatabaseController e' cio' che l'API chiede al controller dei database (internal/databases).
type DatabaseController interface {
	Provision(ctx context.Context, d store.ManagedDB, ownerPassword string) (string, error)
	Delete(ctx context.Context, d store.ManagedDB, actor *store.AppActor) error
	RotateOwner(ctx context.Context, d store.ManagedDB) (string, error)
	// Collegamenti con le app (GIT-552): ruolo di login proprio, variabili, scollegamento e rotazione.
	LinkApp(ctx context.Context, keys store.AppSealer, d store.ManagedDB, app store.App, mode string, actor *store.AppActor) (store.ManagedDBLink, databases.LinkCredentials, error)
	RotateLink(ctx context.Context, keys store.AppSealer, d store.ManagedDB, link store.ManagedDBLink, actor *store.AppActor) (databases.LinkCredentials, error)
	Unlink(ctx context.Context, d store.ManagedDB, link store.ManagedDBLink, actor *store.AppActor) (bool, error)
	// Accesso dalla LAN (GIT-554): SetLAN assegna o libera la porta e, spegnendo, chiude subito il Service.
	SetLAN(ctx context.Context, d store.ManagedDB, enable bool, actor *store.AppActor) (store.ManagedDB, error)
	LANInfo(ctx context.Context, d store.ManagedDB, requestHost string) (databases.LANInfo, error)
	Kick()
	// Backup, ripristino e aggiornamento minor (GIT-553).
	StartBackup(ctx context.Context, d store.ManagedDB, actor *store.AppActor) (store.ManagedDBBackup, error)
	RestoreBackup(ctx context.Context, d store.ManagedDB, b store.ManagedDBBackup, mode string, actor *store.AppActor) error
	DumpExists(rel string) bool
	RequestMinorUpgrade(ctx context.Context, d store.ManagedDB, actor *store.AppActor) error
	RequestMinorUpgradeAll(ctx context.Context, actor *store.AppActor) (int, error)
	MinorUpdateAvailable(d store.ManagedDB) bool
	// Impostazioni d'istanza cambiate dalla console (GIT-557): applicazione e valori in uso.
	SetRuntime(databases.Runtime)
	EffectiveLANRange() (from, to int)
	EffectiveKeep() (scheduled, onDemand int)
}

var _ DatabaseController = (*databases.Controller)(nil)

// DatabasesConfig abilita i database gestiti (interruttore d'istanza, DB23). AllowedMajors sono le major
// ammesse (DB1); vuoto vale 16 e 17.
type DatabasesConfig struct {
	Controller    DatabaseController
	AllowedMajors []int
	// Images sono le major che hanno un'immagine nella tabella del chart (DB5): la console non ne ammette altre.
	// Vuoto vale 16 e 17.
	Images []int
}

// WithDatabases accende i database gestiti. Senza, ogni operazione del tag databases risponde 422 databases_disabled.
func WithDatabases(c DatabasesConfig) Option {
	return func(o *routerOptions) { o.databases = c }
}

var databaseNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,28}[a-z0-9])?$`)

// databaseScopeNS e' lo spazio UUID delle risorse «database di un owner» in identity.
var databaseScopeNS = uuid.MustParse("6b0c9f7e-3a52-4d0c-9a53-7a0c6f5d1d11")

// ownerDatabaseScope e' la risorsa di identity che rappresenta «i database di questo owner»: ha owner e
// visibilita' private, quindi `admin` vale per l'owner utente e per gli amministratori dell'organizzazione
// (M-03), mai per i membri semplici. Deterministica: nessuna riga da ripulire.
func ownerDatabaseScope(owner identityclient.Owner) uuid.UUID {
	return uuid.NewSHA1(databaseScopeNS, []byte("databases:"+owner.ID.String()))
}

func (s *apiServer) databasesOn() bool { return s.databases.Controller != nil }

// databaseOwnerAccess risolve l'owner e dice se il chiamante e' `admin` su di esso (isAdmin). Risponde da solo
// e ritorna ok=false se la funzione e' spenta, l'owner non esiste o identity non risponde; NON risponde 404 a
// chi non e' admin: serve a chi accetta anche un altro permesso (scollegare, ruotare un collegamento, DB9).
func (s *apiServer) databaseOwnerAccess(w http.ResponseWriter, r *http.Request, ownerName string) (owner identityclient.Owner, u trustUser, isAdmin, ok bool) {
	if !s.databasesOn() {
		databaseDisabled(w)
		return identityclient.Owner{}, trustUser{}, false, false
	}
	caller, callerID, ok := callerIdentity(w, r)
	if !ok {
		return identityclient.Owner{}, trustUser{}, false, false
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile applicare i permessi.")
		return identityclient.Owner{}, trustUser{}, false, false
	}
	var err error
	owner, err = s.repoIdentity.ResolveOwner(r.Context(), ownerName)
	if err != nil {
		if errors.Is(err, identityclient.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
			return identityclient.Owner{}, trustUser{}, false, false
		}
		slog.Default().Warn("risoluzione dell'owner non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
		return identityclient.Owner{}, trustUser{}, false, false
	}
	u = trustUser{ID: callerID, Username: caller.Username, Type: "human"}
	if s.readable != nil {
		isAdmin, _, err := s.readable.ReadableResources(r.Context(), callerID)
		if err != nil {
			slog.Default().Warn("verifica dell'amministratore non riuscita", "err", err)
			writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
			return identityclient.Owner{}, trustUser{}, false, false
		}
		if isAdmin {
			u.InstanceAdmin = true
			return owner, u, true, true
		}
	}
	scope := ownerDatabaseScope(owner)
	if err := s.repoIdentity.SetResourceAttributes(r.Context(), scope, owner.Type, owner.ID, "private"); err != nil {
		slog.Default().Warn("attributi dell'owner dei database non impostati", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
		return identityclient.Owner{}, trustUser{}, false, false
	}
	var allowed bool
	allowed, err = s.repoIdentity.HasRole(r.Context(), callerID, scope, "admin")
	if err != nil {
		slog.Default().Warn("verifica del permesso non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
		return identityclient.Owner{}, trustUser{}, false, false
	}
	return owner, u, allowed, true
}

// databaseOwner risolve l'owner e verifica `admin` su di esso. Risponde da solo e ritorna false in ogni
// caso diverso dal permesso: 404 per chi non e' admin dell'owner (DB2), 503 se identity non c'e'.
func (s *apiServer) databaseOwner(w http.ResponseWriter, r *http.Request, ownerName string) (identityclient.Owner, trustUser, bool) {
	owner, u, isAdmin, ok := s.databaseOwnerAccess(w, r, ownerName)
	if !ok {
		return identityclient.Owner{}, trustUser{}, false
	}
	if !isAdmin {
		writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
		return identityclient.Owner{}, trustUser{}, false
	}
	return owner, u, true
}

// trustUser e' il chiamante di un'operazione sui database.
type trustUser struct {
	ID            uuid.UUID
	Username      string
	Type          string
	InstanceAdmin bool
}

func (u trustUser) actor() *store.AppActor {
	return &store.AppActor{ID: u.ID, Username: u.Username, Type: u.Type}
}

// databaseFor risolve owner, permesso e database; 404 se il database non esiste.
func (s *apiServer) databaseFor(w http.ResponseWriter, r *http.Request, ownerName, name string) (store.ManagedDB, trustUser, bool) {
	owner, u, ok := s.databaseOwner(w, r, ownerName)
	if !ok {
		return store.ManagedDB{}, trustUser{}, false
	}
	d, err := s.resources.GetManagedDB(r.Context(), owner.Name, name)
	if errors.Is(err, store.ErrManagedDBNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
		return store.ManagedDB{}, trustUser{}, false
	}
	if err != nil {
		appInternalError(w, "lettura del database", err)
		return store.ManagedDB{}, trustUser{}, false
	}
	return d, u, true
}

func databaseToAPI(d store.ManagedDB) openapi.Database {
	out := openapi.Database{
		Id: openapi_types.UUID(d.ID), Owner: d.OwnerName, Name: openapi.DatabaseName(d.Name), PgMajor: openapi.DatabasePgMajor(d.PgMajor),
		Status: openapi.DatabaseStatus(d.Status), SizeBytes: d.SizeBytes, CpuMillis: d.CPUMillis, MemoryBytes: d.MemoryBytes,
		LanEnabled: d.LANEnabled, LanPort: lanPortIfOn(d), LinkCount: d.LinkCount, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
	if d.CreatedBy != nil {
		out.CreatedBy = &openapi.AppActor{Id: openapi_types.UUID(d.CreatedBy.ID), Username: d.CreatedBy.Username, Type: openapi.AppActorType(d.CreatedBy.Type)}
	}
	if d.UsageBytes != nil {
		out.Usage = &openapi.DatabaseUsage{DatabaseBytes: *d.UsageBytes, DiskState: openapi.DatabaseUsageDiskState(d.DiskState)}
	}
	return out
}

func (s *apiServer) writeDatabaseConflict(w http.ResponseWriter, err error) bool {
	var ce *store.CapacityError
	switch {
	case errors.Is(err, store.ErrManagedDBNameTaken):
		writeError(w, http.StatusConflict, "name_conflict", "L'owner ha gia' un database con questo nome.")
	case errors.As(err, &ce):
		writeErrorDetails(w, http.StatusConflict, "insufficient_capacity", "La quota dell'owner (condivisa con app e volumi) o la riserva delle app non basta per questo database.", map[string]any{
			"owner":   ce.Owner,
			"missing": ce.Missing,
			"limits":  map[string]any{"cpuMillis": ce.Limits.CPUMillis, "memoryBytes": ce.Limits.MemoryBytes, "diskBytes": ce.Limits.DiskBytes},
			"used":    map[string]any{"cpuMillis": ce.Used.CPUMillis, "memoryBytes": ce.Used.MemoryBytes, "diskBytes": ce.Used.DiskBytes},
		})
	default:
		return false
	}
	return true
}

func (s *apiServer) allowedMajor(ctx context.Context, m int) bool {
	for _, a := range s.effectiveMajors(ctx) {
		if a == m {
			return true
		}
	}
	return false
}

// ListDatabases implementa GET /databases/{owner}.
func (s *apiServer) ListDatabases(w http.ResponseWriter, r *http.Request, ownerParam openapi.RepoOwnerParam) {
	owner, _, ok := s.databaseOwner(w, r, string(ownerParam))
	if !ok {
		return
	}
	dbs, err := s.resources.ListManagedDBs(r.Context(), owner.Name)
	if err != nil {
		appInternalError(w, "elenco dei database", err)
		return
	}
	out := openapi.DatabaseList{Items: make([]openapi.Database, 0, len(dbs))}
	for _, d := range dbs {
		out.Items = append(out.Items, s.apiDatabase(r.Context(), d))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateDatabase implementa POST /databases/{owner}: 202 col database `creating` e la password una tantum (DB22).
func (s *apiServer) CreateDatabase(w http.ResponseWriter, r *http.Request, ownerParam openapi.RepoOwnerParam) {
	owner, u, ok := s.databaseOwner(w, r, string(ownerParam))
	if !ok {
		return
	}
	var in openapi.DatabaseCreateInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	name := string(in.Name)
	if !databaseNameRE.MatchString(name) {
		writeFieldError(w, "name", "Il nome e' una label DNS: minuscole, cifre e '-', da 1 a 30 caratteri, senza '-' ai bordi.")
		return
	}
	major := int(in.PgMajor)
	if !s.allowedMajor(r.Context(), major) {
		writeError(w, http.StatusUnprocessableEntity, "pg_major_not_allowed", fmt.Sprintf("La major %d di PostgreSQL non e' ammessa in questa installazione.", major))
		return
	}
	size, cpu, mem := store.DefaultDBSizeBytes, store.DefaultDBCPUMillis, store.DefaultDBMemoryBytes
	if in.SizeBytes != nil {
		size = *in.SizeBytes
	}
	if in.CpuMillis != nil {
		cpu = *in.CpuMillis
	}
	if in.MemoryBytes != nil {
		mem = *in.MemoryBytes
	}
	if size < 1 || cpu < 1 || mem < 1 {
		writeFieldError(w, "resources", "Dimensione, CPU e memoria devono essere positive.")
		return
	}
	if !s.withinDatabaseMax(w, r.Context(), &size, &cpu, &mem) {
		return
	}
	pw, err := databases.NewPassword()
	if err != nil {
		appInternalError(w, "generazione della password", err)
		return
	}
	id, err := uuid.NewV7()
	if err != nil {
		appInternalError(w, "id del database", err)
		return
	}
	d, err := s.resources.CreateManagedDB(r.Context(), store.NewManagedDB{ID: id, OwnerName: owner.Name, OwnerType: owner.Type, OwnerID: owner.ID,
		Name: name, PgMajor: major, SizeBytes: size, CPUMillis: cpu, MemoryBytes: mem, Actor: u.actor()}, s.now())
	if err != nil {
		if s.writeDatabaseConflict(w, err) {
			return
		}
		appInternalError(w, "creazione del database", err)
		return
	}
	// Provision ritorna la password persistita nel Secret: se il reconciler ha preceduto questa chiamata vale la sua.
	pw, err = s.databases.Controller.Provision(r.Context(), d, pw)
	if err != nil {
		// Namespace, RoleBinding o Secret non preparati: il database non nasce (il nome torna libero).
		slog.Default().Error("preparazione del database non riuscita", "db", d.ID, "err", err)
		if rerr := s.resources.RemoveManagedDB(context.WithoutCancel(r.Context()), d.ID, u.actor(), s.now()); rerr != nil {
			slog.Default().Error("database non annullato", "db", d.ID, "err", rerr)
		}
		writeError(w, http.StatusServiceUnavailable, "cluster_unavailable", "Il cluster non ha preparato il database: riprova.")
		return
	}
	writeJSON(w, http.StatusAccepted, openapi.DatabaseCreated{Database: databaseToAPI(d), Credentials: databaseCredentials(d, pw)})
}

func databaseCredentials(d store.ManagedDB, password string) openapi.DatabaseCredentials {
	return openapi.DatabaseCredentials{Username: databases.OwnerRole(d.Name), Password: password, Database: d.Name, SslMode: openapi.DatabaseCredentialsSslMode("verify-full")}
}

// GetDatabase implementa GET /databases/{owner}/{name}.
func (s *apiServer) GetDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, _, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.apiDatabase(r.Context(), d))
}

// UpdateDatabase implementa PATCH /databases/{owner}/{name}: ridimensionamento dichiarato (DB3).
func (s *apiServer) UpdateDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, u, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	var in openapi.DatabaseUpdateInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.SizeBytes == nil && in.CpuMillis == nil && in.MemoryBytes == nil {
		writeFieldError(w, "resources", "Indica almeno uno tra sizeBytes, cpuMillis e memoryBytes.")
		return
	}
	if in.SizeBytes != nil && *in.SizeBytes < 1 || in.CpuMillis != nil && *in.CpuMillis < 1 || in.MemoryBytes != nil && *in.MemoryBytes < 1 {
		writeFieldError(w, "resources", "Dimensione, CPU e memoria devono essere positive.")
		return
	}
	if d.Status == store.DBDeleting {
		writeError(w, http.StatusConflict, "conflict", "Il database e' in eliminazione.")
		return
	}
	if !s.withinDatabaseMax(w, r.Context(), in.SizeBytes, in.CpuMillis, in.MemoryBytes) {
		return
	}
	updated, err := s.resources.UpdateManagedDB(r.Context(), d.ID, store.ManagedDBChange{SizeBytes: in.SizeBytes, CPUMillis: in.CpuMillis, MemoryBytes: in.MemoryBytes}, u.actor(), s.now())
	if err != nil {
		if s.writeDatabaseConflict(w, err) {
			return
		}
		appInternalError(w, "ridimensionamento del database", err)
		return
	}
	s.databases.Controller.Kick()
	writeJSON(w, http.StatusOK, databaseToAPI(updated))
}

// DeleteDatabase implementa DELETE /databases/{owner}/{name}: eliminazione immediata con conferma `owner/nome` (DB19).
func (s *apiServer) DeleteDatabase(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.DeleteDatabaseParams) {
	d, u, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	want := d.OwnerName + "/" + d.Name
	switch {
	case params.Confirm == nil || *params.Confirm == "":
		writeError(w, http.StatusUnprocessableEntity, "confirmation_required", "Conferma l'eliminazione con confirm="+want+": il database e i suoi dati spariscono subito, senza cestino.")
		return
	case *params.Confirm != want:
		writeError(w, http.StatusUnprocessableEntity, "confirmation_mismatch", "La conferma non corrisponde: scrivi "+want+".")
		return
	}
	if err := s.databases.Controller.Delete(r.Context(), d, u.actor()); err != nil {
		slog.Default().Error("eliminazione del database non riuscita", "db", d.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "cluster_unavailable", "Il cluster non ha completato l'eliminazione: il database resta `deleting` e il controller ritenta.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RotateDatabaseCredentials implementa POST /databases/{owner}/{name}/credentials/rotate (DB22).
func (s *apiServer) RotateDatabaseCredentials(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, u, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	pw, err := s.databases.Controller.RotateOwner(r.Context(), d)
	if errors.Is(err, databases.ErrNotReady) {
		writeError(w, http.StatusUnprocessableEntity, "database_not_ready", "Il database non e' ancora pronto: riprova quando e' `running`.")
		return
	}
	if err != nil {
		appInternalError(w, "rotazione della password del database", err)
		return
	}
	if err := s.resources.RecordManagedDBAudit(r.Context(), d, "credentials_rotated", u.actor(), nil, s.now()); err != nil {
		slog.Default().Error("audit della rotazione non scritto", "db", d.ID, "err", err)
	}
	writeJSON(w, http.StatusOK, openapi.DatabaseRotated{Database: databaseToAPI(d), Credentials: databaseCredentials(d, pw)})
}

// GetDatabaseMetrics implementa GET /databases/{owner}/{name}/metrics: dimensione logica e riavvii dal
// modello; CPU e RAM dai campioni di metrics-server raccolti dal controller (GIT-568, DB21). Senza
// campioni con CPU nell'intervallo la sorgente e' dichiarata non disponibile: available=false con
// serie vuote, non zeri, mentre dimensione e riavvii restano.
func (s *apiServer) GetDatabaseMetrics(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.GetDatabaseMetricsParams) {
	d, _, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	rng := "1h"
	if params.Range != nil {
		rng = string(*params.Range)
	}
	spec, ok := appMetricRanges[rng]
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "range: 1h, 6h, 24h o 7d.")
		return
	}
	ctx := r.Context()
	since := s.now().UTC().Add(-spec.span)
	available, err := s.resources.ManagedDBMetricsSourceOK(ctx, d.ID, since)
	if err != nil {
		appInternalError(w, "lettura delle metriche del database", err)
		return
	}
	cpu := openapi.AppMetricSeries{Unit: openapi.Millicores, Points: []openapi.AppMetricPoint{}}
	mem := openapi.AppMetricSeries{Unit: openapi.Bytes, Points: []openapi.AppMetricPoint{}}
	if available {
		buckets, err := s.resources.ManagedDBMetricBuckets(ctx, d.ID, since, spec.step)
		if err != nil {
			appInternalError(w, "lettura delle serie del database", err)
			return
		}
		for _, b := range buckets {
			if b.CPUMillis != nil {
				cpu.Points = append(cpu.Points, openapi.AppMetricPoint{Time: b.Time, Value: float32(*b.CPUMillis)})
			}
			if b.MemoryBytes != nil {
				mem.Points = append(mem.Points, openapi.AppMetricPoint{Time: b.Time, Value: float32(*b.MemoryBytes)})
			}
		}
	}
	out := openapi.DatabaseMetrics{Available: available, Range: openapi.DatabaseMetricsRange(rng), StepSeconds: spec.step, Restarts24h: d.Restarts,
		Cpu: cpu, Memory: mem,
		DatabaseBytes: openapi.AppMetricSeries{Unit: openapi.Bytes, Points: []openapi.AppMetricPoint{}}}
	if d.UsageBytes != nil {
		out.DatabaseBytes.Points = append(out.DatabaseBytes.Points, openapi.AppMetricPoint{Time: s.now().UTC(), Value: float32(*d.UsageBytes)})
		out.Usage = &openapi.DatabaseUsage{DatabaseBytes: *d.UsageBytes, DiskState: openapi.DatabaseUsageDiskState(d.DiskState)}
	}
	writeJSON(w, http.StatusOK, out)
}

// GetDatabaseLogs implementa GET /databases/{owner}/{name}/logs: righe di PostgreSQL conservate dallo
// store come i log delle app (GIT-568, DB21): rileggibili dopo la sostituzione del pod, con la
// stessa retention. `follow` tiene aperta la risposta e manda le righe nuove man mano che il
// controller le scrive.
func (s *apiServer) GetDatabaseLogs(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.GetDatabaseLogsParams) {
	d, _, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	q, ok := s.databaseLogQueryFrom(w, params)
	if !ok {
		return
	}
	lines, err := s.resources.ListManagedDBLogs(r.Context(), d.ID, q.filter)
	if err != nil {
		appInternalError(w, "lettura dei log del database", err)
		return
	}
	aw := newDatabaseLogWriter(w, q.ndjson)
	var cursor int64
	for _, l := range lines {
		if l.ID > cursor {
			cursor = l.ID
		}
		if err := aw.line(l); err != nil {
			return
		}
	}
	aw.flush()
	if q.follow {
		s.followDatabaseLogs(r, d.ID, q, aw, cursor)
	}
}

// databaseLogQuery sono i parametri letti e validati di GET .../logs.
type databaseLogQuery struct {
	filter store.ManagedDBLogFilter
	follow bool
	ndjson bool
}

// databaseLogQueryFrom legge e valida i parametri; un valore sbagliato risponde 400 con il campo.
func (s *apiServer) databaseLogQueryFrom(w http.ResponseWriter, params openapi.GetDatabaseLogsParams) (databaseLogQuery, bool) {
	now := s.now()
	q := databaseLogQuery{follow: params.Follow != nil && *params.Follow}
	f := store.ManagedDBLogFilter{Limit: appLogsDefaultLimit}
	if params.Until != nil {
		u := params.Until.UTC()
		f.Until = &u
	}
	switch {
	case params.Since != nil:
		t, ok := parseAppLogSince(*params.Since, now)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "since: una durata (5m, 1h, 7d) o un istante RFC 3339.")
			return databaseLogQuery{}, false
		}
		f.Since = &t
	case q.follow:
		t := now.Add(-appLogsFollowWindow)
		f.Since = &t
	}
	if params.Grep != nil {
		if len(*params.Grep) > appLogsMaxGrep {
			writeError(w, http.StatusBadRequest, "bad_request", "grep: al massimo 200 caratteri.")
			return databaseLogQuery{}, false
		}
		f.Grep = *params.Grep
	}
	if params.Limit != nil {
		if *params.Limit < 1 || *params.Limit > appLogsMaxLimit {
			writeError(w, http.StatusBadRequest, "bad_request", "limit: da 1 a 10000.")
			return databaseLogQuery{}, false
		}
		f.Limit = *params.Limit
	}
	if params.Format != nil {
		switch string(*params.Format) {
		case "text", "":
		case "ndjson":
			q.ndjson = true
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "format: text o ndjson.")
			return databaseLogQuery{}, false
		}
	}
	q.filter = f
	return q, true
}

// followDatabaseLogs tiene aperta la risposta e manda le righe nuove finche' il client resta o
// finche' `until` non passa. Lo store si interroga ogni secondo a partire dall'ultimo id letto.
func (s *apiServer) followDatabaseLogs(r *http.Request, dbID uuid.UUID, q databaseLogQuery, aw *databaseLogWriter, cursor int64) {
	f := q.filter
	f.Since, f.Limit, f.AfterID = nil, 0, cursor
	tick := time.NewTicker(appLogsPoll)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		if f.Until != nil && !s.now().Before(*f.Until) {
			return
		}
		lines, err := s.resources.ListManagedDBLogs(r.Context(), dbID, f)
		if err != nil {
			return // la risposta è già iniziata: si chiude, il client riapre con since
		}
		for _, l := range lines {
			if l.ID > f.AfterID {
				f.AfterID = l.ID
			}
			if err := aw.line(l); err != nil {
				return
			}
		}
		aw.flush()
	}
}

// databaseLogWriter scrive le righe come testo (solo il messaggio) o come ndjson (DatabaseLogLine).
type databaseLogWriter struct {
	w      http.ResponseWriter
	ndjson bool
	enc    *json.Encoder
	headed bool
}

func newDatabaseLogWriter(w http.ResponseWriter, ndjson bool) *databaseLogWriter {
	return &databaseLogWriter{w: w, ndjson: ndjson, enc: json.NewEncoder(w)}
}

func (aw *databaseLogWriter) start() {
	if aw.headed {
		return
	}
	aw.headed = true
	if aw.ndjson {
		aw.w.Header().Set("Content-Type", "application/x-ndjson")
	} else {
		aw.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	aw.w.WriteHeader(http.StatusOK)
}

func (aw *databaseLogWriter) line(l store.ManagedDBLogLine) error {
	aw.start()
	if aw.ndjson {
		return aw.enc.Encode(openapi.DatabaseLogLine{Time: l.Time, Message: l.Message})
	}
	msg := strings.NewReplacer("\r", " ", "\n", " ").Replace(l.Message)
	_, err := io.WriteString(aw.w, msg+"\n")
	return err
}

func (aw *databaseLogWriter) flush() {
	aw.start()
	_ = http.NewResponseController(aw.w).Flush()
}

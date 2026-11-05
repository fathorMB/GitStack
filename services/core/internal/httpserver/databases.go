package httpserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	corev1 "k8s.io/api/core/v1"

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
	Logs(ctx context.Context, d store.ManagedDB, opts *corev1.PodLogOptions) (io.ReadCloser, error)
	Kick()
	// Backup, ripristino e aggiornamento minor (GIT-553).
	StartBackup(ctx context.Context, d store.ManagedDB, actor *store.AppActor) (store.ManagedDBBackup, error)
	RestoreBackup(ctx context.Context, d store.ManagedDB, b store.ManagedDBBackup, mode string, actor *store.AppActor) error
	DumpExists(rel string) bool
	RequestMinorUpgrade(ctx context.Context, d store.ManagedDB, actor *store.AppActor) error
	RequestMinorUpgradeAll(ctx context.Context, actor *store.AppActor) (int, error)
	MinorUpdateAvailable(d store.ManagedDB) bool
}

var _ DatabaseController = (*databases.Controller)(nil)

// DatabasesConfig abilita i database gestiti (interruttore d'istanza, DB23). AllowedMajors sono le major
// ammesse (DB1); vuoto vale 16 e 17.
type DatabasesConfig struct {
	Controller    DatabaseController
	AllowedMajors []int
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

// databaseOwner risolve l'owner e verifica `admin` su di esso. Risponde da solo e ritorna false in ogni
// caso diverso dal permesso: 404 per chi non e' admin dell'owner (DB2), 503 se identity non c'e'.
func (s *apiServer) databaseOwner(w http.ResponseWriter, r *http.Request, ownerName string) (identityclient.Owner, trustUser, bool) {
	if !s.databasesOn() {
		databaseDisabled(w)
		return identityclient.Owner{}, trustUser{}, false
	}
	caller, callerID, ok := callerIdentity(w, r)
	if !ok {
		return identityclient.Owner{}, trustUser{}, false
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile applicare i permessi.")
		return identityclient.Owner{}, trustUser{}, false
	}
	owner, err := s.repoIdentity.ResolveOwner(r.Context(), ownerName)
	if err != nil {
		if errors.Is(err, identityclient.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
			return identityclient.Owner{}, trustUser{}, false
		}
		slog.Default().Warn("risoluzione dell'owner non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
		return identityclient.Owner{}, trustUser{}, false
	}
	u := trustUser{ID: callerID, Username: caller.Username, Type: "human"}
	if s.readable != nil {
		isAdmin, _, err := s.readable.ReadableResources(r.Context(), callerID)
		if err != nil {
			slog.Default().Warn("verifica dell'amministratore non riuscita", "err", err)
			writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
			return identityclient.Owner{}, trustUser{}, false
		}
		if isAdmin {
			u.InstanceAdmin = true
			return owner, u, true
		}
	}
	scope := ownerDatabaseScope(owner)
	if err := s.repoIdentity.SetResourceAttributes(r.Context(), scope, owner.Type, owner.ID, "private"); err != nil {
		slog.Default().Warn("attributi dell'owner dei database non impostati", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
		return identityclient.Owner{}, trustUser{}, false
	}
	allowed, err := s.repoIdentity.HasRole(r.Context(), callerID, scope, "admin")
	if err != nil {
		slog.Default().Warn("verifica del permesso non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
		return identityclient.Owner{}, trustUser{}, false
	}
	if !allowed {
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
		LanEnabled: d.LANEnabled, LanPort: d.LANPort, LinkCount: d.LinkCount, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
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

func (s *apiServer) allowedMajor(m int) bool {
	allowed := s.databases.AllowedMajors
	if len(allowed) == 0 {
		allowed = []int{16, 17}
	}
	for _, a := range allowed {
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
	if !s.allowedMajor(major) {
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
// modello; CPU e RAM non sono lette da nessuno (GIT-568): `available: false` (serie vuote, non zeri) finche' metrics-server non e' letto.
func (s *apiServer) GetDatabaseMetrics(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.GetDatabaseMetricsParams) {
	d, _, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	rng := "1h"
	if params.Range != nil {
		rng = string(*params.Range)
	}
	steps := map[string]int{"1h": 60, "6h": 300, "24h": 900, "7d": 3600}
	step, ok := steps[rng]
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "range: 1h, 6h, 24h o 7d.")
		return
	}
	out := openapi.DatabaseMetrics{Range: openapi.DatabaseMetricsRange(rng), StepSeconds: step, Restarts24h: d.Restarts,
		Cpu:           openapi.AppMetricSeries{Unit: openapi.Millicores, Points: []openapi.AppMetricPoint{}},
		Memory:        openapi.AppMetricSeries{Unit: openapi.Bytes, Points: []openapi.AppMetricPoint{}},
		DatabaseBytes: openapi.AppMetricSeries{Unit: openapi.Bytes, Points: []openapi.AppMetricPoint{}}}
	if d.UsageBytes != nil {
		// Available resta false: CPU e RAM (metrics-server) non le legge nessuno in GIT-551 (GIT-568); la dimensione sola non basta.
		out.DatabaseBytes.Points = append(out.DatabaseBytes.Points, openapi.AppMetricPoint{Time: s.now().UTC(), Value: float32(*d.UsageBytes)})
		out.Usage = &openapi.DatabaseUsage{DatabaseBytes: *d.UsageBytes, DiskState: openapi.DatabaseUsageDiskState(d.DiskState)}
	}
	writeJSON(w, http.StatusOK, out)
}

// GetDatabaseLogs implementa GET /databases/{owner}/{name}/logs: righe di pods/log del pod di Postgres (DB21).
func (s *apiServer) GetDatabaseLogs(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, params openapi.GetDatabaseLogsParams) {
	d, _, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	limit := int64(1000)
	if params.Limit != nil {
		limit = int64(*params.Limit)
	}
	opts := &corev1.PodLogOptions{Container: databases.WorkloadName, Timestamps: true, TailLines: &limit}
	if params.Follow != nil && *params.Follow {
		opts.Follow = true
	}
	if params.Since != nil && *params.Since != "" {
		if dur, err := time.ParseDuration(*params.Since); err == nil && dur > 0 {
			secs := int64(dur.Seconds())
			opts.SinceSeconds = &secs
		}
	}
	rc, err := s.databases.Controller.Logs(r.Context(), d, opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "database_not_ready", "I log non sono disponibili: il database non ha un pod in esecuzione.")
		return
	}
	defer func() { _ = rc.Close() }()
	ndjson := params.Format != nil && string(*params.Format) == "ndjson"
	if ndjson {
		w.Header().Set("Content-Type", "application/x-ndjson")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	grep := ""
	if params.Grep != nil {
		grep = *params.Grep
	}
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		ts, msg, _ := strings.Cut(sc.Text(), " ")
		if grep != "" && !strings.Contains(msg, grep) {
			continue
		}
		if ndjson {
			t, perr := time.Parse(time.RFC3339Nano, ts)
			if perr != nil {
				t = s.now()
			}
			_, _ = fmt.Fprintf(w, `{"time":%s,"message":%s}`+"\n", strconv.Quote(t.UTC().Format(time.RFC3339Nano)), strconv.Quote(msg))
		} else {
			_, _ = fmt.Fprintln(w, msg)
		}
		if f, ok := w.(http.Flusher); ok && opts.Follow {
			f.Flush()
		}
	}
}

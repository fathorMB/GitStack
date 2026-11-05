package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Impostazioni d'istanza dei database gestiti (v3-d, GIT-557, ADR 0003, DB23) e console Admin.
//
// Interruttore: la funzione si accende e si spegne dal chart (`gitstack config set databases on|off`, install.sh,
// `gitstack upgrade --enable-databases`), perche' toglie e da' permessi Kubernetes. Da qui `enabled` e' solo
// letto. Le altre impostazioni le sceglie l'amministratore e valgono senza riavviare core: major ammesse,
// limiti per database, intervallo delle porte LAN e quanti dump tenere. Un valore mai scelto e' quello del
// chart (major, porte) o il predefinito (limiti, dump).

var defaultDatabaseMajors = []int{16, 17}

// availableMajors sono le major che hanno un'immagine nella tabella del chart (DB5).
func (s *apiServer) availableMajors() []int {
	if len(s.databases.Images) > 0 {
		return s.databases.Images
	}
	return defaultDatabaseMajors
}

// chartMajors sono le major ammesse dal chart (GITSTACK_CORE_DATABASES_ALLOWED_MAJORS o la tabella).
func (s *apiServer) chartMajors() []int {
	if len(s.databases.AllowedMajors) > 0 {
		return s.databases.AllowedMajors
	}
	return defaultDatabaseMajors
}

// effectiveMajors sono le major ammesse adesso: la scelta della console, altrimenti quelle del chart.
func (s *apiServer) effectiveMajors(ctx context.Context) []int {
	st, err := s.resources.GetManagedDBSettings(ctx)
	if err != nil {
		slog.Default().Warn("impostazioni dei database non lette: uso le major del chart", "err", err)
		return s.chartMajors()
	}
	if len(st.AllowedMajors) > 0 {
		return st.AllowedMajors
	}
	return s.chartMajors()
}

// databaseMax sono i limiti per database in uso adesso.
func databaseMax(st store.ManagedDBSettings) (size int64, cpu int, mem int64) {
	size, cpu, mem = store.DefaultMaxDBSizeBytes, store.DefaultMaxDBCPUMillis, store.DefaultMaxDBMemoryBytes
	if st.MaxSizeBytes != nil {
		size = *st.MaxSizeBytes
	}
	if st.MaxCPUMillis != nil {
		cpu = *st.MaxCPUMillis
	}
	if st.MaxMemoryBytes != nil {
		mem = *st.MaxMemoryBytes
	}
	return size, cpu, mem
}

// withinDatabaseMax rifiuta (422 su un campo) una dimensione, una CPU o una memoria sopra il massimo per
// database dell'istanza. I puntatori nil non si controllano. Risponde da sola: ritorna false se ha risposto.
func (s *apiServer) withinDatabaseMax(w http.ResponseWriter, ctx context.Context, size *int64, cpu *int, mem *int64) bool {
	st, err := s.resources.GetManagedDBSettings(ctx)
	if err != nil {
		appInternalError(w, "lettura dei limiti per database", err)
		return false
	}
	maxSize, maxCPU, maxMem := databaseMax(st)
	switch {
	case size != nil && *size > maxSize:
		writeFieldError(w, "sizeBytes", fmt.Sprintf("La dimensione supera il massimo per database di questa installazione (%d byte).", maxSize))
	case cpu != nil && *cpu > maxCPU:
		writeFieldError(w, "cpuMillis", fmt.Sprintf("La CPU supera il massimo per database di questa installazione (%d millesimi di vCPU).", maxCPU))
	case mem != nil && *mem > maxMem:
		writeFieldError(w, "memoryBytes", fmt.Sprintf("La memoria supera il massimo per database di questa installazione (%d byte).", maxMem))
	default:
		return true
	}
	return false
}

// databaseRuntime traduce le scelte della console nelle impostazioni che il controller legge a ogni uso.
func databaseRuntime(st store.ManagedDBSettings) databases.Runtime {
	var rt databases.Runtime
	if st.LANPortFrom != nil && st.LANPortTo != nil {
		rt.LANPortFrom, rt.LANPortTo = *st.LANPortFrom, *st.LANPortTo
	}
	if st.KeepScheduled != nil {
		rt.KeepScheduled = *st.KeepScheduled
	}
	if st.KeepOnDemand != nil {
		rt.KeepOnDemand = *st.KeepOnDemand
	}
	return rt
}

// ApplyDatabaseSettings carica all'avvio le scelte della console nel controller (le chiama databases_wiring).
func ApplyDatabaseSettings(ctx context.Context, st *store.Store, ctl interface{ SetRuntime(databases.Runtime) }) error {
	settings, err := st.GetManagedDBSettings(ctx)
	if err != nil {
		return err
	}
	ctl.SetRuntime(databaseRuntime(settings))
	return nil
}

func (s *apiServer) databaseSettingsResponse(ctx context.Context) (openapi.DatabasesInstanceSettings, error) {
	st, err := s.resources.GetManagedDBSettings(ctx)
	if err != nil {
		return openapi.DatabasesInstanceSettings{}, err
	}
	majors := s.chartMajors()
	if len(st.AllowedMajors) > 0 {
		majors = st.AllowedMajors
	}
	sort.Ints(majors)
	out := openapi.DatabasesInstanceSettings{Enabled: true, AllowedMajors: make([]openapi.DatabasePgMajor, 0, len(majors))}
	for _, m := range majors {
		out.AllowedMajors = append(out.AllowedMajors, openapi.DatabasePgMajor(m))
	}
	size, cpu, mem := databaseMax(st)
	out.MaxPerDatabase.SizeBytes, out.MaxPerDatabase.CpuMillis, out.MaxPerDatabase.MemoryBytes = size, cpu, mem
	from, to := s.databases.Controller.EffectiveLANRange()
	out.LanPortRange = openapi.DatabasesPortRange{From: from, To: to}
	sched, onDemand := s.databases.Controller.EffectiveKeep()
	out.BackupRetention = openapi.DatabasesBackupRetention{Scheduled: sched, OnDemand: onDemand}
	return out, nil
}

// GetAdminDatabasesSettings implementa GET /admin/databases/settings (solo amministratore d'istanza).
func (s *apiServer) GetAdminDatabasesSettings(w http.ResponseWriter, r *http.Request) {
	if !s.databasesOn() {
		databaseDisabled(w)
		return
	}
	if _, ok := s.requireInstanceAdmin(w, r, "vedere le impostazioni dei database"); !ok {
		return
	}
	out, err := s.databaseSettingsResponse(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle impostazioni dei database", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// UpdateAdminDatabasesSettings implementa PATCH /admin/databases/settings.
func (s *apiServer) UpdateAdminDatabasesSettings(w http.ResponseWriter, r *http.Request) {
	if !s.databasesOn() {
		databaseDisabled(w)
		return
	}
	adminID, ok := s.requireInstanceAdmin(w, r, "modificare le impostazioni dei database")
	if !ok {
		return
	}
	var in openapi.DatabasesInstanceSettingsInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if !in.Enabled {
		writeError(w, http.StatusUnprocessableEntity, "databases_switch_cli",
			"L'interruttore dei database non si spegne da qui: toglie i permessi Kubernetes di core e va fatto dal chart con «gitstack config set databases off» (non cancella database ne' dati).")
		return
	}
	st := store.ManagedDBSettings{}
	// Major: almeno una, con un'immagine nella tabella del chart, e tutte quelle in uso da un database.
	available := map[int]bool{}
	for _, m := range s.availableMajors() {
		available[m] = true
	}
	seen := map[int]bool{}
	for _, m := range in.AllowedMajors {
		major := int(m)
		switch {
		case !available[major]:
			writeFieldError(w, "allowedMajors", fmt.Sprintf("La major %d non ha un'immagine nella tabella del chart (databases.postgres.images).", major))
			return
		case seen[major]:
			writeFieldError(w, "allowedMajors", fmt.Sprintf("La major %d e' ripetuta.", major))
			return
		}
		seen[major] = true
		st.AllowedMajors = append(st.AllowedMajors, major)
	}
	if len(st.AllowedMajors) == 0 {
		writeFieldError(w, "allowedMajors", "Serve almeno una major ammessa.")
		return
	}
	sort.Ints(st.AllowedMajors)
	if in.MaxPerDatabase.SizeBytes < 1 || in.MaxPerDatabase.CpuMillis < 1 || in.MaxPerDatabase.MemoryBytes < 1 {
		writeFieldError(w, "maxPerDatabase", "Dimensione, CPU e memoria massime devono essere positive.")
		return
	}
	size, cpu, mem := in.MaxPerDatabase.SizeBytes, in.MaxPerDatabase.CpuMillis, in.MaxPerDatabase.MemoryBytes
	st.MaxSizeBytes, st.MaxCPUMillis, st.MaxMemoryBytes = &size, &cpu, &mem
	if err := databases.ValidatePortRange(in.LanPortRange.From, in.LanPortRange.To); err != nil {
		writeFieldError(w, "lanPortRange", err.Error())
		return
	}
	from, to := in.LanPortRange.From, in.LanPortRange.To
	st.LANPortFrom, st.LANPortTo = &from, &to
	if in.BackupRetention.Scheduled < 1 || in.BackupRetention.OnDemand < 1 {
		writeFieldError(w, "backupRetention", "Servono almeno un dump programmato e uno su richiesta.")
		return
	}
	sched, onDemand := in.BackupRetention.Scheduled, in.BackupRetention.OnDemand
	st.KeepScheduled, st.KeepOnDemand = &sched, &onDemand

	dbs, err := s.resources.ListAllManagedDBs(r.Context())
	if err != nil {
		appInternalError(w, "elenco dei database", err)
		return
	}
	for _, d := range dbs {
		if !seen[d.PgMajor] {
			writeError(w, http.StatusUnprocessableEntity, "major_in_use", fmt.Sprintf("La major %d e' usata dal database %s/%s: non si toglie dalle ammesse (DB6).", d.PgMajor, d.OwnerName, d.Name))
			return
		}
		if d.LANEnabled && d.LANPort != nil && (*d.LANPort < from || *d.LANPort > to) {
			writeError(w, http.StatusUnprocessableEntity, "lan_port_outside_range", fmt.Sprintf("Il database %s/%s ha la porta LAN %d, fuori dal nuovo intervallo %d-%d: spegni prima la sua LAN.", d.OwnerName, d.Name, *d.LANPort, from, to))
			return
		}
	}
	actor := &store.AppActor{ID: adminID, Type: "human"}
	if caller, _, ok := callerIdentity(w, r); ok {
		actor.Username = caller.Username
	}
	if isAgent, err := s.callerIsAgent(r.Context(), adminID); err == nil && isAgent {
		actor.Type = "agent"
	}
	if _, err := s.resources.PutManagedDBSettings(r.Context(), st, actor, s.now()); err != nil {
		appInternalError(w, "salvataggio delle impostazioni dei database", err)
		return
	}
	s.databases.Controller.SetRuntime(databaseRuntime(st))
	out, err := s.databaseSettingsResponse(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle impostazioni dei database", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ListAdminDatabases implementa GET /admin/databases: tutti i database dell'istanza (console Admin).
func (s *apiServer) ListAdminDatabases(w http.ResponseWriter, r *http.Request) {
	if !s.databasesOn() {
		databaseDisabled(w)
		return
	}
	if _, ok := s.requireInstanceAdmin(w, r, "vedere tutti i database"); !ok {
		return
	}
	dbs, err := s.resources.ListAllManagedDBs(r.Context())
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

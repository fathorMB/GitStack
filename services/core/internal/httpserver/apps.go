package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// App di un repo (v3-a, GIT-342, H5/H8): creazione, lettura, modifica, eliminazione e storico
// delle versioni. Il controller dei pod è di un altro item: qui solo stato e API.
// Permessi del repo, senza ruoli nuovi: read vede, admin crea/modifica/elimina; chi non legge il
// repo riceve 404. Un repo archiviato (R10) rifiuta le modifiche con 409 `archived`.

const (
	defaultAppCopies      = 1
	maxAppCopies          = 50
	defaultGuaranteedCPU  = 100
	defaultGuaranteedMem  = 128 << 20
	defaultLimitCPU       = 500
	defaultLimitMem       = 512 << 20
	maxAppDNSLabel        = 63
	appHealthDefaultEvery = 10
)

var (
	appNameRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,28}[a-z0-9])?$`)
	digestRe   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	nonLabelRe = regexp.MustCompile(`[^a-z0-9]+`)
)

// appRepo risolve il repo e verifica il ruolo richiesto (read, write, admin).
func (s *apiServer) appRepo(w http.ResponseWriter, r *http.Request, owner, name, role string, mutate bool) (store.Repo, openapiActor, bool) {
	caller, userID, ok := callerIdentity(w, r)
	if !ok {
		return store.Repo{}, openapiActor{}, false
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile verificare i permessi.")
		return store.Repo{}, openapiActor{}, false
	}
	repo, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return store.Repo{}, openapiActor{}, false
	}
	if role != "read" {
		has, err := s.repoIdentity.HasRole(r.Context(), userID, repo.ID, role)
		if err != nil {
			slog.Default().Warn("verifica del permesso non riuscita", "err", err)
			writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
			return store.Repo{}, openapiActor{}, false
		}
		if !has {
			writeError(w, http.StatusForbidden, "forbidden", "Serve il permesso "+role+" sul repo.")
			return store.Repo{}, openapiActor{}, false
		}
	}
	if mutate && repo.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "archived", "Il repo è archiviato: le app non accettano modifiche.")
		return store.Repo{}, openapiActor{}, false
	}
	return repo, openapiActor{id: userID, username: caller.Username}, true
}

type openapiActor struct {
	id       uuid.UUID
	username string
}

func (a openapiActor) store() *store.AppActor {
	return &store.AppActor{ID: a.id, Username: a.username, Type: "human"}
}

func actorToAPI(a *store.AppActor) *openapi.AppActor {
	if a == nil {
		return nil
	}
	return &openapi.AppActor{Id: openapi_types.UUID(a.ID), Username: a.Username, Type: openapi.AppActorType(a.Type)}
}

func healthToAPI(h *store.AppHealthCheck) *openapi.AppHealthCheck {
	if h == nil {
		return nil
	}
	return &openapi.AppHealthCheck{Path: h.Path, IntervalSeconds: h.IntervalSeconds, TimeoutSeconds: h.TimeoutSeconds}
}

func resourcesToAPI(c store.AppConfig) openapi.AppResources {
	return openapi.AppResources{
		Guaranteed: openapi.AppResourceAmount{CpuMillis: c.Guaranteed.CPUMillis, MemoryBytes: c.Guaranteed.MemoryBytes},
		Limit:      openapi.AppResourceAmount{CpuMillis: c.Limit.CPUMillis, MemoryBytes: c.Limit.MemoryBytes},
	}
}

func imageToAPI(i store.AppImage) openapi.AppImage {
	return openapi.AppImage{Reference: i.Reference, Digest: i.Digest, Source: openapi.AppImageSource(i.Source),
		Verified: i.Verified, SignedBy: i.SignedBy, RunsAsRoot: i.RunsAsRoot}
}

func (s *apiServer) appAddress(label *string) *string {
	if label == nil || s.appsDomain == "" {
		return nil
	}
	a := "https://" + *label + "." + s.appsDomain
	return &a
}

func (s *apiServer) appToAPI(a store.App) openapi.App {
	status, reason := a.Effective()
	ready := a.ReadyCopies
	if status == "stopped" {
		ready = 0
	}
	out := openapi.App{
		Id: openapi_types.UUID(a.ID), Name: a.Name, Owner: a.Owner, Repo: a.RepoName,
		Status: openapi.AppStatus(status), Access: openapi.AppAccessMode(a.Access),
		Address: s.appAddress(a.AddressLabel), Image: imageToAPI(a.Image), ActiveVersion: a.ActiveVersion,
		Port: a.Config.Port, HealthCheck: healthToAPI(a.Config.HealthCheck), Copies: a.Config.Copies, ReadyCopies: ready,
		Resources: resourcesToAPI(a.Config), CreatedAt: a.CreatedAt, CreatedBy: actorToAPI(a.CreatedBy), UpdatedAt: a.UpdatedAt,
	}
	if reason != nil {
		sr := openapi.AppStoppedReason(*reason)
		out.StoppedReason = &sr
	}
	if a.RunningPreviousVer {
		t := true
		out.RunningPreviousVersion = &t
	}
	if a.FollowBranch != nil {
		out.Follow = &openapi.AppFollow{Branch: *a.FollowBranch, Paused: a.AutoDeployPaused}
	}
	if d := a.LastDeploy; d != nil {
		out.LastDeploy = &openapi.AppLastDeploy{Version: d.Version, State: openapi.AppDeployState(d.State), StartedAt: d.StartedAt, FinishedAt: d.FinishedAt, Reason: d.Reason}
	}
	return out
}

func (s *apiServer) versionToAPI(v store.AppVersion, active, previous int) openapi.AppVersion {
	cfg := openapi.AppVersionConfig{
		Port: v.Config.Port, HealthCheck: healthToAPI(v.Config.HealthCheck), Variables: v.Config.Variables,
		Resources: resourcesToAPI(v.Config), Copies: v.Config.Copies, Restricted: &v.Config.Restricted,
		SecretNames: []openapi.AppEnvName{},
	}
	cfg.SecretNames = append(cfg.SecretNames, v.Config.SecretNames...)
	return openapi.AppVersion{
		Number: v.Number, Trigger: openapi.AppVersionTrigger(v.Trigger), State: openapi.AppDeployState(v.State),
		StateReason: v.StateReason, Image: imageToAPI(v.Image),
		// Il registro dei tag non è letto qui: finché non lo dice la pulizia (RG2) l'immagine è disponibile.
		ImageAvailable: true, InBackup: v.Number == active || v.Number == previous,
		Branch: v.Branch, CommitSha: v.CommitSha, Summary: v.Summary, RollbackOf: v.RollbackOf,
		Config: &cfg, CreatedAt: v.CreatedAt, CreatedBy: actorToAPI(v.CreatedBy),
	}
}

// previousVersion è la versione che precede l'attiva (anch'essa sempre nei backup, H4).
func previousVersion(active int) int {
	if active > 1 {
		return active - 1
	}
	return 0
}

// registryHost è l'host del registry (quello pubblico di GitStack).
func (s *apiServer) registryHost() string {
	u, err := url.Parse(s.clone.PublicURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// resolveAppImage risolve il riferimento dell'immagine (H1, RG1): per il registry del repo basta
// `nome:tag` o il solo tag; un riferimento con host è del registry del repo solo se sta sotto
// `<host>/<owner>/<repo>`, altrimenti è esterno. Mai verificata qui: la firma è della CI (H10).
func (s *apiServer) resolveAppImage(ref string, repo store.Repo) (store.AppImage, string) {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > 512 || strings.ContainsAny(ref, " \t\r\n") {
		return store.AppImage{}, "image: riferimento non valido."
	}
	host := s.registryHost()
	base := host + "/" + strings.ToLower(repo.OwnerName) + "/" + strings.ToLower(repo.Name)
	var digest *string
	full := ref
	first, _, hasSlash := strings.Cut(ref, "/")
	switch {
	case hasSlash && (strings.ContainsAny(first, ".:") || first == "localhost"):
		// riferimento completo
	case !hasSlash && !strings.Contains(ref, ":") && !strings.Contains(ref, "@"):
		full = base + ":" + ref
	default:
		if host == "" {
			return store.AppImage{}, "image: il registry del repo non è configurato, serve un riferimento completo."
		}
		full = base + "/" + ref
	}
	if i := strings.Index(full, "@"); i >= 0 {
		d := full[i+1:]
		if !digestRe.MatchString(d) {
			return store.AppImage{}, "image: digest non valido."
		}
		digest = &d
	}
	src := "external"
	if host != "" && (strings.HasPrefix(strings.ToLower(full), base+":") || strings.HasPrefix(strings.ToLower(full), base+"/") || strings.HasPrefix(strings.ToLower(full), base+"@")) {
		src = "registry"
	}
	return store.AppImage{Reference: full, Digest: digest, Source: src}, ""
}

// appAddressLabel è l'etichetta DNS dell'indirizzo automatico: `<app>-<repo>-<owner>` normalizzata.
func appAddressLabel(app, repo, owner string) string {
	l := nonLabelRe.ReplaceAllString(strings.ToLower(app+"-"+repo+"-"+owner), "-")
	return strings.Trim(l, "-")
}

func validHealth(h *openapi.AppHealthCheck) string {
	if h == nil {
		return ""
	}
	if h.Path == "" || len(h.Path) > 200 || !strings.HasPrefix(h.Path, "/") {
		return "healthCheck.path: un percorso che inizia con «/», al massimo 200 caratteri."
	}
	if h.IntervalSeconds < 1 || h.IntervalSeconds > 3600 {
		return "healthCheck.intervalSeconds: da 1 a 3600."
	}
	if h.TimeoutSeconds != nil && (*h.TimeoutSeconds < 1 || *h.TimeoutSeconds > 300) {
		return "healthCheck.timeoutSeconds: da 1 a 300."
	}
	return ""
}

func validPort(p *int) bool { return p == nil || (*p >= 1 && *p <= 65535) }

func validAmounts(g, l openapi.AppResourceAmount) string {
	if g.CpuMillis < 1 || g.MemoryBytes < 1 || l.CpuMillis < 1 || l.MemoryBytes < 1 {
		return "resources: CPU e memoria devono essere positive."
	}
	if l.CpuMillis < g.CpuMillis || l.MemoryBytes < g.MemoryBytes {
		return "resources: il tetto non può essere sotto la parte garantita."
	}
	return ""
}

func (s *apiServer) writeAppConflict(w http.ResponseWriter, err error) bool {
	var ce *store.CapacityError
	switch {
	case errors.Is(err, store.ErrAppNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Il repo ha già un'app con questo nome.")
	case errors.Is(err, store.ErrAppAddressConflict):
		writeError(w, http.StatusConflict, "address_conflict", "L'indirizzo automatico è già usato da un'altra app.")
	case errors.As(err, &ce):
		writeErrorDetails(w, http.StatusConflict, "insufficient_capacity", "La quota delle app dell'owner non basta per le parti garantite.", map[string]any{
			"owner":   ce.Owner,
			"missing": ce.Missing,
			"limits":  map[string]any{"cpuMillis": ce.Limits.CPUMillis, "memoryBytes": ce.Limits.MemoryBytes},
			"used":    map[string]any{"cpuMillis": ce.Used.CPUMillis, "memoryBytes": ce.Used.MemoryBytes},
		})
	default:
		return false
	}
	return true
}

func appInternalError(w http.ResponseWriter, what string, err error) {
	slog.Default().Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno.")
}

// ListApps implementa GET /repos/{owner}/{repo}/apps.
func (s *apiServer) ListApps(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "read", false)
	if !ok {
		return
	}
	apps, err := s.resources.ListApps(r.Context(), repo.ID)
	if err != nil {
		appInternalError(w, "elenco delle app", err)
		return
	}
	out := openapi.AppList{Items: []openapi.App{}}
	for _, a := range apps {
		out.Items = append(out.Items, s.appToAPI(a))
	}
	limits, used, isDefault, err := s.resources.OwnerAppQuota(r.Context(), repo.OwnerName)
	if err != nil {
		appInternalError(w, "quota delle app", err)
		return
	}
	warn := float64(used.CPUMillis) > 0.8*float64(limits.CPUMillis) || float64(used.MemoryBytes) > 0.8*float64(limits.MemoryBytes) ||
		float64(used.DiskBytes) > 0.8*float64(limits.DiskBytes)
	out.Quota = &openapi.AppOwnerQuota{
		Owner: repo.OwnerName, IsDefault: &isDefault, Warning: warn,
		Limits: openapi.AppOwnerQuotaInput{CpuMillis: limits.CPUMillis, MemoryBytes: limits.MemoryBytes, DiskBytes: limits.DiskBytes},
		Used:   openapi.AppOwnerQuotaInput{CpuMillis: used.CPUMillis, MemoryBytes: used.MemoryBytes, DiskBytes: used.DiskBytes},
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateApp implementa POST /repos/{owner}/{repo}/apps.
func (s *apiServer) CreateApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	repo, actor, ok := s.appRepo(w, r, owner, name, "admin", true)
	if !ok {
		return
	}
	var in openapi.AppCreateInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if !appNameRe.MatchString(in.Name) {
		writeFieldError(w, "name", "name: lettere minuscole, cifre e trattini, 1-30 caratteri.")
		return
	}
	img, msg := s.resolveAppImage(in.Image, repo)
	if msg != "" {
		writeFieldError(w, "image", msg)
		return
	}
	cfg := store.AppConfig{
		Port: in.Port, Copies: defaultAppCopies, Variables: map[string]string{}, SecretNames: []string{},
		Guaranteed: store.AppResourceAmount{CPUMillis: defaultGuaranteedCPU, MemoryBytes: defaultGuaranteedMem},
		Limit:      store.AppResourceAmount{CPUMillis: defaultLimitCPU, MemoryBytes: defaultLimitMem},
	}
	if in.Copies != nil {
		if *in.Copies < 1 || *in.Copies > maxAppCopies {
			writeFieldError(w, "copies", "copies: da 1 a 50.")
			return
		}
		cfg.Copies = *in.Copies
	}
	if !validPort(in.Port) {
		writeFieldError(w, "port", "port: da 1 a 65535.")
		return
	}
	if m := validHealth(in.HealthCheck); m != "" {
		writeFieldError(w, "healthCheck", m)
		return
	}
	if in.HealthCheck != nil {
		cfg.HealthCheck = &store.AppHealthCheck{Path: in.HealthCheck.Path, IntervalSeconds: in.HealthCheck.IntervalSeconds, TimeoutSeconds: in.HealthCheck.TimeoutSeconds}
	}
	if in.Resources != nil {
		if m := validAmounts(in.Resources.Guaranteed, in.Resources.Limit); m != "" {
			writeFieldError(w, "resources", m)
			return
		}
		cfg.Guaranteed = store.AppResourceAmount{CPUMillis: in.Resources.Guaranteed.CpuMillis, MemoryBytes: in.Resources.Guaranteed.MemoryBytes}
		cfg.Limit = store.AppResourceAmount{CPUMillis: in.Resources.Limit.CpuMillis, MemoryBytes: in.Resources.Limit.MemoryBytes}
	}
	if in.Restricted != nil {
		cfg.Restricted = *in.Restricted
	}
	var follow *string
	if in.FollowBranch != nil {
		if strings.TrimSpace(*in.FollowBranch) == "" {
			writeFieldError(w, "followBranch", "followBranch: un nome di branch.")
			return
		}
		if img.Source == "external" {
			writeFieldError(w, "followBranch", "followBranch: non disponibile per un'immagine esterna.")
			return
		}
		follow = in.FollowBranch
	}
	var label *string
	if cfg.Port != nil {
		l := appAddressLabel(in.Name, repo.Name, repo.OwnerName)
		if len(l) > maxAppDNSLabel {
			writeErrorDetails(w, http.StatusUnprocessableEntity, "address_too_long", "L'indirizzo automatico supera i 63 caratteri: scegli un nome più corto.",
				map[string]any{"label": l, "length": len(l), "max": maxAppDNSLabel})
			return
		}
		label = &l
	}
	app, ver, err := s.resources.CreateApp(r.Context(), store.NewApp{
		RepoID: repo.ID, Owner: repo.OwnerName, Name: in.Name, AddressLabel: label, Image: img, Config: cfg,
		FollowBranch: follow, Actor: actor.store(),
	}, s.now())
	if err != nil {
		if !s.writeAppConflict(w, err) {
			appInternalError(w, "creazione dell'app", err)
		}
		return
	}
	writeJSON(w, http.StatusAccepted, openapi.AppCreated{App: s.appToAPI(app), Version: s.versionToAPI(ver, app.ActiveVersion, previousVersion(app.ActiveVersion))})
}

func (s *apiServer) appFor(w http.ResponseWriter, r *http.Request, repo store.Repo, name string) (store.App, bool) {
	a, err := s.resources.GetApp(r.Context(), repo.ID, name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "App non trovata.")
		return store.App{}, false
	}
	if err != nil {
		appInternalError(w, "lettura dell'app", err)
		return store.App{}, false
	}
	return a, true
}

// GetApp implementa GET /repos/{owner}/{repo}/apps/{app}.
func (s *apiServer) GetApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "read", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.appToAPI(a))
}

// UpdateApp implementa PATCH /repos/{owner}/{repo}/apps/{app}: porta, controllo di salute, deploy
// automatico e restricted. Una modifica che entra in una versione ne crea una nuova (H8).
func (s *apiServer) UpdateApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, actor, ok := s.appRepo(w, r, owner, name, "admin", true)
	if !ok {
		return
	}
	cur, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	// Il corpo si legge come mappa: «null» (porta, followBranch) è diverso da «assente».
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIssueRequestBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
		return
	}
	isNull := func(m json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(m), []byte("null")) }
	decode := func(key string, v any) bool {
		dec := json.NewDecoder(bytes.NewReader(fields[key]))
		dec.DisallowUnknownFields()
		if dec.Decode(v) != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
			return false
		}
		return true
	}
	var p store.AppPatch
	for key, m := range fields {
		switch key {
		case "port":
			p.SetPort = true
			if !isNull(m) {
				var v int
				if !decode(key, &v) {
					return
				}
				if !validPort(&v) {
					writeFieldError(w, "port", "port: da 1 a 65535.")
					return
				}
				p.Port = &v
			}
		case "healthCheck":
			p.SetHealth = true
			if isNull(m) {
				continue
			}
			var v openapi.AppHealthCheck
			if !decode(key, &v) {
				return
			}
			if msg := validHealth(&v); msg != "" {
				writeFieldError(w, "healthCheck", msg)
				return
			}
			p.HealthCheck = &store.AppHealthCheck{Path: v.Path, IntervalSeconds: v.IntervalSeconds, TimeoutSeconds: v.TimeoutSeconds}
		case "followBranch":
			p.SetFollow = true
			if isNull(m) {
				continue
			}
			var v string
			if !decode(key, &v) {
				return
			}
			if strings.TrimSpace(v) == "" {
				writeFieldError(w, "followBranch", "followBranch: un nome di branch.")
				return
			}
			if cur.Image.Source == "external" {
				writeFieldError(w, "followBranch", "followBranch: non disponibile per un'immagine esterna.")
				return
			}
			p.FollowBranch = &v
		case "autoDeployPaused":
			p.SetPaused = true
			if isNull(m) || !decode(key, &p.AutoDeployPaused) {
				if isNull(m) {
					writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
				}
				return
			}
		case "restricted":
			p.SetRestricted = true
			if isNull(m) || !decode(key, &p.Restricted) {
				if isNull(m) {
					writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
				}
				return
			}
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
			return
		}
	}
	// Senza porta non c'è indirizzo: togliere la porta non cambia l'etichetta già riservata.
	updated, _, err := s.resources.UpdateApp(r.Context(), repo.ID, app, p, actor.store(), s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "App non trovata.")
		return
	}
	if err != nil {
		appInternalError(w, "modifica dell'app", err)
		return
	}
	writeJSON(w, http.StatusOK, s.appToAPI(updated))
}

// DeleteApp implementa DELETE /repos/{owner}/{repo}/apps/{app}.
func (s *apiServer) DeleteApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "admin", true)
	if !ok {
		return
	}
	if err := s.resources.DeleteApp(r.Context(), repo.ID, app); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "App non trovata.")
			return
		}
		appInternalError(w, "eliminazione dell'app", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListAppVersions implementa GET .../apps/{app}/versions, dalla più recente.
func (s *apiServer) ListAppVersions(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam, params openapi.ListAppVersionsParams) {
	repo, _, ok := s.appRepo(w, r, owner, name, "read", false)
	if !ok {
		return
	}
	page, perPage := defaultPage, defaultPerPage
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 || perPage < 1 || perPage > maxPerPage {
		writeError(w, http.StatusBadRequest, "bad_request", "Paginazione non valida.")
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	vs, total, err := s.resources.ListAppVersions(r.Context(), a.ID, perPage, (page-1)*perPage)
	if err != nil {
		appInternalError(w, "elenco delle versioni", err)
		return
	}
	out := openapi.AppVersionList{Items: []openapi.AppVersion{}, Total: total}
	for _, v := range vs {
		out.Items = append(out.Items, s.versionToAPI(v, a.ActiveVersion, previousVersion(a.ActiveVersion)))
	}
	writeJSON(w, http.StatusOK, out)
}

// GetAppVersion implementa GET .../apps/{app}/versions/{version}.
func (s *apiServer) GetAppVersion(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam, version openapi.AppVersionParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "read", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	v, err := s.resources.GetAppVersion(r.Context(), a.ID, int(version))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Versione non trovata.")
		return
	}
	if err != nil {
		appInternalError(w, "lettura della versione", err)
		return
	}
	writeJSON(w, http.StatusOK, s.versionToAPI(v, a.ActiveVersion, previousVersion(a.ActiveVersion)))
}

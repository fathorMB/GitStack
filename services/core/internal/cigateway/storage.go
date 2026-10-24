package cigateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/jackc/pgx/v5"
)

// Artefatti e cache dei job (CI5). Come l'archivio, sono raggiungibili solo con il token del job e
// solo per il repo del job.
//
//	PUT  /ci/v1/artifacts/{name}          carica l'artefatto ZIP dell'esecuzione (corpo = il file)
//	/ci/v1/cache/_apis/artifactcache/...  protocollo v1 di actions/cache (ACTIONS_CACHE_URL = <gateway>/ci/v1/cache/):
//	  GET   cache?keys=a,b&version=v      200 {cacheKey, scope, archiveLocation} oppure 204
//	  POST  caches {key, version, cacheSize}  201 {cacheId}; 409 se esiste già; 413 oltre la quota
//	  PATCH caches/{id}                   un pezzo, Content-Range: bytes a-b/*  (204)
//	  POST  caches/{id} {size}            conferma (204)
//	GET  /ci/v1/cache/blob/{id}           scarica l'archivio (è l'archiveLocation)
//
// Compatibilità: upload-artifact v4 e cache v4 di GitHub usano un protocollo Twirp con upload su Azure
// Blob, che core non parla: gli artefatti passano dall'endpoint qui sopra, le cache dal protocollo v1
// (actions/cache fino alla v3 e client compatibili).
const (
	ArtifactsPath  = "/ci/v1/artifacts/"
	CachePath      = "/ci/v1/cache/"
	cacheAPIPrefix = CachePath + "_apis/artifactcache/"
	cacheBlobPath  = CachePath + "blob/"
)

func (g *gateway) isStoragePath(p string) bool {
	return strings.HasPrefix(p, ArtifactsPath) || strings.HasPrefix(p, CachePath)
}

// jobAuth verifica il token del job; scrive la risposta d'errore e ritorna false se non va.
func (g *gateway) jobAuth(w http.ResponseWriter, r *http.Request) (ci.JobAuth, bool) {
	a, err := ci.ResolveJobToken(r.Context(), g.Pool, g.Now(), bearer(r.Header))
	if errors.Is(err, ci.ErrJobToken) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gitstack-ci"`)
		http.Error(w, "token del job non valido", http.StatusUnauthorized)
		return a, false
	}
	if err != nil {
		g.Log.Error("ci-gateway: verifica del token non riuscita", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return a, false
	}
	if g.Blobs == nil {
		http.Error(w, "archiviazione di artefatti e cache non configurata", http.StatusServiceUnavailable)
		return a, false
	}
	return a, true
}

func (g *gateway) storage(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, ArtifactsPath):
		g.putArtifact(w, r)
	case strings.HasPrefix(r.URL.Path, cacheBlobPath):
		g.cacheDownload(w, r)
	case strings.HasPrefix(r.URL.Path, cacheAPIPrefix):
		g.cacheAPI(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (g *gateway) putArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodPut)
		http.Error(w, "metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	a, ok := g.jobAuth(w, r)
	if !ok {
		return
	}
	name, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), ArtifactsPath))
	if err != nil || !ci.ValidArtifactName(name) {
		http.Error(w, "nome di artefatto non valido", http.StatusBadRequest)
		return
	}
	st, err := ci.LoadSettings(r.Context(), g.Pool)
	if err != nil {
		g.Log.Error("ci-gateway: impostazioni non leggibili", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	if r.ContentLength > st.ArtifactMaxBytes {
		http.Error(w, "artefatto troppo grande", http.StatusRequestEntityTooLarge)
		return
	}
	art, err := ci.SaveArtifact(r.Context(), g.Pool, g.Blobs, g.Now(), a, name, r.Body, st)
	switch {
	case errors.Is(err, ci.ErrTooLarge):
		http.Error(w, "artefatto troppo grande", http.StatusRequestEntityTooLarge)
		return
	case errors.Is(err, ci.ErrArtifactExists):
		http.Error(w, "l'esecuzione ha già un artefatto con questo nome", http.StatusConflict)
		return
	case err != nil:
		g.Log.Error("ci-gateway: salvataggio dell'artefatto non riuscito", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": art.ID, "name": art.Name, "sizeBytes": art.SizeBytes, "expiresAt": art.ExpiresAt})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// refs ritorna il ref dell'esecuzione del job e quello del ramo predefinito del repo.
func (g *gateway) refs(r *http.Request, a ci.JobAuth) (ref, defaultRef string, err error) {
	var branch string
	err = g.Pool.QueryRow(r.Context(), `SELECT r.ref, rp.default_branch FROM core.ci_runs r
		JOIN core.repositories rp ON rp.resource_id = r.repo_id WHERE r.id = $1`, a.RunID).Scan(&ref, &branch)
	return ref, "refs/heads/" + branch, err
}

func (g *gateway) spoolPath(seq int64) string { return ci.SpoolPath(g.SpoolDir, seq) }

func (g *gateway) cacheAPI(w http.ResponseWriter, r *http.Request) {
	a, ok := g.jobAuth(w, r)
	if !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, cacheAPIPrefix)
	switch {
	case rest == "cache" && r.Method == http.MethodGet:
		g.cacheLookup(w, r, a)
	case rest == "caches" && r.Method == http.MethodPost:
		g.cacheReserve(w, r, a)
	case strings.HasPrefix(rest, "caches/") && (r.Method == http.MethodPatch || r.Method == http.MethodPost):
		seq, err := strconv.ParseInt(strings.TrimPrefix(rest, "caches/"), 10, 64)
		if err != nil || seq < 1 {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPatch {
			g.cacheChunk(w, r, a, seq)
		} else {
			g.cacheCommit(w, r, a, seq)
		}
	default:
		http.NotFound(w, r)
	}
}

func (g *gateway) cacheLookup(w http.ResponseWriter, r *http.Request, a ci.JobAuth) {
	keys := strings.Split(r.URL.Query().Get("keys"), ",")
	version := r.URL.Query().Get("version")
	ref, defRef, err := g.refs(r, a)
	if err != nil {
		g.Log.Error("ci-gateway: ref dell'esecuzione non letto", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	c, err := ci.FindCache(r.Context(), g.Pool, g.Now(), a.RepoID, keys, version, ref, defRef)
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		g.Log.Error("ci-gateway: ricerca della cache non riuscita", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result": "hit", "cacheKey": c.Key, "scope": c.Ref,
		"archiveLocation": g.baseURL(r) + cacheBlobPath + strconv.FormatInt(c.Seq, 10),
	})
}

// baseURL è l'indirizzo con cui il pod raggiunge questo gateway: quello configurato, o quello della richiesta.
func (g *gateway) baseURL(r *http.Request) string {
	if g.ArchiveURL != "" {
		return strings.TrimRight(g.ArchiveURL, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (g *gateway) cacheReserve(w http.ResponseWriter, r *http.Request, a ci.JobAuth) {
	var in struct {
		Key       string `json:"key"`
		Version   string `json:"version"`
		CacheSize int64  `json:"cacheSize"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil || in.Key == "" || len(in.Key) > 512 || len(in.Version) > 128 || in.CacheSize < 0 {
		http.Error(w, "richiesta non valida", http.StatusBadRequest)
		return
	}
	st, err := ci.LoadSettings(r.Context(), g.Pool)
	ref, _, err2 := g.refs(r, a)
	if err != nil || err2 != nil {
		g.Log.Error("ci-gateway: impostazioni non leggibili", "err", errors.Join(err, err2))
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	c, err := ci.ReserveCache(r.Context(), g.Pool, g.Now(), a.RepoID, in.Key, in.Version, ref, in.CacheSize, st)
	switch {
	case errors.Is(err, ci.ErrTooLarge):
		http.Error(w, "cache oltre la quota del repository", http.StatusRequestEntityTooLarge)
		return
	case errors.Is(err, ci.ErrCacheExists):
		http.Error(w, "esiste già una cache con questa chiave", http.StatusConflict)
		return
	case err != nil:
		g.Log.Error("ci-gateway: prenotazione della cache non riuscita", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"cacheId": c.Seq})
}

// cacheChunk scrive un pezzo dell'archivio nel file d'appoggio, nella posizione di Content-Range.
func (g *gateway) cacheChunk(w http.ResponseWriter, r *http.Request, a ci.JobAuth, seq int64) {
	c, err := ci.GetCacheBySeq(r.Context(), g.Pool, a.RepoID, seq)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && c.Ready) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		g.Log.Error("ci-gateway: cache non letta", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	var start, end int64
	if _, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/", &start, &end); err != nil || start < 0 || end < start {
		http.Error(w, "Content-Range non valido", http.StatusBadRequest)
		return
	}
	st, err := ci.LoadSettings(r.Context(), g.Pool)
	if err != nil {
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	if end+1 > st.CacheQuotaBytes {
		http.Error(w, "cache oltre la quota del repository", http.StatusRequestEntityTooLarge)
		return
	}
	p := g.spoolPath(seq)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		g.Log.Error("ci-gateway: cartella d'appoggio non creata", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		g.Log.Error("ci-gateway: file d'appoggio non aperto", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = f.Close() }()
	want := end - start + 1
	n, err := io.Copy(io.NewOffsetWriter(f, start), io.LimitReader(r.Body, want+1))
	if err != nil || n != want {
		http.Error(w, "il pezzo non ha la dimensione dichiarata", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *gateway) cacheCommit(w http.ResponseWriter, r *http.Request, a ci.JobAuth, seq int64) {
	var in struct {
		Size int64 `json:"size"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil || in.Size < 0 {
		http.Error(w, "richiesta non valida", http.StatusBadRequest)
		return
	}
	p := g.spoolPath(seq)
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && in.Size == 0 {
			// Cache vuota: nessun pezzo è arrivato.
			f, err = os.CreateTemp("", "ci-cache-empty-")
			if err == nil {
				defer func() { _ = os.Remove(f.Name()) }()
			}
		}
		if err != nil {
			http.Error(w, "nessun dato caricato per questa cache", http.StatusBadRequest)
			return
		}
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || fi.Size() != in.Size {
		http.Error(w, "la dimensione caricata non corrisponde a quella dichiarata", http.StatusBadRequest)
		return
	}
	st, err := ci.LoadSettings(r.Context(), g.Pool)
	if err != nil {
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	_, err = ci.CommitCache(r.Context(), g.Pool, g.Blobs, g.Now(), a.RepoID, seq, f, st)
	if err == nil || errors.Is(err, ci.ErrTooLarge) || errors.Is(err, ci.ErrCacheExists) {
		_ = os.Remove(p) // l'appoggio non serve più; negli altri casi il client può riprovare
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		http.NotFound(w, r)
	case errors.Is(err, ci.ErrCacheExists):
		http.Error(w, "cache già confermata", http.StatusConflict)
	case errors.Is(err, ci.ErrTooLarge):
		http.Error(w, "cache oltre la quota del repository", http.StatusRequestEntityTooLarge)
	case err != nil:
		g.Log.Error("ci-gateway: conferma della cache non riuscita", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (g *gateway) cacheDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "metodo non consentito", http.StatusMethodNotAllowed)
		return
	}
	a, ok := g.jobAuth(w, r)
	if !ok {
		return
	}
	seq, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, cacheBlobPath), 10, 64)
	if err != nil || seq < 1 {
		http.NotFound(w, r)
		return
	}
	c, err := ci.GetCacheBySeq(r.Context(), g.Pool, a.RepoID, seq)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !c.Ready) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		g.Log.Error("ci-gateway: cache non letta", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	rc, err := g.Blobs.Open(r.Context(), c.StorageKey)
	if errors.Is(err, ci.ErrBlobNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		g.Log.Error("ci-gateway: archivio della cache non aperto", "err", err)
		http.Error(w, "servizio non disponibile", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(c.SizeBytes, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

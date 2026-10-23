package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/distribution/distribution/v3/registry/api/errcode"
	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
)

// Tag protetti (RG2) e quote per owner (RG2). Distribution non conosce né gli uni né le altre:
// si applicano in ServeHTTP, prima di passargli la richiesta.

// storageRoot è la radice dei dati di distribution.
const storageRoot = "/docker/registry/v2"

func blobDataPath(hexDigest string) string {
	return storageRoot + "/blobs/sha256/" + hexDigest[:2] + "/" + hexDigest + "/data"
}

func tagLinkPath(name, tag string) string {
	return repoRoot + name + "/_manifests/tags/" + tag + "/current/link"
}

func isNotFound(err error) bool {
	var nf storagedriver.PathNotFoundError
	return errors.As(err, &nf)
}

// listDirs elenca i nomi (ultimo segmento) delle voci di una cartella; cartella assente = vuoto.
func (h *Handler) listDirs(ctx context.Context, dir string) ([]string, error) {
	entries, err := h.driver.List(ctx, dir)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, path.Base(e))
	}
	return out, nil
}

// imageNames elenca i nomi dei repository di distribution sotto un repo: «<id>» e
// «<id>/<nome>…», cioè le cartelle che contengono _manifests o _layers.
func (h *Handler) imageNames(ctx context.Context, repoID string) ([]string, error) {
	var out []string
	var walk func(name string, depth int) error
	walk = func(name string, depth int) error {
		kids, err := h.listDirs(ctx, repoRoot+name)
		if err != nil {
			return err
		}
		isImage := false
		for _, k := range kids {
			if k == "_manifests" || k == "_layers" {
				isImage = true
			}
		}
		if isImage {
			out = append(out, name)
		}
		if depth >= 8 {
			return nil
		}
		for _, k := range kids {
			if strings.HasPrefix(k, "_") {
				continue
			}
			if err := walk(name+"/"+k, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return out, walk(repoID, 0)
}

// tagDigest legge il digest a cui punta un tag; "" se il tag non esiste.
func (h *Handler) tagDigest(ctx context.Context, name, tag string) (string, error) {
	b, err := h.driver.GetContent(ctx, tagLinkPath(name, tag))
	if err != nil {
		if isNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// tagsOf elenca i tag di un'immagine con il digest a cui puntano.
func (h *Handler) tagsOf(ctx context.Context, name string) (map[string]string, error) {
	tags, err := h.listDirs(ctx, repoRoot+name+"/_manifests/tags")
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		d, err := h.tagDigest(ctx, name, t)
		if err != nil {
			return nil, err
		}
		if d != "" {
			out[t] = d
		}
	}
	return out, nil
}

func denied(msg string) error { return errcode.ErrorCodeDenied.WithMessage(msg) }

// guard applica tag protetti e quota a una richiesta già autorizzata. Ritorna false dopo aver
// risposto.
func (h *Handler) guard(w http.ResponseWriter, r *http.Request, rt route, action, repoID string) bool {
	if h.settings == nil || action == actionPull {
		return true
	}
	ctx := r.Context()
	name := rt.upstreamName(repoID)
	isManifest := strings.HasPrefix(rt.tail, "manifests/")
	ref := strings.TrimPrefix(rt.tail, "manifests/")
	needSettings := false
	switch {
	case isManifest && r.Method == http.MethodPut && !strings.Contains(ref, ":"):
		needSettings = true // tag: protetto?
	case isManifest && r.Method == http.MethodDelete:
		needSettings = true
	}
	quotaOn := h.quota > 0 && r.Method != http.MethodDelete && r.Method != http.MethodHead && r.Method != http.MethodGet
	if !needSettings && !quotaOn {
		return true
	}
	st, err := h.settings.get(ctx, repoID)
	if err != nil {
		if errors.Is(err, ErrSettingsNotFound) {
			serveErr(w, errcode.ErrorCodeNameUnknown.WithMessage("repository non trovato"))
			return false
		}
		h.log.Error("impostazioni del registry non disponibili", "err", err)
		serveErr(w, errcode.ErrorCodeUnavailable)
		return false
	}
	if needSettings && len(st.ProtectedTagPatterns) > 0 {
		switch r.Method {
		case http.MethodPut:
			if st.protected(ref) {
				cur, err := h.tagDigest(ctx, name, ref)
				if err != nil {
					h.log.Error("lettura del tag non riuscita", "err", err)
					serveErr(w, errcode.ErrorCodeUnavailable)
					return false
				}
				if cur != "" {
					serveErr(w, denied("il tag "+ref+" è protetto: non si sovrascrive"))
					return false
				}
			}
		case http.MethodDelete:
			// DELETE per tag: distribution v3 fa Untag del tag.
			if !strings.Contains(ref, ":") && st.protected(ref) {
				serveErr(w, denied("il tag "+ref+" è protetto: non si elimina"))
				return false
			}
			tags, err := h.tagsOf(ctx, name)
			if err != nil {
				h.log.Error("lettura dei tag non riuscita", "err", err)
				serveErr(w, errcode.ErrorCodeUnavailable)
				return false
			}
			for t, d := range tags {
				if d == ref && st.protected(t) {
					serveErr(w, denied("l'immagine ha il tag protetto "+t+": non si elimina"))
					return false
				}
			}
		}
	}
	if quotaOn && !h.checkQuota(w, r, st) {
		return false
	}
	return true
}

// QuotaWarningPercent è la soglia dell'avviso sulla quota (RG2).
const QuotaWarningPercent = 80

// checkQuota rifiuta il push se l'owner ha finito la quota (o la finirebbe con questa richiesta) e
// avvisa, con l'intestazione Warning e il log, quando supera l'80%.
func (h *Handler) checkQuota(w http.ResponseWriter, r *http.Request, st Settings) bool {
	used, err := h.ownerUsage(r.Context(), st)
	if err != nil {
		h.log.Error("calcolo della quota non riuscito", "owner", st.Owner, "err", err)
		serveErr(w, errcode.ErrorCodeUnavailable)
		return false
	}
	incoming := r.ContentLength
	if incoming < 0 {
		incoming = 0
	}
	if used >= h.quota || used+incoming > h.quota {
		h.log.Warn("quota del registry esaurita: push rifiutato", "owner", st.Owner, "usedBytes", used, "quotaBytes", h.quota)
		serveErr(w, denied(fmt.Sprintf("quota del registry esaurita per %s: %d byte usati su %d", st.Owner, used, h.quota)))
		return false
	}
	if pct := (used + incoming) * 100 / h.quota; pct >= QuotaWarningPercent {
		w.Header().Set("Warning", fmt.Sprintf(`299 - "quota del registry di %s al %d%% (%d byte su %d)"`, st.Owner, pct, used+incoming, h.quota))
		h.log.Warn("quota del registry oltre l'80%", "owner", st.Owner, "percent", pct)
	}
	return true
}

type usageEntry struct {
	bytes int64
	at    time.Time
}

// ownerUsage è lo spazio, in byte, dei blob (layer e manifest) usati dai repo dell'owner; un
// blob condiviso fra più immagini si conta una volta. Il risultato vale usageTTL.
func (h *Handler) ownerUsage(ctx context.Context, st Settings) (int64, error) {
	key := st.Owner
	h.usageMu.Lock()
	if e, ok := h.usage[key]; ok && h.now().Sub(e.at) < h.usageTTL {
		h.usageMu.Unlock()
		return e.bytes, nil
	}
	h.usageMu.Unlock()
	ids := st.OwnerRepoIDs
	if len(ids) == 0 {
		ids = []string{st.RepoID}
	}
	n, err := h.usageOf(ctx, ids)
	if err != nil {
		return 0, err
	}
	h.usageMu.Lock()
	h.usage[key] = usageEntry{bytes: n, at: h.now()}
	h.usageMu.Unlock()
	return n, nil
}

// invalidateUsage dimentica il conteggio dell'owner (dopo la pulizia).
func (h *Handler) invalidateUsage() {
	h.usageMu.Lock()
	h.usage = map[string]usageEntry{}
	h.usageMu.Unlock()
}

// blobDigests elenca gli hex dei blob collegati a un'immagine: layer e revisioni dei manifest.
func (h *Handler) blobDigests(ctx context.Context, name string, into map[string]struct{}) error {
	for _, sub := range []string{"/_layers/sha256", "/_manifests/revisions/sha256"} {
		hexes, err := h.listDirs(ctx, repoRoot+name+sub)
		if err != nil {
			return err
		}
		for _, x := range hexes {
			if len(x) >= 3 {
				into[x] = struct{}{}
			}
		}
	}
	return nil
}

func (h *Handler) usageOf(ctx context.Context, repoIDs []string) (int64, error) {
	set := map[string]struct{}{}
	for _, id := range repoIDs {
		if !uuidRe.MatchString(id) {
			continue
		}
		names, err := h.imageNames(ctx, id)
		if err != nil {
			return 0, err
		}
		for _, n := range names {
			if err := h.blobDigests(ctx, n, set); err != nil {
				return 0, err
			}
		}
	}
	var total int64
	for x := range set {
		fi, err := h.driver.Stat(ctx, blobDataPath(x))
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return 0, err
		}
		total += fi.Size()
	}
	return total, nil
}

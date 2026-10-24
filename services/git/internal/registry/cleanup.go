package registry

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/registry/storage"
	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
)

// Pulizia del registry (RG2). Le immagini senza tag si eliminano dopo N giorni (default 7); quelle
// con un tag mai da sole, salvo le regole facoltative del repo: tenere solo gli ultimi N tag di
// un'immagine, eliminare i tag sha-* più vecchi di X giorni. I tag protetti non si toccano
// mai. I blob che nessuna immagine referenzia si raccolgono con il mark-and-sweep di
// distribution, ma solo se più vecchi di BlobGrace (un push in corso li ha caricati prima del
// manifest).

// CleanupReport è l'esito di una pulizia.
type CleanupReport struct {
	// Repos è il numero di repo esaminati; Skipped quelli saltati (archiviati, sconosciuti a core).
	Repos, Skipped int
	// DeletedTags sono i tag eliminati dalle regole (formato «<immagine>:<tag>»).
	DeletedTags []string
	// DeletedImages sono le immagini (manifest) eliminate (formato «<immagine>@<digest>»).
	DeletedImages []string
}

type cleaner struct {
	h    *Handler
	mu   sync.Mutex // una pulizia alla volta
	stp  chan struct{}
	done chan struct{}
}

func newCleaner(h *Handler) *cleaner { return &cleaner{h: h} }

func (c *cleaner) start(every time.Duration) {
	c.stp, c.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(c.done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-c.stp:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), every)
				if rep, err := c.h.Cleanup(ctx); err != nil {
					c.h.log.Error("pulizia del registry non riuscita", "err", err)
				} else if len(rep.DeletedTags)+len(rep.DeletedImages) > 0 {
					c.h.log.Info("pulizia del registry", "tags", len(rep.DeletedTags), "images", len(rep.DeletedImages))
				}
				cancel()
			}
		}
	}()
}

func (c *cleaner) stop() {
	if c.stp == nil {
		return
	}
	close(c.stp)
	<-c.done
	c.stp = nil
}

// Cleanup esegue una pulizia ora. Un repo di cui core non risponde si salta (mai una regola con
// impostazioni indovinate); l'orologio è quello della Config.
func (h *Handler) Cleanup(ctx context.Context) (CleanupReport, error) {
	var rep CleanupReport
	if h.cleanup == nil {
		return rep, errors.New("registry: la pulizia richiede le impostazioni di core")
	}
	h.cleanup.mu.Lock()
	defer h.cleanup.mu.Unlock()
	ids, err := h.listDirs(ctx, strings.TrimSuffix(repoRoot, "/"))
	if err != nil {
		return rep, fmt.Errorf("registry: elenco dei repo: %w", err)
	}
	ns, err := storage.NewRegistry(ctx, h.driver, storage.EnableDelete)
	if err != nil {
		return rep, err
	}
	now := h.now()
	for _, id := range ids {
		if !uuidRe.MatchString(id) {
			continue
		}
		st, err := h.settings.src.PackageSettings(ctx, id)
		if err != nil {
			rep.Skipped++
			if !errors.Is(err, ErrSettingsNotFound) {
				h.log.Warn("pulizia: impostazioni non disponibili, repo saltato", "repoId", id, "err", err)
			}
			continue
		}
		if st.Archived {
			rep.Skipped++
			continue
		}
		rep.Repos++
		names, err := h.imageNames(ctx, id)
		if err != nil {
			return rep, err
		}
		for _, n := range names {
			if err := h.cleanImage(ctx, ns, n, st, now, &rep); err != nil {
				return rep, fmt.Errorf("registry: pulizia di %s: %w", n, err)
			}
		}
	}
	if err := storage.MarkAndSweep(ctx, &graceDriver{StorageDriver: h.driver, now: now, grace: h.blobGrace}, ns, storage.GCOpts{Quiet: true}); err != nil {
		return rep, fmt.Errorf("registry: raccolta dei blob: %w", err)
	}
	h.invalidateUsage()
	return rep, nil
}

// graceDriver non cancella i blob più giovani della tolleranza.
type graceDriver struct {
	storagedriver.StorageDriver
	now   time.Time
	grace time.Duration
}

func (g *graceDriver) Delete(ctx context.Context, p string) error {
	if strings.HasPrefix(p, storageRoot+"/blobs/") {
		if fi, err := g.Stat(ctx, p+"/data"); err == nil && g.now.Sub(fi.ModTime()) < g.grace {
			return nil
		}
	}
	return g.StorageDriver.Delete(ctx, p)
}

func (h *Handler) modTime(ctx context.Context, p string) (time.Time, bool, error) {
	fi, err := h.driver.Stat(ctx, p)
	if err != nil {
		if isNotFound(err) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	return fi.ModTime(), true, nil
}

func (h *Handler) cleanImage(ctx context.Context, ns distribution.Namespace, name string, st Settings, now time.Time, rep *CleanupReport) error {
	named, err := reference.WithName(name)
	if err != nil {
		return nil // nome che distribution non gestisce: non è un'immagine nostra
	}
	repo, err := ns.Repository(ctx, named)
	if err != nil {
		return err
	}
	ms, err := repo.Manifests(ctx)
	if err != nil {
		return err
	}
	tags, err := h.tagsOf(ctx, name)
	if err != nil {
		return err
	}
	type tagInfo struct {
		name string
		at   time.Time
	}
	var open []tagInfo // tag non protetti, con la data dell'ultimo push
	auxiliary := map[string]digest.Digest{}
	for t := range tags {
		if subject, ok := provenanceSubject(t); ok {
			auxiliary[t] = subject
			continue
		}
		at, ok, err := h.modTime(ctx, tagLinkPath(name, t))
		if err != nil {
			return err
		}
		if !ok || st.protected(t) {
			continue
		}
		open = append(open, tagInfo{t, at})
	}
	sort.Slice(open, func(i, j int) bool {
		if !open[i].at.Equal(open[j].at) {
			return open[i].at.After(open[j].at)
		}
		return open[i].name > open[j].name
	})
	drop := map[string]bool{}
	if st.DeleteShaTagsAfterDays > 0 {
		for _, t := range open {
			if isShaTag(t.name) && now.Sub(t.at) > time.Duration(st.DeleteShaTagsAfterDays)*24*time.Hour {
				drop[t.name] = true
			}
		}
	}
	if st.KeepLastTags > 0 && len(open) > st.KeepLastTags {
		for _, t := range open[st.KeepLastTags:] {
			drop[t.name] = true
		}
	}
	for t := range drop {
		if st.protected(t) {
			continue // difesa in più: i protetti non sono mai in open
		}
		if err := repo.Tags(ctx).Untag(ctx, t); err != nil {
			return err
		}
		delete(tags, t)
		rep.DeletedTags = append(rep.DeletedTags, name+":"+t)
	}

	// Immagini senza tag: quelle che nessun tag, né un indice con tag, referenzia.
	keep := map[digest.Digest]bool{}
	var queue []digest.Digest
	for _, d := range tags {
		dg := digest.Digest(d)
		if !keep[dg] {
			keep[dg] = true
			queue = append(queue, dg)
		}
	}
	for len(queue) > 0 {
		dg := queue[0]
		queue = queue[1:]
		m, err := ms.Get(ctx, dg)
		if err != nil {
			continue // manifest illeggibile: resta com'è, lo vedrà l'amministratore
		}
		for _, ref := range m.References() {
			if ok, _ := ms.Exists(ctx, ref.Digest); ok && !keep[ref.Digest] {
				keep[ref.Digest] = true
				queue = append(queue, ref.Digest)
			}
		}
	}
	// Le firme Cosign e gli SBOM sono artefatti OCI con tag tecnico. Non devono
	// mantenere in vita l'immagine sorgente, ma restano finché essa Ã¨ raggiungibile
	// da un tag o da un indice. Quando la sorgente sparisce si stacca anche l'artefatto.
	for tag, subject := range auxiliary {
		if !keep[subject] {
			if err := repo.Tags(ctx).Untag(ctx, tag); err != nil {
				return err
			}
			delete(tags, tag)
			rep.DeletedTags = append(rep.DeletedTags, name+":"+tag)
			continue
		}
		artifact := digest.Digest(tags[tag])
		if !keep[artifact] {
			keep[artifact] = true
			queue = append(queue, artifact)
		}
	}
	for len(queue) > 0 {
		dg := queue[0]
		queue = queue[1:]
		m, err := ms.Get(ctx, dg)
		if err != nil {
			continue
		}
		for _, ref := range m.References() {
			if ok, _ := ms.Exists(ctx, ref.Digest); ok && !keep[ref.Digest] {
				keep[ref.Digest] = true
				queue = append(queue, ref.Digest)
			}
		}
	}
	revs, err := h.listDirs(ctx, repoRoot+name+"/_manifests/revisions/sha256")
	if err != nil {
		return err
	}
	limit := time.Duration(st.untaggedDays()) * 24 * time.Hour
	for _, x := range revs {
		dg := digest.NewDigestFromEncoded(digest.SHA256, x)
		if keep[dg] {
			continue
		}
		at, ok, err := h.modTime(ctx, repoRoot+name+"/_manifests/revisions/sha256/"+x+"/link")
		if err != nil {
			return err
		}
		if !ok || now.Sub(at) <= limit {
			continue
		}
		if err := ms.Delete(ctx, dg); err != nil {
			return err
		}
		rep.DeletedImages = append(rep.DeletedImages, name+"@"+dg.String())
	}
	return nil
}

var provenanceTag = regexp.MustCompile(`^sha256-([a-f0-9]{64})\.(sig|sbom)$`)

func provenanceSubject(tag string) (digest.Digest, bool) {
	m := provenanceTag.FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	return digest.NewDigestFromEncoded(digest.SHA256, m[1]), true
}

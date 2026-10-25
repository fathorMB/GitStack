package registry

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"
	"time"
)

// ErrSettingsNotFound: core non conosce il repo (inesistente o eliminato).
var ErrSettingsNotFound = errors.New("registry: repo sconosciuto a core")

// DefaultUntaggedDays è il default di RG2: le immagini senza tag si eliminano dopo 7 giorni.
const DefaultUntaggedDays = 7

// Settings sono le impostazioni di un repo (RG2), come le dà core.
type Settings struct {
	RepoID, Owner, Name string
	// Archived: un repo archiviato (R10) non è toccato dalla pulizia.
	Archived bool
	// ProtectedTagPatterns sono modelli path.Match («v*») dei tag che non si sovrascrivono né si eliminano.
	ProtectedTagPatterns []string
	// DeleteUntaggedAfterDays: giorni dopo cui un'immagine senza tag si elimina (default 7).
	DeleteUntaggedAfterDays int
	// KeepLastTags: se > 0, per ogni immagine si tengono solo gli ultimi N tag.
	KeepLastTags int
	// DeleteShaTagsAfterDays: se > 0, i tag sha-* più vecchi di X giorni si eliminano.
	DeleteShaTagsAfterDays int
	// IncludeInBackup: le immagini del repo entrano nel backup (default no).
	IncludeInBackup bool
	// OwnerRepoIDs sono gli id dei repo dello stesso owner (la quota è per owner).
	OwnerRepoIDs []string
}

// SettingsSource è il lato core delle impostazioni. Il backend del registry può implementarlo:
// senza, il registry non applica tag protetti, quote né pulizia.
type SettingsSource interface {
	// PackageSettings ritorna ErrSettingsNotFound se core non conosce il repo.
	PackageSettings(ctx context.Context, repoID string) (Settings, error)
	// RecordRemovedTag conserva nel core un tag eliminato da una regola di pulizia.
	RecordRemovedTag(ctx context.Context, repoID string, tag RemovedTag) error
	// RemovedTags ritorna il registro persistente dei tag tolti dalla pulizia.
	RemovedTags(ctx context.Context, repoID string) ([]RemovedTag, error)
}

// RemovedTag è il dettaglio persistente di un tag tolto dalla pulizia automatica.
type RemovedTag struct {
	Image     string    `json:"image"`
	Name      string    `json:"name"`
	Digest    string    `json:"digest"`
	Rule      string    `json:"rule"`
	RemovedAt time.Time `json:"removedAt"`
}

// protected dice se il tag corrisponde a uno dei modelli protetti.
func (s Settings) protected(tag string) bool {
	for _, p := range s.ProtectedTagPatterns {
		if ok, err := path.Match(p, tag); err == nil && ok {
			return true
		}
	}
	return false
}

func (s Settings) untaggedDays() int {
	if s.DeleteUntaggedAfterDays < 1 {
		return DefaultUntaggedDays
	}
	return s.DeleteUntaggedAfterDays
}

func isShaTag(tag string) bool { return strings.HasPrefix(tag, "sha-") }

// settingsCache tiene per poco le impostazioni, così un push con molte richieste non chiama
// core a ogni blob. Una modifica delle impostazioni vale entro ttl.
type settingsCache struct {
	src SettingsSource
	ttl time.Duration
	now func() time.Time
	mu  sync.Mutex
	m   map[string]cachedSettings
}

type cachedSettings struct {
	s  Settings
	at time.Time
}

func newSettingsCache(src SettingsSource, ttl time.Duration) *settingsCache {
	return &settingsCache{src: src, ttl: ttl, now: time.Now, m: map[string]cachedSettings{}}
}

func (c *settingsCache) get(ctx context.Context, repoID string) (Settings, error) {
	c.mu.Lock()
	e, ok := c.m[repoID]
	c.mu.Unlock()
	if ok && c.now().Sub(e.at) < c.ttl {
		return e.s, nil
	}
	s, err := c.src.PackageSettings(ctx, repoID)
	if err != nil {
		return Settings{}, err
	}
	c.mu.Lock()
	c.m[repoID] = cachedSettings{s: s, at: c.now()}
	c.mu.Unlock()
	return s, nil
}

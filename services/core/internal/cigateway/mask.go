package cigateway

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/google/uuid"
)

const maskerTTL = 2 * time.Hour

type maskEntry struct {
	m  *ci.Masker
	at time.Time
}

// maskCache tiene in memoria il masker di ogni job in corso (segreti + token del job).
// Si rifà dal database se il gateway riparte a metà job.
type maskCache struct {
	mu sync.Mutex
	m  map[uuid.UUID]maskEntry
}

func (c *maskCache) put(job uuid.UUID, m *ci.Masker) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[uuid.UUID]maskEntry{}
	}
	now := time.Now()
	for k, e := range c.m {
		if now.Sub(e.at) > maskerTTL {
			delete(c.m, k)
		}
	}
	c.m[job] = maskEntry{m: m, at: now}
}

func (c *maskCache) get(job uuid.UUID) (*ci.Masker, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[job]
	return e.m, ok
}

func (c *maskCache) drop(job uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, job)
}

func maskValues(secrets map[string]string) []string {
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, secrets[k])
	}
	return out
}

// maskerFor dà il masker del job; se non è in cache lo ricostruisce dai segreti del repo
// e dal token con cui il runner sta chiamando.
func (g *gateway) maskerFor(ctx context.Context, a ci.JobAuth, token string) (*ci.Masker, error) {
	if m, ok := g.maskers.get(a.JobID); ok {
		return m, nil
	}
	vals := []string{token}
	if g.Keys != nil {
		env, err := ci.LoadJobEnv(ctx, g.Pool, g.Keys, a.RepoID)
		if err != nil {
			return nil, err
		}
		vals = append(vals, maskValues(env.Secrets)...)
	}
	m := ci.NewMasker(vals...)
	g.maskers.put(a.JobID, m)
	return m, nil
}

package attachments

import (
	"context"
	"log/slog"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// DefaultOrphanTTL: un allegato caricato e mai collegato a una issue o a un
// commento (salvataggio non riuscito o abbandonato) si elimina dopo 24 ore.
const DefaultOrphanTTL = 24 * time.Hour

// Cleaner elimina gli allegati orfani: file e riga. Sicuro con più repliche
// (FOR UPDATE SKIP LOCKED) e idempotente.
type Cleaner struct {
	Store *store.Store
	Disk  *Disk
	// TTL: età oltre la quale un allegato non collegato è orfano.
	TTL time.Duration
	// Now è l'orologio (iniettabile nei test); nil = time.Now.
	Now func() time.Time
	Log *slog.Logger
}

func (c *Cleaner) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Cleaner) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return DefaultOrphanTTL
}

// RunOnce elimina gli orfani scaduti e ritorna quanti.
func (c *Cleaner) RunOnce(ctx context.Context) (int, error) {
	log := c.Log
	if log == nil {
		log = slog.Default()
	}
	n, err := c.Store.DeleteOrphanAttachments(ctx, c.now().Add(-c.ttl()), func(a store.Attachment) error {
		return c.Disk.Remove(a.RepoID, a.ID)
	})
	if err != nil {
		log.Warn("pulizia degli allegati orfani non riuscita", "err", err)
	}
	if n > 0 {
		log.Info("allegati orfani eliminati", "count", n)
	}
	return n, err
}

// Run esegue RunOnce ogni `every` finché il contesto non finisce.
func (c *Cleaner) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		_, _ = c.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

package identityclient

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"
)

// Valori di partenza dal README di identity ("Come il gateway verifica
// sessioni e token"): 30 s per gli esiti positivi, 5 s per i negativi.
const (
	DefaultPositiveTTL = 30 * time.Second
	DefaultNegativeTTL = 5 * time.Second

	// maxEntries limita la memoria della cache: le credenziali inventate
	// sono esiti negativi e non devono poterla riempire.
	maxEntries = 10000
)

// Cache avvolge un Verifier con una cache in memoria.
//
//   - la chiave è lo SHA-256 della credenziale: il valore in chiaro non resta
//     in memoria dopo la richiesta e non finisce nei log;
//   - un esito positivo vive PositiveTTL (o meno, se identity suggerisce un
//     TTL più breve) e comunque mai oltre expiresAt della credenziale;
//   - un esito negativo vive NegativeTTL;
//   - se identity non risponde si serve solo ciò che è ancora nel TTL: la
//     voce scaduta non viene riusata né allungata, e l'errore
//     (ErrUnavailable) arriva al chiamante, che risponde 503. Mai fail open.
type Cache struct {
	next        Verifier
	now         func() time.Time
	positiveTTL time.Duration
	negativeTTL time.Duration

	mu      sync.Mutex
	entries map[[sha256.Size]byte]entry
}

type entry struct {
	result  Result
	expires time.Time
}

// NewCache crea la cache. now è iniettabile per i test (nil = time.Now).
func NewCache(next Verifier, positiveTTL, negativeTTL time.Duration, now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	return &Cache{
		next: next, now: now,
		positiveTTL: positiveTTL, negativeTTL: negativeTTL,
		entries: make(map[[sha256.Size]byte]entry),
	}
}

// Key è la chiave di cache di una credenziale.
func Key(credential string) [sha256.Size]byte {
	return sha256.Sum256([]byte(credential))
}

// Verify implementa Verifier.
func (c *Cache) Verify(ctx context.Context, credential string, kind Kind) (Result, error) {
	key := Key(credential)
	now := c.now()

	c.mu.Lock()
	e, ok := c.entries[key]
	if ok && !now.Before(e.expires) {
		delete(c.entries, key)
		ok = false
	}
	c.mu.Unlock()
	if ok {
		return e.result, nil
	}

	res, err := c.next.Verify(ctx, credential, kind)
	if err != nil {
		return Result{}, err
	}

	now = c.now()
	ttl := c.negativeTTL
	if res.Active {
		ttl = c.positiveTTL
	}
	if res.TTL > 0 && res.TTL < ttl {
		ttl = res.TTL
	}
	if res.Active && res.Principal.ExpiresAt != nil {
		if left := res.Principal.ExpiresAt.Sub(now); left < ttl {
			ttl = left
		}
	}
	if ttl > 0 {
		c.store(key, entry{result: res, expires: now.Add(ttl)}, now)
	}
	return res, nil
}

// Forget toglie dalla cache la voce di una credenziale (per esempio dopo un
// cambio di password, che cambia mustChangePassword).
func (c *Cache) Forget(credential string) {
	c.mu.Lock()
	delete(c.entries, Key(credential))
	c.mu.Unlock()
}

func (c *Cache) store(key [sha256.Size]byte, e entry, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxEntries {
		for k, v := range c.entries {
			if !now.Before(v.expires) {
				delete(c.entries, k)
			}
		}
		for k := range c.entries {
			if len(c.entries) < maxEntries {
				break
			}
			delete(c.entries, k)
		}
	}
	c.entries[key] = e
}

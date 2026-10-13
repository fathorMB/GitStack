package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// maxBuckets è il tetto di chiavi tracciate per limitatore: oltre, una
// credenziale o un IP nuovo non crea un bucket (la memoria resta limitata
// anche sotto un flood di token inventati).
const maxBuckets = 100_000

// RateLimitConfig configura il limitatore token-bucket del gateway (F6,
// V11.1.4). Un rate <= 0 disattiva il rispettivo limitatore.
type RateLimitConfig struct {
	// CredentialRate è il rifornimento (richieste/secondo) del bucket di ogni
	// credenziale (token gst_ o cookie di sessione); CredentialBurst la
	// capienza.
	CredentialRate  float64
	CredentialBurst int
	// IPRate e IPBurst valgono per ogni IP client.
	IPRate  float64
	IPBurst int

	// ClientIP ricava l'IP vero del client (proxy.ClientIP con i proxy
	// fidati: X-Forwarded-For conta solo da loro). Se nil si usa RemoteAddr.
	ClientIP func(*http.Request) string

	Logger *slog.Logger
	// Now sostituisce l'orologio (test).
	Now func() time.Time
}

// Scope dei rifiuti, usati come etichetta nei log e nei contatori.
const (
	ScopeCredential = "credential"
	ScopeIP         = "ip"
)

// RateLimiter è lo stato del limitatore; Middleware lo aggancia alla catena.
type RateLimiter struct {
	cfg  RateLimitConfig
	cred *bucketSet
	ip   *bucketSet
}

// NewRateLimiter costruisce il limitatore. Con entrambi i rate a zero
// Middleware lascia passare tutto.
func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	rl := &RateLimiter{cfg: cfg}
	if cfg.CredentialRate > 0 {
		rl.cred = newBucketSet(ScopeCredential, cfg.CredentialRate, cfg.CredentialBurst, cfg.Now, cfg.Logger)
	}
	if cfg.IPRate > 0 {
		rl.ip = newBucketSet(ScopeIP, cfg.IPRate, cfg.IPBurst, cfg.Now, cfg.Logger)
	}
	return rl
}

// Rejected ritorna il numero di richieste rifiutate per scope
// (ScopeCredential, ScopeIP) dall'avvio: è il contatore su cui si basa
// l'alert, riportato anche nei log (messaggio "rate limit exceeded").
func (rl *RateLimiter) Rejected(scope string) uint64 {
	switch scope {
	case ScopeCredential:
		if rl.cred != nil {
			return rl.cred.rejected.Load()
		}
	case ScopeIP:
		if rl.ip != nil {
			return rl.ip.rejected.Load()
		}
	}
	return 0
}

// Middleware è il middleware da inserire nella catena.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	if rl.cred == nil && rl.ip == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rl.ip != nil {
			ip := ""
			if rl.cfg.ClientIP != nil {
				ip = rl.cfg.ClientIP(r)
			}
			if ip == "" {
				ip = remoteHost(r)
			}
			if ok, wait := rl.ip.allow(ip); !ok {
				reject(w, wait)
				return
			}
		}
		if rl.cred != nil {
			if cred, _ := credentialFrom(r); cred != "" {
				if ok, wait := rl.cred.allow(cred); !ok {
					reject(w, wait)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func remoteHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func reject(w http.ResponseWriter, wait time.Duration) {
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeAuthError(w, http.StatusTooManyRequests, "rate_limited",
		"Troppe richieste: riprova dopo il tempo indicato in Retry-After.", map[string]any{"retryAfterSeconds": secs})
}

type bucket struct {
	tokens float64
	last   time.Time
}

type bucketSet struct {
	scope string
	rate  float64
	burst float64
	now   func() time.Time
	log   *slog.Logger

	rejected atomic.Uint64

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
	lastLog   time.Time
	suppress  uint64
}

func newBucketSet(scope string, rate float64, burst int, now func() time.Time, log *slog.Logger) *bucketSet {
	if burst < 1 {
		burst = 1
	}
	return &bucketSet{scope: scope, rate: rate, burst: float64(burst), now: now, log: log, buckets: map[string]*bucket{}}
}

// allow consuma un token dalla chiave; se non ce ne sono ritorna false e
// dopo quanto ce ne sarà uno.
func (s *bucketSet) allow(key string) (bool, time.Duration) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked(now)
	b, ok := s.buckets[key]
	if !ok {
		if len(s.buckets) >= maxBuckets {
			// Tabella piena anche dopo la pulizia: non si traccia la chiave
			// nuova (meglio non limitare che esaurire la memoria).
			return true, 0
		}
		b = &bucket{tokens: s.burst, last: now}
		s.buckets[key] = b
	} else if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = math.Min(s.burst, b.tokens+el*s.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / s.rate * float64(time.Second))
	s.rejectedLocked(key, now)
	return false, wait
}

// sweepLocked toglie i bucket che si sarebbero già riempiti del tutto (la
// chiave è indistinguibile da una nuova): al più una volta al minuto.
func (s *bucketSet) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < time.Minute {
		return
	}
	s.lastSweep = now
	full := time.Duration(s.burst / s.rate * float64(time.Second))
	for k, b := range s.buckets {
		if now.Sub(b.last) >= full {
			delete(s.buckets, k)
		}
	}
}

// rejectedLocked conta il rifiuto e lo logga al più una volta al secondo
// (con il numero dei rifiuti soppressi), con la chiave offuscata.
func (s *bucketSet) rejectedLocked(key string, now time.Time) {
	total := s.rejected.Add(1)
	if now.Sub(s.lastLog) < time.Second {
		s.suppress++
		return
	}
	s.lastLog = now
	s.log.Warn("rate limit exceeded",
		"scope", s.scope, "key", obfuscate(s.scope, key),
		"rejected_total", total, "suppressed_since_last_log", s.suppress)
	s.suppress = 0
}

// obfuscate non porta mai la credenziale nei log: per le credenziali un
// prefisso dello SHA-256, per gli IP l'indirizzo.
func obfuscate(scope, key string) string {
	if scope == ScopeIP {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return "sha256:" + hex.EncodeToString(sum[:4])
}

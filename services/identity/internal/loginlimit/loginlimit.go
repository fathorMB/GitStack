// Package loginlimit limita i tentativi di login falliti, per utente e per
// indirizzo IP, con una finestra scorrevole.
//
// Un tentativo fallito viene registrato sia sulla chiave dell'utente (lo
// username/email normalizzato in minuscolo, esista o no: così l'utente
// inesistente si comporta come uno esistente) sia su quella dell'IP. Quando
// una delle due chiavi ha raggiunto il massimo di fallimenti dentro la
// finestra, Check risponde con il tempo dopo cui si può riprovare. Un login
// riuscito azzera la chiave dell'utente (non quella dell'IP).
//
// Lo stato è in memoria del processo: con più repliche di identity il limite
// vale per replica. Il tempo arriva da un clock iniettato, così i test
// provano lo sblocco senza sleep.
//
// Configurazione (FromEnv), tutte opzionali:
//
//	GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_USER  fallimenti per utente nella finestra (default 5)
//	GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_IP    fallimenti per IP nella finestra (default 20)
//	GITSTACK_IDENTITY_LOGIN_WINDOW             durata finestra, formato Go (default 15m)
package loginlimit

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config sono i parametri del limitatore.
type Config struct {
	MaxPerUser int
	MaxPerIP   int
	Window     time.Duration
}

// Default sono i valori usati quando la configurazione non dice altro.
var Default = Config{MaxPerUser: 5, MaxPerIP: 20, Window: 15 * time.Minute}

// FromEnv legge la configurazione dalle variabili GITSTACK_IDENTITY_LOGIN_*
// tramite getenv (os.Getenv in produzione).
func FromEnv(getenv func(string) string) (Config, error) {
	c := Default
	for _, f := range []struct {
		name string
		dst  *int
	}{
		{"GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_USER", &c.MaxPerUser},
		{"GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_IP", &c.MaxPerIP},
	} {
		if v := getenv(f.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return c, fmt.Errorf("%s: atteso un intero positivo", f.name)
			}
			*f.dst = n
		}
	}
	if v := getenv("GITSTACK_IDENTITY_LOGIN_WINDOW"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return c, fmt.Errorf("GITSTACK_IDENTITY_LOGIN_WINDOW: attesa una durata positiva (es. 15m)")
		}
		c.Window = d
	}
	return c, nil
}

// maxKeys limita la memoria: chiavi inventate da un attaccante non possono
// farla crescere senza limite.
const maxKeys = 100_000

// Limiter è il limitatore; sicuro per uso concorrente.
type Limiter struct {
	cfg Config
	now func() time.Time

	mu    sync.Mutex
	fails map[string][]time.Time // chiave -> istanti dei fallimenti nella finestra
}

// New crea un Limiter. now nil usa time.Now.
func New(cfg Config, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{cfg: cfg, now: now, fails: map[string][]time.Time{}}
}

func userKey(login string) string { return "u:" + strings.ToLower(strings.TrimSpace(login)) }
func ipKey(ip string) string      { return "i:" + ip }

// prune toglie i fallimenti fuori finestra dalla chiave; ritorna quelli rimasti.
func (l *Limiter) prune(key string, now time.Time) []time.Time {
	ts := l.fails[key]
	cut := now.Add(-l.cfg.Window)
	i := 0
	for i < len(ts) && !ts[i].After(cut) {
		i++
	}
	if i == len(ts) {
		delete(l.fails, key)
		return nil
	}
	if i > 0 {
		ts = ts[i:]
		l.fails[key] = ts
	}
	return ts
}

// retryAfter: se la chiave ha già `max` fallimenti nella finestra, dopo
// quanto il più vecchio di quelli che contano esce dalla finestra.
func (l *Limiter) retryAfter(key string, max int, now time.Time) time.Duration {
	ts := l.prune(key, now)
	if len(ts) < max {
		return 0
	}
	// Si sblocca quando il fallimento (len-max)-esimo esce dalla finestra.
	return ts[len(ts)-max].Add(l.cfg.Window).Sub(now)
}

// Check dice se un tentativo per (login, ip) è ammesso. Se non lo è,
// restituisce false e dopo quanto riprovare.
func (l *Limiter) Check(login, ip string) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	ru := l.retryAfter(userKey(login), l.cfg.MaxPerUser, now)
	ri := l.retryAfter(ipKey(ip), l.cfg.MaxPerIP, now)
	if ru <= 0 && ri <= 0 {
		return true, 0
	}
	if ri > ru {
		ru = ri
	}
	return false, ru
}

// Fail registra un tentativo fallito per (login, ip).
func (l *Limiter) Fail(login, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.fails) >= maxKeys {
		l.sweep(now)
	}
	for _, k := range []string{userKey(login), ipKey(ip)} {
		if _, ok := l.fails[k]; !ok && len(l.fails) >= maxKeys {
			continue // pieno anche dopo lo sweep: meglio non registrare che crescere
		}
		l.fails[k] = append(l.prune(k, now), now)
	}
}

// Success azzera i fallimenti dell'utente dopo un login riuscito.
func (l *Limiter) Success(login string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, userKey(login))
}

func (l *Limiter) sweep(now time.Time) {
	for k := range l.fails {
		l.prune(k, now)
	}
}

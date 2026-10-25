//go:build integration

package stackitest

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Avvio dei servizi come binari (GIT-368).
//
// freeAddr apre una porta, la chiude e la restituisce: fra la Close e il bind
// del binario figlio un altro processo della macchina (Postgres, testcontainers,
// altri pacchetti di test) può prendersi la stessa porta effimera. Il figlio
// allora esce, o resta vivo senza ascoltare, scrivendo «address already in use».
// startSvc è l'unico modo di avviare un servizio: se il figlio esce prima di
// ascoltare, oppure il suo log dice «address already in use», sceglie indirizzi
// nuovi e riprova, fino a maxStartAttempts volte.

const (
	maxStartAttempts = 3
	addrInUseText    = "address already in use"
	startTimeout     = 30 * time.Second
)

// pickHook, se non nil, è chiamato dopo la scelta degli indirizzi di un
// tentativo e prima di avviare il figlio: i test lo usano per occupare una porta.
var pickHook func(t *testing.T, name string, attempt int, addrs []string)

// startLogf scrive la riga di log di un nuovo tentativo; i test la sostituiscono per leggerla.
var startLogf = func(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
}

// syncBuf è un buffer che si può leggere mentre il figlio ci scrive.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// proc è un processo figlio con il suo log e un canale chiuso alla sua uscita.
type proc struct {
	name string
	cmd  *exec.Cmd
	logs *syncBuf
	done chan struct{}
	err  error // esito di cmd.Wait, valido dopo la chiusura di done
}

func spawn(name, bin string, env []string, args ...string) (*proc, error) {
	p := &proc{name: name, logs: &syncBuf{}, done: make(chan struct{})}
	p.cmd = exec.Command(bin, args...)
	p.cmd.Env = env
	p.cmd.Stdout, p.cmd.Stderr = p.logs, p.logs
	if err := p.cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		p.err = p.cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// stop ferma il figlio (per PID) e aspetta che sia uscito.
func (p *proc) stop() {
	_ = p.cmd.Process.Kill()
	<-p.done
}

func (p *proc) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// startError è un fallimento di avvio; retry dice se vale la pena riprovare con indirizzi nuovi.
type startError struct {
	retry bool
	msg   string
}

func (e *startError) Error() string { return e.msg }

// check dice perché il figlio non può più diventare pronto, o nil.
func (p *proc) check(addr string) *startError {
	if p == nil {
		return nil
	}
	if p.exited() {
		exit := "codice di uscita 0"
		if p.err != nil {
			exit = p.err.Error()
		}
		return &startError{retry: true, msg: fmt.Sprintf("%s è uscito prima di ascoltare su %s (%s)\nlog:\n%s", p.name, addr, exit, p.logs.String())}
	}
	if l := p.logs.String(); strings.Contains(l, addrInUseText) {
		return &startError{retry: true, msg: fmt.Sprintf("%s non ha potuto ascoltare su %s: %s\nlog:\n%s", p.name, addr, addrInUseText, l)}
	}
	return nil
}

// awaitTCP aspetta che qualcuno accetti connessioni su addr. Se p non è nil,
// smette appena il figlio esce o il suo log dice «address already in use»,
// senza arrivare al timeout. Lo stato del figlio si guarda prima e dopo ogni
// connessione riuscita: una porta presa da un altro processo accetterebbe lo
// stesso.
func awaitTCP(addr string, p *proc, timeout time.Duration) error {
	return awaitCond(addr, p, timeout, func() bool {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
}

// awaitHealthz aspetta GET /healthz = 200 su addr, con le stesse regole di awaitTCP.
func awaitHealthz(addr string, p *proc, timeout time.Duration) error {
	cl := &http.Client{Timeout: 2 * time.Second}
	return awaitCond(addr, p, timeout, func() bool {
		resp, err := cl.Get("http://" + addr + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
}

func awaitCond(addr string, p *proc, timeout time.Duration, ready func() bool) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := p.check(addr); err != nil {
			return err
		}
		if ready() {
			if err := p.check(addr); err != nil {
				return err
			}
			return nil
		}
		if !time.Now().Before(deadline) {
			msg := fmt.Sprintf("nessuno ascolta su %s", addr)
			if p != nil {
				msg += fmt.Sprintf(" dopo %s\nlog di %s:\n%s", timeout, p.name, p.logs.String())
			}
			return &startError{msg: msg}
		}
		var gone <-chan struct{}
		if p != nil {
			gone = p.done
		}
		select {
		case <-gone:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// waitTCP aspetta che si ascolti su addr e fallisce subito se il figlio è uscito.
func waitTCP(t *testing.T, addr string, p *proc) {
	t.Helper()
	if err := awaitTCP(addr, p, startTimeout); err != nil {
		t.Fatal(err)
	}
}

// startSvc avvia il servizio `name` su naddr indirizzi nuovi. env riceve gli
// indirizzi del tentativo e dà l'ambiente (oltre a quello del processo di
// test). Il primo indirizzo risponde a /healthz; gli altri devono accettare
// connessioni TCP. Ritorna gli indirizzi dell'ultimo tentativo, quello riuscito.
func startSvc(t *testing.T, name, bin string, naddr int, env func(addrs []string) []string) []string {
	t.Helper()
	var last error
	for attempt := 1; attempt <= maxStartAttempts; attempt++ {
		addrs := make([]string, naddr)
		for i := range addrs {
			addrs[i] = freeAddr(t)
		}
		if pickHook != nil {
			pickHook(t, name, attempt, addrs)
		}
		full := os.Environ()
		if name == "gateway" {
			// Timeout ampi gateway→core e gateway→identity: con -race e i
			// pacchetti in parallelo lo stack è carico e il default di produzione
			// (5 s) scade (503 core_unavailable, run CI 39300966677, GIT-308).
			// È un limite del banco di prova: il default di produzione non cambia.
			// Un valore passato esplicitamente dal test vince (l'ultimo ha la precedenza).
			full = append(full, "GITSTACK_CORE_TIMEOUT=60s", "GITSTACK_IDENTITY_TIMEOUT=60s")
		}
		full = append(full, env(addrs)...)
		p, err := spawn(name, bin, full)
		if err != nil {
			t.Fatalf("avvio di %s: %v", name, err)
		}
		err = awaitHealthz(addrs[0], p, startTimeout)
		for _, extra := range addrs[1:] {
			if err != nil {
				break
			}
			err = awaitTCP(extra, p, startTimeout)
		}
		if err == nil {
			t.Cleanup(func() {
				p.stop()
				logs := p.logs.String()
				if t.Failed() {
					t.Logf("log di %s:\n%s", name, logs)
				}
				for _, secret := range []string{serviceSecret, coreSecret, gitSecret, adminPassword, newPassword} {
					if strings.Contains(logs, secret) {
						t.Errorf("un segreto compare nei log di %s", name)
					}
				}
			})
			return addrs
		}
		p.stop()
		var se *startError
		if !errors.As(err, &se) || !se.retry {
			t.Fatalf("%s non è partito: %v", name, err)
		}
		last = err
		if attempt < maxStartAttempts {
			startLogf(t, "%s: tentativo %d/%d fallito su %s, riprovo con indirizzi nuovi", name, attempt, maxStartAttempts, strings.Join(addrs, ","))
		}
	}
	t.Fatalf("%s non è partito dopo %d tentativi; ultimo errore, con il log del figlio: %v", name, maxStartAttempts, last)
	return nil
}

// startOne avvia un servizio con un solo indirizzo, che passa nella variabile addrKey.
func startOne(t *testing.T, name, bin, addrKey string, extra ...string) string {
	t.Helper()
	return startSvc(t, name, bin, 1, func(a []string) []string {
		return append([]string{addrKey + "=" + a[0]}, extra...)
	})[0]
}

// lateCore è il server HTTP di core con il router montato dopo: gli indirizzi
// degli altri servizi (che core chiama) si conoscono solo quando sono partiti,
// ma core deve avere già un URL per dirlo a gateway e git.
type lateCore struct {
	*httptest.Server
	h atomic.Pointer[http.Handler]
}

func newLateCore(t *testing.T) *lateCore {
	t.Helper()
	l := &lateCore{}
	l.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := l.h.Load(); h != nil {
			(*h).ServeHTTP(w, r)
			return
		}
		http.Error(w, "core non ancora pronto", http.StatusServiceUnavailable)
	}))
	t.Cleanup(l.Close)
	return l
}

func (l *lateCore) set(h http.Handler) { l.h.Store(&h) }

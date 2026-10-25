//go:build integration

package stackitest

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAwaitTCPFiglioUscito: se il figlio esce prima di ascoltare, l'attesa
// fallisce subito con la sua uscita e il suo log, non al timeout (GIT-368).
// Il «figlio» è il binario di test stesso, lanciato senza test: stampa PASS ed esce.
func TestAwaitTCPFiglioUscito(t *testing.T) {
	p, err := spawn("figlio", os.Args[0], os.Environ(), "-test.run=^$")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.stop)
	addr := freeAddr(t) // nessuno ascolta qui
	const timeout = 60 * time.Second
	start := time.Now()
	werr := awaitTCP(addr, p, timeout)
	took := time.Since(start)
	if werr == nil {
		t.Fatal("awaitTCP è riuscita senza nessuno in ascolto")
	}
	if took > 15*time.Second {
		t.Fatalf("awaitTCP ha aspettato %s: doveva fallire appena il figlio è uscito (timeout %s)", took, timeout)
	}
	for _, w := range []string{"figlio è uscito prima di ascoltare su " + addr, "PASS"} {
		if !strings.Contains(werr.Error(), w) {
			t.Errorf("manca %q nell'errore:\n%v", w, werr)
		}
	}
	if se, ok := werr.(*startError); !ok || !se.retry {
		t.Errorf("l'uscita del figlio deve essere un errore da ritentare: %#v", werr)
	}
	t.Logf("fallita dopo %s: %.60s", took.Round(time.Millisecond), strings.ReplaceAll(werr.Error(), "\n", " | "))
}

// TestStartSvcRiprovaSePortaOccupata: la porta che freeAddr ha appena dato al
// servizio git è occupata da un altro processo (qui: net.Listen del test) fra
// la scelta e il bind del figlio. Lo stack parte lo stesso al tentativo
// successivo, con indirizzi nuovi e una riga di log sul nuovo tentativo (GIT-368).
func TestStartSvcRiprovaSePortaOccupata(t *testing.T) {
	var mu sync.Mutex
	var taken []string
	var lines []string
	pickHook = func(t *testing.T, name string, attempt int, addrs []string) {
		if name != "git" || attempt != 1 {
			return
		}
		l, err := net.Listen("tcp", addrs[0])
		if err != nil {
			t.Fatalf("il test non riesce a occupare %s: %v", addrs[0], err)
		}
		t.Cleanup(func() { _ = l.Close() })
		go func() { // accetta e tiene aperto: un vero squatter risponde al TCP
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				t.Cleanup(func() { _ = c.Close() })
			}
		}()
		mu.Lock()
		taken = append(taken, addrs[0])
		mu.Unlock()
	}
	startLogf = func(t *testing.T, format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
		t.Logf(format, args...)
	}
	t.Cleanup(func() {
		pickHook = nil
		startLogf = func(t *testing.T, f string, a ...any) { t.Helper(); t.Logf(f, a...) }
	})

	e := newGitEnv(t)

	mu.Lock()
	defer mu.Unlock()
	if len(taken) != 1 {
		t.Fatalf("la porta del servizio git doveva essere occupata una volta: %v", taken)
	}
	if e.gitHTTP == "http://"+taken[0] {
		t.Fatalf("il servizio git usa ancora l'indirizzo occupato %s", taken[0])
	}
	var retried bool
	for _, l := range lines {
		if strings.Contains(l, "git: tentativo 1/3 fallito") && strings.Contains(l, taken[0]) && strings.Contains(l, "riprovo") {
			retried = true
		}
	}
	if !retried {
		t.Fatalf("nessuna riga di log sul nuovo tentativo del servizio git: %q", lines)
	}
	resp, err := http.Get(e.gitHTTP + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz del servizio git dopo il nuovo tentativo: %d", resp.StatusCode)
	}
	// e lo stack funziona davvero: un repo creato via API, push e lettura.
	e.createRepo("alice", "dopo-riprova", "private")
	u := e.httpsURL("alice", e.tokens["alice"], "/alice/dopo-riprova.git")
	w := e.initWork(u, map[string]string{"a.txt": "a\n"})
	e.mustGit(w, "", "push", "-q", "origin", "main")
	if e.remoteHead(u, "") == "" {
		t.Error("il repo non è leggibile dopo il nuovo tentativo")
	}
}

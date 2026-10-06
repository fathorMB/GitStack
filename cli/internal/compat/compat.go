// Package compat confronta la versione di gs con quella del server (G6):
// versione diversa = avviso su stderr, versione maggiore diversa = rifiuto.
//
// Il server la espone in GET /v1/meta (pubblico). L'esito si tiene in una
// cache breve su file, così il controllo non costa una richiesta a ogni
// comando.
package compat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CacheTTL è per quanto tempo vale l'esito in cache.
const CacheTTL = time.Hour

// ErrMajorMismatch è l'errore di Check quando la versione maggiore di gs
// e quella del server sono diverse (gs deve uscire con exit 1).
var ErrMajorMismatch = errors.New("versione maggiore incompatibile")

var now = time.Now

type meta struct {
	ServerVersion string `json:"server_version"`
	APIVersion    string `json:"api_version"`
}

type cacheEntry struct {
	BaseURL       string    `json:"base_url"`
	CheckedAt     time.Time `json:"checked_at"`
	ServerVersion string    `json:"server_version"` // vuoto: il server non ha /meta
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)

// parse ritorna la versione maggiore se v è nella forma vX.Y.Z o X.Y.Z.
func parse(v string) (major int, ok bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// Check confronta clientVersion con la versione del server di baseURL
// (la radice dell'API, es. "https://git.example/v1"; si interroga
// baseURL + "/meta"). Ritorna un errore che wrappa ErrMajorMismatch solo se
// le versioni sono entrambe semver con maggiore diversa; le differenze
// minori e quelle fra versioni non semver (sha-..., dev) sono solo un
// avviso su stderr. Un server senza /meta (404), irraggiungibile o con una
// risposta illeggibile non produce né avvisi né errori: il controllo non
// deve impedire di lavorare. cachePath vuoto disattiva la cache.
func Check(ctx context.Context, httpClient *http.Client, baseURL, clientVersion, cachePath string, stderr io.Writer) error {
	base := strings.TrimRight(baseURL, "/")
	serverVersion, ok := fromCache(cachePath, base)
	if !ok {
		var err error
		serverVersion, err = fetch(ctx, httpClient, base)
		if err != nil {
			return nil
		}
		writeCache(cachePath, cacheEntry{BaseURL: base, CheckedAt: now(), ServerVersion: serverVersion})
	}
	if serverVersion == "" || serverVersion == clientVersion {
		return nil
	}

	cm, cok := parse(clientVersion)
	sm, sok := parse(serverVersion)
	if cok && sok && cm != sm {
		return fmt.Errorf("%w: gs è %s, il server è %s: scarica la versione del server da %s/downloads",
			ErrMajorMismatch, clientVersion, serverVersion, hostOf(base))
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "avviso: gs è %s ma il server è %s: aggiorna gs da %s/downloads\n",
			clientVersion, serverVersion, hostOf(base))
	}
	return nil
}

// hostOf toglie il suffisso /v1 dalla base dell'API.
func hostOf(base string) string {
	return strings.TrimSuffix(base, "/v1")
}

func fetch(ctx context.Context, c *http.Client, base string) (string, error) {
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/meta", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /meta: status %d", resp.StatusCode)
	}
	var m meta
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&m); err != nil {
		return "", err
	}
	return m.ServerVersion, nil
}

func fromCache(path, base string) (string, bool) {
	if path == "" {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var e cacheEntry
	if json.Unmarshal(b, &e) != nil || e.BaseURL != base {
		return "", false
	}
	if age := now().Sub(e.CheckedAt); age < 0 || age >= CacheTTL {
		return "", false
	}
	return e.ServerVersion, true
}

func writeCache(path string, e cacheEntry) {
	if path == "" {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o600)
}

// Package smarthttp serve il protocollo smart HTTP di git (R1:
// https://<host>/<owner>/<repo>.git) con il binario git ufficiale
// (`git upload-pack|receive-pack --stateless-rpc`).
//
// Autenticazione: Basic, la password è un token personale (l'utente è
// ignorato); nessun accesso anonimo (P2): senza credenziali 401 con
// WWW-Authenticate, così git le chiede. I permessi li applica il pacchetto
// access: fetch richiede read, push write; repo inesistente, eliminato o non
// leggibile risponde sempre 404. Il protocollo "dumb" non è supportato.
package smarthttp

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
	"github.com/fathorMB/GitStack/services/git/internal/receiverules"
)

// pathRe: /<owner>/<repo>.git/<endpoint>.
var pathRe = regexp.MustCompile(`^/([^/]+)/([^/]+)\.git/(info/refs|git-upload-pack|git-receive-pack)$`)

// Match dice se il percorso è una rotta smart HTTP.
func Match(path string) bool { return pathRe.MatchString(path) }

// Authorizer è la parte di access.Authorizer usata dall'handler.
type Authorizer interface {
	Authenticate(ctx context.Context, token string) (access.Principal, bool, error)
	Authorize(ctx context.Context, p access.Principal, owner, name string, write bool) (string, error)
}

// RepoAuthorizer è un Authorizer che dà anche il repo risolto da core: serve
// per applicare le regole alla ricezione del push (branch protetto).
type RepoAuthorizer interface {
	AuthorizeRepo(ctx context.Context, p access.Principal, owner, name string, write bool) (string, access.RepoRef, error)
}

// Handler serve le rotte smart HTTP.
type Handler struct {
	Auth Authorizer
	// Events pubblica git.push dopo i push riusciti (nil = nessun evento).
	Events *pushevent.Notifier
	// Rules sono le regole alla ricezione del push (R6, R9); nil = nessuna.
	Rules  *receiverules.Rules
	GitBin string
	Logger *slog.Logger
	// Realm è il realm del Basic auth (default "GitStack").
	Realm string
}

// ServeHTTP implementa http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m := pathRe.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	owner, name, endpoint := m[1], m[2], m[3]

	var service string
	switch endpoint {
	case "info/refs":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			textErr(w, http.StatusMethodNotAllowed, "Metodo non consentito.")
			return
		}
		service = r.URL.Query().Get("service")
	default:
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			textErr(w, http.StatusMethodNotAllowed, "Metodo non consentito.")
			return
		}
		service = endpoint
	}
	if service != "git-upload-pack" && service != "git-receive-pack" {
		textErr(w, http.StatusForbidden, "Serve il protocollo smart HTTP di git.")
		return
	}
	write := service == "git-receive-pack"

	// 1. Autenticazione (P2): prima di tutto, anche per repo inesistenti.
	token, ok := basicPassword(r)
	if !ok {
		h.challenge(w)
		return
	}
	p, valid, err := h.Auth.Authenticate(r.Context(), token)
	if err != nil {
		h.logger().Error("verifica del token non riuscita", "err", err)
		textErr(w, http.StatusServiceUnavailable, "Servizio temporaneamente non disponibile.")
		return
	}
	if !valid {
		h.challenge(w)
		return
	}

	// 2. Permessi.
	var (
		dir string
		ref access.RepoRef
	)
	if ra, ok := h.Auth.(RepoAuthorizer); ok {
		dir, ref, err = ra.AuthorizeRepo(r.Context(), p, owner, name, write)
	} else {
		dir, err = h.Auth.Authorize(r.Context(), p, owner, name, write)
	}
	switch {
	case err == nil:
	case errors.Is(err, access.ErrNotFound):
		textErr(w, http.StatusNotFound, "Repository non trovato.")
		return
	case errors.Is(err, access.ErrForbidden):
		textErr(w, http.StatusForbidden, "Permesso negato.")
		return
	case errors.Is(err, access.ErrArchived):
		textErr(w, http.StatusForbidden, "Repository archiviato: push non consentito.")
		return
	default:
		h.logger().Error("controllo dei permessi non riuscito", "err", err)
		textErr(w, http.StatusServiceUnavailable, "Servizio temporaneamente non disponibile.")
		return
	}

	// 3. Il binario git.
	sub := strings.TrimPrefix(service, "git-")
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
	if endpoint == "info/refs" {
		w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		banner := "# service=" + service + "\n"
		_, _ = io.WriteString(w, pktLine(banner)+"0000")
		_ = h.run(w, r, sub, dir, ref, nil, true)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/x-"+service+"-request" {
		textErr(w, http.StatusUnsupportedMediaType, "Content-Type non valido.")
		return
	}
	var body io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			textErr(w, http.StatusBadRequest, "Corpo gzip non valido.")
			return
		}
		defer func() { _ = zr.Close() }()
		body = zr
	}
	w.Header().Set("Content-Type", "application/x-"+service+"-result")
	w.WriteHeader(http.StatusOK)
	var push *pushevent.Push
	if write {
		push = h.Events.Begin(r.Context(), pushevent.TargetOf(dir, owner, name, ref), p)
	}
	if h.run(w, r, sub, dir, ref, body, false) {
		push.Done()
	}
}

func (h *Handler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func (h *Handler) challenge(w http.ResponseWriter) {
	realm := h.Realm
	if realm == "" {
		realm = "GitStack"
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
	textErr(w, http.StatusUnauthorized, "Autenticazione richiesta: usa un token personale come password.")
}

// run esegue git upload-pack/receive-pack in modalità stateless-rpc e
// scrive l'uscita nella risposta (già con gli header inviati). Ritorna true
// se git è finito con successo.
func (h *Handler) run(w http.ResponseWriter, r *http.Request, sub, dir string, ref access.RepoRef, stdin io.Reader, advertise bool) bool {
	var args []string
	if sub == "receive-pack" {
		args = h.Rules.GitArgs()
	}
	args = append(args, sub, "--stateless-rpc")
	if advertise {
		args = append(args, "--advertise-refs")
	}
	args = append(args, dir)
	bin := h.GitBin
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(r.Context(), bin, args...)
	cmd.Env = append(cleanEnv(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	if sub == "receive-pack" {
		cmd.Env = append(cmd.Env, h.Rules.Env(receiverules.ProtectedBranch(ref))...)
	}
	if gp := r.Header.Get("Git-Protocol"); gp != "" && safeProtocol(gp) {
		cmd.Env = append(cmd.Env, "GIT_PROTOCOL="+gp)
	}
	rc := http.NewResponseController(w)
	if stdin != nil {
		// La risposta parte mentre il corpo della richiesta è ancora in lettura.
		_ = rc.EnableFullDuplex()
	}
	cmd.Stdin = stdin
	cmd.Stdout = &flushWriter{w: w, rc: rc}
	var stderr strings.Builder
	cmd.Stderr = &limitedBuilder{b: &stderr, max: 4096}
	err := cmd.Run()
	if err != nil && r.Context().Err() == nil {
		h.logger().Warn("git ha terminato con errore", "service", sub, "err", err, "stderr", strings.TrimSpace(stderr.String()))
	}
	return err == nil
}

var protoRe = regexp.MustCompile(`^[A-Za-z0-9=:_.,-]{1,128}$`)

func safeProtocol(s string) bool { return protoRe.MatchString(s) }

func pktLine(s string) string {
	const hex = "0123456789abcdef"
	n := len(s) + 4
	return string([]byte{hex[(n>>12)&0xf], hex[(n>>8)&0xf], hex[(n>>4)&0xf], hex[n&0xf]}) + s
}

// basicPassword estrae la password dal Basic auth; l'utente è ignorato.
func basicPassword(r *http.Request) (string, bool) {
	_, pw, ok := r.BasicAuth()
	if !ok || pw == "" {
		return "", false
	}
	return pw, true
}

func textErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, msg+"\n")
}

func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// flushWriter fa il flush a ogni scrittura: il protocollo è interattivo.
type flushWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	_ = f.rc.Flush()
	return n, err
}

type limitedBuilder struct {
	b   *strings.Builder
	max int
}

func (l *limitedBuilder) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

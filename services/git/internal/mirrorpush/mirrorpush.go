// Package mirrorpush è l'esecutore dei mirror in push (M-04/V8, GIT-179): spinge
// il branch principale e i tag di un repo su un remote HTTPS, MAI con un push
// forzato.
//
// Garanzie, tutte coperte da test:
//
//   - Niente force. Gli argomenti sono fissi: `push --porcelain <url>
//     refs/heads/<main>:refs/heads/<main> refs/tags/*:refs/tags/*`, senza
//     --force, senza `+` nei refspec, senza cancellazioni di ref (nessun
//     refspec con sorgente vuota). Se la destinazione ha un branch avanti o un
//     tag diverso, git rifiuta e l'esito è «diverged»: nessuna sovrascrittura.
//   - La credenziale non passa MAI da argv né dall'URL (che non può avere
//     userinfo): solo dall'ambiente del processo (GIT_CONFIG_COUNT con
//     http.extraHeader). Lo stderr di git è ripulito da token e intestazione
//     prima di uscire da questo pacchetto.
//   - Il DNS è fissato: core risolve l'host, controlla l'IP con pkg/egress e
//     passa l'IP scelto; git lo usa con http.curloptResolve, quindi un
//     cambio di risposta DNS fra controllo e push non sposta la connessione.
//     I redirect non si seguono (http.followRedirects=false) e nessun proxy
//     dell'ambiente è usato.
//   - Un solo push alla volta per (repo, destinazione): un secondo si rifiuta
//     con ErrBusy.
package mirrorpush

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// DefaultTimeout è il tempo massimo di un push.
const DefaultTimeout = 2 * time.Minute

// Errori di validazione dell'input e di concorrenza.
var (
	ErrInvalid = errors.New("mirrorpush: richiesta non valida")
	ErrBusy    = errors.New("mirrorpush: un push per questo mirror è già in corso")
)

// Stati di un ref nell'esito.
const (
	StatusOK       = "pushed"
	StatusUpToDate = "up-to-date"
	StatusRejected = "rejected"
	StatusError    = "error"
)

// Input è una richiesta di push.
type Input struct {
	// URL di destinazione: https, senza userinfo.
	URL      string
	Username string
	Token    string
	// IP scelto da core dopo il controllo di egress; fissa la connessione.
	IP string
	// DefaultBranch è il branch principale del repo.
	DefaultBranch string
}

// RefResult è l'esito per ref.
type RefResult struct {
	Ref    string `json:"ref"`
	Status string `json:"status"`
	// SHA locale del ref (valorizzato per ok e up-to-date).
	SHA string `json:"sha,omitempty"`
	// Reason: il motivo del rifiuto o dell'errore, es. non-fast-forward.
	Reason string `json:"reason,omitempty"`
}

// Result è l'esito di un push.
type Result struct {
	// OK: nessun ref rifiutato e git è uscito con successo.
	OK bool `json:"ok"`
	// Diverged: almeno un ref rifiutato perché la destinazione ha una storia
	// diversa (non-fast-forward, fetch first, tag già esistente e diverso).
	Diverged bool        `json:"diverged"`
	Refs     []RefResult `json:"refs"`
	// Error: testo ripulito da credenziali, per last_error.
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// Service esegue i push.
type Service struct {
	Run *gitrun.Runner
	// Dir risolve l'id del repo nella sua cartella.
	Dir func(id string) (string, error)
	// Timeout del singolo push; zero = DefaultTimeout.
	Timeout time.Duration
	// ExtraEnv si aggiunge all'ambiente di git: nei test, GIT_SSL_CAINFO per
	// fidarsi della CA del server di prova. In produzione è vuoto.
	ExtraEnv []string

	mu   sync.Mutex
	busy map[string]bool
}

func (s *Service) acquire(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy == nil {
		s.busy = map[string]bool{}
	}
	if s.busy[key] {
		return false
	}
	s.busy[key] = true
	return true
}

func (s *Service) release(key string) {
	s.mu.Lock()
	delete(s.busy, key)
	s.mu.Unlock()
}

// Push spinge branch principale e tag del repo.
func (s *Service) Push(ctx context.Context, repoID string, in Input) (Result, error) {
	u, ip, err := validate(in)
	if err != nil {
		return Result{}, err
	}
	dir, err := s.Dir(repoID)
	if err != nil {
		return Result{}, err
	}
	key := repoID + "\x00" + in.URL
	if !s.acquire(key) {
		return Result{}, ErrBusy
	}
	defer s.release(key)

	start := time.Now()
	local, err := s.localRefs(ctx, dir, in.DefaultBranch)
	if err != nil {
		return Result{}, err
	}
	res := Result{Refs: []RefResult{}}
	if len(local.specs) == 0 {
		res.OK = true // niente da spingere: repo vuoto
		return res, nil
	}

	args := buildArgs(u, ip, in.URL, local.specs)
	out, runErr := s.Run.RunEnv(ctx, dir, s.timeout(), s.env(in), args...)
	res.DurationMs = time.Since(start).Milliseconds()
	redact := redactor(in)
	if runErr != nil {
		res.Error = redact(runErr.Error())
		if errors.Is(runErr, context.DeadlineExceeded) {
			res.Error = "timeout del push"
		}
		return res, nil
	}
	lines := ParsePorcelain(string(out.Stdout))
	for _, l := range lines {
		rr := RefResult{Ref: l.To, Status: l.Status, Reason: l.Reason}
		if rr.Status == StatusOK || rr.Status == StatusUpToDate {
			rr.SHA = local.sha[l.To]
		}
		if l.Status == StatusRejected && l.Diverged() {
			res.Diverged = true
		}
		res.Refs = append(res.Refs, rr)
	}
	rejected := 0
	for _, r := range res.Refs {
		if r.Status == StatusRejected || r.Status == StatusError {
			rejected++
		}
	}
	res.OK = out.ExitCode == 0 && rejected == 0
	if !res.OK {
		res.Error = failureText(res, redact(string(out.Stderr)), out.ExitCode)
	}
	return res, nil
}

func (s *Service) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

// env: la credenziale e le impostazioni di rete passano solo di qui.
func (s *Service) env(in Input) []string {
	cred := base64.StdEncoding.EncodeToString([]byte(in.Username + ":" + in.Token))
	env := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+cred,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		// Nessun proxy dell'ambiente: bypasserebbe l'IP fissato.
		"NO_PROXY=*", "no_proxy=*",
		"HTTP_PROXY=", "http_proxy=", "HTTPS_PROXY=", "https_proxy=", "ALL_PROXY=", "all_proxy=",
	}
	return append(env, s.ExtraEnv...)
}

// buildArgs è l'argv completo (dopo --git-dir) del push: opzioni -c, comando e
// refspec. Niente credenziali, niente --force, nessun `+`.
func buildArgs(u *url.URL, ip netip.Addr, rawURL string, specs []string) []string {
	args := pushArgs(u, ip)
	args = append(args, "push", "--porcelain", rawURL)
	return append(args, specs...)
}

// pushArgs sono le opzioni -c: tutto ciò che irrigidisce il trasporto.
func pushArgs(u *url.URL, ip netip.Addr) []string {
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	addr := ip.String()
	if ip.Is6() {
		addr = "[" + addr + "]"
	}
	return []string{
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "http.followRedirects=false",
		"-c", "http.proxy=",
		"-c", "credential.helper=",
		"-c", "http.curloptResolve=" + host + ":" + port + ":" + addr,
	}
}

func validate(in Input) (*url.URL, netip.Addr, error) {
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, netip.Addr{}, fmt.Errorf("%w: url deve essere https", ErrInvalid)
	}
	if u.User != nil {
		return nil, netip.Addr{}, fmt.Errorf("%w: niente credenziali nell'url", ErrInvalid)
	}
	if strings.ContainsAny(in.URL, " \t\r\n") || strings.HasPrefix(in.URL, "-") {
		return nil, netip.Addr{}, fmt.Errorf("%w: url non valido", ErrInvalid)
	}
	if in.Token == "" || in.Username == "" {
		return nil, netip.Addr{}, fmt.Errorf("%w: servono utente e token", ErrInvalid)
	}
	if strings.ContainsAny(in.Username+in.Token, "\r\n\x00") {
		return nil, netip.Addr{}, fmt.Errorf("%w: credenziale non valida", ErrInvalid)
	}
	ip, err := netip.ParseAddr(in.IP)
	if err != nil {
		return nil, netip.Addr{}, fmt.Errorf("%w: ip non valido", ErrInvalid)
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil && u.Port() != "" {
		return nil, netip.Addr{}, fmt.Errorf("%w: porta non valida", ErrInvalid)
	}
	if strings.HasPrefix(in.DefaultBranch, "+") || strings.Contains(in.DefaultBranch, "+") {
		return nil, netip.Addr{}, fmt.Errorf("%w: branch principale non valido", ErrInvalid)
	}
	if err := gitref.ValidateRef(in.DefaultBranch); err != nil {
		return nil, netip.Addr{}, fmt.Errorf("%w: branch principale: %v", ErrInvalid, err)
	}
	return u, ip, nil
}

type localState struct {
	specs []string
	sha   map[string]string // ref -> sha locale
}

// localRefs elenca ciò che si può spingere: il branch principale (se esiste)
// e i tag. I refspec sono senza `+` e senza sorgente vuota, per costruzione.
func (s *Service) localRefs(ctx context.Context, dir, branch string) (localState, error) {
	st := localState{sha: map[string]string{}}
	out, err := s.Run.Output(ctx, dir, nil, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads/"+branch, "refs/tags/")
	if err != nil {
		return st, err
	}
	hasTags := false
	for _, line := range strings.Split(string(out), "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if ref == "refs/heads/"+branch {
			st.specs = append(st.specs, ref+":"+ref)
			st.sha[ref] = sha
		} else if strings.HasPrefix(ref, "refs/tags/") {
			hasTags = true
			st.sha[ref] = sha
		}
	}
	if hasTags {
		st.specs = append(st.specs, "refs/tags/*:refs/tags/*")
	}
	return st, nil
}

func failureText(res Result, stderr string, exit int) string {
	var parts []string
	for _, r := range res.Refs {
		if r.Status == StatusRejected || r.Status == StatusError {
			p := r.Ref + ": " + r.Status
			if r.Reason != "" {
				p += " (" + r.Reason + ")"
			}
			parts = append(parts, p)
		}
	}
	text := strings.Join(parts, "; ")
	if text == "" {
		text = lastLines(stderr, 3)
	}
	if text == "" {
		text = fmt.Sprintf("git push è uscito con codice %d", exit)
	}
	if len(text) > 1000 {
		text = text[:1000]
	}
	return text
}

func lastLines(s string, n int) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			keep = append(keep, l)
		}
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, " | ")
}

// redactor toglie da un testo il token, la credenziale base64 e ogni
// intestazione Authorization.
func redactor(in Input) func(string) string {
	cred := base64.StdEncoding.EncodeToString([]byte(in.Username + ":" + in.Token))
	return func(s string) string {
		for _, secret := range []string{in.Token, cred, "Authorization: Basic " + cred} {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "***")
			}
		}
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			if strings.Contains(strings.ToLower(l), "authorization:") {
				lines[i] = "[intestazione rimossa]"
			}
		}
		return string(bytes.ToValidUTF8([]byte(strings.Join(lines, "\n")), []byte("?")))
	}
}

// Redact è esportata per i test e per chi logga.
func Redact(in Input, s string) string { return redactor(in)(s) }

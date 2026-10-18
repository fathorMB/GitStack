// Package mirrorpull è l'esecutore dei mirror in pull (M-09/A2, GIT-196): aggiorna
// un repo nudo da un'origine HTTPS esterna in modo prudente, senza mai
// sovrascrivere né cancellare niente.
//
// Regole di prodotto (brain [c_4e8670cfd950d497]), tutte coperte da test:
//
//   - MP1. Tutti i branch e i tag si scaricano in uno spazio temporaneo
//     (refs/mirror-incoming/), poi i ref veri si aggiornano solo se il ref è
//     nuovo o il branch avanza in fast-forward. Un tag già presente con un altro
//     commit, un branch riscritto (o rimasto indietro) e un ref cancellato
//     nell'origine restano com'erano e tornano nell'elenco «diverged»; gli
//     altri ref si aggiornano comunque. Mai --force, mai --prune, mai `+` nei
//     refspec, nemmeno verso lo spazio temporaneo (che si svuota prima).
//   - MP5. Solo https:// senza userinfo; l'IP lo sceglie core dopo pkg/egress e
//     git lo usa con http.curloptResolve (stesse protezioni del push: pacchetto
//     mirrorpush). Le CA private passano da GIT_SSL_CAINFO (ExtraEnv).
//   - MP3. La credenziale passa solo dall'ambiente del processo git, mai da
//     argv né dall'URL; ogni testo che esce dal pacchetto è ripulito.
//   - Un solo fetch alla volta per repo (ErrBusy), con timeout.
//   - Dopo l'aggiornamento il Notifier pubblica git.push per i ref cambiati,
//     con l'autore indicato da core (il «mirror»).
//   - Repo vuoto: il branch principale diventa quello di HEAD dell'origine.
package mirrorpull

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/mirrorpush"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
)

// DefaultTimeout è il tempo massimo del fetch: un'origine grande è più lenta
// di un push di soli tag.
const DefaultTimeout = 10 * time.Minute

// IncomingNS è lo spazio temporaneo dei ref scaricati.
const IncomingNS = "refs/mirror-incoming/"

// Errori di validazione e concorrenza.
var (
	ErrInvalid = mirrorpush.ErrInvalid
	ErrBusy    = errors.New("mirrorpull: un aggiornamento per questo repo è già in corso")
	// ErrForbidden: l'IP fissato è una destinazione vietata (rifiutata prima di ogni connessione).
	ErrForbidden = errors.New("mirrorpull: destinazione vietata")
)

// Stati di un ref nell'esito.
const (
	StatusCreated  = "created"
	StatusUpdated  = "updated"
	StatusUpToDate = "up-to-date"
	StatusDiverged = "diverged"
	StatusError    = "error"
)

// Motivi di divergenza.
const (
	ReasonNonFastForward = "non-fast-forward"
	ReasonTagMoved       = "tag-moved"
	ReasonDeleted        = "deleted-at-origin"
	ReasonChangedLocally = "changed-locally"
)

// Input è una richiesta di aggiornamento.
type Input struct {
	// URL dell'origine: https, senza userinfo.
	URL string
	// Username e Token: entrambi o nessuno (origine pubblica).
	Username string
	Token    string
	// IP scelto da core dopo il controllo di egress.
	IP string
	// DefaultBranch del repo e identità canonica per l'evento git.push.
	DefaultBranch string
	Owner, Name   string
	// Pusher è l'autore dell'evento: il «mirror» (id utente valido per core).
	PusherID       string
	PusherUsername string
}

// RefResult è l'esito per ref.
type RefResult struct {
	Ref    string `json:"ref"`
	Status string `json:"status"`
	// SHA locale dopo l'aggiornamento (o quello rimasto, se diverged).
	SHA string `json:"sha,omitempty"`
	// Origin: lo sha dell'origine, se diverso da quello locale.
	Origin string `json:"origin,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Result è l'esito di un aggiornamento.
type Result struct {
	// OK: fetch riuscito e nessun ref divergente.
	OK       bool        `json:"ok"`
	Diverged bool        `json:"diverged"`
	Refs     []RefResult `json:"refs"`
	// HeadBranch: valorizzato solo se il repo era vuoto e il branch principale
	// è stato impostato su quello di HEAD dell'origine (MP8).
	HeadBranch string `json:"headBranch,omitempty"`
	// Changed: numero di ref creati o aggiornati.
	Changed    int    `json:"changed"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// Service esegue gli aggiornamenti.
type Service struct {
	Run *gitrun.Runner
	// Dir risolve l'id del repo nella sua cartella.
	Dir func(id string) (string, error)
	// Events pubblica git.push; nil = nessun evento.
	Events *pushevent.Notifier
	// Timeout del fetch; zero = DefaultTimeout.
	Timeout time.Duration
	// MaxFetchBytes: tetto di crescita della cartella degli oggetti del repo in
	// un fetch (F18, V12.1.1: un'origine non può riempire il volume git). Oltre,
	// il fetch è interrotto e gli oggetti scaricati sono eliminati. 0 = nessun tetto.
	MaxFetchBytes int64
	// CheckIP è l'ultima difesa sull'IP fissato da core (che applica la policy
	// completa di pkg/egress, con le liste e i CIDR del cluster): nil = DefaultCheckIP,
	// che rifiuta loopback, link-local, non specificato e multicast.
	CheckIP func(netip.Addr) error
	// ExtraEnv: GIT_SSL_CAINFO per le CA private; nei test, la CA del server.
	ExtraEnv []string
	// Proxy sceglie il proxy aziendale per l'origine (N9; in produzione
	// http.ProxyFromEnvironment). nil = mai un proxy. Con un proxy git risolve
	// tramite il proxy e l'IP fissato non vale: core ha già giudicato ogni
	// indirizzo dell'origine.
	Proxy func(*http.Request) (*url.URL, error)

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

func (s *Service) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

// DefaultCheckIP rifiuta gli indirizzi che un mirror non deve mai raggiungere,
// qualunque sia la configurazione: loopback, link-local, non specificato,
// multicast.
func DefaultCheckIP(ip netip.Addr) error {
	switch {
	case ip.IsLoopback(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsUnspecified(), ip.IsMulticast():
		return fmt.Errorf("indirizzo %s non ammesso", ip)
	}
	return nil
}

func validate(in Input) (*url.URL, netip.Addr, error) {
	u, ip, err := mirrorpush.ValidateRemote(in.URL, in.IP)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	if (in.Username == "") != (in.Token == "") {
		return nil, netip.Addr{}, fmt.Errorf("%w: servono utente e token insieme", ErrInvalid)
	}
	if strings.ContainsAny(in.Username+in.Token, "\r\n\x00") {
		return nil, netip.Addr{}, fmt.Errorf("%w: credenziale non valida", ErrInvalid)
	}
	if in.Owner == "" || in.Name == "" || in.PusherID == "" || in.PusherUsername == "" {
		return nil, netip.Addr{}, fmt.Errorf("%w: servono owner, name e pusher", ErrInvalid)
	}
	if in.DefaultBranch != "" {
		if strings.Contains(in.DefaultBranch, "+") {
			return nil, netip.Addr{}, fmt.Errorf("%w: branch principale non valido", ErrInvalid)
		}
		if err := gitref.ValidateRef(in.DefaultBranch); err != nil {
			return nil, netip.Addr{}, fmt.Errorf("%w: branch principale: %v", ErrInvalid, err)
		}
	}
	return u, ip, nil
}

// fetchArgs è l'argv completo (dopo --git-dir) del fetch. Niente credenziali,
// niente --force, niente --prune, niente `+`: i refspec scrivono solo nello
// spazio temporaneo, che è vuoto.
func fetchArgs(u *url.URL, ip netip.Addr, rawURL string) []string {
	return fetchArgsProxy(u, ip, rawURL, false)
}

// fetchArgsProxy è fetchArgs, con o senza proxy (vedi TransportArgsProxy).
func fetchArgsProxy(u *url.URL, ip netip.Addr, rawURL string, proxied bool) []string {
	args := mirrorpush.TransportArgsProxy(u, ip, proxied)
	args = append(args,
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
		"fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--no-progress",
		rawURL,
		"refs/heads/*:"+IncomingNS+"heads/*",
		"refs/tags/*:"+IncomingNS+"tags/*",
	)
	return args
}

// lsRemoteHeadArgs legge a quale branch punta HEAD dell'origine.
func lsRemoteHeadArgs(u *url.URL, ip netip.Addr, rawURL string, proxied bool) []string {
	return append(mirrorpush.TransportArgsProxy(u, ip, proxied), "ls-remote", "--symref", rawURL, "HEAD")
}

// Pull aggiorna il repo dall'origine.
func (s *Service) Pull(ctx context.Context, repoID string, in Input) (Result, error) {
	u, ip, err := validate(in)
	if err != nil {
		return Result{}, err
	}
	check := s.CheckIP
	if check == nil {
		check = DefaultCheckIP
	}
	if err := check(ip.Unmap()); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrForbidden, err)
	}
	dir, err := s.Dir(repoID)
	if err != nil {
		return Result{}, err
	}
	proxy := mirrorpush.ProxyFor(s.Proxy, u)
	env, err := mirrorpush.NetEnvProxy(in.Username, in.Token, s.ExtraEnv, proxy)
	if err != nil {
		return Result{}, err
	}
	if !s.acquire(repoID) {
		return Result{}, ErrBusy
	}
	defer s.release(repoID)

	start := time.Now()
	redact := mirrorpush.Redactor(in.Username, in.Token)
	if proxy != nil {
		if pw, ok := proxy.User.Password(); ok && pw != "" {
			inner := redact
			redact = func(s string) string { return inner(strings.ReplaceAll(s, pw, "***")) }
		}
	}
	res := Result{Refs: []RefResult{}}

	local, err := pushevent.Take(ctx, s.Run, dir)
	if err != nil {
		return Result{}, err
	}
	event := s.Events.BeginAs(ctx, pushevent.Target{
		Dir: dir, RepoID: repoID, Owner: in.Owner, Name: in.Name, DefaultBranch: in.DefaultBranch,
	}, gitpush.Pusher{ID: in.PusherID, Username: in.PusherUsername, Type: gitpush.PusherHuman})

	// Lo spazio temporaneo si svuota prima e dopo: i refspec non hanno `+`,
	// quindi deve partire vuoto, e non deve tenere vivi oggetti di ref scartati.
	if err := s.clearIncoming(ctx, dir); err != nil {
		return Result{}, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.clearIncoming(c, dir)
	}()

	deadline := time.Now().Add(s.timeout())
	fetchCtx, stopFetch := context.WithCancel(ctx)
	capped := s.watchGrowth(fetchCtx, stopFetch, dir)
	out, runErr := s.Run.RunEnv(fetchCtx, dir, s.timeout(), env, fetchArgsProxy(u, ip, in.URL, proxy != nil)...)
	stopFetch()
	if capped() {
		return s.capExceeded(ctx, dir, start, res), nil
	}
	if runErr != nil {
		res.Error = redact(runErr.Error())
		if errors.Is(runErr, context.DeadlineExceeded) {
			res.Error = "timeout del fetch"
		}
		res.DurationMs = time.Since(start).Milliseconds()
		return res, nil
	}
	if out.ExitCode != 0 {
		res.Error = failureText(redact(string(out.Stderr)), out.ExitCode)
		res.DurationMs = time.Since(start).Milliseconds()
		return res, nil
	}

	incoming, err := s.listIncoming(ctx, dir)
	if err != nil {
		return Result{}, err
	}
	s.apply(ctx, dir, local, incoming, &res)

	// MP8: repo vuoto, il branch principale diventa quello di HEAD dell'origine.
	defaultBranch := in.DefaultBranch
	if !hasBranch(local) && createdBranch(res.Refs) {
		remaining := time.Until(deadline)
		if remaining < 10*time.Second {
			remaining = 10 * time.Second
		}
		if head := s.originHead(ctx, dir, remaining, env, u, ip, in.URL, proxy != nil, res.Refs); head != "" {
			if _, err := s.Run.Output(ctx, dir, nil, "symbolic-ref", "HEAD", "refs/heads/"+head); err == nil {
				res.HeadBranch = head
				defaultBranch = head
			}
		}
	}

	if res.Changed > 0 {
		event.SetDefaultBranch(defaultBranch)
		event.Done()
	}
	res.Diverged = anyStatus(res.Refs, StatusDiverged)
	res.OK = !res.Diverged && !anyStatus(res.Refs, StatusError)
	if !res.OK {
		res.Error = divergedText(res.Refs)
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

// objectsSize è la dimensione dei file sotto <dir>/objects (compresi i
// tmp_pack in scrittura).
func objectsSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(filepath.Join(dir, "objects"), func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// capPollInterval è ogni quanto si misura la crescita durante il fetch.
const capPollInterval = 100 * time.Millisecond

// watchGrowth misura la crescita degli oggetti del repo mentre il fetch gira e
// lo interrompe (stop) quando supera MaxFetchBytes. Ritorna una funzione che
// ferma il controllo e dice se il tetto è stato superato, anche con una
// misura finale (un fetch più veloce di un intervallo di controllo). Con tetto
// 0 non fa niente.
func (s *Service) watchGrowth(ctx context.Context, stop context.CancelFunc, dir string) func() bool {
	limit := s.MaxFetchBytes
	if limit <= 0 {
		return func() bool { return false }
	}
	base := objectsSize(dir)
	var exceeded atomic.Bool
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(capPollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				if objectsSize(dir)-base > limit {
					exceeded.Store(true)
					stop()
					return
				}
			}
		}
	}()
	return func() bool {
		close(done)
		<-finished
		return exceeded.Load() || objectsSize(dir)-base > limit
	}
}

// capExceeded chiude un aggiornamento fermato dal tetto: nessun ref cambia, gli
// oggetti scaricati (ora senza ref) e i pacchetti incompleti si eliminano.
func (s *Service) capExceeded(ctx context.Context, dir string, start time.Time, res Result) Result {
	_ = s.clearIncoming(ctx, dir)
	tmp, _ := filepath.Glob(filepath.Join(dir, "objects", "pack", "tmp_pack_*"))
	for _, f := range tmp {
		_ = os.Remove(f)
	}
	_, _ = s.Run.Output(ctx, dir, nil, "gc", "--prune=now", "--quiet")
	res.Error = fmt.Sprintf("il fetch supera il tetto di %s per aggiornamento (GITSTACK_GIT_MIRROR_MAX_FETCH_SIZE): interrotto, nessun ref aggiornato", humanBytes(s.MaxFetchBytes))
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GB", n>>30)
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d byte", n)
}

// apply decide ref per ref e scrive solo creazioni e fast-forward.
func (s *Service) apply(ctx context.Context, dir string, local, incoming pushevent.Snapshot, res *Result) {
	names := map[string]struct{}{}
	for n := range local {
		names[n] = struct{}{}
	}
	for n := range incoming {
		names[n] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	for _, name := range sorted {
		old, hadLocal := local[name]
		neu, hadIn := incoming[name]
		rr := RefResult{Ref: name}
		switch {
		case hadLocal && !hadIn:
			rr.Status, rr.Reason, rr.SHA = StatusDiverged, ReasonDeleted, old
		case !hadLocal:
			rr = s.update(ctx, dir, name, neu, zeroOf(neu), StatusCreated)
		case old == neu:
			rr.Status, rr.SHA = StatusUpToDate, old
		case strings.HasPrefix(name, "refs/tags/"):
			rr.Status, rr.Reason, rr.SHA, rr.Origin = StatusDiverged, ReasonTagMoved, old, neu
		default:
			if s.isAncestor(ctx, dir, old, neu) {
				rr = s.update(ctx, dir, name, neu, old, StatusUpdated)
			} else {
				rr.Status, rr.Reason, rr.SHA, rr.Origin = StatusDiverged, ReasonNonFastForward, old, neu
			}
		}
		if rr.Status == StatusCreated || rr.Status == StatusUpdated {
			res.Changed++
		}
		res.Refs = append(res.Refs, rr)
	}
}

// update scrive il ref con il controllo del valore precedente: se nel
// frattempo qualcuno l'ha cambiato (un push) git rifiuta e non si sovrascrive.
func (s *Service) update(ctx context.Context, dir, ref, neu, old, ok string) RefResult {
	_, err := s.Run.Output(ctx, dir, nil, "update-ref", "-m", "mirror pull", ref, neu, old)
	if err != nil {
		return RefResult{Ref: ref, Status: StatusDiverged, Reason: ReasonChangedLocally, Origin: neu}
	}
	return RefResult{Ref: ref, Status: ok, SHA: neu}
}

func (s *Service) isAncestor(ctx context.Context, dir, old, neu string) bool {
	_, err := s.Run.Output(ctx, dir, nil, "merge-base", "--is-ancestor", old, neu)
	return err == nil
}

// listIncoming ritorna i ref scaricati con il nome che avranno nel repo.
func (s *Service) listIncoming(ctx context.Context, dir string) (pushevent.Snapshot, error) {
	out, err := s.Run.Output(ctx, dir, nil, "for-each-ref", "--format=%(objectname) %(refname)", IncomingNS)
	if err != nil {
		return nil, err
	}
	snap := pushevent.Snapshot{}
	for _, line := range strings.Split(string(out), "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		rest := strings.TrimPrefix(ref, IncomingNS)
		switch {
		case strings.HasPrefix(rest, "heads/"):
			snap["refs/heads/"+strings.TrimPrefix(rest, "heads/")] = sha
		case strings.HasPrefix(rest, "tags/"):
			snap["refs/tags/"+strings.TrimPrefix(rest, "tags/")] = sha
		}
	}
	return snap, nil
}

// clearIncoming cancella ogni ref dello spazio temporaneo (solo quello).
func (s *Service) clearIncoming(ctx context.Context, dir string) error {
	out, err := s.Run.Output(ctx, dir, nil, "for-each-ref", "--format=%(refname)", IncomingNS)
	if err != nil {
		return err
	}
	var in strings.Builder
	for _, ref := range strings.Split(string(out), "\n") {
		ref = strings.TrimSpace(ref)
		if strings.HasPrefix(ref, IncomingNS) {
			in.WriteString("delete " + ref + "\n")
		}
	}
	if in.Len() == 0 {
		return nil
	}
	_, err = s.Run.Output(ctx, dir, strings.NewReader(in.String()), "update-ref", "--stdin")
	return err
}

// originHead legge il branch di HEAD dell'origine; vuoto se non lo sa dire o
// se non è uno dei branch appena creati.
func (s *Service) originHead(ctx context.Context, dir string, timeout time.Duration, env []string, u *url.URL, ip netip.Addr, rawURL string, proxied bool, refs []RefResult) string {
	out, err := s.Run.RunEnv(ctx, dir, timeout, env, lsRemoteHeadArgs(u, ip, rawURL, proxied)...)
	if err != nil || out.ExitCode != 0 {
		return ""
	}
	for _, line := range strings.Split(string(out.Stdout), "\n") {
		target, ok := strings.CutPrefix(strings.TrimSpace(line), "ref: ")
		if !ok {
			continue
		}
		ref, _, _ := strings.Cut(target, "\t")
		branch, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok || gitref.ValidateRef(branch) != nil {
			continue
		}
		for _, r := range refs {
			if r.Ref == ref && r.Status == StatusCreated {
				return branch
			}
		}
	}
	return ""
}

func hasBranch(snap pushevent.Snapshot) bool {
	for n := range snap {
		if strings.HasPrefix(n, "refs/heads/") {
			return true
		}
	}
	return false
}

func createdBranch(refs []RefResult) bool {
	for _, r := range refs {
		if r.Status == StatusCreated && strings.HasPrefix(r.Ref, "refs/heads/") {
			return true
		}
	}
	return false
}

func anyStatus(refs []RefResult, status string) bool {
	for _, r := range refs {
		if r.Status == status {
			return true
		}
	}
	return false
}

func zeroOf(sha string) string { return strings.Repeat("0", len(sha)) }

func divergedText(refs []RefResult) string {
	var parts []string
	for _, r := range refs {
		if r.Status == StatusDiverged || r.Status == StatusError {
			parts = append(parts, r.Ref+" ("+r.Reason+")")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	more := ""
	if len(parts) > 5 {
		more = fmt.Sprintf(" e altri %d", len(parts)-5)
		parts = parts[:5]
	}
	return "ref divergenti, lasciati come sono: " + strings.Join(parts, "; ") + more
}

func failureText(stderr string, exit int) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			keep = append(keep, l)
		}
	}
	if len(keep) > 3 {
		keep = keep[len(keep)-3:]
	}
	text := strings.Join(keep, " | ")
	if text == "" {
		text = fmt.Sprintf("git fetch è uscito con codice %d", exit)
	}
	if len(text) > 1000 {
		text = text[:1000]
	}
	return text
}

// Package cirunner è il runner del pod di un job (ADR 0001 §1 e §4): parla runner.v1 col
// ci-gateway, scarica il codice del job, esegue un solo job con act in modalità host
// (act -j <job>) e riporta log, passi ed esito. Un pod = un job: finito il job, Run ritorna.
//
// Il runner usa solo l'indirizzo del gateway: il pod non ha altra rete verso il cluster.
package cirunner

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"code.gitea.io/actions-proto-go/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Header e percorsi del ci-gateway (stessi nomi di internal/cigateway).
const (
	headerRunnerToken = "x-runner-token"
	headerRunnerName  = "x-runner-name"
	archivePath       = "/ci/v1/archive"
)

// ErrNoTask: nessun job in coda entro FetchTimeout.
var ErrNoTask = errors.New("nessun job in coda")

// Config configura Run.
type Config struct {
	// GatewayURL è l'indirizzo del ci-gateway, es. http://core:8081.
	GatewayURL string
	// Secret è la credenziale dei runner interni (solo per FetchTask).
	Secret string
	// Labels sono le etichette di un runner esterno: act le esegue in modalità host (-P etichetta=-self-hosted).
	Labels []string
	// Name compare come runner_name del job.
	Name string
	// ActBin è il binario di act (default "act"); ActArgs sono argomenti aggiuntivi prima di quelli fissi.
	ActBin  string
	ActArgs []string
	// ActionMirror e' l'URL base del mirror amministrato (http o https): se presente, le
	// action pubbliche dei passi si scaricano da li' e non da github.com (vedi MirrorActions).
	// Vuoto: https://github.com, scaricate comunque dal runner e senza il token del job.
	ActionMirror string
	// GitBin e' il binario git per scaricare le action dal mirror (default "git").
	GitBin string
	// WorkDir contiene workspace/ (il codice) e workflow/ (il file del workflow).
	WorkDir string
	// FetchTimeout: quanto aspettare un job (default 2 minuti).
	FetchTimeout time.Duration
	// FlushEvery: ogni quanto si inviano log e stato dei passi (default 500 ms).
	FlushEvery time.Duration
	HTTP       *http.Client
	Log        *slog.Logger
	// ActEnv sono variabili aggiuntive per act (oltre a quelle del processo).
	ActEnv []string
}

// Result è l'esito di un job eseguito e riportato.
type Result struct {
	TaskID     int64
	Job        string
	Conclusion runnerv1.Result
}

func (c *Config) defaults() {
	if c.HTTP == nil {
		c.HTTP = &http.Client{}
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
	if c.ActBin == "" {
		c.ActBin = "act"
	}
	if c.FetchTimeout <= 0 {
		c.FetchTimeout = 2 * time.Minute
	}
	if c.FlushEvery <= 0 {
		c.FlushEvery = 500 * time.Millisecond
	}
	if c.Name == "" {
		c.Name, _ = os.Hostname()
	}
	c.GatewayURL = strings.TrimRight(c.GatewayURL, "/")
}

func withToken[T any](msg *T, token string) *connect.Request[T] {
	r := connect.NewRequest(msg)
	r.Header().Set(headerRunnerToken, token)
	return r
}

// Run prende un job, lo esegue e ne riporta l'esito. Ritorna ErrNoTask se non ne arriva
// uno entro FetchTimeout. Un errore dopo la presa del job lo riporta come fallito, se
// il gateway è raggiungibile.
func Run(ctx context.Context, cfg Config) (Result, error) {
	cfg.defaults()
	if cfg.GatewayURL == "" || cfg.Secret == "" || cfg.WorkDir == "" {
		return Result{}, errors.New("servono l'indirizzo del gateway, il segreto del runner e la cartella di lavoro")
	}
	client := runnerv1connect.NewRunnerServiceClient(cfg.HTTP, cfg.GatewayURL)

	task, err := fetch(ctx, client, cfg)
	if err != nil {
		return Result{}, err
	}
	token := task.GetSecrets()["GITHUB_TOKEN"]
	cx := task.GetContext().AsMap()
	jobKey, _ := cx["job"].(string)
	event, _ := cx["event_name"].(string)
	res := Result{TaskID: task.GetId(), Job: jobKey}
	if token == "" || jobKey == "" {
		return res, errors.New("task senza token o senza job")
	}
	rep := &reporter{client: client, token: token, taskID: task.GetId(), log: cfg.Log}

	conclusion := runnerv1.Result_RESULT_FAILURE
	archiveURL, _ := cx["gitstack_archive_url"].(string)
	if archiveURL == "" {
		archiveURL = cfg.GatewayURL + archivePath
	}
	cfg.ActEnv = append(append([]string{}, cfg.ActEnv...), CacheEnv(archiveURL, token)...)
	// act non conosce il repo (il workspace e' un archivio senza .git) e sceglie da solo
	// GITHUB_REPOSITORY: i passi leggono owner/nome vero da qui (gitstack-build, GIT-371).
	if repo, _ := cx["repository"].(string); repo != "" {
		cfg.ActEnv = append(cfg.ActEnv, "GITSTACK_REPOSITORY="+repo)
	}
	// Lo sha e il ref del run: act li prende dal repo git della cartella (che qui non c'e') o
	// dal file evento, mai dall'ambiente, quindi sotto si scrivono il file evento e un .git
	// minimo. Nell'ambiente restano comunque, per chi li legge fuori da act (GIT-439).
	runSHA, _ := cx["sha"].(string)
	runRef, _ := cx["ref"].(string)
	if runSHA != "" {
		cfg.ActEnv = append(cfg.ActEnv, "GITHUB_SHA="+runSHA)
	}
	if runRef != "" {
		cfg.ActEnv = append(cfg.ActEnv, "GITHUB_REF="+runRef)
	}
	prepare := func() error {
		ws := filepath.Join(cfg.WorkDir, "workspace")
		if err := downloadArchive(ctx, cfg.HTTP, archiveURL, token, ws); err != nil {
			return fmt.Errorf("download del codice: %w", err)
		}
		if err := writeGitHead(ws, runSHA); err != nil {
			return fmt.Errorf("sha del run: %w", err)
		}
		if err := writeEventFile(filepath.Join(cfg.WorkDir, "workflow", eventFileName), cx); err != nil {
			return fmt.Errorf("file evento: %w", err)
		}
		wf := filepath.Join(cfg.WorkDir, "workflow", "job.yml")
		if err := os.MkdirAll(filepath.Dir(wf), 0o750); err != nil {
			return err
		}
		actionsBase := cfg.ActionMirror
		if strings.TrimSpace(actionsBase) == "" {
			actionsBase = "https://github.com"
		}
		payload, err := MirrorActions(ctx, task.GetWorkflowPayload(), actionsBase, cfg.GitBin,
			filepath.Join(cfg.WorkDir, "workspace", ".gitstack-actions"), append(withoutCIEnv(os.Environ()), "HOME="+cfg.WorkDir))
		if err != nil {
			return err
		}
		return os.WriteFile(wf, payload, 0o640)
	}
	if err := prepare(); err != nil {
		rep.line("errore: " + err.Error())
		_ = rep.flush(ctx, conclusion, true)
		return res, err
	}
	conclusion = runAct(ctx, cfg, rep, event, jobKey, token)
	res.Conclusion = conclusion
	if err := rep.flush(ctx, conclusion, true); err != nil {
		return res, fmt.Errorf("esito non riportato: %w", err)
	}
	return res, nil
}

func fetch(ctx context.Context, client runnerv1connect.RunnerServiceClient, cfg Config) (*runnerv1.Task, error) {
	deadline := time.Now().Add(cfg.FetchTimeout)
	for {
		req := withToken(&runnerv1.FetchTaskRequest{}, cfg.Secret)
		req.Header().Set(headerRunnerName, cfg.Name)
		res, err := client.FetchTask(ctx, req)
		switch {
		case err == nil && res.Msg.GetTask() != nil:
			return res.Msg.GetTask(), nil
		case err != nil && connect.CodeOf(err) == connect.CodeUnauthenticated:
			return nil, fmt.Errorf("credenziale del runner rifiutata: %w", err)
		case err != nil:
			cfg.Log.Warn("FetchTask non riuscita", "err", err)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, ErrNoTask
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

var (
	ansi       = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	stepStart  = regexp.MustCompile(`⭐\s+Run Main (.+)$`)
	stepResult = regexp.MustCompile(`(Success|Failure|Skipped)\s+-\s+Main .*\[[0-9.]+[^\]]*\]\s*$`)
)

// platformsFor sono le piattaforme di act: le ubuntu-* del pod; per un runner esterno (labels
// non vuote) anche «self-hosted» e le sue etichette normalizzate come sul server
// (minuscolo, senza «:schema», niente vuoti né doppioni). Una piattaforma che act non
// conosce fa saltare il job con uscita 0, cioè un job «verde» senza passi eseguiti.
func platformsFor(labels []string) []string {
	out := append([]string{}, actPlatforms...)
	if len(labels) == 0 {
		return out
	}
	return append(out, NormalizeLabels(append([]string{"self-hosted"}, labels...))...)
}

// NormalizeLabels è ci.NormalizeLabels (qui duplicata: il runner non importa il pacchetto del server).
func NormalizeLabels(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, l := range in {
		l = strings.ToLower(strings.TrimSpace(l))
		if i := strings.IndexByte(l, ':'); i >= 0 {
			l = l[:i]
		}
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// gitstack-build (GIT-371) è la label dei job di build: stesso esecutore in modalità host, ma il pod
// è quello del namespace di build. Senza il -P act salterebbe il job con uscita 0.
var actPlatforms = []string{"ubuntu-latest", "ubuntu-24.04", "ubuntu-22.04", "ubuntu-20.04", "gitstack-build"}

const eventFileName = "event.json"

var sha1Hex = regexp.MustCompile(`^[0-9a-f]{40}$`)

// writeGitHead da' al workspace (un archivio, senza .git) un repo git minimo con HEAD staccato
// sullo sha del run: e' da li' che act ricava github.sha e GITHUB_SHA. Senza, act li lascia
// vuoti (v0.2.89 non li legge ne' dal file evento ne' dall'ambiente). Uno sha non valido non
// scrive nulla; un .git gia' presente nell'archivio resta com'e'.
func writeGitHead(ws, sha string) error {
	if !sha1Hex.MatchString(sha) {
		return nil
	}
	dir := filepath.Join(ws, ".git")
	if _, err := os.Lstat(dir); err == nil {
		return nil
	}
	for _, d := range []string{"objects", filepath.Join("refs", "heads")} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte("[core]\n\trepositoryformatversion = 0\n"), 0o640); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "HEAD"), []byte(sha+"\n"), 0o640)
}

// writeEventFile scrive il payload dell'evento per `act -e`: gli input del run (workflow_dispatch,
// quelli risolti dal server con i default), il ref e lo sha, il repository e il numero della PR.
// I passi li vedono in github.event.*.
func writeEventFile(path string, cx map[string]any) error {
	ev := map[string]any{}
	if in, ok := cx["inputs"].(map[string]any); ok {
		ev["inputs"] = in
	}
	ref, _ := cx["ref"].(string)
	sha, _ := cx["sha"].(string)
	if ref != "" {
		ev["ref"] = ref
	}
	if sha != "" {
		ev["after"] = sha
		ev["head_commit"] = map[string]any{"id": sha}
	}
	if repo, _ := cx["repository"].(string); repo != "" {
		ev["repository"] = map[string]any{"full_name": repo}
	}
	if n, ok := cx["pull_number"].(float64); ok {
		ev["number"] = int64(n)
		ev["pull_request"] = map[string]any{"number": int64(n), "head": map[string]any{"sha": sha}}
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o640)
}

// runAct esegue `act <evento> -j <job>` in modalità host sul workspace e ne segue l'output.
func runAct(ctx context.Context, cfg Config, rep *reporter, event, jobKey, token string) runnerv1.Result {
	if event == "" {
		event = "push"
	}
	args := append([]string{}, cfg.ActArgs...)
	args = append(args, event, "-e", filepath.Join(cfg.WorkDir, "workflow", eventFileName),
		"-W", filepath.Join(cfg.WorkDir, "workflow", "job.yml"), "-j", jobKey)
	for _, p := range platformsFor(cfg.Labels) {
		args = append(args, "-P", p+"=-self-hosted")
	}
	args = append(args, "--pull=false", "--no-cache-server", "-s", "GITHUB_TOKEN")
	cmd := exec.CommandContext(ctx, cfg.ActBin, args...)
	cmd.Dir = filepath.Join(cfg.WorkDir, "workspace")
	// I passi del workflow ereditano l'ambiente di act: il segreto dei runner (e ogni
	// GITSTACK_CI_*) non deve arrivarci, o un workflow potrebbe prendere i job altrui.
	cmd.Env = append(append(withoutCIEnv(os.Environ()), cfg.ActEnv...), "GITHUB_TOKEN="+token, "NO_COLOR=1",
		// uid 10001 non ha una home e il filesystem radice è di sola lettura: act scrive la cache qui.
		"HOME="+cfg.WorkDir, "XDG_CACHE_HOME="+filepath.Join(cfg.WorkDir, ".cache"))
	out, err := cmd.StdoutPipe()
	if err != nil {
		rep.line("errore: " + err.Error())
		return runnerv1.Result_RESULT_FAILURE
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		rep.line("errore: avvio di act: " + err.Error())
		return runnerv1.Result_RESULT_FAILURE
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // invio periodico di log e passi
		defer wg.Done()
		t := time.NewTicker(cfg.FlushEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = rep.flush(ctx, runnerv1.Result_RESULT_UNSPECIFIED, false)
			}
		}
	}()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		rep.actLine(ansi.ReplaceAllString(sc.Text(), ""))
	}
	waitErr := cmd.Wait()
	close(stop)
	wg.Wait()
	if waitErr != nil {
		rep.line("act terminato con errore: " + waitErr.Error())
		return runnerv1.Result_RESULT_FAILURE
	}
	return runnerv1.Result_RESULT_SUCCESS
}

// reporter accumula righe di log e stato dei passi e li invia al gateway.
type reporter struct {
	client runnerv1connect.RunnerServiceClient
	token  string
	taskID int64
	log    *slog.Logger

	mu      sync.Mutex
	rows    []*runnerv1.LogRow // non ancora riconosciute dal gateway
	base    int64              // indice della prima riga di rows
	total   int64              // righe prodotte
	steps   []*runnerv1.StepState
	current *runnerv1.StepState
}

func (r *reporter) line(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appendLocked(s)
}

func (r *reporter) appendLocked(s string) {
	r.rows = append(r.rows, &runnerv1.LogRow{Time: timestamppb.Now(), Content: s})
	r.total++
	if r.current != nil {
		r.current.LogLength++
	}
}

func (r *reporter) actLine(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := stepStart.FindStringSubmatch(s); m != nil {
		r.closeCurrentLocked(runnerv1.Result_RESULT_UNSPECIFIED)
		r.current = &runnerv1.StepState{Id: int64(len(r.steps)), StartedAt: timestamppb.Now(), LogIndex: r.total}
		r.steps = append(r.steps, r.current)
	} else if m := stepResult.FindStringSubmatch(s); m != nil && r.current != nil {
		res := runnerv1.Result_RESULT_SUCCESS
		switch m[1] {
		case "Failure":
			res = runnerv1.Result_RESULT_FAILURE
		case "Skipped":
			res = runnerv1.Result_RESULT_SKIPPED
		}
		r.appendLocked(s)
		r.closeCurrentLocked(res)
		return
	}
	r.appendLocked(s)
}

func (r *reporter) closeCurrentLocked(res runnerv1.Result) {
	if r.current == nil {
		return
	}
	if res != runnerv1.Result_RESULT_UNSPECIFIED {
		r.current.Result = res
		r.current.StoppedAt = timestamppb.Now()
	}
	r.current = nil
}

// flush invia le righe in attesa e lo stato dei passi; con final=true anche l'esito del job
// (i passi ancora aperti prendono l'esito del job, o «annullato» se il job è fallito prima di finirli).
func (r *reporter) flush(ctx context.Context, result runnerv1.Result, final bool) error {
	r.mu.Lock()
	if final && r.current != nil {
		r.closeCurrentLocked(result)
	}
	rows := append([]*runnerv1.LogRow(nil), r.rows...)
	base := r.base
	steps := make([]*runnerv1.StepState, len(r.steps))
	for i, s := range r.steps {
		steps[i] = &runnerv1.StepState{Id: s.Id, Result: s.Result, StartedAt: s.StartedAt, StoppedAt: s.StoppedAt, LogIndex: s.LogIndex, LogLength: s.LogLength}
	}
	r.mu.Unlock()

	// Alla fine si ritenta: l'esito non deve perdersi per un errore passeggero.
	attempts := 1
	if final {
		attempts = 5
	}
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(i) * 500 * time.Millisecond):
			}
		}
		if err = r.send(ctx, base, rows, steps, result); err == nil {
			return nil
		}
		if connect.CodeOf(err) == connect.CodeUnauthenticated || connect.CodeOf(err) == connect.CodePermissionDenied {
			return err
		}
	}
	return err
}

func (r *reporter) send(ctx context.Context, base int64, rows []*runnerv1.LogRow, steps []*runnerv1.StepState, result runnerv1.Result) error {
	if len(rows) > 0 || result != runnerv1.Result_RESULT_UNSPECIFIED {
		for len(rows) > 0 {
			chunk := rows
			if len(chunk) > 1000 {
				chunk = chunk[:1000]
			}
			res, err := r.client.UpdateLog(ctx, withToken(&runnerv1.UpdateLogRequest{TaskId: r.taskID, Index: base, Rows: chunk}, r.token))
			if err != nil {
				return err
			}
			ack := res.Msg.GetAckIndex()
			if ack <= base { // il gateway non ha preso nulla: si ritenta dopo
				return fmt.Errorf("log non riconosciuti (ack %d)", ack)
			}
			n := int(ack - base)
			if n > len(rows) {
				n = len(rows)
			}
			rows, base = rows[n:], base+int64(n)
			r.mu.Lock()
			drop := int(base - r.base)
			if drop > len(r.rows) {
				drop = len(r.rows)
			}
			r.rows, r.base = r.rows[drop:], base
			r.mu.Unlock()
		}
	}
	_, err := r.client.UpdateTask(ctx, withToken(&runnerv1.UpdateTaskRequest{
		State: &runnerv1.TaskState{Id: r.taskID, Result: result, Steps: steps}}, r.token))
	return err
}

// downloadArchive scarica l'archivio tar.gz del repo e lo estrae in dest (senza la cartella radice).
func downloadArchive(ctx context.Context, hc *http.Client, url, token, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
		return fmt.Errorf("il gateway ha risposto %d", res.StatusCode)
	}
	return extractTarGz(res.Body, dest)
}

// CacheEnv sono le variabili che actions/cache e actions/upload-artifact leggono per parlare con
// il ci-gateway (GIT-330). L'indirizzo è quello dell'archivio del job senza il suo percorso, quindi
// lo stesso con cui il pod raggiunge il gateway; ACTIONS_RUNTIME_TOKEN è il token del job (scade
// con lui e vale solo per il suo repo), mai il segreto del runner. ACTIONS_RESULTS_URL fa arrivare
// upload-artifact v4 al ci-gateway, che lo rifiuta con un messaggio chiaro invece di un errore di rete.
func CacheEnv(archiveURL, jobToken string) []string {
	base := strings.TrimRight(strings.TrimSuffix(archiveURL, archivePath), "/")
	return []string{
		"ACTIONS_CACHE_URL=" + base + "/ci/v1/cache/",
		"ACTIONS_RESULTS_URL=" + base + "/",
		"ACTIONS_RUNTIME_TOKEN=" + RuntimeToken(jobToken),
	}
}

// RuntimeToken avvolge il token del job in un JWT senza firma, perche' upload-artifact@v4 lo
// decodifica prima di chiamare il servizio (`token.split('.')[1]`, claim scp «Actions.Results:
// <run>:<job>»): con un token opaco cade con «Invalid token specified» e il messaggio di
// GitStack sugli artefatti v4 non si vede mai (GIT-393). Il segreto resta il token del job,
// nel claim gsj: il ci-gateway lo estrae e lo verifica come sempre, la «firma» non vale nulla.
func RuntimeToken(jobToken string) string {
	enc := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(map[string]string{"scp": "Actions.Results:gitstack:gitstack", "gsj": jobToken})
	return enc([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + enc(payload) + ".x"
}

// withoutCIEnv toglie dall'ambiente ogni variabile GITSTACK_CI_*: act non ne ha bisogno.
func withoutCIEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GITSTACK_CI_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

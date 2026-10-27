package ci

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// I limiti sono fissi: il workflow può solo chiedere meno (CI7).
const (
	MaxCPU       = "2"
	MaxMemory    = "4Gi"
	MaxEphemeral = "20Gi"
	MaxRuntime   = 2 * time.Hour
)

// PodCreator è il confine con Kubernetes. L'adattatore usa il client del
// cluster; l'interfaccia mantiene il dispatcher provabile senza un cluster.
type PodCreator interface {
	Create(context.Context, Pod) error
}

// PodWatcher attende la chiusura del pod. Il dispatcher non lascia mai un job
// in_progress dopo una scadenza, un eviction o un errore di scheduling.
type PodWatcher interface {
	Wait(context.Context, string, string) (string, error)
}

// PodDeleter rimuove il pod concluso; non e' richiesto ai finti minimi usati
// dai test del dispatcher.
type PodDeleter interface {
	Delete(context.Context, string, string) error
}

// PodInspector serve alla riconciliazione dopo un riavvio di core.
type PodInspector interface {
	Exists(context.Context, string, string) (bool, error)
}

// Pod è la forma minima, indipendente dal client Kubernetes, del pod effimero.
type Pod struct {
	Name, Namespace, Image, PriorityClass string
	ActiveDeadlineSeconds                 int64
	AutomountServiceAccountToken          bool
	RunAsUser                             int64
	AllowPrivilegeEscalation              bool
	ReadOnlyRootFilesystem                bool
	DropAllCapabilities                   bool
	InitGate                              bool
	CPU, Memory, EphemeralStorage         string
	Command                               []string
	Env                                   map[string]string
	// CAConfigMap è il ConfigMap (chiave ca.crt) con la CA aziendale, nel namespace dei job (N9).
	CAConfigMap string
	// BuildKit non nil: pod di build (ADR 0001 §6), con il sidecar buildkitd rootless.
	BuildKit *BuildKit
}

// RunnerConfig contiene solo i dati che l'amministratore può scegliere.
type RunnerConfig struct {
	Namespace, Image, GatewayURL, GateInternalAddr, ActionMirror string
	Capacity                                                     int
	// ProxyEnv sono HTTP_PROXY, HTTPS_PROXY, NO_PROXY (anche in minuscolo) della rete aziendale (N4, N9):
	// i job scaricano action ed esecutore attraverso lo stesso proxy dei servizi.
	ProxyEnv map[string]string
	// CAConfigMap: ConfigMap con la CA aziendale (chiave ca.crt) nel namespace dei job; vuoto = nessuna.
	CAConfigMap string
	// BuildNamespace, BuildImage e RegistryAddr servono ai job con la label gitstack-build:
	// namespace di build, immagine rootless di BuildKit e host:porta del gateway nel cluster
	// (il registry /v2/). Vuoti: quei job falliscono senza creare pod.
	BuildNamespace, BuildImage, RegistryAddr string
	// Logs e Log: dove scrivere la causa di un dispatch fallito (log del job e log di core).
	// Nil: solo slog.Default() e nessuna riga nel log del job.
	Logs *LogStore
	Log  *slog.Logger
}

func (c RunnerConfig) normal() RunnerConfig {
	if c.Namespace == "" {
		c.Namespace = "gitstack-ci-jobs"
	}
	if c.Capacity < 1 {
		c.Capacity = 3
	}
	if c.Capacity > 3 {
		c.Capacity = 3
	}
	return c
}

// PodForJob crea un pod restricted e rootless. Il gate, incluso nell'immagine,
// è fail-closed finché la policy non rende irraggiungibile il cluster.
func PodForJob(c RunnerConfig, job JobRow, run RunRow) (Pod, error) {
	c = c.normal()
	if strings.TrimSpace(c.Image) == "" {
		return Pod{}, fmt.Errorf("immagine runner CI obbligatoria")
	}
	name := "ci-" + strings.ReplaceAll(job.ID.String(), "-", "")
	env := map[string]string{}
	for k, v := range c.ProxyEnv {
		if v != "" && !strings.HasPrefix(k, "GITSTACK_") {
			env[k] = v
		}
	}
	if c.CAConfigMap != "" {
		env["SSL_CERT_DIR"] = CAMountDir
		env["NODE_EXTRA_CA_CERTS"] = CAMountDir + "/ca.crt"
	}
	env["GITSTACK_CI_GATEWAY_URL"] = c.GatewayURL
	env["GITSTACK_CI_GATE_INTERNAL_ADDR"] = c.GateInternalAddr
	env["GITSTACK_CI_ACTION_MIRROR"] = c.ActionMirror
	env["GITSTACK_CI_RUNNER"] = "act-host"
	p := Pod{Name: name, Namespace: c.Namespace, Image: c.Image, PriorityClass: "gitstack-ci-job",
		ActiveDeadlineSeconds: int64(MaxRuntime / time.Second), AutomountServiceAccountToken: false,
		RunAsUser: 10001, AllowPrivilegeEscalation: false, ReadOnlyRootFilesystem: true,
		DropAllCapabilities: true, CPU: MaxCPU, Memory: MaxMemory, EphemeralStorage: MaxEphemeral,
		InitGate: true,
		Command:  []string{"/usr/local/bin/ci-runner"}, Env: env, CAConfigMap: c.CAConfigMap}
	if IsBuildJob(job.RunsOn) {
		return podForBuild(c, p)
	}
	return p, nil
}

// CAMountDir è dove il pod del job monta la CA aziendale (N9).
const CAMountDir = "/etc/gitstack/corporate-ca"

// PodForJobTicket aggiunge solo al processo supervisore il biglietto monouso
// emesso per questo job. Il ci-runner lo elimina prima di avviare act.
func PodForJobTicket(c RunnerConfig, job JobRow, run RunRow, ticket string) (Pod, error) {
	p, err := PodForJob(c, job, run)
	if err != nil {
		return Pod{}, err
	}
	if strings.TrimSpace(ticket) == "" {
		return Pod{}, fmt.Errorf("biglietto CI obbligatorio")
	}
	p.Env["GITSTACK_CI_RUNNER_SECRET"] = ticket
	return p, nil
}

func podNameFor(job JobRow) string { return "ci-" + strings.ReplaceAll(job.ID.String(), "-", "") }

// dispatchFailed registra perché un job è stato chiuso in failure senza pod: una riga
// di log di core (reason distinto: pod_spec, runner_type, pod_create) e la stessa
// causa nel log del job, così `gs run view --log` la mostra. Il biglietto monouso e
// i valori dell'ambiente del pod non finiscono mai nel messaggio.
func (c RunnerConfig) dispatchFailed(ctx context.Context, pool *pgxpool.Pool, now time.Time, job JobRow, run RunRow, pod Pod, reason string, cause error, ticket string) {
	c.dispatchNote(ctx, pool, now, job, pod, slog.LevelError, reason, "dispatch del job CI non riuscito", cause, ticket)
}

// dispatchNote e' la riga di log di core e del job per un dispatch fallito o rinviato.
func (c RunnerConfig) dispatchNote(ctx context.Context, pool *pgxpool.Pool, now time.Time, job JobRow, pod Pod, level slog.Level, reason, text string, cause error, ticket string) {
	log := c.Log
	if log == nil {
		log = slog.Default()
	}
	msg := cause.Error()
	if ticket != "" {
		msg = strings.ReplaceAll(msg, ticket, "***")
	}
	ns := pod.Namespace
	if ns == "" {
		ns = c.normal().Namespace
	}
	log.Log(ctx, level, text, "reason", reason, "job_id", job.ID, "run_id", job.RunID,
		"job", job.Name, "namespace", ns, "pod", pod.Name, "err", msg)
	if c.Logs == nil || c.Logs.Dir == "" {
		return
	}
	var lines int64
	if err := pool.QueryRow(ctx, `SELECT lines FROM core.ci_job_logs WHERE job_id = $1`, job.ID).Scan(&lines); err != nil {
		lines = 0
	}
	line := LogLine{Time: now, Content: fmt.Sprintf("%s (%s): %s [pod %s/%s]", strings.Replace(text, "job CI", "job", 1), reason, msg, ns, pod.Name)}
	if _, err := c.Logs.Append(ctx, pool, now, job.ID, lines, []LogLine{line}); err != nil {
		log.Error("causa del dispatch non scritta nel log del job", "job_id", job.ID, "err", err)
	}
}

// DispatchOnce prende un solo job interno e ne riflette l'esito nel modello
// della coda. I runner esterni non vengono mai assegnati a un pod interno.
func DispatchOnce(ctx context.Context, pool *pgxpool.Pool, api interface {
	PodCreator
	PodWatcher
}, cfg RunnerConfig, now time.Time) (bool, error) {
	job, run, ok, err := ClaimNext(ctx, pool, now, cfg.normal().Capacity, "internal-pod")
	if err != nil || !ok {
		return ok, err
	}
	if job.RunnerType != "internal_pod" {
		cfg.dispatchFailed(ctx, pool, now, job, run, Pod{Name: podNameFor(job)}, "runner_type",
			fmt.Errorf("runner_type %q diverso da internal_pod", job.RunnerType), "")
		return false, CompleteJob(ctx, pool, now, job.ID, ConclusionFailure)
	}
	pod, err := PodForJob(cfg, job, run)
	if err != nil {
		cfg.dispatchFailed(ctx, pool, now, job, run, Pod{Name: podNameFor(job)}, "pod_spec", err, "")
		return false, CompleteJob(ctx, pool, now, job.ID, ConclusionFailure)
	}
	if err := api.Create(ctx, pod); err != nil {
		cfg.dispatchFailed(ctx, pool, now, job, run, pod, "pod_create", err, "")
		return false, CompleteJob(ctx, pool, now, job.ID, ConclusionFailure)
	}
	conclusion, err := api.Wait(ctx, pod.Namespace, pod.Name)
	if err != nil || conclusion == "" {
		conclusion = ConclusionFailure
	}
	if conclusion != ConclusionSuccess && conclusion != ConclusionFailure && conclusion != ConclusionTimedOut {
		conclusion = ConclusionFailure
	}
	return true, CompleteJob(ctx, pool, time.Now().UTC(), job.ID, conclusion)
}

// DispatchPods riempie i posti liberi senza aspettare la fine dei pod gia' creati.
// ClaimNext serializza il conteggio globale, quindi piu' repliche di core non
// superano Capacity. Ogni osservatore chiude e poi elimina il proprio pod.
func DispatchPods(ctx context.Context, pool *pgxpool.Pool, api interface {
	PodCreator
	PodWatcher
}, cfg RunnerConfig, now time.Time, rep Reporter) (int, error) {
	started := 0
	for {
		job, run, ticket, ok, err := ClaimNextWithTicketReported(ctx, pool, now, cfg.normal().Capacity, "internal-pod", rep)
		if err != nil || !ok {
			return started, err
		}
		pod, err := PodForJobTicket(cfg, job, run, ticket)
		reason, cause := "", error(nil)
		switch {
		case err != nil:
			reason, cause = "pod_spec", err
			pod.Name = podNameFor(job)
		case job.RunnerType != "internal_pod":
			reason, cause = "runner_type", fmt.Errorf("runner_type %q diverso da internal_pod", job.RunnerType)
		default:
			if cerr := api.Create(ctx, pod); cerr != nil {
				reason, cause = "pod_create", cerr
			}
		}
		if cause != nil && reason == "pod_create" && IsQuotaRejection(cause) {
			// Rifiuto transitorio (GIT-452): il posto in Kubernetes non e' ancora libero.
			// Il job torna in coda e si riprova al prossimo giro; si esce dal ciclo, cosi'
			// il tick di main.go (1 s) fa da backoff invece di riprovare subito.
			requeued, rerr := cfg.requeueOnQuota(ctx, pool, now, job, run, pod, cause, ticket, rep)
			if rerr != nil {
				return started, rerr
			}
			if requeued {
				return started, nil
			}
		} else if cause == nil {
			quotaRetries.clear(job.ID)
		}
		if cause != nil {
			cfg.dispatchFailed(ctx, pool, now, job, run, pod, reason, cause, ticket)
			if cerr := CompleteJobReported(ctx, pool, now, job.ID, ConclusionFailure, rep); cerr != nil {
				return started, cerr
			}
			continue
		}
		started++
		go observePod(ctx, pool, api, pod, job.ID, rep)
	}
}

// QuotaRetryTimeout e' per quanto tempo un job rifiutato dalla ResourceQuota resta in
// coda (riprovato a ogni tick) prima di chiudersi in failure con reason pod_quota_timeout.
// Il conteggio parte dal primo rifiuto e vive in memoria: un riavvio di core lo riazzera.
var QuotaRetryTimeout = 5 * time.Minute

// PodGoneTimeout e' l'attesa massima che un pod eliminato sparisca prima di chiudere il job.
var PodGoneTimeout = 30 * time.Second

// podGonePoll e' l'intervallo di controllo dell'eliminazione del pod.
var podGonePoll = 250 * time.Millisecond

// IsQuotaRejection dice se Create e' stata rifiutata dalla ResourceQuota (Forbidden con
// "exceeded quota"): un errore transitorio, a differenza di ogni altro rifiuto.
func IsQuotaRejection(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "exceeded quota") && strings.Contains(msg, "forbidden")
}

type quotaTracker struct {
	mu    sync.Mutex
	first map[uuid.UUID]time.Time
}

var quotaRetries = &quotaTracker{first: map[uuid.UUID]time.Time{}}

// note registra un rifiuto: ritorna se e' il primo e da quando dura.
func (q *quotaTracker) note(id uuid.UUID, now time.Time) (first bool, since time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	since, ok := q.first[id]
	if !ok {
		q.first[id] = now
		return true, now
	}
	return false, since
}

func (q *quotaTracker) clear(id uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.first, id)
}

// requeueOnQuota rimette in coda il job rifiutato per quota. Ritorna false se il tempo
// massimo e' scaduto (o il job non e' piu' in corso): il chiamante lo chiude in failure.
// La riga di log (reason pod_quota) c'e' solo al primo rifiuto, per non riempire il log a 1 Hz.
func (c RunnerConfig) requeueOnQuota(ctx context.Context, pool *pgxpool.Pool, now time.Time, job JobRow, run RunRow, pod Pod, cause error, ticket string, rep Reporter) (bool, error) {
	first, since := quotaRetries.note(job.ID, now)
	if now.Sub(since) >= QuotaRetryTimeout {
		quotaRetries.clear(job.ID)
		c.dispatchFailed(ctx, pool, now, job, run, pod, "pod_quota_timeout",
			fmt.Errorf("posto non liberato entro %s: %w", QuotaRetryTimeout, cause), ticket)
		return false, nil
	}
	requeued, err := RequeueJob(ctx, pool, job.ID, rep)
	if err != nil || !requeued {
		quotaRetries.clear(job.ID)
		return false, err
	}
	if first {
		c.dispatchNote(ctx, pool, now, job, pod, slog.LevelWarn, "pod_quota",
			"dispatch rinviato, job rimesso in coda", cause, ticket)
	}
	return true, nil
}

// observePod aspetta l'esito del pod e solo dopo libera il posto. Il posto e' il job
// in_progress che ClaimNext conta, mentre la ResourceQuota ci-jobs conta il pod finche'
// esiste: se il job fosse chiuso prima dell'eliminazione, il job successivo (needs)
// verrebbe preso quando la quota e' ancora piena (GIT-452). Quindi: Delete, attesa che il
// pod sparisca (se l'adattatore sa dirlo, al piu' PodGoneTimeout), poi CompleteJob.
// Se il pod non puo' essere eliminato il job si chiude comunque: il requeue per quota
// in DispatchPods copre il resto.
func observePod(ctx context.Context, pool *pgxpool.Pool, api PodWatcher, pod Pod, jobID uuid.UUID, rep Reporter) {
	conclusion, err := api.Wait(ctx, pod.Namespace, pod.Name)
	if err != nil || (conclusion != ConclusionSuccess && conclusion != ConclusionFailure && conclusion != ConclusionTimedOut) {
		conclusion = ConclusionFailure
	}
	bg := context.WithoutCancel(ctx)
	if deleter, ok := api.(PodDeleter); ok {
		if derr := deleter.Delete(bg, pod.Namespace, pod.Name); derr == nil {
			if insp, ok := api.(PodInspector); ok {
				waitPodGone(bg, insp, pod)
			}
		}
	}
	_ = CompleteJobReported(bg, pool, time.Now().UTC(), jobID, conclusion, rep)
}

func waitPodGone(ctx context.Context, insp PodInspector, pod Pod) {
	deadline := time.Now().Add(PodGoneTimeout)
	for time.Now().Before(deadline) {
		exists, err := insp.Exists(ctx, pod.Namespace, pod.Name)
		if err == nil && !exists {
			return
		}
		time.Sleep(podGonePoll)
	}
}

// Reconcile chiude i job in corso il cui pod non esiste piu' (per esempio
// dopo un riavvio o una cancellazione). Il gancio ticketExpired verra' usato
// dalla presa monouso: per ora e' nil e non cambia lo stato dei job validi.
func Reconcile(ctx context.Context, pool *pgxpool.Pool, pods PodInspector, namespace string, ticketExpired func(JobRow) bool, now time.Time, rep Reporter) error {
	return ReconcileNS(ctx, pool, pods, namespace, "", ticketExpired, now, rep)
}

// ReconcileNS è Reconcile con il namespace di build: i pod dei job gitstack-build stanno lì.
func ReconcileNS(ctx context.Context, pool *pgxpool.Pool, pods PodInspector, namespace, buildNamespace string, ticketExpired func(JobRow) bool, now time.Time, rep Reporter) error {
	rows, err := pool.Query(ctx, `SELECT `+jobColumns+` FROM core.ci_jobs WHERE status = 'in_progress' AND runner_type = 'internal_pod'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return err
		}
		ns := namespace
		if buildNamespace != "" && IsBuildJob(job.RunsOn) {
			ns = buildNamespace
		}
		missing := ticketExpired != nil && ticketExpired(job)
		if !missing {
			exists, err := pods.Exists(ctx, ns, "ci-"+strings.ReplaceAll(job.ID.String(), "-", ""))
			if err != nil {
				return err
			}
			missing = !exists
		}
		if missing {
			if err := CompleteJobReported(ctx, pool, now, job.ID, ConclusionFailure, rep); err != nil {
				return err
			}
			continue
		}
		// Dopo il riavvio nessuna goroutine sta piu' osservando un pod vivo:
		// riprendiamo l'attesa prima di prendere altri job dalla coda.
		if watcher, ok := pods.(PodWatcher); ok {
			pod := Pod{Name: "ci-" + strings.ReplaceAll(job.ID.String(), "-", ""), Namespace: ns}
			go observePod(ctx, pool, watcher, pod, job.ID, rep)
		}
	}
	return rows.Err()
}

// ProxyEnvFrom legge le variabili del proxy aziendale (maiuscole e minuscole) con lookup: quelle
// che core ha già per webhook e SMTP si passano ai pod dei job (N4, N9).
func ProxyEnvFrom(lookup func(string) string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		if v := lookup(k); v != "" {
			out[k] = v
		}
	}
	return out
}

package apps

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

var routeGVR = schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "ingressroutes"}

// ErrNotDeployed: l'app non ha ancora un Deployment (nessun deploy è arrivato in fondo).
var ErrNotDeployed = errors.New("l'app non ha ancora un deploy in esecuzione")

// PullCredential sono le credenziali con cui il cluster scarica le immagini del registry del repo.
type PullCredential struct {
	Server, Username, Password string
}

// Config del controller.
type Config struct {
	// AppsDomain: dominio delle app (H2). Vuoto: nessuna rotta.
	AppsDomain string
	// AuthMiddleware: Middleware Traefik forward-auth del login (H6). Senza, le app con accesso
	// `login` non ricevono una rotta: mai esporle senza il login.
	AuthMiddleware string
	// ProgressTimeout: tempo massimo perché una versione superi la salute (default 120 s).
	ProgressTimeout time.Duration
	// FailureGrace: da quanto tempo un errore dei pod (pull, crash) deve durare per dichiarare
	// fallito il deploy prima del timeout (default 10 s): un errore transitorio non basta.
	FailureGrace time.Duration
	// PollInterval: ogni quanto si legge lo stato di un rollout (default 1 s).
	PollInterval time.Duration
	// LoopInterval: ogni quanto cerca deploy in corso (default 2 s); SyncEvery: sync dello stato (5 s).
	LoopInterval time.Duration
	SyncEvery    time.Duration
	// PullCredentials: credenziali del registry del repo (pull secret). Nil: nessun pull secret.
	PullCredentials func(ctx context.Context, app store.App) (*PullCredential, error)
	Now             func() time.Time
	Log             *slog.Logger
}

func (c *Config) defaults() {
	if c.ProgressTimeout <= 0 {
		c.ProgressTimeout = 120 * time.Second
	}
	if c.FailureGrace <= 0 {
		c.FailureGrace = 10 * time.Second
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.LoopInterval <= 0 {
		c.LoopInterval = 2 * time.Second
	}
	if c.SyncEvery <= 0 {
		c.SyncEvery = 5 * time.Second
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
}

// Controller riconcilia le app di core con Kubernetes.
type Controller struct {
	st   *store.Store
	kube kubernetes.Interface
	dyn  dynamic.Interface
	cfg  Config

	kick    chan struct{}
	mu      sync.Mutex
	running map[uuid.UUID]bool
}

// New crea il controller. dyn può essere nil: allora non crea le rotte Traefik.
func New(st *store.Store, kube kubernetes.Interface, dyn dynamic.Interface, cfg Config) *Controller {
	cfg.defaults()
	return &Controller{st: st, kube: kube, dyn: dyn, cfg: cfg, kick: make(chan struct{}, 1), running: map[uuid.UUID]bool{}}
}

// Kick sveglia il ciclo: c'è un deploy nuovo o una scala da applicare.
func (c *Controller) Kick() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Run gira finché ctx non finisce. I deploy rimasti «in corso» (core riavviato) si riprendono: il
// ciclo è idempotente.
func (c *Controller) Run(ctx context.Context) {
	tick := time.NewTicker(c.cfg.LoopInterval)
	defer tick.Stop()
	var lastSync, lastCollect time.Time
	for {
		c.processPending(ctx)
		if now := time.Now(); now.Sub(lastSync) >= c.cfg.SyncEvery {
			lastSync = now
			c.Sync(ctx)
			if now.Sub(lastCollect) >= 6*c.cfg.SyncEvery {
				lastCollect = now
				c.Collect(ctx)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-c.kick:
		case <-tick.C:
		}
	}
}

func (c *Controller) processPending(ctx context.Context) {
	pending, err := c.st.PendingAppDeploys(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.cfg.Log.Warn("apps: lettura dei deploy in corso non riuscita", "err", err)
		}
		return
	}
	for _, p := range pending {
		c.mu.Lock()
		busy := c.running[p.App.ID]
		if !busy {
			c.running[p.App.ID] = true
		}
		c.mu.Unlock()
		if busy {
			continue
		}
		go func() {
			defer func() {
				c.mu.Lock()
				delete(c.running, p.App.ID)
				c.mu.Unlock()
				c.Kick()
			}()
			c.RunDeploy(ctx, p)
		}()
	}
}

func (c *Controller) isDeploying(id uuid.UUID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running[id]
}

// desiredReplicas: le copie volute; zero con il repo archiviato o eliminato (l'app si ferma, la
// riga resta, H5).
func desiredReplicas(app store.App) int32 {
	if app.RepoArchivedAt != nil || app.RepoDeletedAt != nil {
		return 0
	}
	return int32(app.Config.Copies)
}

// RunDeploy porta in esecuzione la versione e ne scrive l'esito. È sincrono (bloccante fino alla
// salute ok o al fallimento); Run lo lancia in una goroutine per app.
func (c *Controller) RunDeploy(ctx context.Context, p store.PendingAppDeploy) {
	app, v := p.App, p.Version
	log := c.cfg.Log.With("app", app.ID, "version", v.Number)
	reason := c.rollout(ctx, app, v)
	if ctx.Err() != nil {
		return // core si ferma: il deploy resta «in corso» e riparte al prossimo avvio
	}
	if reason == "" {
		if err := c.st.FinishAppDeploy(ctx, app.ID, v.Number, "serving", "", c.cfg.Now()); err != nil {
			log.Warn("apps: esito del deploy non scritto", "err", err)
			return
		}
		log.Info("apps: versione partita")
		c.Sync(ctx)
		return
	}
	log.Warn("apps: deploy fallito", "reason", reason)
	// Resta in esecuzione la precedente: si rimette il suo template (stesso hash, Kubernetes
	// riporta il vecchio ReplicaSet) e si aspetta che sia di nuovo a regime.
	if app.ActiveVersion != v.Number {
		if prev, err := c.st.GetAppVersion(ctx, app.ID, app.ActiveVersion); err == nil && prev.State == "serving" {
			if err := c.restorePrevious(ctx, app, prev); err != nil {
				log.Warn("apps: ripristino della versione precedente non riuscito", "err", err)
				reason += " (il ripristino della versione precedente non è riuscito: " + err.Error() + ")"
			}
		}
	}
	if err := c.st.FinishAppDeploy(ctx, app.ID, v.Number, "failed", reason, c.cfg.Now()); err != nil {
		log.Warn("apps: esito del deploy non scritto", "err", err)
		return
	}
	c.Sync(ctx)
}

func (c *Controller) restorePrevious(ctx context.Context, app store.App, prev store.AppVersion) error {
	if err := c.apply(ctx, app, prev); err != nil {
		return err
	}
	if r := c.waitReady(ctx, app, prev); r != "" {
		return errors.New(r)
	}
	return nil
}

// rollout applica la versione e aspetta la salute. Ritorna "" se è partita, altrimenti il motivo.
func (c *Controller) rollout(ctx context.Context, app store.App, v store.AppVersion) string {
	if app.RepoDeletedAt != nil || app.RepoArchivedAt != nil {
		return "il repo è archiviato o eliminato: l'app non si avvia"
	}
	if err := c.apply(ctx, app, v); err != nil {
		return "applicazione su Kubernetes non riuscita: " + err.Error()
	}
	return c.waitReady(ctx, app, v)
}

// apply crea o aggiorna namespace, pull secret, Service, rotta e Deployment della versione.
func (c *Controller) apply(ctx context.Context, app store.App, v store.AppVersion) error {
	ns := Namespace(app.ID)
	if err := c.ensureNamespace(ctx, app, v.Config.Restricted); err != nil {
		return fmt.Errorf("namespace: %w", err)
	}
	pull := ""
	if v.Image.Source == "registry" && c.cfg.PullCredentials != nil {
		cred, err := c.cfg.PullCredentials(ctx, app)
		if err != nil {
			return fmt.Errorf("credenziali del registry: %w", err)
		}
		if cred != nil {
			if err := c.ensurePullSecret(ctx, ns, app.ID, cred); err != nil {
				return fmt.Errorf("pull secret: %w", err)
			}
			pull = pullSecretName
		}
	}
	if err := c.ensureService(ctx, app, v.Config); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if err := c.ensureRoute(ctx, app, v.Config); err != nil {
		// Senza le CRD di Traefik (o con Traefik non ancora pronto) la rotta manca, ma la versione
		// può partire: la rotta si riprova al prossimo deploy.
		c.cfg.Log.Warn("apps: rotta non applicata", "app", app.ID, "err", err)
	}
	deps := c.kube.AppsV1().Deployments(ns)
	opts := BuildOptions{Replicas: desiredReplicas(app), ProgressDeadlineSeconds: int32(c.cfg.ProgressTimeout / time.Second), PullSecret: pull}
	cur, err := deps.Get(ctx, WorkloadName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = deps.Create(ctx, BuildDeployment(app, v, opts), metav1.CreateOptions{})
		return err
	case err != nil:
		return err
	}
	opts.RestartedAt = cur.Spec.Template.Annotations[AnnotationRestartedAt]
	want := BuildDeployment(app, v, opts)
	want.ResourceVersion = cur.ResourceVersion
	_, err = deps.Update(ctx, want, metav1.UpdateOptions{})
	return err
}

func (c *Controller) ensureNamespace(ctx context.Context, app store.App, restricted bool) error {
	want := BuildNamespace(app, restricted)
	cur, err := c.kube.CoreV1().Namespaces().Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = c.kube.CoreV1().Namespaces().Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if cur.Labels[LabelManaged] != "true" || cur.Labels[LabelAppID] != app.ID.String() {
		return fmt.Errorf("il namespace %s esiste ma non è di questa app", want.Name)
	}
	if cur.Labels["pod-security.kubernetes.io/enforce"] == want.Labels["pod-security.kubernetes.io/enforce"] {
		return nil
	}
	cur.Labels["pod-security.kubernetes.io/enforce"] = want.Labels["pod-security.kubernetes.io/enforce"]
	_, err = c.kube.CoreV1().Namespaces().Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

func (c *Controller) ensurePullSecret(ctx context.Context, ns string, id uuid.UUID, cred *PullCredential) error {
	auth := base64.StdEncoding.EncodeToString([]byte(cred.Username + ":" + cred.Password))
	cfg, _ := json.Marshal(map[string]any{"auths": map[string]any{cred.Server: map[string]string{
		"username": cred.Username, "password": cred.Password, "auth": auth}}})
	want := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: pullSecretName, Namespace: ns, Labels: objectLabels(id)},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: cfg},
	}
	secrets := c.kube.CoreV1().Secrets(ns)
	cur, err := secrets.Get(ctx, pullSecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = secrets.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	cur.Data = want.Data
	_, err = secrets.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

func (c *Controller) ensureService(ctx context.Context, app store.App, cfg store.AppConfig) error {
	want := BuildService(app, cfg)
	services := c.kube.CoreV1().Services(Namespace(app.ID))
	if want == nil {
		err := services.Delete(ctx, WorkloadName, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	cur, err := services.Get(ctx, WorkloadName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = services.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	cur.Spec.Selector, cur.Spec.Ports, cur.Labels = want.Spec.Selector, want.Spec.Ports, want.Labels
	_, err = services.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// routeHost è l'indirizzo automatico dell'app, "" se non ne ha diritto.
func (c *Controller) routeHost(app store.App, cfg store.AppConfig) string {
	if c.dyn == nil || c.cfg.AppsDomain == "" || app.AddressLabel == nil || cfg.Port == nil {
		return ""
	}
	if app.Access == "login" && c.cfg.AuthMiddleware == "" {
		return ""
	}
	return *app.AddressLabel + "." + c.cfg.AppsDomain
}

func (c *Controller) ensureRoute(ctx context.Context, app store.App, cfg store.AppConfig) error {
	if c.dyn == nil {
		return nil
	}
	routes := c.dyn.Resource(routeGVR).Namespace(Namespace(app.ID))
	host := c.routeHost(app, cfg)
	if host == "" {
		err := routes.Delete(ctx, WorkloadName, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil // niente rotta, oppure le CRD di Traefik non ci sono
		}
		return err
	}
	auth := ""
	if app.Access == "login" {
		auth = c.cfg.AuthMiddleware
	}
	want := BuildRoute(app, host, auth)
	cur, err := routes.Get(ctx, WorkloadName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = routes.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	want.SetResourceVersion(cur.GetResourceVersion())
	_, err = routes.Update(ctx, want, metav1.UpdateOptions{})
	return err
}

// waitReady aspetta che la versione sia a regime (tutte le copie aggiornate, pronte e disponibili:
// la salute decide). Ritorna "" se è partita, altrimenti il motivo del fallimento.
func (c *Controller) waitReady(ctx context.Context, app store.App, v store.AppVersion) string {
	ns := Namespace(app.ID)
	deadline := time.Now().Add(c.cfg.ProgressTimeout)
	var failSince time.Time
	var lastFailure string
	for {
		d, err := c.kube.AppsV1().Deployments(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
		if ctx.Err() != nil {
			return ""
		}
		if err == nil && d.Status.ObservedGeneration >= d.Generation {
			if rolloutComplete(d.Spec.Replicas, d.Status.Replicas, d.Status.UpdatedReplicas, d.Status.ReadyReplicas, d.Status.AvailableReplicas) {
				return ""
			}
			if f := c.podFailure(ctx, ns, v.Number); f != "" {
				if lastFailure != f {
					lastFailure, failSince = f, time.Now()
				}
				if time.Since(failSince) >= c.cfg.FailureGrace {
					return f
				}
			} else {
				lastFailure = ""
			}
		}
		if time.Now().After(deadline) {
			msg := fmt.Sprintf("la versione non è diventata pronta entro %d secondi", int(c.cfg.ProgressTimeout/time.Second))
			if lastFailure != "" {
				return lastFailure
			}
			if d := c.podSummary(ctx, ns, v.Number); d != "" {
				msg += ": " + d
			}
			return msg
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(c.cfg.PollInterval):
		}
	}
}

func rolloutComplete(replicas *int32, total, updated, ready, available int32) bool {
	want := int32(1)
	if replicas != nil {
		want = *replicas
	}
	return updated == want && total == want && ready == want && available == want
}

var fatalWaiting = map[string]bool{
	"ImagePullBackOff": true, "ErrImagePull": true, "InvalidImageName": true, "CreateContainerConfigError": true,
	"CreateContainerError": true, "RunContainerError": true, "CrashLoopBackOff": true,
}

func (c *Controller) versionPods(ctx context.Context, ns string, version int) []corev1.Pod {
	pods, err := c.kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: labels.Set{"app.kubernetes.io/name": WorkloadName}.String()})
	if err != nil {
		return nil
	}
	var out []corev1.Pod
	for _, p := range pods.Items {
		if p.Annotations[AnnotationVersion] == strconv.Itoa(version) && p.DeletionTimestamp == nil {
			out = append(out, p)
		}
	}
	return out
}

// podFailure dà il motivo se un pod della versione è in un errore che non passa da solo
// (immagine non scaricabile, container che termina in continuazione).
func (c *Controller) podFailure(ctx context.Context, ns string, version int) string {
	for _, p := range c.versionPods(ctx, ns, version) {
		if r := containerFailure(p); r != "" {
			// Un container che riparte perché le sonde falliscono sembra un crash: la causa vera è
			// negli eventi del pod (Unhealthy). Senza il permesso di leggerli resta il motivo del container.
			if h := c.probeFailure(ctx, ns, p.Name); h != "" && !strings.HasPrefix(r, "ImagePull") && !strings.HasPrefix(r, "ErrImage") {
				return h
			}
			return r
		}
	}
	return ""
}

// probeFailure dà il messaggio dell'ultimo evento «Unhealthy» del pod (sonda di salute fallita).
func (c *Controller) probeFailure(ctx context.Context, ns, pod string) string {
	evs, err := c.kube.CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.name=" + pod + ",reason=Unhealthy"})
	if err != nil || len(evs.Items) == 0 {
		return ""
	}
	// Conta solo la liveness: è lei che fa ripartire il container. Una readiness che non passa da
	// sola non è un crash (il timeout la riporta come «controllo di salute»).
	var last *corev1.Event
	for i := range evs.Items {
		e := &evs.Items[i]
		if strings.Contains(e.Message, "Liveness probe failed") && (last == nil || e.LastTimestamp.After(last.LastTimestamp.Time)) {
			last = e
		}
	}
	if last == nil {
		return ""
	}
	return trim("il controllo di salute non è passato: " + last.Message)
}

func containerFailure(p corev1.Pod) string {
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && fatalWaiting[w.Reason] {
			if w.Reason == "CrashLoopBackOff" || cs.RestartCount >= 1 && cs.LastTerminationState.Terminated != nil {
				if t := cs.LastTerminationState.Terminated; t != nil {
					return fmt.Sprintf("il container termina in continuazione (codice %d, %s)", t.ExitCode, firstNonEmpty(t.Reason, "terminato"))
				}
			}
			return trim(w.Reason + ": " + w.Message)
		}
		if t := cs.LastTerminationState.Terminated; t != nil && cs.RestartCount >= 2 {
			return fmt.Sprintf("il container termina in continuazione (codice %d, %s)", t.ExitCode, firstNonEmpty(t.Reason, "terminato"))
		}
	}
	return ""
}

func (c *Controller) podSummary(ctx context.Context, ns string, version int) string {
	var parts []string
	for _, p := range c.versionPods(ctx, ns, version) {
		switch {
		case p.Status.Phase == corev1.PodPending && len(p.Status.ContainerStatuses) == 0:
			for _, cond := range p.Status.Conditions {
				if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
					parts = append(parts, trim("pod non schedulato: "+cond.Message))
				}
			}
		default:
			for _, cs := range p.Status.ContainerStatuses {
				if !cs.Ready {
					parts = append(parts, "il controllo di salute non è passato")
				}
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// Restart riavvia le copie una alla volta (H12): cambia l'annotazione del template, Kubernetes fa
// l'aggiornamento graduale senza togliere copie pronte. Non crea una versione.
func (c *Controller) Restart(ctx context.Context, app store.App) error {
	deps := c.kube.AppsV1().Deployments(Namespace(app.ID))
	for attempt := 0; ; attempt++ {
		d, err := deps.Get(ctx, WorkloadName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return ErrNotDeployed
		}
		if err != nil {
			return err
		}
		if d.Spec.Template.Annotations == nil {
			d.Spec.Template.Annotations = map[string]string{}
		}
		d.Spec.Template.Annotations[AnnotationRestartedAt] = c.cfg.Now().Format(time.RFC3339Nano)
		_, err = deps.Update(ctx, d, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) && attempt < 5 {
			continue
		}
		if err != nil {
			return err
		}
		if err := c.st.SetAppObserved(ctx, app.ID, "restarting", app.ReadyCopies); err != nil {
			return err
		}
		c.Kick()
		return nil
	}
}

// Scale porta le repliche del Deployment alle copie dell'app (già validate e scritte dalla quota).
func (c *Controller) Scale(ctx context.Context, app store.App) error {
	deps := c.kube.AppsV1().Deployments(Namespace(app.ID))
	for attempt := 0; ; attempt++ {
		d, err := deps.Get(ctx, WorkloadName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil // le copie si applicano al primo deploy
		}
		if err != nil {
			return err
		}
		want := desiredReplicas(app)
		if d.Spec.Replicas != nil && *d.Spec.Replicas == want {
			return nil
		}
		d.Spec.Replicas = &want
		_, err = deps.Update(ctx, d, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) && attempt < 5 {
			continue
		}
		c.Kick()
		return err
	}
}

// Sync allinea copie volute e stato osservato (running, restarting, unhealthy, starting) di ogni
// app senza deploy in corso. Autoguarigione: se le repliche del cluster non sono quelle del
// modello (scala fatta mentre core era giù, repo archiviato) le rimette.
func (c *Controller) Sync(ctx context.Context) {
	ids, err := c.st.AppIDs(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if c.isDeploying(id) {
			continue
		}
		app, err := c.st.GetAppByID(ctx, id)
		if err != nil || (app.LastDeploy != nil && app.LastDeploy.State == "in_progress") {
			continue
		}
		c.syncApp(ctx, app)
	}
}

func (c *Controller) syncApp(ctx context.Context, app store.App) {
	ns := Namespace(app.ID)
	d, err := c.kube.AppsV1().Deployments(ns).Get(ctx, WorkloadName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.reapply(ctx, app)
		}
		return
	}
	if app.Status == "failed" {
		if v, err := c.st.GetAppVersion(ctx, app.ID, app.ActiveVersion); err == nil && v.State == "failed" {
			return // il primo deploy è fallito: l'app resta «fallita» finché non ne arriva un altro
		}
	}
	want := desiredReplicas(app)
	if d.Spec.Replicas == nil || *d.Spec.Replicas != want {
		if err := c.Scale(ctx, app); err != nil {
			c.cfg.Log.Warn("apps: scala non riuscita", "app", app.ID, "err", err)
		}
		return
	}
	ready := int(d.Status.AvailableReplicas)
	var status string
	rolling := d.Status.ObservedGeneration < d.Generation || d.Status.UpdatedReplicas != want || d.Status.Replicas != want
	switch {
	case want == 0:
		status = "stopped"
		ready = 0
	case ready >= int(want) && !rolling:
		status = "running"
	case rolling && ready > 0:
		status = "restarting"
	case c.podFailure(ctx, ns, app.ActiveVersion) != "":
		status = "unhealthy"
	case ready < int(want) && app.Status == "running":
		status = "unhealthy"
	default:
		status = "starting"
		if app.Status == "restarting" {
			status = "restarting"
		}
	}
	if status != app.Status || ready != app.ReadyCopies {
		if err := c.st.SetAppObserved(ctx, app.ID, status, ready); err != nil {
			c.cfg.Log.Warn("apps: stato osservato non scritto", "app", app.ID, "err", err)
		}
	}
}

// reapply riporta la versione attiva di un'app il cui Deployment non c'è più: succede dopo un
// restore su un cluster pulito (H4), dove le app tornano dal database (versione, configurazione,
// immagini nel registry ripristinato).
func (c *Controller) reapply(ctx context.Context, app store.App) {
	v, err := c.st.GetAppVersion(ctx, app.ID, app.ActiveVersion)
	if !shouldReapply(app, v, err) {
		return
	}
	if err := c.apply(ctx, app, v); err != nil {
		c.cfg.Log.Warn("apps: riapplicazione della versione attiva non riuscita", "app", app.ID, "version", v.Number, "err", err)
		return
	}
	c.cfg.Log.Info("apps: versione attiva riapplicata (Deployment assente)", "app", app.ID, "version", v.Number)
}

// shouldReapply dice se la versione attiva va riapplicata: l'app deve avere copie volute (repo
// non archiviato né eliminato), non essere «fallita» al primo deploy e la versione attiva deve
// essere quella in servizio.
func shouldReapply(app store.App, v store.AppVersion, verr error) bool {
	return verr == nil && desiredReplicas(app) > 0 && app.Status != "failed" && v.State == "serving"
}

// Collect toglie i namespace del controller di app che non esistono più nel modello (app o repo
// eliminati). Agisce solo su namespace con la label gitstack.io/managed-app=true e il nome
// coerente con l'app-id della label; se il modello non si legge non fa niente.
func (c *Controller) Collect(ctx context.Context) {
	ids, err := c.st.AppIDs(ctx)
	if err != nil {
		return
	}
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id.String()] = true
	}
	list, err := c.kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: LabelManaged + "=true"})
	if err != nil {
		return
	}
	for _, n := range list.Items {
		id := n.Labels[LabelAppID]
		if _, err := uuid.Parse(id); err != nil || n.Name != NamePrefix+id || known[id] || n.DeletionTimestamp != nil {
			continue
		}
		if err := c.kube.CoreV1().Namespaces().Delete(ctx, n.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			c.cfg.Log.Warn("apps: namespace orfano non eliminato", "namespace", n.Name, "err", err)
			continue
		}
		c.cfg.Log.Info("apps: namespace orfano eliminato", "namespace", n.Name)
	}
}

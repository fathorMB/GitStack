// Package apps è il controller dei deploy delle app (v3-a, GIT-343, H7/H12, ADR 0002): traduce
// una versione del modello dati in Deployment, Service e rotta Traefik nel namespace dell'app,
// decide dal controllo di salute quando la versione è partita e tiene in esecuzione la precedente
// se l'avvio fallisce. Vive in core come goroutine con coda (non è un servizio) e parla con
// Kubernetes solo nei namespace `gitstack-app-<id>` con la label gitstack.io/managed-app=true.
//
// Scostamento dall'ADR: un solo Deployment per app (non uno per revisione). L'aggiornamento
// graduale con maxUnavailable=0 tiene in servizio le copie della versione precedente finché la
// nuova non supera la readiness; se non ci riesce il controller rimette il template precedente,
// che Kubernetes riconosce (stesso hash) e riporta al vecchio ReplicaSet. Stesso effetto sul
// Service, meno oggetti da tenere allineati.
package apps

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

const (
	// LabelManaged marca i namespace del controller: è l'unico perimetro in cui agisce.
	LabelManaged = "gitstack.io/managed-app"
	LabelAppID   = "gitstack.io/app-id"
	// AnnotationVersion è il numero di versione nel template del pod.
	AnnotationVersion = "gitstack.io/version"
	// AnnotationRestartedAt cambia a ogni riavvio e viene portata avanti dai deploy successivi.
	AnnotationRestartedAt = "gitstack.io/restarted-at"

	NamePrefix     = "gitstack-app-"
	WorkloadName   = "app"
	pullSecretName = "registry-pull"
	containerName  = "app"
	servicePort    = 80
)

// Namespace è il namespace Kubernetes dell'app.
func Namespace(id uuid.UUID) string { return NamePrefix + id.String() }

func selectorLabels(id uuid.UUID) map[string]string {
	return map[string]string{LabelAppID: id.String(), "app.kubernetes.io/name": WorkloadName}
}

func objectLabels(id uuid.UUID) map[string]string {
	l := selectorLabels(id)
	l[LabelManaged] = "true"
	l["app.kubernetes.io/managed-by"] = "gitstack-core"
	return l
}

// ImageRef è il riferimento da scaricare: se la versione ha il digest, lo si fissa (ADR: immagine
// per digest), altrimenti resta il tag.
func ImageRef(v store.AppVersion) string {
	ref := v.Image.Reference
	if v.Image.Digest != nil && !strings.Contains(ref, "@") {
		if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
			ref = ref[:i]
		}
		return ref + "@" + *v.Image.Digest
	}
	return ref
}

// BuildOptions sono le scelte di istanza che entrano nel Deployment.
type BuildOptions struct {
	// Replicas: copie volute adesso (la versione non le porta: la scala non crea versioni).
	Replicas int32
	// ProgressDeadlineSeconds è il tempo dopo cui Kubernetes dichiara il rollout bloccato.
	ProgressDeadlineSeconds int32
	// PullSecret: nome del Secret con le credenziali del registry del repo; vuoto per le immagini esterne.
	PullSecret string
	// RestartedAt: valore da portare avanti dal Deployment esistente.
	RestartedAt string
}

// BuildDeployment traduce una versione nel Deployment dell'app.
func BuildDeployment(app store.App, v store.AppVersion, o BuildOptions) *appsv1.Deployment {
	cfg := v.Config
	ann := map[string]string{AnnotationVersion: strconv.Itoa(v.Number)}
	if o.RestartedAt != "" {
		ann[AnnotationRestartedAt] = o.RestartedAt
	}
	labels := objectLabels(app.ID)
	zero := intstr.FromInt32(0)
	one := intstr.FromInt32(1)
	replicas := o.Replicas
	deadline := o.ProgressDeadlineSeconds
	if deadline <= 0 {
		deadline = 120
	}
	f := false
	t := true
	sc := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &f,
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	if cfg.Restricted {
		// H9: l'app «restricted» non gira come root; senza il flag il root è permesso.
		sc.RunAsNonRoot = &t
	}
	c := corev1.Container{
		Name:            containerName,
		Image:           ImageRef(v),
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             envVars(cfg.Variables, app.Config.Port),
		SecurityContext: sc,
		Resources: corev1.ResourceRequirements{
			Requests: resourceList(cfg.Guaranteed),
			Limits:   resourceList(cfg.Limit),
		},
	}
	minReady := int32(0)
	if cfg.Port != nil {
		c.Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: int32(*cfg.Port), Protocol: corev1.ProtocolTCP}}
		c.ReadinessProbe, c.LivenessProbe = probes(cfg)
		// Il pod resta in servizio qualche secondo dopo il segnale di stop: il tempo che la
		// rimozione dagli endpoint arrivi a Traefik e a kube-proxy, senza richieste perse.
		c.Lifecycle = &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: 5}}}
	} else {
		// Senza porta non c'è nulla da interrogare: la versione «parte» se il processo resta
		// su per qualche secondo (un crash immediato la manderebbe in CrashLoopBackOff).
		minReady = 5
	}
	pod := corev1.PodSpec{
		AutomountServiceAccountToken: &f,
		EnableServiceLinks:           &f,
		Containers:                   []corev1.Container{c},
	}
	if o.PullSecret != "" {
		pod.ImagePullSecrets = []corev1.LocalObjectReference{{Name: o.PullSecret}}
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(app.ID), Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas:                &replicas,
			MinReadySeconds:         minReady,
			ProgressDeadlineSeconds: &deadline,
			RevisionHistoryLimit:    int32Ptr(3),
			Selector:                &metav1.LabelSelector{MatchLabels: selectorLabels(app.ID)},
			// Nessuna copia in meno durante l'aggiornamento: la nuova parte prima che la vecchia si fermi.
			Strategy: appsv1.DeploymentStrategy{
				Type:          appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{MaxUnavailable: &zero, MaxSurge: &one},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: ann},
				Spec:       pod,
			},
		},
	}
}

func int32Ptr(i int32) *int32 { return &i }

func probes(cfg store.AppConfig) (ready, live *corev1.Probe) {
	handler := corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(int32(*cfg.Port))}}
	period, timeout := int32(5), int32(2)
	if hc := cfg.HealthCheck; hc != nil {
		path := hc.Path
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		handler = corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(int32(*cfg.Port))}}
		if hc.IntervalSeconds > 0 {
			period = int32(hc.IntervalSeconds)
		}
		if hc.TimeoutSeconds != nil && *hc.TimeoutSeconds > 0 {
			timeout = int32(*hc.TimeoutSeconds)
		}
	}
	ready = &corev1.Probe{ProbeHandler: handler, PeriodSeconds: period, TimeoutSeconds: timeout, SuccessThreshold: 1, FailureThreshold: 2}
	// La liveness parte dopo che la readiness ha già avuto più tentativi: non uccide un avvio lento.
	live = &corev1.Probe{ProbeHandler: handler, InitialDelaySeconds: 15, PeriodSeconds: max(period, 5), TimeoutSeconds: timeout, FailureThreshold: 3}
	return ready, live
}

func envVars(vars map[string]string, port *int) []corev1.EnvVar {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]corev1.EnvVar, 0, len(keys)+1)
	for _, k := range keys {
		out = append(out, corev1.EnvVar{Name: k, Value: vars[k]})
	}
	if port != nil {
		has := false
		for _, e := range out {
			has = has || e.Name == "PORT"
		}
		if !has {
			out = append(out, corev1.EnvVar{Name: "PORT", Value: strconv.Itoa(*port)})
		}
	}
	return out
}

func resourceList(r store.AppResourceAmount) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(r.CPUMillis), resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(r.MemoryBytes, resource.BinarySI),
	}
}

// BuildService espone la porta dell'app dentro il cluster; nil se l'app non ha porta. La porta di
// destinazione è il nome «http» del container: cambiare la porta di una versione non tocca il
// Service, così durante un deploy (o un ritorno automatico) le copie in servizio restano raggiunte
// sulla loro porta.
func BuildService(app store.App, cfg store.AppConfig) *corev1.Service {
	if cfg.Port == nil {
		return nil
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(app.ID), Labels: objectLabels(app.ID)},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabels(app.ID),
			Ports:    []corev1.ServicePort{{Name: "http", Port: servicePort, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP}},
		},
	}
}

// BuildNamespace: PSS baseline per default (il root è permesso, H9), restricted per le app che lo
// chiedono; mai privilegi, hostPath o capability (ADR 0002).
func BuildNamespace(app store.App, restricted bool) *corev1.Namespace {
	level := "baseline"
	if restricted {
		level = "restricted"
	}
	l := objectLabels(app.ID)
	l["pod-security.kubernetes.io/enforce"] = level
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace(app.ID), Labels: l}}
}

// BuildRoute è l'IngressRoute Traefik `websecure` verso il Service (ADR 0002: tls {} e il
// certificato wildcard è il TLSStore default). authMiddleware, se non vuoto, è il forward-auth
// del login: `nome` (nel namespace dell'app) o `namespace/nome`.
func BuildRoute(app store.App, host, authMiddleware string) *unstructured.Unstructured {
	route := map[string]any{
		"kind":  "Rule",
		"match": fmt.Sprintf("Host(`%s`)", host),
		"services": []any{map[string]any{
			"name": WorkloadName,
			"port": int64(servicePort),
		}},
	}
	if authMiddleware != "" {
		m := map[string]any{"name": authMiddleware}
		if ns, name, ok := strings.Cut(authMiddleware, "/"); ok {
			m = map[string]any{"name": name, "namespace": ns}
		}
		route["middlewares"] = []any{m}
	}
	labels := map[string]any{}
	for k, v := range objectLabels(app.ID) {
		labels[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "IngressRoute",
		"metadata": map[string]any{
			"name": WorkloadName, "namespace": Namespace(app.ID), "labels": labels,
		},
		"spec": map[string]any{
			"entryPoints": []any{"websecure"},
			"routes":      []any{route},
			"tls":         map[string]any{},
		},
	}}
}

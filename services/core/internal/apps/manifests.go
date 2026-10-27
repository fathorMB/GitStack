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
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/pkg/names"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

const (
	// LabelManaged marca i namespace del controller: è l'unico perimetro in cui agisce.
	LabelManaged = "gitstack.io/managed-app"
	LabelAppID   = "gitstack.io/app-id"
	// LabelRepoID lega il namespace e i pod al repository: la policy H9 lo usa
	// per permettere traffico solo fra le app dello stesso repository.
	LabelRepoID = "gitstack.io/repo-id"
	// LabelSiblingAlias marca i Service ExternalName con cui un'app raggiunge per nome le sorelle
	// dello stesso repo (H15): il controller tocca solo i Service con questa label.
	LabelSiblingAlias = "gitstack.io/sibling-alias"
	// AnnotationVersion è il numero di versione nel template del pod.
	AnnotationVersion = "gitstack.io/version"
	// AnnotationRestartedAt cambia a ogni riavvio e viene portata avanti dai deploy successivi.
	AnnotationRestartedAt = "gitstack.io/restarted-at"

	NamePrefix     = "gitstack-app-"
	WorkloadName   = "app"
	pullSecretName = "registry-pull"
	appSecretName  = "app-secrets"
	containerName  = "app"
	servicePort    = 80
	// DeclaredPortName è il nome della seconda porta del Service `app`: la porta dichiarata dall'app.
	DeclaredPortName = "app"
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

func appLabels(app store.App) map[string]string {
	l := objectLabels(app.ID)
	l[LabelRepoID] = app.RepoID.String()
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
	// AppSecret: nome del Secret con i valori correnti dei segreti dell'app.
	AppSecret string
	// RestartedAt: valore da portare avanti dal Deployment esistente.
	RestartedAt string
	// ProxyEnv è la configurazione N9 dell'istanza. Viene dal processo core e
	// non può essere sostituita dalle variabili dichiarate dall'app.
	ProxyEnv map[string]string
	// EgressRestricted: l'app ha la restrizione dell'uscita (H16). La sua NetworkPolicy non apre il proxy,
	// quindi non riceve nessuna variabile del proxy N9 (esce in modo diretto verso le destinazioni elencate).
	EgressRestricted bool
	// GateImage è l'immagine (di core) dell'init container «gate» (GIT-418): l'app parte solo dopo che la
	// NetworkPolicy è stata applicata al pod. Vuoto: nessun cancello (solo prove senza l'immagine di core).
	GateImage string
	// Gateway è il percorso dell'app verso l'API di GitStack (GIT-442, H16): nil = non configurato,
	// nessuna variabile GITSTACK_API_URL.
	Gateway *GatewayAccess
}

// GatewayAccess descrive la porta interna del gateway che le app ospitate raggiungono dentro il
// cluster (GIT-442, H16). Viene dal chart (env di core) come TraefikNamespace; senza, la policy e il
// Deployment restano quelli di prima. La rete apre solo il trasporto: l'autorizzazione è del token.
type GatewayAccess struct {
	// Namespace e PodLabels scelgono i pod del gateway; Service è il nome del suo Service nel
	// namespace e Port la porta interna (stessa sul Service e sul container).
	Namespace string
	PodLabels map[string]string
	Service   string
	Port      int
}

// Valid dice se la configurazione è completa: tutti i campi, e una porta valida.
func (g *GatewayAccess) Valid() bool {
	return g != nil && g.Namespace != "" && len(g.PodLabels) > 0 && g.Service != "" && g.Port > 0 && g.Port < 65536
}

// APIURL è il valore di GITSTACK_API_URL: l'API sul Service del gateway, con il prefisso /v1 del
// gateway (la porta interna non passa da Traefik, che toglie /api).
func (g *GatewayAccess) APIURL() string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/v1", g.Service, g.Namespace, g.Port)
}

// AppKeysURL è il valore di GITSTACK_APP_KEYS_URL (GIT-444): le chiavi pubbliche con cui l'app
// verifica le intestazioni firmate H6, sulla porta interna del gateway (senza prefisso /v1 né /api).
func (g *GatewayAccess) AppKeysURL() string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/.well-known/gitstack-app-keys", g.Service, g.Namespace, g.Port)
}

// NetworkPolicyOptions sono le dipendenze d'istanza della policy H9.
type NetworkPolicyOptions struct {
	// Gateway: se valido, una regola di egress fissa verso i soli pod del gateway sulla porta interna
	// (anche per le app con l'uscita ristretta). Nil: nessuna regola.
	Gateway          *GatewayAccess
	TraefikNamespace string
	ProxyEnv         map[string]string
	// ProxyIPs è la risoluzione fatta da core per un proxy nominato.
	ProxyIPs map[string][]net.IP
	// ClusterCIDRs e NodeAddresses sono quelli di pkg/egress (installazione): sempre bloccati
	// anche in uscita dalle app (H16). Vuoti: default di pkg/egress, senza indirizzi del nodo.
	ClusterCIDRs  []string
	NodeAddresses []string
	// Deny è la lista vietata dell'installazione: nessuna apertura la supera.
	Deny []string
	// PrivateOpenings sono gli indirizzi:porta della rete privata aperti dall'amministratore (già
	// risolti, GIT-385). Valgono per le app senza restrizione; il builder ricontrolla ognuno con
	// egress.Policy.CheckOpening e scarta quelli che le regole di fondo vietano.
	PrivateOpenings []netip.AddrPort
	// Restriction: se non nil l'app esce solo verso questi indirizzi:porta (oltre a peer dello stesso
	// repo e DNS), senza internet aperto né proxy. Una slice vuota chiude ogni altra uscita.
	Restriction *[]netip.AddrPort
}

// BuildDeployment traduce una versione nel Deployment dell'app.
func BuildDeployment(app store.App, v store.AppVersion, o BuildOptions) *appsv1.Deployment {
	cfg := v.Config
	ann := map[string]string{AnnotationVersion: strconv.Itoa(v.Number)}
	if o.RestartedAt != "" {
		ann[AnnotationRestartedAt] = o.RestartedAt
	}
	labels := appLabels(app)
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
		Env:             envVars(cfg.Variables, app.Config.Port, systemEnv(v, o), deploymentProxyEnv(o)),
		SecurityContext: sc,
		Resources: corev1.ResourceRequirements{
			Requests: resourceList(cfg.Guaranteed),
			Limits:   resourceList(cfg.Limit),
		},
	}
	if o.AppSecret != "" {
		c.EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: o.AppSecret}}}}
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
		PriorityClassName:            "gitstack-apps",
		AutomountServiceAccountToken: &f,
		EnableServiceLinks:           &f,
		Containers:                   []corev1.Container{c},
	}
	if o.GateImage != "" {
		pod.InitContainers = []corev1.Container{GateContainer(o.GateImage, GateCommand)}
	}
	if o.PullSecret != "" {
		pod.ImagePullSecrets = []corev1.LocalObjectReference{{Name: o.PullSecret}}
	}
	// Strategia: RollingUpdate senza copie in meno (la nuova parte prima che la vecchia si fermi).
	strategy := appsv1.DeploymentStrategy{
		Type:          appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{MaxUnavailable: &zero, MaxSurge: &one},
	}
	if len(cfg.Volumes) > 0 {
		// H3: con i volumi l'app gira in una sola copia e l'aggiornamento è Recreate: il PVC è
		// ReadWriteOnce, due pod non possono scrivere insieme. Il pod vecchio si ferma, poi parte il nuovo.
		strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		for _, v := range cfg.Volumes {
			pod.Volumes = append(pod.Volumes, corev1.Volume{Name: volumeKubeName(v.Name), VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: PVCName(v.Name)}}})
			pod.Containers[0].VolumeMounts = append(pod.Containers[0].VolumeMounts, corev1.VolumeMount{Name: volumeKubeName(v.Name), MountPath: v.MountPath})
		}
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(app.ID), Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas:                &replicas,
			MinReadySeconds:         minReady,
			ProgressDeadlineSeconds: &deadline,
			RevisionHistoryLimit:    int32Ptr(3),
			Selector:                &metav1.LabelSelector{MatchLabels: selectorLabels(app.ID)},
			Strategy:                strategy,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: ann},
				Spec:       pod,
			},
		},
	}
}

func int32Ptr(i int32) *int32 { return &i }

// StorageClassVolumes è la storage class dei volumi: local-path, quella di k3s (H3).
const StorageClassVolumes = "local-path"

// LabelVolume marca i PVC del controller con il nome del volume.
const LabelVolume = "gitstack.io/volume"

// PVCName è il nome del PVC di un volume dell'app.
func PVCName(volume string) string { return "vol-" + volume }

func volumeKubeName(volume string) string { return "vol-" + volume }

// BuildPVC è il PersistentVolumeClaim di un volume: ReadWriteOnce, local-path, dimensione fissa. Il
// controller non lo modifica dopo la creazione (la dimensione non cambia, H3).
func BuildPVC(app store.App, v store.AppVolumeSpec) *corev1.PersistentVolumeClaim {
	class := StorageClassVolumes
	labels := appLabels(app)
	labels[LabelVolume] = v.Name
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: PVCName(v.Name), Namespace: Namespace(app.ID), Labels: labels},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &class,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: *resource.NewQuantity(v.SizeBytes, resource.BinarySI)},
			},
		},
	}
}

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

// Variabili di sistema (H14): GitStack le passa a ogni deploy e il prefisso GITSTACK_ è riservato,
// quindi variabili e segreti dell'app non possono usarlo (API e UI, vedi IsReservedEnvName).
const (
	ReservedEnvPrefix = "GITSTACK_"
	EnvAppVersion     = "GITSTACK_APP_VERSION"
	EnvDeployedAt     = "GITSTACK_DEPLOYED_AT"
	// EnvCommitSha è il commit dell'etichetta revision dell'immagine (GIT-386), letto da core al
	// deploy e salvato nella versione: qui non si chiama nessuno.
	EnvCommitSha = "GITSTACK_COMMIT_SHA"
	// EnvAPIURL è l'API di GitStack dal gateway nel cluster (GIT-442): presente solo se core conosce
	// il gateway interno.
	EnvAPIURL = "GITSTACK_API_URL"
	// EnvAppKeysURL sono le chiavi pubbliche delle intestazioni firmate H6 sulla porta interna del
	// gateway (GIT-444): stesse condizioni di EnvAPIURL.
	EnvAppKeysURL = "GITSTACK_APP_KEYS_URL"
)

// IsReservedEnvName dice se il nome di una variabile o di un segreto dell'app usa il prefisso
// riservato. Il confronto non distingue maiuscole e minuscole.
func IsReservedEnvName(name string) bool {
	return len(name) >= len(ReservedEnvPrefix) && strings.EqualFold(name[:len(ReservedEnvPrefix)], ReservedEnvPrefix)
}

// systemEnv sono le variabili di sistema di una versione: GITSTACK_APP_VERSION è il tag
// dell'immagine (assente se l'immagine è solo per digest), GITSTACK_DEPLOYED_AT è l'istante della
// versione in RFC 3339 UTC, GITSTACK_COMMIT_SHA il commit della versione (assente se nil, H14).
// Restano uguali a ogni riavvio della stessa versione.
func systemEnv(v store.AppVersion, o BuildOptions) map[string]string {
	out := map[string]string{}
	if o.Gateway.Valid() {
		out[EnvAPIURL] = o.Gateway.APIURL()
		out[EnvAppKeysURL] = o.Gateway.AppKeysURL()
	}
	if tag := imageTag(v.Image.Reference); tag != "" {
		out[EnvAppVersion] = tag
	}
	if !v.CreatedAt.IsZero() {
		out[EnvDeployedAt] = v.CreatedAt.UTC().Format(time.RFC3339)
	}
	if v.CommitSha != nil && *v.CommitSha != "" {
		out[EnvCommitSha] = *v.CommitSha
	}
	return out
}

// imageTag è il tag di un riferimento (il digest dopo @ non conta); "" se il riferimento non ne ha.
func imageTag(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 || i < strings.LastIndex(ref, "/") {
		return ""
	}
	return ref[i+1:]
}

// deploymentProxyEnv è la configurazione N9 da passare al pod: nessuna per un'app con l'uscita ristretta.
func deploymentProxyEnv(o BuildOptions) map[string]string {
	if o.EgressRestricted {
		return nil
	}
	return o.ProxyEnv
}

// HasProxy dice se la configurazione N9 contiene un proxy di uscita (HTTP_PROXY o HTTPS_PROXY validi).
func HasProxy(env map[string]string) bool {
	return len(proxyEndpoints(env)) > 0
}

// envVars compone l'ambiente del container: variabili dell'app, poi quelle di sistema (che non si
// sovrascrivono), poi la configurazione N9 del processo core, infine PORT.
func envVars(vars map[string]string, port *int, system, proxy map[string]string) []corev1.EnvVar {
	values := make(map[string]string, len(vars)+len(system)+len(proxy)+1)
	for k, v := range vars {
		values[k] = v
	}
	for k, v := range system {
		values[k] = v
	}
	for k, v := range proxy {
		values[k] = v
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]corev1.EnvVar, 0, len(keys)+1)
	for _, k := range keys {
		out = append(out, corev1.EnvVar{Name: k, Value: values[k]})
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

// ProxyEnvFrom prende solo le variabili N9 del processo core. Il controller non
// propaga altro ambiente nei pod delle app.
func ProxyEnvFrom(lookup func(string) string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		if v := lookup(k); v != "" {
			out[k] = v
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
	ports := []corev1.ServicePort{{Name: "http", Port: servicePort, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP}}
	// H15: con il nome interno (http://api:5432) le altre app usano la porta dichiarata dall'app,
	// non solo la 80 di ingress e forward-auth. Se la dichiarata è la 80 basta la prima.
	if *cfg.Port != servicePort {
		ports = append(ports, corev1.ServicePort{Name: DeclaredPortName, Port: int32(*cfg.Port), TargetPort: intstr.FromInt32(int32(*cfg.Port)), Protocol: corev1.ProtocolTCP})
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(app.ID), Labels: appLabels(app)},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabels(app.ID),
			Ports:    ports,
		},
	}
}

// SiblingAlias è un nome interno non pubblicabile: nome dell'app sorella e motivo.
type SiblingAlias struct {
	Name, Skipped string
}

// reservedAliasNames sono nomi che nel namespace dell'app hanno già un significato.
var reservedAliasNames = map[string]bool{WorkloadName: true, "kubernetes": true}

// BuildSiblingAliases dà i Service ExternalName da tenere nel namespace di `app`: uno per ogni
// altra app dello stesso repo con un Service (H15). Punta al Service `app` della sorella, che il
// controller non ricrea a ogni deploy: l'alias non cambia con deploy, rollback, riavvio e scala.
// Le app di altri repo non entrano mai; i nomi non pubblicabili tornano in skipped con il motivo.
func BuildSiblingAliases(app store.App, apps []store.App) (aliases []*corev1.Service, skipped []SiblingAlias) {
	for _, s := range apps {
		if s.ID == app.ID || s.RepoID != app.RepoID || s.Config.Port == nil {
			continue
		}
		switch {
		case reservedAliasNames[s.Name]:
			skipped = append(skipped, SiblingAlias{s.Name, "nome riservato nel namespace"})
			continue
		case !validAliasName(s.Name):
			skipped = append(skipped, SiblingAlias{s.Name, "non è un nome DNS valido (deve iniziare con una lettera)"})
			continue
		}
		l := appLabels(app)
		l[LabelSiblingAlias] = "true"
		aliases = append(aliases, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: Namespace(app.ID), Labels: l},
			Spec: corev1.ServiceSpec{
				Type:         corev1.ServiceTypeExternalName,
				ExternalName: WorkloadName + "." + Namespace(s.ID) + ".svc.cluster.local",
			},
		})
	}
	sort.Slice(aliases, func(i, j int) bool { return aliases[i].Name < aliases[j].Name })
	return aliases, skipped
}

// validAliasName: etichetta DNS-1035 (un Service non può iniziare con una cifra).
func validAliasName(n string) bool {
	if names.ValidateAppName(n) != nil {
		return false
	}
	return n[0] >= 'a' && n[0] <= 'z'
}

// BuildNamespace: PSS baseline per default (il root è permesso, H9), restricted per le app che lo
// chiedono; mai privilegi, hostPath o capability (ADR 0002).
func BuildNamespace(app store.App, restricted bool) *corev1.Namespace {
	level := "baseline"
	if restricted {
		level = "restricted"
	}
	l := appLabels(app)
	l["pod-security.kubernetes.io/enforce"] = level
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace(app.ID), Labels: l}}
}

// BuildNetworkPolicy crea l'unica policy dell'app. L'egress permette i peer
// dello stesso repo, il DNS, il proxy se configurato e internet aperto (H16) con le barriere
// di pkg/egress: rete privata (RFC 1918, ULA, CGNAT), loopback, link-local, cluster e nodo
// restano chiusi. Le porte di Kubernetes sono su indirizzi privati, quindi già coperte.
func BuildNetworkPolicy(app store.App, options ...NetworkPolicyOptions) *networkingv1.NetworkPolicy {
	o := NetworkPolicyOptions{TraefikNamespace: "kube-system"}
	if len(options) > 0 {
		o = options[0]
	}
	if o.TraefikNamespace == "" {
		o.TraefikNamespace = "kube-system"
	}
	labels := appLabels(app)
	peer := networkingv1.NetworkPolicyPeer{
		NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{LabelRepoID: app.RepoID.String()}},
		PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{LabelManaged: "true"}},
	}
	http := intstr.FromString("http")
	ingress := []networkingv1.NetworkPolicyIngressRule{
		{From: []networkingv1.NetworkPolicyPeer{peer}},
		{From: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": o.TraefikNamespace}}, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "traefik"}}}}, Ports: []networkingv1.NetworkPolicyPort{{Port: &http}}},
	}
	egress := []networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{peer}}}
	endpoints := proxyEndpoints(o.ProxyEnv)
	// DNS sempre: serve ai nomi interni delle app sorelle (H15) e ai proxy con nome. Nessun'altra uscita.
	{
		dnsPort := intstr.FromInt(53)
		dns := networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}}, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}}}
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{dns}, Ports: []networkingv1.NetworkPolicyPort{{Protocol: proto(corev1.ProtocolUDP), Port: &dnsPort}, {Protocol: proto(corev1.ProtocolTCP), Port: &dnsPort}}})
	}
	if o.Gateway.Valid() { // GIT-442: l'API di GitStack dal gateway nel cluster, solo la porta interna
		gwPort := intstr.FromInt(o.Gateway.Port)
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": o.Gateway.Namespace}},
				PodSelector:       &metav1.LabelSelector{MatchLabels: o.Gateway.PodLabels},
			}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: proto(corev1.ProtocolTCP), Port: &gwPort}},
		})
	}
	if o.Restriction != nil { // uscita ristretta (H16): solo gli indirizzi elencati, niente internet né proxy
		endpoints = nil
	}
	for _, endpoint := range endpoints {
		ports := []networkingv1.NetworkPolicyPort{{Port: &endpoint.port}}
		if endpoint.named {
			for _, ip := range o.ProxyIPs[endpoint.host] {
				egress = append(egress, proxyRule(ip.String(), ports))
			}
			continue
		}
		egress = append(egress, proxyRule(endpoint.host, ports))
	}
	policy := EgressPolicy(o.ClusterCIDRs, o.NodeAddresses, o.Deny)
	if o.Restriction == nil {
		egress = append(egress, internetRule(o))
		egress = append(egress, addressRules(policy, o.PrivateOpenings)...)
	} else {
		egress = append(egress, addressRules(policy, *o.Restriction)...)
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "app-isolation", Namespace: Namespace(app.ID), Labels: labels},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{LabelManaged: "true"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress:     ingress,
			Egress:      egress,
		},
	}
}

func proto(p corev1.Protocol) *corev1.Protocol { return &p }
func proxyRule(ip string, ports []networkingv1.NetworkPolicyPort) networkingv1.NetworkPolicyEgressRule {
	return networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: hostCIDR(ip)}}}, Ports: ports}
}

// hostCIDR dà /32 per un IPv4 e /128 per un IPv6; un testo che non è un IP resta com'era con /32.
func hostCIDR(ip string) string {
	if a, err := netip.ParseAddr(ip); err == nil {
		return netip.PrefixFrom(a.Unmap().WithZone(""), a.Unmap().BitLen()).String()
	}
	return ip + "/32"
}

// addressRules: una regola per indirizzo e porta (TCP), /32 o /128. Ogni voce passa ancora da
// egress.Policy.CheckOpening: loopback, link-local, cluster, nodo e porte di Kubernetes non si aprono
// qualunque cosa arrivi dal database. Ordine stabile, senza doppioni.
func addressRules(policy *egress.Policy, list []netip.AddrPort) []networkingv1.NetworkPolicyEgressRule {
	seen := OpenedSet{}
	for _, ap := range list {
		if policy.CheckOpening(ap.Addr(), ap.Port()) == nil {
			seen[netip.AddrPortFrom(ap.Addr().Unmap().WithZone(""), ap.Port())] = true
		}
	}
	var rules []networkingv1.NetworkPolicyEgressRule
	for _, ap := range sortedAddrPorts(seen) {
		port := intstr.FromInt(int(ap.Port()))
		rules = append(rules, networkingv1.NetworkPolicyEgressRule{
			To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: hostCIDR(ap.Addr().String())}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: proto(corev1.ProtocolTCP), Port: &port}},
		})
	}
	return rules
}

// internetRule apre tutto internet (IPv4 e IPv6) meno le eccezioni di pkg/egress (H16).
func internetRule(o NetworkPolicyOptions) networkingv1.NetworkPolicyEgressRule {
	policy, err := egress.NewPolicy(egress.Config{ClusterCIDRs: o.ClusterCIDRs, NodeAddresses: o.NodeAddresses})
	if err != nil { // configurazione non valida: restano i default, mai meno barriere
		policy, _ = egress.NewPolicy(egress.Config{})
	}
	v4, v6 := policy.NetworkExceptions()
	strs := func(l []netip.Prefix) []string {
		out := make([]string, len(l))
		for i, p := range l {
			out[i] = p.String()
		}
		return out
	}
	return networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{
		{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: strs(v4)}},
		{IPBlock: &networkingv1.IPBlock{CIDR: "::/0", Except: strs(v6)}},
	}}
}

type proxyEndpoint struct {
	host  string
	port  intstr.IntOrString
	named bool
}

func proxyEndpoints(env map[string]string) []proxyEndpoint {
	seen := map[string]bool{}
	var endpoints []proxyEndpoint
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		u, err := url.Parse(env[key])
		if env[key] == "" || err != nil || u.Hostname() == "" {
			continue
		}
		p := 80
		if u.Scheme == "https" {
			p = 443
		}
		if x := u.Port(); x != "" {
			if n, err := strconv.Atoi(x); err == nil {
				p = n
			}
		}
		host := u.Hostname()
		identity := host + ":" + strconv.Itoa(p)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		endpoints = append(endpoints, proxyEndpoint{host: host, port: intstr.FromInt(p), named: net.ParseIP(host) == nil})
	}
	return endpoints
}

// BuildRoute è l'IngressRoute Traefik `websecure` verso il Service (ADR 0002: tls {} e il
// certificato wildcard è il TLSStore default). I Middleware sono quelli del namespace dell'app:
// sempre gitstack-strip (le intestazioni X-GitStack-* del client non arrivano all'app) e, con
// `login`, subito dopo gitstack-auth (il forward-auth del login GitStack, H6).
func BuildRoute(app store.App, host string, login bool) *unstructured.Unstructured {
	mws := []any{map[string]any{"name": MiddlewareStrip}}
	if login {
		mws = append(mws, map[string]any{"name": MiddlewareAuth})
	}
	route := map[string]any{
		"kind":  "Rule",
		"match": fmt.Sprintf("Host(`%s`)", host),
		"services": []any{map[string]any{
			"name": WorkloadName,
			"port": int64(servicePort),
		}},
		"middlewares": mws,
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

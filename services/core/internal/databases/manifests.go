package databases

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Manifest di un database gestito (ADR 0003, DB4/DB5): un namespace gitstack-db-<id> con StatefulSet a
// 1 replica, PVC local-path, Service ClusterIP e NetworkPolicy deny di default. Funzioni pure: i test
// dei manifest le verificano senza cluster.

const (
	// NamePrefix e' il prefisso del namespace; la VAP del chart lo richiede insieme alla label managed-db.
	NamePrefix = "gitstack-db-"
	// LabelManagedDB marca i namespace dei database: i verbi del ClusterRole modello valgono solo li' (DB4).
	LabelManagedDB = "gitstack.io/managed-db"
	// LabelDBID lega ogni oggetto all'id del database: l'eliminazione non tocca un namespace di altri.
	LabelDBID = "gitstack.io/db-id"
	// LabelDBLinkPrefix: la label del namespace dell'app collegata e' gitstack.io/db-link-<id-db>=true.
	LabelDBLinkPrefix = "gitstack.io/db-link-"

	// WorkloadName e' il nome di StatefulSet, Service e container; il pod si chiama pg-0.
	WorkloadName = "pg"
	PodName      = "pg-0"
	// RoleBindingName e' il RoleBinding verso il ClusterRole modello (GIT-565), solo nel namespace del database.
	RoleBindingName = "gitstack-db-controller"

	SecretAuth   = "pg-auth"
	SecretTLS    = "pg-tls"
	SecretConfig = "pg-config"

	// AdminRole e' il superuser del controller: solo da socket locale (DB7).
	AdminRole = "gitstack_admin"
	// PGData e' dentro un sottopercorso del volume: la radice del PVC local-path non accetta chmod (ADR punto 2).
	PGData = "/var/lib/postgresql/data/pgdata"
	// PostgresUID e' l'utente dell'immagine alpine ufficiale.
	PostgresUID int64 = 70

	StorageClass  = "local-path"
	PriorityClass = "gitstack-apps"
	PostgresPort  = 5432

	annotationSpecHash = "gitstack.io/spec-hash"
)

// Namespace e' il namespace del database.
func Namespace(id uuid.UUID) string { return NamePrefix + id.String() }

// ServiceHost e' il nome interno nei SAN del certificato (ADR punto 4): pg.gitstack-db-<id>.svc.cluster.local.
func ServiceHost(id uuid.UUID) string {
	return WorkloadName + "." + Namespace(id) + ".svc.cluster.local"
}

// Image e' una voce della tabella major -> immagine per digest (GITSTACK_CORE_DATABASES_POSTGRES_IMAGES).
type Image struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest"`
}

// Ref e' il riferimento per digest: repository@sha256:...
func (i Image) Ref() string { return i.Repository + "@" + i.Digest }

// ParseImages legge la tabella dal JSON del chart.
func ParseImages(raw string) (map[int]Image, error) {
	var in map[string]Image
	if raw == "" {
		return map[int]Image{}, nil
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil, fmt.Errorf("tabella delle immagini dei database: %w", err)
	}
	out := make(map[int]Image, len(in))
	for k, v := range in {
		major, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("major %q non numerica", k)
		}
		if v.Repository == "" || len(v.Digest) != len("sha256:")+64 || v.Digest[:7] != "sha256:" {
			return nil, fmt.Errorf("major %d: serve repository e digest sha256", major)
		}
		out[major] = v
	}
	return out, nil
}

func selectorLabels(id uuid.UUID) map[string]string {
	return map[string]string{LabelDBID: id.String(), "app.kubernetes.io/name": WorkloadName}
}

func objectLabels(id uuid.UUID) map[string]string {
	l := selectorLabels(id)
	l[LabelManagedDB] = "true"
	l["app.kubernetes.io/managed-by"] = "gitstack-core"
	return l
}

// BuildNamespace: label managed-db e Pod Security restricted (DB4).
func BuildNamespace(db store.ManagedDB) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace(db.ID), Labels: map[string]string{
		LabelManagedDB:                       "true",
		LabelDBID:                            db.ID.String(),
		"pod-security.kubernetes.io/enforce": "restricted",
		"pod-security.kubernetes.io/audit":   "restricted",
		"app.kubernetes.io/managed-by":       "gitstack-core",
	}}}
}

// BuildRoleBinding lega il ServiceAccount di core al ClusterRole modello nel solo namespace del database.
func BuildRoleBinding(ns, saNamespace, saName, clusterRole string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: RoleBindingName, Namespace: ns, Labels: map[string]string{LabelManagedDB: "true"}},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: saName, Namespace: saNamespace}},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: clusterRole},
	}
}

// HBA e' pg_hba.conf (DB7, DB8): gitstack_admin solo dal socket locale, in rete solo hostssl con
// scram-sha-256 e hostnossl rifiutato.
const HBA = `# Generato da GitStack (ADR 0003, DB7/DB8): non modificare.
local   all   ` + AdminRole + `   trust
local   all   all                reject
hostssl all   ` + AdminRole + `   0.0.0.0/0   reject
hostssl all   ` + AdminRole + `   ::/0        reject
hostssl all   all                0.0.0.0/0   scram-sha-256
hostssl all   all                ::/0        scram-sha-256
hostnossl all all                0.0.0.0/0   reject
hostnossl all all                ::/0        reject
`

// BuildConfigSecret: pg_hba.conf in un Secret (il ClusterRole modello non ha verbi sui ConfigMap).
func BuildConfigSecret(db store.ManagedDB) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: SecretConfig, Namespace: Namespace(db.ID), Labels: objectLabels(db.ID)},
		Data: map[string][]byte{"pg_hba.conf": []byte(HBA)}}
}

// BuildService: ClusterIP sulla 5432.
func BuildService(db store.ManagedDB) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(db.ID), Labels: objectLabels(db.ID)},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: selectorLabels(db.ID),
			Ports: []corev1.ServicePort{{Name: "postgres", Port: PostgresPort, TargetPort: intstr.FromInt32(PostgresPort), Protocol: corev1.ProtocolTCP}}}}
}

// NetworkPolicyName e' la NetworkPolicy deny di default (ADR punto 4): ingresso solo dai namespace delle
// app collegate (label db-link-<id>), nessuna uscita.
const NetworkPolicyName = "db-isolation"

func BuildNetworkPolicy(db store.ManagedDB) *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt32(PostgresPort)
	return &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: NetworkPolicyName, Namespace: Namespace(db.ID), Labels: objectLabels(db.ID)},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{LabelDBLinkPrefix + db.ID.String(): "true"}}}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
			}},
		}}
}

// Accesso dalla LAN (GIT-554, ADR 0003 punto 6): Service LoadBalancer (ServiceLB di k3s, come la porta SSH
// di git) sulla porta assegnata al database, piu' la NetworkPolicy db-lan che ammette l'ingresso da fuori dal
// CIDR dei pod. db-isolation resta com'e': le app non collegate restano respinte.
const (
	LANServiceName       = "pg-lan"
	LANNetworkPolicyName = "db-lan"
	// DefaultPodCIDR e' il CIDR dei pod di k3s: la LAN non e' la rete delle app, quindi e' escluso.
	DefaultPodCIDR = "10.42.0.0/16"
)

// BuildLANService e' il Service LoadBalancer del database sulla porta LAN assegnata.
func BuildLANService(db store.ManagedDB, port int) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: LANServiceName, Namespace: Namespace(db.ID), Labels: objectLabels(db.ID)},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, Selector: selectorLabels(db.ID),
			Ports: []corev1.ServicePort{{Name: "postgres-lan", Port: int32(port), TargetPort: intstr.FromInt32(PostgresPort), Protocol: corev1.ProtocolTCP}}}}
}

// BuildLANNetworkPolicy ammette la 5432 da qualunque sorgente tranne il CIDR dei pod (la rete delle app).
func BuildLANNetworkPolicy(db store.ManagedDB, podCIDR string) *networkingv1.NetworkPolicy {
	if podCIDR == "" {
		podCIDR = DefaultPodCIDR
	}
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt32(PostgresPort)
	return &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: LANNetworkPolicyName, Namespace: Namespace(db.ID), Labels: objectLabels(db.ID)},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{podCIDR}}}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
			}},
		}}
}

// StatefulSetOptions sono le scelte d'istanza che entrano nello StatefulSet.
type StatefulSetOptions struct {
	Image         Image
	PriorityClass string
	StorageClass  string
}

func bptr(b bool) *bool    { return &b }
func i64(v int64) *int64   { return &v }
func i32(v int32) *int32   { return &v }
func str(s string) *string { return &s }

// BuildStatefulSet: pod non root uid 70, drop ALL, root fs in sola lettura, seccomp RuntimeDefault, PGDATA
// in un sottopercorso del volume (DB4, DB5). La dimensione dichiarata e' la richiesta del PVC, ma
// `local-path` non la applica: il limite e' morbido (ADR punto 3).
func BuildStatefulSet(db store.ManagedDB, o StatefulSetOptions) *appsv1.StatefulSet {
	if o.PriorityClass == "" {
		o.PriorityClass = PriorityClass
	}
	if o.StorageClass == "" {
		o.StorageClass = StorageClass
	}
	mem := *resource.NewQuantity(db.MemoryBytes, resource.BinarySI)
	cpu := *resource.NewMilliQuantity(int64(db.CPUMillis), resource.DecimalSI)
	size := *resource.NewQuantity(db.SizeBytes, resource.BinarySI)
	secretMode := int32(0o440)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: WorkloadName, Namespace: Namespace(db.ID), Labels: objectLabels(db.ID)},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    i32(1),
			ServiceName: WorkloadName,
			Selector:    &metav1.LabelSelector{MatchLabels: selectorLabels(db.ID)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: objectLabels(db.ID)},
				Spec: corev1.PodSpec{
					PriorityClassName:             o.PriorityClass,
					AutomountServiceAccountToken:  bptr(false),
					TerminationGracePeriodSeconds: i64(60),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: bptr(true), RunAsUser: i64(PostgresUID), RunAsGroup: i64(PostgresUID), FSGroup: i64(PostgresUID),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:  WorkloadName,
						Image: o.Image.Ref(),
						Args: []string{
							"-c", "listen_addresses=*",
							"-c", "ssl=on",
							"-c", "ssl_cert_file=/etc/gitstack/pg-tls/tls.crt",
							"-c", "ssl_key_file=/etc/gitstack/pg-tls/tls.key",
							"-c", "hba_file=/etc/gitstack/pg-config/pg_hba.conf",
							"-c", "password_encryption=scram-sha-256",
						},
						Env: []corev1.EnvVar{
							{Name: "POSTGRES_USER", Value: AdminRole},
							{Name: "POSTGRES_DB", Value: "postgres"},
							// trust vale solo per initdb e il socket locale: pg_hba.conf sopra vieta gitstack_admin in rete.
							{Name: "POSTGRES_HOST_AUTH_METHOD", Value: "trust"},
							{Name: "PGDATA", Value: PGData},
						},
						Ports: []corev1.ContainerPort{{Name: "postgres", ContainerPort: PostgresPort}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem},
							Limits:   corev1.ResourceList{corev1.ResourceMemory: mem},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: bptr(false), ReadOnlyRootFilesystem: bptr(true), RunAsNonRoot: bptr(true),
							RunAsUser: i64(PostgresUID), RunAsGroup: i64(PostgresUID),
							Capabilities:   &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
						ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{
							Command: []string{"pg_isready", "-h", "/var/run/postgresql", "-U", AdminRole}}},
							InitialDelaySeconds: 3, PeriodSeconds: 3, FailureThreshold: 20},
						LivenessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{
							Command: []string{"pg_isready", "-h", "/var/run/postgresql", "-U", AdminRole}}},
							InitialDelaySeconds: 60, PeriodSeconds: 10, FailureThreshold: 6},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "data", MountPath: "/var/lib/postgresql/data"},
							{Name: "run", MountPath: "/var/run/postgresql"},
							{Name: "tmp", MountPath: "/tmp"},
							{Name: "pg-tls", MountPath: "/etc/gitstack/pg-tls", ReadOnly: true},
							{Name: "pg-config", MountPath: "/etc/gitstack/pg-config", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "run", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "pg-tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: SecretTLS, DefaultMode: &secretMode}}},
						{Name: "pg-config", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: SecretConfig}}},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "data", Labels: objectLabels(db.ID)},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					StorageClassName: str(o.StorageClass),
					Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}},
				},
			}},
		},
	}
	sts.Annotations = map[string]string{annotationSpecHash: specHash(sts)}
	return sts
}

// specHash e' l'impronta dello StatefulSet voluto, senza i campi immutabili: serve a riscriverlo solo se cambia.
func specHash(sts *appsv1.StatefulSet) string {
	b, _ := json.Marshal(sts.Spec.Template)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

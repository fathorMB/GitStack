package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotFound indica che la risorsa chiesta al cluster non esiste.
var ErrNotFound = errors.New("risorsa non trovata")

// Secret è un Secret Kubernetes ripulito (senza uid, resourceVersion,
// namespace), pronto per essere salvato e riapplicato.
type Secret struct {
	Name string          `json:"name"`
	Raw  json.RawMessage `json:"object"`
}

// Cluster è tutto ciò che backup e restore chiedono al cluster. In
// produzione lo realizza KubectlCluster; i test usano un finto.
type Cluster interface {
	// Replicas legge le repliche desiderate del Deployment; found=false se
	// non esiste (componente disattivato).
	Replicas(ctx context.Context, deployment string) (n int, found bool, err error)
	// Scale imposta le repliche del Deployment.
	Scale(ctx context.Context, deployment string, n int) error
	// WaitStopped aspetta che non ci siano più pod del componente.
	WaitStopped(ctx context.Context, component string) error
	// WaitReady aspetta che il Deployment abbia le repliche pronte.
	WaitReady(ctx context.Context, deployment string) error
	// Restart fa ripartire i pod del Deployment (rollout restart); chi
	// chiama aspetta poi WaitReady.
	Restart(ctx context.Context, deployment string) error
	// StatefulSetExists dice se lo StatefulSet esiste (NATS del chart; con
	// NATS esterno o disattivato non c'è).
	StatefulSetExists(ctx context.Context, name string) (bool, error)
	// RestartStatefulSet fa ripartire i pod dello StatefulSet (rollout
	// restart); chi chiama aspetta poi WaitStatefulSetReady.
	RestartStatefulSet(ctx context.Context, name string) error
	// WaitStatefulSetReady aspetta che il rollout dello StatefulSet sia finito.
	WaitStatefulSetReady(ctx context.Context, name string) error
	// PGExec esegue `args` nel pod di Postgres (o, con il database esterno,
	// nel pod client) con stdin/stdout collegati.
	PGExec(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error
	// VolumePath è la cartella sull'host del volume del PVC (ErrNotFound se
	// il PVC non esiste).
	VolumePath(ctx context.Context, pvc string) (string, error)
	// Secrets elenca i Secret di GitStack (label part-of=gitstack).
	Secrets(ctx context.Context) ([]Secret, error)
	// ApplySecret crea o aggiorna il Secret.
	ApplySecret(ctx context.Context, s Secret) error
}

// ExecFunc esegue un comando con stdin/stdout collegati. Iniettabile.
type ExecFunc func(ctx context.Context, env []string, stdin io.Reader, stdout io.Writer, name string, args ...string) error

// ExecStream è l'ExecFunc di produzione: lo stderr, che per kubectl e
// pg_dump contiene solo diagnostica, finisce nell'errore (mai nei log
// normali).
func ExecStream(ctx context.Context, env []string, stdin io.Reader, stdout io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 2000 {
			msg = msg[:2000] + "…"
		}
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// KubectlCluster realizza Cluster con kubectl (o `k3s kubectl`).
type KubectlCluster struct {
	Bin        string   // "kubectl" o il percorso di k3s
	Pre        []string // ["kubectl"] se Bin è k3s
	Kubeconfig string
	Namespace  string
	Release    string
	// ExternalPG: il database è un Postgres esterno (N8). I comandi di
	// database girano nel pod client <release>-pgclient (psql e pg_dump già
	// configurati con host, utente, TLS e password del Secret), non nello
	// StatefulSet del Postgres bundle, che in questo caso non esiste.
	ExternalPG bool
	Exec       ExecFunc
}

func (k *KubectlCluster) run(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	return k.runIn(ctx, k.Namespace, stdin, stdout, args...)
}

// runIn esegue kubectl nel namespace dato (i volumi delle app stanno nei namespace delle app).
func (k *KubectlCluster) runIn(ctx context.Context, ns string, stdin io.Reader, stdout io.Writer, args ...string) error {
	ex := k.Exec
	if ex == nil {
		ex = ExecStream
	}
	full := append(append([]string{}, k.Pre...), "-n", ns)
	full = append(full, args...)
	return ex(ctx, []string{"KUBECONFIG=" + k.Kubeconfig}, stdin, stdout, k.Bin, full...)
}

func (k *KubectlCluster) out(ctx context.Context, args ...string) ([]byte, error) {
	return k.outIn(ctx, k.Namespace, args...)
}

func (k *KubectlCluster) outIn(ctx context.Context, ns string, args ...string) ([]byte, error) {
	var b bytes.Buffer
	err := k.runIn(ctx, ns, nil, &b, args...)
	return b.Bytes(), err
}

// Replicas implementa Cluster.
func (k *KubectlCluster) Replicas(ctx context.Context, deployment string) (int, bool, error) {
	var b bytes.Buffer
	err := k.run(ctx, nil, &b, "get", "deployment", deployment, "--ignore-not-found", "-o", "jsonpath={.spec.replicas}")
	if err != nil {
		return 0, false, err
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		// --ignore-not-found stampa niente sia se manca il Deployment, sia
		// se spec.replicas è assente: lo distingue una seconda lettura.
		o, err := k.out(ctx, "get", "deployment", deployment, "--ignore-not-found", "-o", "name")
		if err != nil {
			return 0, false, err
		}
		if strings.TrimSpace(string(o)) == "" {
			return 0, false, nil
		}
		return 1, true, nil // replicas assente = default 1
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, false, fmt.Errorf("repliche di %s non leggibili: %q", deployment, s)
	}
	return n, true, nil
}

// Scale implementa Cluster.
func (k *KubectlCluster) Scale(ctx context.Context, deployment string, n int) error {
	return k.run(ctx, nil, io.Discard, "scale", "deployment", deployment, fmt.Sprintf("--replicas=%d", n))
}

func (k *KubectlCluster) selector(component string) string {
	return "app.kubernetes.io/name=" + component + ",app.kubernetes.io/instance=" + k.Release
}

// WaitStopped implementa Cluster.
func (k *KubectlCluster) WaitStopped(ctx context.Context, component string) error {
	// `kubectl wait --for=delete` non fallisce se non ci sono pod.
	return k.run(ctx, nil, io.Discard, "wait", "--for=delete", "pod", "-l", k.selector(component), "--timeout=180s")
}

// WaitReady implementa Cluster.
func (k *KubectlCluster) WaitReady(ctx context.Context, deployment string) error {
	return k.run(ctx, nil, io.Discard, "rollout", "status", "deployment/"+deployment, "--timeout=300s")
}

// Restart implementa Cluster.
func (k *KubectlCluster) Restart(ctx context.Context, deployment string) error {
	return k.run(ctx, nil, io.Discard, "rollout", "restart", "deployment/"+deployment)
}

// StatefulSetExists implementa Cluster.
func (k *KubectlCluster) StatefulSetExists(ctx context.Context, name string) (bool, error) {
	o, err := k.out(ctx, "get", "statefulset", name, "--ignore-not-found", "-o", "name")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(o)) != "", nil
}

// RestartStatefulSet implementa Cluster.
func (k *KubectlCluster) RestartStatefulSet(ctx context.Context, name string) error {
	return k.run(ctx, nil, io.Discard, "rollout", "restart", "statefulset/"+name)
}

// WaitStatefulSetReady implementa Cluster.
func (k *KubectlCluster) WaitStatefulSetReady(ctx context.Context, name string) error {
	return k.run(ctx, nil, io.Discard, "rollout", "status", "statefulset/"+name, "--timeout=300s")
}

// PGExec implementa Cluster.
func (k *KubectlCluster) PGExec(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	target := "statefulset/" + k.Release + "-postgres"
	if k.ExternalPG {
		target = "deployment/" + k.Release + "-pgclient"
	}
	full := []string{"exec", "-i", target, "--"}
	return k.run(ctx, stdin, stdout, append(full, args...)...)
}

// VolumePath implementa Cluster: risale dal PVC al PV e ne legge il
// percorso locale (local-path di k3s: hostPath o local).
func (k *KubectlCluster) VolumePath(ctx context.Context, pvc string) (string, error) {
	return k.volumePath(ctx, k.Namespace, pvc)
}

func (k *KubectlCluster) volumePath(ctx context.Context, ns, pvc string) (string, error) {
	o, err := k.outIn(ctx, ns, "get", "pvc", pvc, "--ignore-not-found", "-o", "jsonpath={.spec.volumeName}")
	if err != nil {
		return "", err
	}
	pv := strings.TrimSpace(string(o))
	if pv == "" {
		chk, err := k.outIn(ctx, ns, "get", "pvc", pvc, "--ignore-not-found", "-o", "name")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(string(chk)) == "" {
			return "", fmt.Errorf("%w: pvc %s", ErrNotFound, pvc)
		}
		return "", fmt.Errorf("%w: %s", ErrNotBound, pvc)
	}
	var spec struct {
		Spec struct {
			HostPath *struct {
				Path string `json:"path"`
			} `json:"hostPath"`
			Local *struct {
				Path string `json:"path"`
			} `json:"local"`
		} `json:"spec"`
	}
	// Un PV non ha namespace: il flag -n è ignorato da kubectl.
	o, err = k.outIn(ctx, ns, "get", "pv", pv, "-o", "json")
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(o, &spec); err != nil {
		return "", fmt.Errorf("risposta di kubectl non valida per il volume %s: %w", pv, err)
	}
	switch {
	case spec.Spec.Local != nil && spec.Spec.Local.Path != "":
		return spec.Spec.Local.Path, nil
	case spec.Spec.HostPath != nil && spec.Spec.HostPath.Path != "":
		return spec.Spec.HostPath.Path, nil
	}
	return "", fmt.Errorf("il volume %s non è una cartella locale dell'host (local-path): non supportato", pv)
}

// Secrets implementa Cluster.
func (k *KubectlCluster) Secrets(ctx context.Context) ([]Secret, error) {
	o, err := k.out(ctx, "get", "secrets", "-l", "app.kubernetes.io/part-of=gitstack", "-o", "json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Type       string            `json:"type"`
			Data       map[string]string `json:"data"`
			StringData map[string]string `json:"stringData"`
		} `json:"items"`
	}
	if err := json.Unmarshal(o, &list); err != nil {
		return nil, fmt.Errorf("risposta di kubectl non valida: %w", err)
	}
	var res []Secret
	for _, it := range list.Items {
		ann := map[string]string{}
		if v, ok := it.Metadata.Annotations["helm.sh/resource-policy"]; ok {
			ann["helm.sh/resource-policy"] = v
		}
		meta := map[string]any{"name": it.Metadata.Name, "labels": it.Metadata.Labels}
		if len(ann) > 0 {
			meta["annotations"] = ann
		}
		obj := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": meta, "type": it.Type, "data": it.Data}
		raw, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		res = append(res, Secret{Name: it.Metadata.Name, Raw: raw})
	}
	return res, nil
}

// ApplySecret implementa Cluster.
func (k *KubectlCluster) ApplySecret(ctx context.Context, s Secret) error {
	return k.run(ctx, bytes.NewReader(s.Raw), io.Discard, "apply", "-f", "-")
}

// AppVolumePath implementa AppVolumeCluster.
func (k *KubectlCluster) AppVolumePath(ctx context.Context, v AppVolumeRef) (string, error) {
	return k.volumePath(ctx, v.Namespace(), v.PVC())
}

// StopApp implementa AppVolumeCluster: il Deployment "app" del namespace dell'app va a zero repliche.
// Core, che in restore riparte per ultimo, riporta le copie volute (Sync).
func (k *KubectlCluster) StopApp(ctx context.Context, v AppVolumeRef) error {
	ns := v.Namespace()
	o, err := k.outIn(ctx, ns, "get", "deployment", "app", "--ignore-not-found", "-o", "name")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(o)) == "" {
		return nil
	}
	if err := k.runIn(ctx, ns, nil, io.Discard, "scale", "deployment", "app", "--replicas=0"); err != nil {
		return err
	}
	return k.runIn(ctx, ns, nil, io.Discard, "wait", "--for=delete", "pod", "-l", "gitstack.io/app-id="+v.AppID, "--timeout=180s")
}

// EnsureAppVolume implementa AppVolumeCluster. Su un'installazione pulita il namespace e il PVC
// dell'app non esistono e local-path crea la cartella solo al primo pod: qui si crea la cartella
// sotto base e si legano a mano un PersistentVolume (hostPath, come quelli di local-path, con
// l'annotazione del provisioner perché sappia toglierlo) e un PVC con le stesse label e dimensione
// del controller. Il controller di core, riapplicando l'app, trova PVC e dati già al loro posto.
func (k *KubectlCluster) EnsureAppVolume(ctx context.Context, v AppVolumeRef, base string) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	ns, pvc := v.Namespace(), v.PVC()
	p, err := k.volumePath(ctx, ns, pvc)
	switch {
	case err == nil:
		return p, nil
	case errors.Is(err, ErrNotBound):
		// Un PVC mai usato non ha dati: lo si ricrea legato alla cartella ripristinata.
		if e := k.runIn(ctx, ns, nil, io.Discard, "delete", "pvc", pvc, "--wait=true", "--timeout=60s"); e != nil {
			return "", fmt.Errorf("eliminazione del PVC %s non usato: %w", pvc, e)
		}
	case !errors.Is(err, ErrNotFound):
		return "", err
	}
	node, err := k.outIn(ctx, ns, "get", "nodes", "-o", "jsonpath={.items[0].metadata.labels.kubernetes\\.io/hostname}")
	if err != nil {
		return "", fmt.Errorf("nome del nodo: %w", err)
	}
	host := strings.TrimSpace(string(node))
	if host == "" {
		return "", errors.New("nome del nodo non trovato")
	}
	pv := "pvc-restore-" + v.AppID + "-" + v.Name
	dir := filepath.Join(base, pv+"_"+ns+"_"+pvc)
	if err := ensureDir(dir); err != nil {
		return "", fmt.Errorf("cartella del volume: %w", err)
	}
	size := strconv.FormatInt(v.SizeBytes, 10)
	appLabels := map[string]string{
		"gitstack.io/app-id": v.AppID, "app.kubernetes.io/name": "app",
		"gitstack.io/managed-app": "true", "app.kubernetes.io/managed-by": "gitstack-core",
	}
	if v.RepoID != "" {
		appLabels["gitstack.io/repo-id"] = v.RepoID
	}
	nsLabels := map[string]string{}
	pvcLabels := map[string]string{"gitstack.io/volume": v.Name}
	for key, val := range appLabels {
		nsLabels[key] = val
		pvcLabels[key] = val
	}
	nsLabels["pod-security.kubernetes.io/enforce"] = "baseline"
	class := "local-path"
	objs := []map[string]any{
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns, "labels": nsLabels}},
		{"apiVersion": "v1", "kind": "PersistentVolume",
			"metadata": map[string]any{"name": pv, "annotations": map[string]string{"pv.kubernetes.io/provisioned-by": "rancher.io/local-path"}},
			"spec": map[string]any{
				"capacity": map[string]string{"storage": size}, "accessModes": []string{"ReadWriteOnce"},
				"persistentVolumeReclaimPolicy": "Delete", "storageClassName": class, "volumeMode": "Filesystem",
				"claimRef": map[string]string{"namespace": ns, "name": pvc},
				"hostPath": map[string]string{"path": dir, "type": "DirectoryOrCreate"},
				"nodeAffinity": map[string]any{"required": map[string]any{"nodeSelectorTerms": []any{map[string]any{
					"matchExpressions": []any{map[string]any{"key": "kubernetes.io/hostname", "operator": "In", "values": []string{host}}}}}}},
			}},
		{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]any{"name": pvc, "namespace": ns, "labels": pvcLabels},
			"spec": map[string]any{
				"accessModes": []string{"ReadWriteOnce"}, "storageClassName": class, "volumeName": pv,
				"resources": map[string]any{"requests": map[string]string{"storage": size}},
			}},
	}
	list := map[string]any{"apiVersion": "v1", "kind": "List", "items": objs}
	body, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	if err := k.runIn(ctx, ns, bytes.NewReader(body), io.Discard, "apply", "-f", "-"); err != nil {
		return "", fmt.Errorf("creazione di namespace, volume e PVC: %w", err)
	}
	return dir, nil
}

// Traefik di k3s: HelmChart `traefik` in kube-system, con volume persistente `traefik` per acme.json.
const (
	traefikNamespace = "kube-system"
	traefikName      = "traefik"
)

// ACMEPath implementa TraefikCluster.
func (k *KubectlCluster) ACMEPath(ctx context.Context) (string, error) {
	return k.volumePath(ctx, traefikNamespace, traefikName)
}

// StopTraefik implementa TraefikCluster.
func (k *KubectlCluster) StopTraefik(ctx context.Context) (int, error) {
	var b bytes.Buffer
	if err := k.runIn(ctx, traefikNamespace, nil, &b, "get", "deployment", traefikName, "--ignore-not-found", "-o", "jsonpath={.spec.replicas}"); err != nil {
		return 0, err
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		o, err := k.outIn(ctx, traefikNamespace, "get", "deployment", traefikName, "--ignore-not-found", "-o", "name")
		if err != nil {
			return 0, err
		}
		if strings.TrimSpace(string(o)) == "" {
			return 0, nil
		}
		s = "1"
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("repliche di Traefik non leggibili: %q", s)
	}
	if n == 0 {
		return 0, nil
	}
	if err := k.runIn(ctx, traefikNamespace, nil, io.Discard, "scale", "deployment", traefikName, "--replicas=0"); err != nil {
		return 0, err
	}
	err = k.runIn(ctx, traefikNamespace, nil, io.Discard, "wait", "--for=delete", "pod", "-l", "app.kubernetes.io/name=traefik", "--timeout=180s")
	return n, err
}

// StartTraefik implementa TraefikCluster.
func (k *KubectlCluster) StartTraefik(ctx context.Context, n int) error {
	if n <= 0 {
		return nil
	}
	if err := k.runIn(ctx, traefikNamespace, nil, io.Discard, "scale", "deployment", traefikName, fmt.Sprintf("--replicas=%d", n)); err != nil {
		return err
	}
	return k.runIn(ctx, traefikNamespace, nil, io.Discard, "rollout", "status", "deployment/"+traefikName, "--timeout=300s")
}

// Verifica a compile time.
var (
	_ TraefikCluster   = (*KubectlCluster)(nil)
	_ Cluster          = (*KubectlCluster)(nil)
	_ AppVolumeCluster = (*KubectlCluster)(nil)
)

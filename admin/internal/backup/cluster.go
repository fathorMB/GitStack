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
	// PGExec esegue `args` nel pod di Postgres con stdin/stdout collegati.
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
	Exec       ExecFunc
}

func (k *KubectlCluster) run(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	ex := k.Exec
	if ex == nil {
		ex = ExecStream
	}
	full := append(append([]string{}, k.Pre...), "-n", k.Namespace)
	full = append(full, args...)
	return ex(ctx, []string{"KUBECONFIG=" + k.Kubeconfig}, stdin, stdout, k.Bin, full...)
}

func (k *KubectlCluster) out(ctx context.Context, args ...string) ([]byte, error) {
	var b bytes.Buffer
	err := k.run(ctx, nil, &b, args...)
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

// PGExec implementa Cluster.
func (k *KubectlCluster) PGExec(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	full := []string{"exec", "-i", "statefulset/" + k.Release + "-postgres", "--"}
	return k.run(ctx, stdin, stdout, append(full, args...)...)
}

// VolumePath implementa Cluster: risale dal PVC al PV e ne legge il
// percorso locale (local-path di k3s: hostPath o local).
func (k *KubectlCluster) VolumePath(ctx context.Context, pvc string) (string, error) {
	o, err := k.out(ctx, "get", "pvc", pvc, "--ignore-not-found", "-o", "jsonpath={.spec.volumeName}")
	if err != nil {
		return "", err
	}
	pv := strings.TrimSpace(string(o))
	if pv == "" {
		chk, err := k.out(ctx, "get", "pvc", pvc, "--ignore-not-found", "-o", "name")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(string(chk)) == "" {
			return "", fmt.Errorf("%w: pvc %s", ErrNotFound, pvc)
		}
		return "", fmt.Errorf("pvc %s non ancora associato a un volume", pvc)
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
	o, err = k.out(ctx, "get", "pv", pv, "-o", "json")
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

// Verifica a compile time.
var _ Cluster = (*KubectlCluster)(nil)

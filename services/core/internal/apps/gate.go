package apps

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// GIT-418 (H9, H16): cancello fail-closed all'avvio dei pod delle app.
//
// Il controller di NetworkPolicy di k3s (kube-router) applica le regole a un pod DOPO il suo
// avvio, non prima. Misurato su k3d (k3s v1.36.4): nei primi secondi un pod in un namespace app
// raggiunge l'API Kubernetes (6443), il kubelet (10250) e ogni servizio sull'IP del nodo; a
// regime tutto questo è chiuso (le catene INPUT/FORWARD di kube-router filtrano anche il
// traffico pod→nodo). L'app parte quindi solo dopo che la barriera in uscita è attiva: un init
// container aspetta che l'API Kubernetes (sempre raggiungibile senza policy, sempre chiusa con
// quella delle app) smetta di rispondere. Le regole di un pod sono installate insieme, quindi
// «API chiusa» vale anche per il resto della rete privata e del nodo.

// GateContainerName è il nome dell'init container del cancello.
const GateContainerName = "gate"

// GateTimeout è l'attesa massima del cancello: oltre, l'init container esce con errore (l'app
// non parte; Kubernetes lo riprova) invece di far partire l'app senza barriera.
const GateTimeout = 60 * time.Second

// GateCommand è il comando del cancello nell'immagine di core.
var GateCommand = []string{"/usr/local/bin/core", "apps-gate"}

// GateContainer costruisce l'init container del cancello con l'immagine e il comando dati
// (per i pod delle app: l'immagine di core e GateCommand).
func GateContainer(image string, command []string) corev1.Container {
	f, t := false, true
	uid := int64(65532)
	small := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20m"), corev1.ResourceMemory: resource.MustParse("32Mi")}
	return corev1.Container{
		Name:            GateContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         command,
		Resources:       corev1.ResourceRequirements{Requests: small, Limits: small},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &f,
			ReadOnlyRootFilesystem:   &t,
			RunAsNonRoot:             &t,
			RunAsUser:                &uid,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
}

// RunGate aspetta che l'API Kubernetes (KUBERNETES_SERVICE_HOST:KUBERNETES_SERVICE_PORT, che il
// kubelet inietta sempre) diventi irraggiungibile. Errore se non lo diventa entro timeout o se
// manca l'indirizzo: fail-closed.
func RunGate(ctx context.Context, getenv func(string) string, timeout time.Duration) error {
	host, port := getenv("KUBERNETES_SERVICE_HOST"), getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return errors.New("apps gate: KUBERNETES_SERVICE_HOST/PORT mancanti, barriera non verificabile")
	}
	return waitUnreachable(ctx, net.JoinHostPort(host, port), timeout)
}

func waitUnreachable(ctx context.Context, address string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		d := net.Dialer{Timeout: time.Second}
		conn, err := d.DialContext(ctx, "tcp", address)
		if err != nil && ctx.Err() == nil {
			return nil
		}
		if err == nil {
			_ = conn.Close()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("apps gate: %s ancora raggiungibile dopo %s, la NetworkPolicy non è stata applicata al pod", address, timeout)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Comando ci-gate: cancello fail-closed del pod CI.
//
// Il dispatcher disabilita il service account e i service links; il cancello
// rifiuta di avviare il runner se una credenziale Kubernetes o il socket del
// runtime del nodo sono comunque comparsi nel pod. La NetworkPolicy completa la
// barriera prima che il container job sia creato.
package main

import (
	"fmt"
	"os"
)

func main() {
	for _, path := range []string{
		"/var/run/secrets/kubernetes.io/serviceaccount/token",
		"/var/run/docker.sock",
		"/run/containerd/containerd.sock",
	} {
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(os.Stderr, "ci gate: barriera violata: %s presente\n", path)
			os.Exit(1)
		}
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		fmt.Fprintln(os.Stderr, "ci gate: barriera violata: service links Kubernetes presenti")
		os.Exit(1)
	}
}

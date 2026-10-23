// Comando ci-gate: cancello fail-closed del pod CI.
//
// Il dispatcher disabilita il service account e i service links; il cancello
// rifiuta di avviare il runner se una credenziale Kubernetes o il socket del
// runtime del nodo sono comunque comparsi nel pod. La NetworkPolicy completa la
// barriera prima che il container job sia creato.
package main

import (
	"fmt"
	"net"
	"os"
	"time"
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
	// Il kubelet inietta sempre queste variabili: non sono service links. Il
	// cancello aspetta invece che la policy renda irraggiungibile l'API.
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host != "" && port != "" && !waitBlocked(net.JoinHostPort(host, port), 20*time.Second) {
		fmt.Fprintln(os.Stderr, "ci gate: API Kubernetes ancora raggiungibile")
		os.Exit(1)
	}
}

func waitBlocked(address string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			return true
		}
		_ = conn.Close()
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

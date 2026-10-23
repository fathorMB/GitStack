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
	// cancello aspetta invece che la policy renda irraggiungibili l'API e un
	// endpoint interno di core, prima che il container del job possa partire.
	var endpoints []string
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host != "" && port != "" {
		endpoints = append(endpoints, net.JoinHostPort(host, port))
	}
	if internal := os.Getenv("GITSTACK_CI_GATE_INTERNAL_ADDR"); internal != "" {
		endpoints = append(endpoints, internal)
	}
	if !waitBlockedAll(endpoints, 20*time.Second) {
		fmt.Fprintln(os.Stderr, "ci gate: API Kubernetes o rete interna ancora raggiungibile")
		os.Exit(1)
	}
}

func waitBlocked(address string, timeout time.Duration) bool {
	return waitBlockedAll([]string{address}, timeout)
}

func waitBlockedAll(addresses []string, timeout time.Duration) bool {
	if len(addresses) == 0 {
		return true
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		blocked := true
		for _, address := range addresses {
			conn, err := net.DialTimeout("tcp", address, time.Second)
			if err == nil {
				blocked = false
				_ = conn.Close()
			}
		}
		if blocked {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

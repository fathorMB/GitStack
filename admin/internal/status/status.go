// Package status raccoglie lo stato dell'installazione per `gitstack status`:
// versione del server, salute dei servizi, ultimo backup.
//
// La salute dei servizi si legge in due modi complementari: kubectl sul
// cluster (Deployment e StatefulSet della release: repliche pronte su
// desiderate) e l'endpoint HTTP /api/healthz del gateway, quello che vede
// anche un client.
package status

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backupstate"
	"github.com/fathorMB/GitStack/admin/internal/config"
)

// ErrCluster indica che il cluster non si è potuto interrogare (kubectl
// assente, kubeconfig illeggibile, API server giù).
var ErrCluster = errors.New("cluster non interrogabile")

// Runner esegue un comando esterno (kubectl). Iniettabile nei test.
type Runner interface {
	Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

// ExecRunner è il Runner di produzione.
type ExecRunner struct{}

// Run implementa Runner.
func (ExecRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return out, fmt.Errorf("%w: %s", err, msg)
		}
		return out, err
	}
	return out, nil
}

// Service è lo stato di un workload della release.
type Service struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Desired int    `json:"desired"`
	Ready   int    `json:"ready"`
	Image   string `json:"image"`
}

// Healthy è vero se tutte le repliche desiderate sono pronte e ce n'è
// almeno una.
func (s Service) Healthy() bool { return s.Desired > 0 && s.Ready >= s.Desired }

// Report è il risultato di Collect.
type Report struct {
	Host          string    `json:"host"`
	SSHPort       int       `json:"ssh_port"`
	Release       string    `json:"release"`
	Namespace     string    `json:"namespace"`
	ServerVersion string    `json:"server_version"`
	Services      []Service `json:"services"`
	ClusterError  string    `json:"cluster_error,omitempty"`
	APIHealthy    bool      `json:"api_healthy"`
	APIDetail     string    `json:"api_detail"`
	LastBackup    string    `json:"last_backup"`
}

// Healthy è vero se il cluster risponde, ogni servizio è pronto e l'API
// risponde.
func (r *Report) Healthy() bool {
	if r.ClusterError != "" || len(r.Services) == 0 || !r.APIHealthy {
		return false
	}
	for _, s := range r.Services {
		if !s.Healthy() {
			return false
		}
	}
	return true
}

// Collector raccoglie lo stato.
type Collector struct {
	Runner Runner
	HTTP   *http.Client
	// LookPath cerca un eseguibile (default exec.LookPath).
	LookPath func(string) (string, error)
}

// Collect interroga cluster, API e cartella dei backup. Un cluster
// irraggiungibile non è un errore di Collect: finisce in Report.ClusterError
// perché lo stato (API ok, cluster no) resti visibile.
func (c *Collector) Collect(ctx context.Context, cfg *config.Config, configDir string) *Report {
	r := &Report{
		Host:      cfg.Host,
		SSHPort:   cfg.SSHPort,
		Release:   cfg.Release,
		Namespace: cfg.Namespace,
	}
	svcs, err := c.services(ctx, cfg)
	if err != nil {
		r.ClusterError = err.Error()
	}
	r.Services = svcs
	r.ServerVersion = serverVersion(svcs, cfg)
	r.APIHealthy, r.APIDetail = c.api(ctx, cfg)
	r.LastBackup = lastBackupState(configDir)
	return r
}

// kubectlCommand sceglie `kubectl` se c'è, altrimenti `k3s kubectl` (k3s lo
// porta con sé, senza metterlo nel PATH).
func (c *Collector) kubectlCommand() (string, []string, error) {
	look := c.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if p, err := look("kubectl"); err == nil {
		return p, nil, nil
	}
	if p, err := look("k3s"); err == nil {
		return p, []string{"kubectl"}, nil
	}
	return "", nil, fmt.Errorf("%w: né kubectl né k3s nel PATH", ErrCluster)
}

type workloadList struct {
	Items []struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Replicas *int `json:"replicas"`
			Template struct {
				Spec struct {
					Containers []struct {
						Image string `json:"image"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas int `json:"readyReplicas"`
		} `json:"status"`
	} `json:"items"`
}

func (c *Collector) services(ctx context.Context, cfg *config.Config) ([]Service, error) {
	bin, pre, err := c.kubectlCommand()
	if err != nil {
		return nil, err
	}
	args := append(pre, "get", "deployments,statefulsets",
		"-n", cfg.Namespace,
		"-l", "app.kubernetes.io/part-of=gitstack,app.kubernetes.io/instance="+cfg.Release,
		"-o", "json")
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := c.Runner.Run(cctx, []string{"KUBECONFIG=" + cfg.Kubeconfig}, bin, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCluster, err)
	}
	var wl workloadList
	if err := json.Unmarshal(out, &wl); err != nil {
		return nil, fmt.Errorf("%w: risposta di kubectl non valida: %v", ErrCluster, err)
	}
	var res []Service
	for _, it := range wl.Items {
		s := Service{Name: it.Metadata.Name, Kind: it.Kind, Ready: it.Status.ReadyReplicas, Desired: 1}
		if it.Spec.Replicas != nil {
			s.Desired = *it.Spec.Replicas
		}
		if cs := it.Spec.Template.Spec.Containers; len(cs) > 0 {
			s.Image = cs[0].Image
		}
		res = append(res, s)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Name < res[j].Name })
	return res, nil
}

// serverVersion è il tag dell'immagine del gateway installata (la versione
// realmente in esecuzione); se il cluster non risponde, quella scritta
// nel config dall'installer.
func serverVersion(svcs []Service, cfg *config.Config) string {
	for _, s := range svcs {
		if strings.HasSuffix(s.Name, "-gateway") {
			if tag := imageTag(s.Image); tag != "" {
				return tag
			}
		}
	}
	if cfg.ImageTag != "" {
		return cfg.ImageTag
	}
	return "sconosciuta"
}

func imageTag(image string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i:], "/") {
		return ""
	}
	return image[i+1:]
}

// newAPIClient crea il client per /api/healthz. Con la CA interna (ca_cert) si
// fida del suo certificato oltre alle CA di sistema; la verifica resta attiva.
func newAPIClient(cfg *config.Config) (*http.Client, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	if cfg.CACert == "" {
		return client, nil
	}
	pem, err := os.ReadFile(cfg.CACert)
	if err != nil {
		return nil, fmt.Errorf("certificato della CA %s: %w", cfg.CACert, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("certificato della CA %s: nessun certificato PEM valido", cfg.CACert)
	}
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	return client, nil
}

func (c *Collector) api(ctx context.Context, cfg *config.Config) (bool, string) {
	client := c.HTTP
	scheme := "http"
	if cfg.TLS != "" && cfg.TLS != "insecure" {
		scheme = "https"
	}
	if client == nil {
		var err error
		client, err = newAPIClient(cfg)
		if err != nil {
			return false, err.Error()
		}
	}
	url := scheme + "://" + cfg.Host + "/api/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("%s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("%s: HTTP %d", url, resp.StatusCode)
	}
	return true, url + ": HTTP 200"
}

// lastBackupState legge il file di stato dell'ultimo backup e lo
// formatta per la stampa. Se il file non esiste, ritorna «nessuno».
func lastBackupState(configDir string) string {
	if configDir == "" {
		configDir = "/etc/gitstack"
	}
	s := backupstate.Read(configDir)
	if s.At.IsZero() {
		return "nessuno"
	}
	at := s.At.Format("2006-01-02 15:04:05")
	if s.Success {
		return fmt.Sprintf("ultimo successo: %s (%s)", at, s.Path)
	}
	return fmt.Sprintf("ultimo errore: %s — %s", at, s.Error)
}

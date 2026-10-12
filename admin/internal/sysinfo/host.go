package sysinfo

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
)

// Sezioni Access, Services e Node di Admin · System (GIT-218, mockup 16).

// Access: come si raggiunge GitStack. Solo dati pubblici: host, modo TLS,
// scadenza del certificato servito e porta SSH. Mai chiavi né Secret.
type Access struct {
	Host string `json:"host"`
	// TLSMode: internal, custom, letsencrypt o insecure (solo HTTP).
	TLSMode string `json:"tlsMode"`
	// TLSExpiresAt è la scadenza del certificato che il server presenta ora;
	// nil se non si è potuta leggere (o con HTTP).
	TLSExpiresAt *time.Time `json:"tlsExpiresAt,omitempty"`
	SSHPort      int        `json:"sshPort"`
}

// ServiceState è un Deployment o StatefulSet della release.
type ServiceState struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Desired int    `json:"desired"`
	Ready   int    `json:"ready"`
	Image   string `json:"image,omitempty"`
	Healthy bool   `json:"healthy"`
}

// Services è lo stato dei workload come lo raccoglie `gitstack status`.
// `Available` è falso se il cluster non si è potuto interrogare (Error dice
// perché). L'istante della rilevazione è Info.UpdatedAt.
type Services struct {
	Available bool           `json:"available"`
	Error     string         `json:"error,omitempty"`
	Items     []ServiceState `json:"items"`
}

// Metric confronta un valore dell'host con il profilo N1. Level: ok (almeno
// il consigliato), below_recommended (sopra il minimo, sotto il consigliato),
// below_minimum, unknown (non letto). Le unità sono core per la CPU e byte
// per memoria e disco (GB decimali, come l'installer).
type Metric struct {
	Available   bool   `json:"available"`
	Value       int64  `json:"value"`
	Minimum     int64  `json:"minimum"`
	Recommended int64  `json:"recommended"`
	Level       string `json:"level"`
}

// Disk è il filesystem che ospita i dati di k3s: dimensione e spazio libero.
type Disk struct {
	Metric
	Path         string `json:"path,omitempty"`
	FreeBytes    int64  `json:"freeBytes"`
	MinFreeBytes int64  `json:"minFreeBytes"`
}

// Node è la macchina, confrontata con il profilo N1.
type Node struct {
	// Available è falso se nessuna misura è stata possibile («non disponibile»).
	Available bool   `json:"available"`
	CPU       Metric `json:"cpu"`
	Memory    Metric `json:"memory"`
	Disk      Disk   `json:"disk"`
}

// Profilo N1 [c_d345922b89557a3f], gli stessi valori di deploy/install.sh
// (MIN_*/REC_*): GB decimali.
const (
	gb = int64(1_000_000_000)

	MinCPU            = 4
	RecommendedCPU    = 8
	MinMemoryBytes    = 8 * gb
	RecMemoryBytes    = 16 * gb
	MinDiskBytes      = 60 * gb
	RecDiskBytes      = 200 * gb
	MinDiskFreeBytes  = 20 * gb
	defaultDiskTarget = "/var/lib/rancher"
)

// NodeProbe sono le misure grezze dell'host; 0 = non letto.
type NodeProbe struct {
	CPUCores       int
	MemoryBytes    int64
	DiskPath       string
	DiskTotalBytes int64
	DiskFreeBytes  int64
}

// ProbeNode misura CPU, memoria e disco della macchina che ospita k3s. Sulle
// piattaforme senza /proc e statfs restituisce solo ciò che sa leggere.
func ProbeNode() NodeProbe {
	p := NodeProbe{CPUCores: runtime.NumCPU()}
	p.MemoryBytes = readMemTotal("/proc/meminfo")
	target := defaultDiskTarget
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		target = "/"
	}
	p.DiskPath = target
	p.DiskTotalBytes, p.DiskFreeBytes = diskUsage(target)
	return p
}

// readMemTotal legge MemTotal (KiB) da un meminfo; 0 se non si riesce.
func readMemTotal(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return parseMemTotal(string(b))
}

func parseMemTotal(s string) int64 {
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return 0
		}
		kib, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil || kib < 0 {
			return 0
		}
		return kib * 1024
	}
	return 0
}

// level confronta v con minimo e consigliato.
func level(v, min, rec int64) string {
	switch {
	case v >= rec:
		return "recommended"
	case v >= min:
		return "below_recommended"
	default:
		return "below_minimum"
	}
}

func metric(v, min, rec int64) Metric {
	m := Metric{Value: v, Minimum: min, Recommended: rec, Level: "unknown"}
	if v > 0 {
		m.Available = true
		m.Level = level(v, min, rec)
	}
	return m
}

// BuildNode confronta le misure con il profilo N1.
func BuildNode(p NodeProbe) Node {
	n := Node{
		CPU:    metric(int64(p.CPUCores), MinCPU, RecommendedCPU),
		Memory: metric(p.MemoryBytes, MinMemoryBytes, RecMemoryBytes),
	}
	n.Disk = Disk{Metric: metric(p.DiskTotalBytes, MinDiskBytes, RecDiskBytes), Path: p.DiskPath,
		FreeBytes: p.DiskFreeBytes, MinFreeBytes: MinDiskFreeBytes}
	// Lo spazio libero sotto il minimo è bloccante come per l'installer.
	if n.Disk.Available && p.DiskFreeBytes < MinDiskFreeBytes {
		n.Disk.Level = "below_minimum"
	}
	n.Available = n.CPU.Available || n.Memory.Available || n.Disk.Available
	return n
}

func buildAccess(cfg *config.Config, expiry *time.Time) Access {
	mode := cfg.TLS
	if mode == "" {
		mode = "insecure"
	}
	a := Access{Host: cfg.Host, TLSMode: mode, SSHPort: cfg.SSHPort}
	if mode != "insecure" {
		a.TLSExpiresAt = expiry
	}
	return a
}

func buildServices(r *status.Report) Services {
	s := Services{Items: []ServiceState{}}
	if r == nil {
		s.Error = "stato dei servizi non raccolto"
		return s
	}
	if r.ClusterError != "" {
		s.Error = truncate(r.ClusterError)
		return s
	}
	s.Available = true
	for _, svc := range r.Services {
		s.Items = append(s.Items, ServiceState{Name: svc.Name, Kind: svc.Kind, Desired: svc.Desired,
			Ready: svc.Ready, Image: svc.Image, Healthy: svc.Healthy()})
	}
	return s
}

// ProbeCertExpiry legge la scadenza del certificato che `host` presenta su
// HTTPS (porta 443, o quella indicata). La verifica della catena è spenta
// apposta: serve la data, anche di un certificato già scaduto o di una CA
// interna; non si invia nulla e non si fa nessuna scelta di fiducia.
func ProbeCertExpiry(ctx context.Context, host string) (time.Time, error) {
	addr := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		addr = net.JoinHostPort(host, "443")
	}
	sni, _, _ := net.SplitHostPort(addr)
	d := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 5 * time.Second},
		Config:    &tls.Config{ServerName: sni, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec // solo lettura della scadenza
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := d.DialContext(cctx, "tcp", addr)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = conn.Close() }()
	tc, ok := conn.(*tls.Conn)
	if !ok {
		return time.Time{}, errors.New("connessione non TLS")
	}
	certs := tc.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return time.Time{}, errors.New("nessun certificato presentato")
	}
	return certs[0].NotAfter.UTC(), nil
}

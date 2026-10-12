// Package sysinfo compone e pubblica le informazioni dell'installazione che
// la pagina Admin · System mostra (GIT-213, mockup 16).
//
// I servizi non vedono l'host (/etc/gitstack/config.yaml, lo stato dell'ultimo
// backup) né i values della release. Per questo `gitstack` scrive un ConfigMap
// `<release>-system-info` con la chiave system.json, che il chart monta in
// core come file: core lo legge e lo espone con GET /admin/system, solo
// all'amministratore. Il formato del JSON è quello di AdminSystem nel
// contratto (api/openapi.yaml): i nomi dei campi devono restare gli stessi.
//
// Mai segreti nel documento: né la password del database, né le chiavi S3, né
// le credenziali di un URL di proxy. Si copiano solo campi scelti, uno per
// uno; non si serializza mai config.Config.
package sysinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/backupstate"
	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
)

// Key è la chiave del ConfigMap; ConfigMapSuffix il suffisso del suo nome.
const (
	Key             = "system.json"
	ConfigMapSuffix = "-system-info"
	// maxError limita gli errori copiati nel documento.
	maxError = 500
)

// ConfigMapName è il nome del ConfigMap per la release (lo stesso che il
// chart monta in core: <fullname>-system-info, con fullname = release).
func ConfigMapName(release string) string { return release + ConfigMapSuffix }

// Info è il documento system.json. Coincide con AdminSystem del contratto,
// meno `published` che aggiunge core.
type Info struct {
	Schema    int       `json:"schema"`
	UpdatedAt time.Time `json:"updatedAt"`
	Database  Database  `json:"database"`
	Backup    Backup    `json:"backup"`
	Versions  Versions  `json:"versions"`
	Network   Network   `json:"network"`
}

// Database: modo "internal" o "external", mai la password.
type Database struct {
	Mode          string `json:"mode"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	Name          string `json:"name,omitempty"`
	SSLMode       string `json:"sslmode,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`
}

// Run è l'esito di un backup o di un caricamento: state none|success|failed.
type Run struct {
	State string     `json:"state"`
	At    *time.Time `json:"at,omitempty"`
	Name  string     `json:"name,omitempty"`
	Error string     `json:"error,omitempty"`
}

// S3 non contiene mai AccessKey, SecretKey né il file della chiave.
type S3 struct {
	Configured bool   `json:"configured"`
	Endpoint   string `json:"endpoint,omitempty"`
	Bucket     string `json:"bucket,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
	Retention  string `json:"retention,omitempty"`
	Upload     Run    `json:"upload"`
}

// Backup è la sezione dei backup.
type Backup struct {
	Destination string `json:"destination,omitempty"`
	Retention   int    `json:"retention,omitempty"`
	Last        Run    `json:"last"`
	S3          S3     `json:"s3"`
}

// Versions: k3sUpdate è none, patch o minor.
type Versions struct {
	GitStack    string `json:"gitstack"`
	K3s         string `json:"k3s,omitempty"`
	K3sDeclared string `json:"k3sDeclared,omitempty"`
	K3sUpdate   string `json:"k3sUpdate"`
}

// Network è la rete aziendale (N9).
type Network struct {
	Configured          bool   `json:"configured"`
	HTTPProxy           string `json:"httpProxy,omitempty"`
	HTTPSProxy          string `json:"httpsProxy,omitempty"`
	NoProxy             string `json:"noProxy,omitempty"`
	ProxyHasCredentials bool   `json:"proxyHasCredentials"`
	CorporateCA         bool   `json:"corporateCA"`
	MirrorCA            bool   `json:"mirrorCA"`
}

// Input sono le fonti da cui si compone il documento.
type Input struct {
	Cfg *config.Config
	// Report è lo stato raccolto da status.Collector (può essere nil).
	Report *status.Report
	// Backup è lo stato dell'ultimo backup (backupstate.Read).
	Backup backupstate.State
	// HelmValues sono i values della release (helm get values -o json); nil
	// se non si sono potuti leggere: la rete risulta «non configurato».
	HelmValues map[string]any
	Now        time.Time
}

// Build compone il documento.
func Build(in Input) Info {
	cfg := in.Cfg
	info := Info{Schema: 1, UpdatedAt: in.Now.UTC()}

	info.Database.Mode = "internal"
	info.Database.ServerVersion = cfg.Postgres.Version
	if cfg.Postgres.External {
		p := cfg.Postgres
		info.Database = Database{Mode: "external", Host: p.Host, Port: p.Port, Name: p.Database,
			SSLMode: p.SSLMode, ServerVersion: p.Version}
	}
	if in.Report != nil && in.Report.Database.Version != "" {
		info.Database.ServerVersion = in.Report.Database.Version
	}

	info.Backup.Destination = cfg.Backup.Destination
	info.Backup.Retention = cfg.Backup.Retention
	info.Backup.Last = run(in.Backup.Success, in.Backup.At, in.Backup.Path, in.Backup.Error, !in.Backup.At.IsZero())
	info.Backup.S3.Upload = Run{State: "none"}
	if s3 := cfg.Backup.S3; s3 != nil {
		endpoint, _ := stripCredentials(s3.Endpoint)
		ret := s3.Retention
		if ret == "" {
			ret = fmt.Sprint(config.DefaultS3Retention)
		}
		info.Backup.S3 = S3{Configured: true, Endpoint: endpoint, Bucket: s3.Bucket, Prefix: s3.Prefix,
			Retention: ret, Upload: Run{State: "none"}}
		if u := in.Backup.Upload; u != nil && !u.At.IsZero() {
			info.Backup.S3.Upload = run(u.Success, u.At, u.Name, u.Error, true)
		}
	}

	info.Versions = Versions{GitStack: cfg.ImageTag, K3sUpdate: "none"}
	if r := in.Report; r != nil {
		if r.ServerVersion != "" && r.ServerVersion != "sconosciuta" {
			info.Versions.GitStack = r.ServerVersion
		}
		info.Versions.K3s = r.K3sVersion
		info.Versions.K3sDeclared = r.K3sDeclared
		if r.K3sUpdate == "patch" || r.K3sUpdate == "minor" {
			info.Versions.K3sUpdate = r.K3sUpdate
		}
	}

	info.Network = networkFromValues(in.HelmValues)
	return info
}

func run(success bool, at time.Time, name, errText string, exists bool) Run {
	if !exists {
		return Run{State: "none"}
	}
	t := at.UTC()
	r := Run{State: "success", At: &t, Name: name}
	if !success {
		r.State = "failed"
		r.Error = truncate(errText)
	}
	return r
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxError {
		return s[:maxError] + "…"
	}
	return s
}

// stripCredentials toglie user:password da un URL e dice se c'erano. Un
// valore che non si legge come URL ma contiene «@» si scarta del tutto.
func stripCredentials(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		if strings.Contains(raw, "@") {
			return "", true
		}
		return raw, false
	}
	if u.User == nil {
		return raw, false
	}
	u.User = nil
	return u.String(), true
}

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return strings.TrimSpace(s)
}

// networkFromValues legge proxy, CA aziendale e CA dei mirror dai values
// della release (chart: proxy.http, proxy.https, proxy.noProxy,
// corporateCA.configMap, git.mirror.caConfigMap).
func networkFromValues(v map[string]any) Network {
	var n Network
	if v == nil {
		return n
	}
	var c1, c2 bool
	n.HTTPProxy, c1 = stripCredentials(str(v, "proxy", "http"))
	n.HTTPSProxy, c2 = stripCredentials(str(v, "proxy", "https"))
	n.ProxyHasCredentials = c1 || c2
	n.NoProxy = str(v, "proxy", "noProxy")
	n.CorporateCA = str(v, "corporateCA", "configMap") != ""
	n.MirrorCA = str(v, "git", "mirror", "caConfigMap") != ""
	n.Configured = n.HTTPProxy != "" || n.HTTPSProxy != "" || n.ProxyHasCredentials || n.CorporateCA || n.MirrorCA
	return n
}

// Manifest è il ConfigMap da applicare.
func Manifest(cfg *config.Config, info Info) ([]byte, error) {
	doc, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}
	m := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      ConfigMapName(cfg.Release),
			"namespace": cfg.Namespace,
			"labels": map[string]string{
				"app.kubernetes.io/part-of":    "gitstack",
				"app.kubernetes.io/instance":   cfg.Release,
				"app.kubernetes.io/managed-by": "gitstack-admin",
			},
		},
		"data": map[string]string{Key: string(doc)},
	}
	return json.Marshal(m)
}

// Applier applica un manifest (kubectl apply -f -).
type Applier func(ctx context.Context, manifest []byte) error

// KubectlApplier applica con kubectl (o `k3s kubectl` se pre non è vuoto).
func KubectlApplier(bin string, pre []string, kubeconfig string, ex backup.ExecFunc) Applier {
	if ex == nil {
		ex = backup.ExecStream
	}
	return func(ctx context.Context, manifest []byte) error {
		args := append(append([]string{}, pre...), "apply", "-f", "-")
		return ex(ctx, []string{"KUBECONFIG=" + kubeconfig}, bytes.NewReader(manifest), io.Discard, bin, args...)
	}
}

// Publish compone il manifest e lo applica.
func Publish(ctx context.Context, cfg *config.Config, info Info, apply Applier) error {
	m, err := Manifest(cfg, info)
	if err != nil {
		return err
	}
	return apply(ctx, m)
}

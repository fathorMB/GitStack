package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/upgrade"
)

// Percorsi di rete aziendale (N9). Come per GITSTACK_TLS_DIR, i test li
// spostano con variabili d'ambiente.
const (
	defaultNetworkDir   = "/etc/gitstack/network"
	defaultK3sDir       = "/etc/rancher/k3s"
	defaultSystemdDir   = "/etc/systemd/system"
	defaultSystemCADir  = "/usr/local/share/ca-certificates"
	k3sDropInName       = "10-gitstack-network.conf"
	systemCAFileName    = "gitstack-corporate-ca.crt"
	k3sBuiltinNoProxy   = "localhost,127.0.0.1,::1,10.42.0.0/16,10.43.0.0/16,.svc,.cluster.local"
	networkWaitDefault  = 5 * time.Minute
	networkProbeTimeout = 10 * time.Second
)

// networkPaths sono le posizioni dei file generati.
type networkPaths struct {
	NetworkDir, K3sDir, SystemdDir, SystemCADir string
}

func (a *App) networkPaths() networkPaths {
	get := func(env, def string) string {
		if v := a.Getenv(env); v != "" {
			return v
		}
		return def
	}
	return networkPaths{
		NetworkDir:  get("GITSTACK_NETWORK_DIR", defaultNetworkDir),
		K3sDir:      get("GITSTACK_K3S_DIR", defaultK3sDir),
		SystemdDir:  get("GITSTACK_SYSTEMD_DIR", defaultSystemdDir),
		SystemCADir: get("GITSTACK_CA_CERTS_DIR", defaultSystemCADir),
	}
}

func (p networkPaths) caCopy() string     { return filepath.Join(p.NetworkDir, "ca.crt") }
func (p networkPaths) registries() string { return filepath.Join(p.K3sDir, "registries.yaml") }
func (p networkPaths) dropIn() string {
	return filepath.Join(p.SystemdDir, "k3s.service.d", k3sDropInName)
}
func (p networkPaths) systemCA() string { return filepath.Join(p.SystemCADir, systemCAFileName) }

// effectiveHTTPS: senza https_proxy vale http_proxy (come in install.sh).
func effectiveHTTPS(n config.Network) string {
	if n.HTTPSProxy != "" {
		return n.HTTPSProxy
	}
	return n.HTTPProxy
}

// nodeAddrs sono gli indirizzi IPv4 della macchina (senza loopback), ordinati
// e separati da virgole: l'API server parla al kubelet su <IP del nodo>:10250
// e non deve passare dal proxy. Stesso elenco e ordine di net_node_addrs in
// deploy/install.sh.
func nodeAddrs(addrs []string) string {
	seen := map[string]bool{}
	var out []string
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil || ip.To4() == nil || ip.IsLoopback() || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// k3sNoProxy è il NO_PROXY di k3s: reti del cluster e suffissi interni,
// indirizzi del nodo e quanto indicato dall'utente.
func k3sNoProxy(n config.Network, addrs []string) string {
	out := k3sBuiltinNoProxy
	if a := nodeAddrs(addrs); a != "" {
		out += "," + a
	}
	if n.NoProxy != "" {
		out += "," + n.NoProxy
	}
	return out
}

// mirrorAuthority è host[:porta] del mirror (la chiave di configs in registries.yaml).
func mirrorAuthority(mirror string) string {
	u, err := url.Parse(mirror)
	if err != nil {
		return ""
	}
	return u.Host
}

func q(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// renderRegistries produce registries.yaml di k3s: il mirror vale per ogni
// immagine ("*"), comprese quelle di sistema di k3s. Vuoto = nessun mirror.
// Lo stesso formato lo scrive install.sh (write_registries_yaml).
func renderRegistries(n config.Network, caPath string) string {
	if n.RegistryMirror == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Scritto da GitStack (install.sh o gitstack config set network): non modificarlo a mano.\n")
	b.WriteString("mirrors:\n  \"*\":\n    endpoint:\n      - " + q(n.RegistryMirror) + "\n")
	hasAuth := n.RegistryUsername != ""
	hasCA := n.CABundle != "" && strings.HasPrefix(n.RegistryMirror, "https://")
	if hasAuth || hasCA {
		b.WriteString("configs:\n  " + q(mirrorAuthority(n.RegistryMirror)) + ":\n")
		if hasAuth {
			b.WriteString("    auth:\n      username: " + q(n.RegistryUsername) + "\n      password: " + q(n.RegistryPassword) + "\n")
		}
		if hasCA {
			b.WriteString("    tls:\n      ca_file: " + q(caPath) + "\n")
		}
	}
	return b.String()
}

// systemdEnv scrive una riga Environment= (apici doppi, % raddoppiato).
func systemdEnv(k, v string) string {
	v = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(v)
	return `Environment="` + k + "=" + v + `"` + "\n"
}

// renderDropIn produce il drop-in systemd di k3s con le variabili di proxy
// (containerd e kubelet le ereditano). Vuoto = nessun proxy.
func renderDropIn(n config.Network, addrs []string) string {
	https := effectiveHTTPS(n)
	if n.HTTPProxy == "" && https == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Scritto da GitStack (install.sh o gitstack config set network): non modificarlo a mano.\n[Service]\n")
	np := k3sNoProxy(n, addrs)
	for _, k := range []string{"HTTP_PROXY", "http_proxy"} {
		if n.HTTPProxy != "" {
			b.WriteString(systemdEnv(k, n.HTTPProxy))
		}
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy"} {
		b.WriteString(systemdEnv(k, https))
	}
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		b.WriteString(systemdEnv(k, np))
	}
	return b.String()
}

// writeIfChanged scrive path (0600 per i file con segreti, altrimenti 0644)
// solo se il contenuto cambia; content vuoto rimuove il file. Dice se è cambiato.
func writeIfChanged(path, content string, mode os.FileMode) (bool, error) {
	old, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if content == "" {
		if !exists {
			return false, nil
		}
		return true, os.Remove(path)
	}
	if exists && string(old) == content {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gitstack.*")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}

// readCABundle legge un file PEM e controlla che contenga almeno un
// certificato valido.
func readCABundle(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	n := 0
	rest := b
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(blk.Bytes); err != nil {
			return nil, fmt.Errorf("%s: certificato non valido: %v", path, err)
		}
		n++
	}
	if n == 0 {
		return nil, fmt.Errorf("%s: nessun certificato PEM trovato", path)
	}
	return b, nil
}

// noProxyMatches dice se host è escluso dal proxy da una lista NO_PROXY.
func noProxyMatches(list, host string) bool {
	for _, e := range strings.Split(list, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		h := strings.ToLower(host)
		switch {
		case e == "":
		case e == "*" || e == h:
			return true
		case strings.HasPrefix(e, ".") && strings.HasSuffix(h, e):
			return true
		case strings.Contains(e, "/"):
			if _, cidr, err := net.ParseCIDR(e); err == nil {
				if ip := net.ParseIP(h); ip != nil && cidr.Contains(ip) {
					return true
				}
			}
		case strings.HasSuffix(h, "."+e):
			return true
		}
	}
	return false
}

// probeResult è l'esito di una prova di raggiungibilità.
type probeResult struct {
	What string
	OK   bool
	Note string
}

// probeNetwork prova a raggiungere proxy, mirror delle immagini e dei
// download con la configurazione data (stessa logica del preflight di
// install.sh): una risposta HTTP qualunque basta, è il silenzio che segnala
// il guasto.
func probeNetwork(ctx context.Context, n config.Network, ca []byte) []probeResult {
	var out []probeResult
	for _, p := range []struct{ what, v string }{{"proxy HTTP", n.HTTPProxy}, {"proxy HTTPS", n.HTTPSProxy}} {
		if p.v == "" {
			continue
		}
		u, err := url.Parse(p.v)
		if err != nil {
			continue
		}
		host := u.Host
		if u.Port() == "" {
			host = net.JoinHostPort(u.Hostname(), map[string]string{"https": "443", "socks5": "1080"}[u.Scheme])
			if u.Scheme == "http" {
				host = net.JoinHostPort(u.Hostname(), "3128")
			}
		}
		d := net.Dialer{Timeout: networkProbeTimeout}
		c, err := d.DialContext(ctx, "tcp", host)
		if err != nil {
			out = append(out, probeResult{p.what + " " + config.Redact(p.v), false, "non raggiungibile: " + err.Error()})
			continue
		}
		_ = c.Close()
		out = append(out, probeResult{p.what + " " + config.Redact(p.v), true, ""})
	}
	pool, _ := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	if len(ca) > 0 {
		pool.AppendCertsFromPEM(ca)
	}
	httpGet := func(rawURL, user, pass string) (int, error) {
		u, err := url.Parse(rawURL)
		if err != nil {
			return 0, err
		}
		proxy := n.HTTPProxy
		if u.Scheme == "https" {
			proxy = effectiveHTTPS(n)
		}
		tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
		if proxy != "" && !noProxyMatches(n.NoProxy, u.Hostname()) {
			pu, err := url.Parse(proxy)
			if err != nil {
				return 0, err
			}
			tr.Proxy = http.ProxyURL(pu)
		}
		cl := &http.Client{Transport: tr, Timeout: networkProbeTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return 0, err
		}
		if user != "" {
			req.SetBasicAuth(user, n.RegistryPassword)
		}
		resp, err := cl.Do(req)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}
	// Un proxy che nega la destinazione risponde (403, 503...) ma non è la
	// destinazione: si accetta solo la risposta giusta del mirror.
	if n.RegistryMirror != "" {
		u := strings.TrimRight(n.RegistryMirror, "/") + "/v2/"
		what := "mirror delle immagini " + u
		code, err := httpGet(u, n.RegistryUsername, "")
		switch {
		case err != nil:
			out = append(out, probeResult{what, false, err.Error()})
		case code == http.StatusUnauthorized && n.RegistryUsername != "":
			out = append(out, probeResult{what, false, "credenziali rifiutate (401)"})
		case code == http.StatusOK || code == http.StatusUnauthorized:
			out = append(out, probeResult{what, true, ""})
		default:
			out = append(out, probeResult{what, false, fmt.Sprintf("risponde HTTP %d (atteso 200 o 401)", code)})
		}
	}
	if n.DownloadMirror != "" {
		u := strings.TrimRight(n.DownloadMirror, "/") + "/k3s/install.sh"
		what := "mirror dei download " + u
		code, err := httpGet(u, "", "")
		switch {
		case err != nil:
			out = append(out, probeResult{what, false, err.Error()})
		case code >= 200 && code < 400:
			out = append(out, probeResult{what, true, ""})
		default:
			out = append(out, probeResult{what, false, fmt.Sprintf("risponde HTTP %d (atteso 2xx)", code)})
		}
	}
	return out
}

func runConfigSetNetwork(ctx context.Context, a *App, args []string) int {
	fs := flag.NewFlagSet("config set network", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	httpProxy := fs.String("http-proxy", "", "URL del proxy HTTP")
	httpsProxy := fs.String("https-proxy", "", "URL del proxy HTTPS (default: quello HTTP)")
	noProxy := fs.String("no-proxy", "", "destinazioni senza proxy, separate da virgole")
	regMirror := fs.String("registry-mirror", "", "URL del mirror delle immagini")
	regUser := fs.String("registry-username", "", "utente del mirror")
	regPassFile := fs.String("registry-password-file", "", "file con la password del mirror (o $GITSTACK_REGISTRY_PASSWORD)")
	dlMirror := fs.String("download-mirror", "", "URL del mirror dei download (k3s, Helm, gitstack)")
	caBundle := fs.String("ca-bundle", "", "file PEM con la CA aziendale")
	clearAll := fs.Bool("clear", false, "azzera tutta la configurazione di rete")
	force := fs.Bool("force", false, "applica anche se la prova di raggiungibilità fallisce")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		if err == nil {
			err = fmt.Errorf("argomenti inattesi: %s", strings.Join(fs.Args(), " "))
		}
		a.errorf("config set network: %v", err)
		a.networkUsage()
		return ExitUsage
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "config" && f.Name != "force" {
			given[f.Name] = true
		}
	})
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	n := cfg.Network
	if *clearAll {
		n = config.Network{}
	}
	changed := *clearAll
	set := func(name string, dst *string, v string) {
		if given[name] {
			if *dst != v {
				changed = true
			}
			*dst = v
		}
	}
	set("http-proxy", &n.HTTPProxy, *httpProxy)
	set("https-proxy", &n.HTTPSProxy, *httpsProxy)
	set("no-proxy", &n.NoProxy, *noProxy)
	set("registry-mirror", &n.RegistryMirror, *regMirror)
	set("registry-username", &n.RegistryUsername, *regUser)
	set("download-mirror", &n.DownloadMirror, *dlMirror)
	if given["registry-password-file"] || a.Getenv("GITSTACK_REGISTRY_PASSWORD") != "" {
		pw := a.Getenv("GITSTACK_REGISTRY_PASSWORD")
		if given["registry-password-file"] && *regPassFile != "" {
			b, err := os.ReadFile(*regPassFile)
			if err != nil {
				a.errorf("config set network: %v", err)
				return ExitUsage
			}
			pw = strings.TrimRight(string(b), "\r\n")
		}
		if pw != n.RegistryPassword {
			changed = true
		}
		n.RegistryPassword = pw
	}
	if n.RegistryMirror == "" && (given["registry-mirror"] || *clearAll) {
		n.RegistryUsername, n.RegistryPassword = "", ""
	}
	paths := a.networkPaths()
	var caPEM []byte
	if given["ca-bundle"] {
		if *caBundle == "" {
			if n.CABundle != "" {
				changed = true
			}
			n.CABundle = ""
		} else {
			b, err := readCABundle(*caBundle)
			if err != nil {
				a.errorf("config set network: --ca-bundle: %v", err)
				return ExitUsage
			}
			caPEM = b
			n.CABundle = paths.caCopy()
			old, _ := os.ReadFile(n.CABundle)
			if !bytes.Equal(old, b) {
				changed = true
			}
		}
	} else if n.CABundle != "" {
		caPEM, _ = os.ReadFile(n.CABundle)
	}
	if len(given) == 0 && !*clearAll {
		a.errorf("config set network: indica almeno un'opzione")
		a.networkUsage()
		return ExitUsage
	}
	if err := n.Validate(); err != nil {
		a.errorf("config set network: %v", err)
		return ExitUsage
	}
	if !changed {
		_, _ = fmt.Fprintln(a.Stdout, "La configurazione di rete è già questa: niente da fare.")
		return ExitOK
	}
	if cfg.ChartDir == "" || !dirHasChart(cfg.ChartDir) {
		a.errorf("config set network: il chart di GitStack non è sul disco (chart_dir %q): rilancia deploy/install.sh con le opzioni di rete, che lo copia.", cfg.ChartDir)
		return ExitConfig
	}

	// 1. Prova di raggiungibilità, prima di toccare niente.
	var bad []string
	for _, r := range probeNetwork(ctx, n, caPEM) {
		if r.OK {
			_, _ = fmt.Fprintf(a.Stdout, "OK   %s\n", r.What)
			continue
		}
		_, _ = fmt.Fprintf(a.Stdout, "KO   %s: %s\n", r.What, r.Note)
		bad = append(bad, r.What)
	}
	if len(bad) > 0 && !*force {
		a.errorf("config set network: non rispondono: %s. Non ho cambiato niente (--force per applicare lo stesso).", strings.Join(bad, "; "))
		return ExitConfig
	}

	// 2. File generati. Quelli di k3s fanno riavviare k3s solo se cambiano.
	_, _ = fmt.Fprintln(a.Stdout, "==> Rigenero i file di rete")
	k3sChanged := false
	if caPEM != nil && given["ca-bundle"] {
		if _, err := writeIfChanged(paths.caCopy(), string(caPEM), 0o644); err != nil {
			a.errorf("config set network: %v", err)
			return ExitUnexpected
		}
	}
	if n.CABundle == "" {
		_ = os.Remove(paths.caCopy())
	}
	sysCA := ""
	if caPEM != nil {
		sysCA = string(caPEM)
	}
	caCh, err := writeIfChanged(paths.systemCA(), sysCA, 0o644)
	if err != nil {
		a.errorf("config set network: CA di sistema: %v", err)
		return ExitUnexpected
	}
	regCh, err := writeIfChanged(paths.registries(), renderRegistries(n, paths.caCopy()), 0o600)
	if err != nil {
		a.errorf("config set network: registries.yaml: %v", err)
		return ExitUnexpected
	}
	dropCh, err := writeIfChanged(paths.dropIn(), renderDropIn(n, a.localAddrs()), 0o644)
	if err != nil {
		a.errorf("config set network: drop-in di k3s: %v", err)
		return ExitUnexpected
	}
	upgrade.StripK3sEnvProxy(paths.SystemdDir)
	k3sChanged = regCh || dropCh || (caCh && n.RegistryMirror != "")
	if caCh {
		if out, err := a.Runner.Run(ctx, nil, "update-ca-certificates"); err != nil {
			_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: update-ca-certificates: %v %s\n", err, strings.TrimSpace(string(out)))
		}
	}

	// 3. Riavvio controllato di k3s (containerd rilegge registries.yaml e
	// l'ambiente solo all'avvio), poi attesa del nodo Ready.
	env := []string{"KUBECONFIG=" + cfg.Kubeconfig}
	if k3sChanged {
		_, _ = fmt.Fprintln(a.Stdout, "==> Riavvio k3s (le immagini e il proxy cambiano solo al riavvio)")
		if dropCh {
			if out, err := a.Runner.Run(ctx, nil, "systemctl", "daemon-reload"); err != nil {
				a.errorf("config set network: systemctl daemon-reload: %v\n%s", err, strings.TrimSpace(string(out)))
				return ExitUnexpected
			}
		}
		if out, err := a.Runner.Run(ctx, nil, "systemctl", "restart", "k3s"); err != nil {
			a.errorf("config set network: systemctl restart k3s: %v\n%s\nI file sono già scritti: sistema la causa (journalctl -u k3s) e rilancia lo stesso comando.", err, strings.TrimSpace(string(out)))
			return ExitCluster
		}
		if err := a.waitNodeReady(ctx, env); err != nil {
			a.errorf("config set network: %v", err)
			return ExitCluster
		}
	}

	// 4. CA nei pod: ConfigMap <release>-corporate-ca (chiave ca.crt).
	cmName := ""
	if n.CABundle != "" {
		cmName = cfg.Release + "-corporate-ca"
		_, _ = fmt.Fprintf(a.Stdout, "==> ConfigMap %s con la CA aziendale\n", cmName)
		bundle, err := a.corporateCABundle(caPEM, paths)
		if err != nil {
			a.errorf("config set network: %v", err)
			return ExitUnexpected
		}
		defer func() { _ = os.Remove(bundle) }()
		yml, err := a.Runner.Run(ctx, env, "k3s", "kubectl", "-n", cfg.Namespace, "create", "configmap", cmName,
			"--from-file=ca.crt="+bundle, "--dry-run=client", "-o", "yaml")
		if err != nil {
			a.errorf("config set network: kubectl create configmap: %v\n%s", err, strings.TrimSpace(string(yml)))
			return ExitCluster
		}
		tmp, err := os.CreateTemp(paths.NetworkDir, ".cm.*.yaml")
		if err != nil {
			tmp, err = os.CreateTemp("", "gitstack-cm.*.yaml")
		}
		if err != nil {
			a.errorf("config set network: %v", err)
			return ExitUnexpected
		}
		defer func() { _ = os.Remove(tmp.Name()) }()
		_, _ = tmp.Write(yml)
		_ = tmp.Close()
		if out, err := a.Runner.Run(ctx, env, "k3s", "kubectl", "apply", "-f", tmp.Name()); err != nil {
			a.errorf("config set network: kubectl apply: %v\n%s", err, strings.TrimSpace(string(out)))
			return ExitCluster
		}
	}

	// 5. Servizi: i valori Helm proxy.* e corporateCA.configMap (chart N9b).
	_, _ = fmt.Fprintln(a.Stdout, "==> Aggiorno i servizi (proxy e CA nei pod)")
	helmArgs := []string{"upgrade", cfg.Release, cfg.ChartDir, "--namespace", cfg.Namespace, "--reuse-values",
		"--set", "proxy.http=" + helmEscape(n.HTTPProxy),
		"--set", "proxy.https=" + helmEscape(effectiveHTTPS(n)),
		"--set", "proxy.noProxy=" + helmEscape(n.NoProxy),
		"--set", "corporateCA.configMap=" + cmName,
		"--wait", "--timeout", "5m"}
	if out, err := a.Runner.Run(ctx, env, a.helmBin(), helmArgs...); err != nil {
		a.errorf("config set network: helm upgrade fallito: %v\n%s\nI file di k3s sono già aggiornati: dopo aver sistemato la causa rilancia lo stesso comando.", err, strings.TrimSpace(string(out)))
		return ExitCluster
	}

	// 6. Configurazione, per ultima.
	if err := config.SetNetwork(path, n); err != nil {
		a.errorf("config set network: %v", err)
		return ExitUnexpected
	}
	_, _ = fmt.Fprintf(a.Stdout, "Rete aziendale aggiornata (config: %s).\n", path)
	a.printNetworkSummary(n)
	// Admin · System non resta vecchio dopo un cambio di rete (best effort).
	if c2, err := config.Load(path); err == nil {
		a.autoPublish(ctx, c2, path)
	}
	return ExitOK
}

// corporateCABundle scrive il file che va nel ConfigMap dei pod: CA di sistema
// più CA aziendale. Per il mirror git il chart sostituisce le CA di sistema con
// questo file (corporateCA.configMap): senza quelle pubbliche un mirror verso un
// host raggiunto direttamente (NO_PROXY) non si fiderebbe più. Stessa logica di
// ensure_corporate_ca_configmap in deploy/install.sh. Restituisce il percorso
// del file temporaneo, da rimuovere.
func (a *App) corporateCABundle(corporate []byte, paths networkPaths) (string, error) {
	sys := a.Getenv("GITSTACK_SYSTEM_CA_BUNDLE")
	if sys == "" {
		sys = "/etc/ssl/certs/ca-certificates.crt"
	}
	var buf bytes.Buffer
	if b, err := os.ReadFile(sys); err == nil {
		buf.Write(b)
		buf.WriteByte('\n')
	}
	buf.Write(corporate)
	dir := paths.NetworkDir
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir = ""
	}
	f, err := os.CreateTemp(dir, ".ca-bundle.*.crt")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

// helmEscape protegge le virgole di un valore per --set.
func helmEscape(v string) string {
	return strings.NewReplacer(`\`, `\\`, ",", `\,`).Replace(v)
}

func (a *App) waitNodeReady(ctx context.Context, env []string) error {
	timeout, poll := a.ServingTimeout, a.ServingPoll
	if timeout <= 0 {
		timeout = networkWaitDefault
	}
	if poll <= 0 {
		poll = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		out, err := a.Runner.Run(ctx, env, "k3s", "kubectl", "get", "nodes", "--no-headers")
		if err == nil {
			for _, l := range strings.Split(string(out), "\n") {
				if f := strings.Fields(l); len(f) > 1 && f[1] == "Ready" {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("il nodo k3s non è tornato Ready entro %s dopo il riavvio (journalctl -u k3s -n 100)", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (a *App) networkUsage() {
	_, _ = fmt.Fprintln(a.Stderr, `Uso: gitstack config set network [opzioni]
  --http-proxy URL  --https-proxy URL  --no-proxy a,b,c
  --registry-mirror URL  --registry-username U  --registry-password-file FILE
  --download-mirror URL  --ca-bundle FILE
  --clear (azzera tutto)  --force (applica anche se non rispondono)  --config PERCORSO
Un'opzione con valore vuoto (--http-proxy "") toglie quella impostazione.`)
}

func (a *App) printNetworkSummary(n config.Network) {
	rows := [][2]string{
		{"proxy HTTP", config.Redact(n.HTTPProxy)}, {"proxy HTTPS", config.Redact(effectiveHTTPS(n))},
		{"no proxy", n.NoProxy}, {"mirror immagini", n.RegistryMirror},
		{"mirror download", n.DownloadMirror}, {"CA aziendale", n.CABundle},
	}
	var keys []string
	for _, r := range rows {
		if r[1] != "" {
			keys = append(keys, fmt.Sprintf("  %-16s %s", r[0], r[1]))
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		_, _ = fmt.Fprintln(a.Stdout, "Nessun proxy, mirror o CA configurati.")
		return
	}
	_, _ = fmt.Fprintln(a.Stdout, strings.Join(keys, "\n"))
}

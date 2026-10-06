package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// tlsToolPath è lo script che emette il certificato (installato da install.sh).
const tlsToolPath = "/usr/local/sbin/gitstack-tls"

var hostNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil && !strings.Contains(s, ":")
}

// validHost accetta un nome DNS (anche .local) o un IPv4.
func validHost(h string) error {
	if isIPv4(h) {
		return nil
	}
	if len(h) > 253 || !hostNameRe.MatchString(h) || strings.Contains(h, "..") {
		return fmt.Errorf("%q non è un nome host valido (lettere, cifre, '-' e '.'; senza schema né porta) né un IPv4", h)
	}
	return nil
}

func isLocalName(h string) bool {
	return strings.HasSuffix(strings.ToLower(h), ".local")
}

// notPublicName dice se Let's Encrypt rifiuterebbe il nome (stessa regola di install.sh).
func notPublicName(h string) bool {
	h = strings.ToLower(h)
	if isIPv4(h) || !strings.Contains(h, ".") {
		return true
	}
	for _, s := range []string{".local", ".lan", ".internal", ".home", ".localdomain"} {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

func runConfig(ctx context.Context, a *App, args []string) int {
	if len(args) < 2 || args[0] != "set" {
		a.errorf("config: uso: gitstack config set host <nome> [--config PERCORSO]")
		a.usage(a.Stderr)
		return ExitUsage
	}
	switch args[1] {
	case "host":
		return runConfigSetHost(ctx, a, args[2:])
	default:
		a.errorf("config set: chiave sconosciuta %q (oggi: host)", args[1])
		return ExitUsage
	}
}

func (a *App) localAddrs() []string {
	if a.LocalAddrs != nil {
		return a.LocalAddrs()
	}
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, ad := range addrs {
		if ipn, ok := ad.(*net.IPNet); ok {
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

// checkHostResolves verifica col resolver di sistema (getent hosts: con
// nss-mdns risolve anche i .local) che il nome punti a un indirizzo della
// macchina. Restituisce un avviso, mai un errore: non blocca il comando.
func (a *App) checkHostResolves(ctx context.Context, name string) string {
	if isIPv4(name) {
		return ""
	}
	out, err := a.Runner.Run(ctx, nil, "getent", "hosts", name)
	if err != nil || strings.TrimSpace(string(out)) == "" {
		w := fmt.Sprintf("il nome %q non risolve su questa macchina (getent hosts).", name)
		if isLocalName(name) {
			w += " Per un nome .local serve avahi-daemon attivo (e libnss-mdns): vedi deploy/README.md, sezione «Nomi .local»."
		} else {
			w += " Crea il record DNS (o la voce nel router) prima che gli utenti lo usino."
		}
		return w
	}
	mine := map[string]bool{}
	for _, ad := range a.localAddrs() {
		mine[ad] = true
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && mine[f[0]] {
			return ""
		}
	}
	return fmt.Sprintf("il nome %q risolve (%s) ma non a un indirizzo di questa macchina: i client non raggiungeranno GitStack con quel nome.", name, strings.Fields(string(out))[0])
}

func (a *App) helmBin() string {
	look := a.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if p, err := look("helm"); err == nil {
		return p
	}
	return "helm"
}

func runConfigSetHost(ctx context.Context, a *App, args []string) int {
	fs := flag.NewFlagSet("config set host", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	// Gli argomenti posizionali possono precedere i flag.
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			a.errorf("config set host: %v", err)
			return ExitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) != 1 {
		a.errorf("config set host: serve un solo nome o IP: gitstack config set host <nome>")
		return ExitUsage
	}
	host := strings.ToLower(pos[0])
	if err := validHost(host); err != nil {
		a.errorf("config set host: %v", err)
		return ExitUsage
	}
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	if cfg.Host == host {
		_, _ = fmt.Fprintf(a.Stdout, "Il host è già %s: niente da fare.\n", host)
		return ExitOK
	}
	if cfg.TLS == "letsencrypt" && notPublicName(host) {
		a.errorf("config set host: %q non è un nome pubblico e l'installazione usa Let's Encrypt (che non emette certificati per IP e domini locali).", host)
		return ExitConfig
	}
	if cfg.ChartDir == "" || !dirHasChart(cfg.ChartDir) {
		a.errorf("config set host: il chart di GitStack non è sul disco (chart_dir %q): rilancia deploy/install.sh --host %s, che lo copia e imposta il nome.", cfg.ChartDir, host)
		return ExitConfig
	}

	if w := a.checkHostResolves(ctx, host); w != "" {
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: %s\n", w)
	}
	if isLocalName(host) {
		_, _ = fmt.Fprintln(a.Stderr, "ATTENZIONE: nome .local (mDNS): serve avahi-daemon sul server e client con supporto mDNS; i pod di k3s non risolvono .local.")
	}

	tlsDir := a.Getenv("GITSTACK_TLS_DIR")
	if tlsDir == "" {
		tlsDir = filepath.Join(filepath.Dir(path), "tls")
	}
	tlsBin := a.Getenv("GITSTACK_TLS_BIN")
	if tlsBin == "" {
		tlsBin = tlsToolPath
	}

	// 1. Certificato (internal e custom: con custom lo script ricontrolla i SAN
	// del certificato del cliente e avvisa se non coprono il nome).
	if cfg.TLS == "internal" || cfg.TLS == "custom" {
		names, ips := "", readTLSIPs(tlsDir)
		if isIPv4(host) {
			ips = joinUnique(ips, host)
		} else {
			names = host
		}
		_, _ = fmt.Fprintf(a.Stdout, "==> Rigenero il certificato (SAN: %s)\n", strings.Trim(names+","+ips, ","))
		out, err := a.Runner.Run(ctx, []string{"GITSTACK_TLS_DIR=" + tlsDir}, tlsBin, "ensure", "--names", names, "--ips", ips)
		if err != nil {
			a.errorf("config set host: gitstack-tls: %v\n%s", err, strings.TrimSpace(string(out)))
			return ExitUnexpected
		}
		if len(out) > 0 {
			_, _ = a.Stdout.Write(out)
		}
	}

	// 2. Servizi: gli URL pubblici sono valori Helm (core e OIDC di identity).
	scheme := "https"
	if cfg.TLS == "insecure" || cfg.TLS == "" {
		scheme = "http"
	}
	url := scheme + "://" + host
	_, _ = fmt.Fprintf(a.Stdout, "==> Aggiorno i servizi (URL pubblico %s)\n", url)
	helmArgs := []string{"upgrade", cfg.Release, cfg.ChartDir, "--namespace", cfg.Namespace, "--reuse-values",
		"--set", "core.env.publicUrl=" + url, "--set", "identity.oidc.publicUrl=" + url, "--wait", "--timeout", "5m"}
	if out, err := a.Runner.Run(ctx, []string{"KUBECONFIG=" + cfg.Kubeconfig}, a.helmBin(), helmArgs...); err != nil {
		a.errorf("config set host: helm upgrade fallito: %v\n%s\nIl certificato è già stato rigenerato: dopo aver sistemato la causa rilancia lo stesso comando.", err, strings.TrimSpace(string(out)))
		return ExitCluster
	}

	// 3. Configurazione, per ultima: se i passi prima falliscono si può rilanciare.
	if err := rewriteHost(path, host); err != nil {
		a.errorf("config set host: %v", err)
		return ExitUnexpected
	}
	updateInstallConf(tlsDir, host)

	_, _ = fmt.Fprintf(a.Stdout, "Host impostato a %s (config: %s).\n", host, path)
	_, _ = fmt.Fprintf(a.Stdout, "ATTENZIONE: cambiano gli indirizzi di clone (%s/<owner>/<repo>.git, ssh://git@%s:%d/<owner>/<repo>.git) e i link nelle email già inviate puntano ancora al nome vecchio: aggiorna i clone esistenti con git remote set-url.\n", url, host, cfg.SSHPort)
	return ExitOK
}

func dirHasChart(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "Chart.yaml"))
	return err == nil
}

// readTLSIPs legge TLS_IPS da tls.conf (gli IP già nel certificato).
func readTLSIPs(tlsDir string) string {
	b, err := os.ReadFile(filepath.Join(tlsDir, "tls.conf"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "TLS_IPS="); ok {
			return v
		}
	}
	return ""
}

func joinUnique(list, v string) string {
	for _, x := range strings.Split(list, ",") {
		if x == v {
			return list
		}
	}
	if list == "" {
		return v
	}
	return list + "," + v
}

// rewriteHost cambia solo la riga `host:` del file, conservando commenti e
// resto, e lo riscrive in modo atomico con modo 0600.
func rewriteHost(path, host string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	done := false
	for i, l := range lines {
		if strings.HasPrefix(l, "host:") {
			lines[i] = "host: " + host
			done = true
			break
		}
	}
	if !done {
		return errors.New("riga host: non trovata nel file di configurazione")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(strings.Join(lines, "\n")); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// updateInstallConf allinea HOSTS di install.conf, perché rieseguire
// install.sh senza --host non riporti il nome vecchio. Best effort.
func updateInstallConf(tlsDir, host string) {
	p := filepath.Join(tlsDir, "install.conf")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "HOSTS=") {
			lines[i] = "HOSTS=" + host
		}
	}
	_ = os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o600)
}

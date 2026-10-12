package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Network è la sezione `network:` di config.yaml (N9): la scrive install.sh
// (--http-proxy, --registry-mirror...) e la cambia `gitstack config set
// network`. Il file è root-only (0600): può contenere la password del mirror.
type Network struct {
	HTTPProxy        string `yaml:"http_proxy"`
	HTTPSProxy       string `yaml:"https_proxy"`
	NoProxy          string `yaml:"no_proxy"`
	RegistryMirror   string `yaml:"registry_mirror"`
	RegistryUsername string `yaml:"registry_username"`
	RegistryPassword string `yaml:"registry_password"`
	DownloadMirror   string `yaml:"download_mirror"`
	// CABundle è la copia della CA aziendale tenuta da GitStack.
	CABundle string `yaml:"ca_bundle"`
}

// IsZero dice se non è configurato niente.
func (n Network) IsZero() bool { return n == Network{} }

// Validate controlla i valori di rete.
func (n Network) Validate() error {
	for name, v := range map[string]string{"http_proxy": n.HTTPProxy, "https_proxy": n.HTTPSProxy} {
		if v == "" {
			continue
		}
		if err := checkURL(v, "http", "https", "socks5"); err != nil {
			return fmt.Errorf("network.%s: %v", name, err)
		}
	}
	for name, v := range map[string]string{"registry_mirror": n.RegistryMirror, "download_mirror": n.DownloadMirror} {
		if v == "" {
			continue
		}
		if err := checkURL(v, "http", "https"); err != nil {
			return fmt.Errorf("network.%s: %v", name, err)
		}
	}
	if strings.ContainsAny(n.NoProxy, " \t\n") {
		return fmt.Errorf("network.no_proxy: elementi separati da virgole, senza spazi")
	}
	if (n.RegistryUsername == "") != (n.RegistryPassword == "") {
		return fmt.Errorf("network.registry_username e registry_password vanno impostati insieme")
	}
	if n.RegistryUsername != "" && n.RegistryMirror == "" {
		return fmt.Errorf("network.registry_username senza registry_mirror")
	}
	if n.CABundle != "" && !strings.HasPrefix(n.CABundle, "/") && !filepath.IsAbs(n.CABundle) {
		return fmt.Errorf("network.ca_bundle %q deve essere un percorso assoluto", n.CABundle)
	}
	return nil
}

func checkURL(v string, schemes ...string) error {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q non è un URL (atteso <schema>://host[:porta])", redact(v))
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return nil
		}
	}
	return fmt.Errorf("schema %q non supportato (ammessi: %s)", u.Scheme, strings.Join(schemes, ", "))
}

// redact toglie la password da un URL, per i messaggi.
func redact(v string) string {
	if u, err := url.Parse(v); err == nil && u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
			return u.String()
		}
	}
	return v
}

// Redact è redact esportata per i messaggi dei comandi.
func Redact(v string) string { return redact(v) }

// yamlQuote scrive s come stringa YAML tra apici doppi.
func yamlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

// SetNetwork riscrive la sezione `network:` di path (le chiavi vuote non si
// scrivono; tutto vuoto toglie la sezione), conservando il resto del file, in
// modo atomico con modo 0600.
func SetNetwork(path string, n Network) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var kept []string
	skipping := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		switch {
		case l == "network:":
			skipping = true
			continue
		case skipping && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || l == ""):
			continue
		default:
			skipping = false
		}
		kept = append(kept, l)
	}
	if !n.IsZero() {
		kept = append(kept, "network:")
		for _, kv := range [][2]string{
			{"http_proxy", n.HTTPProxy}, {"https_proxy", n.HTTPSProxy}, {"no_proxy", n.NoProxy},
			{"registry_mirror", n.RegistryMirror}, {"registry_username", n.RegistryUsername},
			{"registry_password", n.RegistryPassword}, {"download_mirror", n.DownloadMirror},
			{"ca_bundle", n.CABundle},
		} {
			if kv[1] != "" {
				kept = append(kept, "  "+kv[0]+": "+yamlQuote(kv[1]))
			}
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(strings.Join(kept, "\n") + "\n"); err != nil {
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

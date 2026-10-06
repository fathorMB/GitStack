// Package config gestisce la configurazione multi-istanza di gs (G7).
//
// Layout, nella cartella GS_CONFIG_DIR o, se assente, <UserConfigDir>/gs:
//
//	config.yaml          default_host: ultima istanza configurata
//	hosts/<host>.yaml    user, git_protocol e, solo se non c'è il portachiavi, token
//
// File 0600 e cartelle 0700 su Unix; su Windows ACL solo per l'utente corrente.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvConfigDir è la variabile che sposta la cartella di configurazione.
const EnvConfigDir = "GS_CONFIG_DIR"

// HostConfig è la configurazione di una istanza.
type HostConfig struct {
	User        string `yaml:"user,omitempty"`
	GitProtocol string `yaml:"git_protocol,omitempty"`
	Token       string `yaml:"token,omitempty"`
}

type mainFile struct {
	DefaultHost string `yaml:"default_host,omitempty"`
}

// Config è la cartella di configurazione.
type Config struct {
	dir string
}

// Dir ricava la cartella di configurazione dall'ambiente.
func Dir(getenv func(string) string) (string, error) {
	if d := getenv(EnvConfigDir); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cartella di configurazione: %w (imposta %s)", err, EnvConfigDir)
	}
	return filepath.Join(base, "gs"), nil
}

// New apre la configurazione nella cartella dir (non la crea finché non si scrive).
func New(dir string) *Config { return &Config{dir: dir} }

// Load apre la configurazione dalla cartella derivata dall'ambiente.
func Load(getenv func(string) string) (*Config, error) {
	d, err := Dir(getenv)
	if err != nil {
		return nil, err
	}
	return New(d), nil
}

// Path è la cartella di configurazione.
func (c *Config) Path() string { return c.dir }

// HostPath è il file di una istanza. I caratteri non ammessi nei nomi file
// (i due punti di host:porta) diventano "_".
func (c *Config) HostPath(host string) string {
	name := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(strings.ToLower(host))
	return filepath.Join(c.dir, "hosts", name+".yaml")
}

// DefaultHost è l'istanza predefinita, vuota se non ce n'è.
func (c *Config) DefaultHost() (string, error) {
	var m mainFile
	if err := readYAML(filepath.Join(c.dir, "config.yaml"), &m); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return m.DefaultHost, nil
}

// Host legge la configurazione di una istanza; ok è false se non esiste.
func (c *Config) Host(host string) (hc HostConfig, ok bool, err error) {
	err = readYAML(c.HostPath(host), &hc)
	if errors.Is(err, fs.ErrNotExist) {
		return HostConfig{}, false, nil
	}
	if err != nil {
		return HostConfig{}, false, err
	}
	return hc, true, nil
}

// SaveHost scrive la configurazione di una istanza e la rende predefinita
// (default_host = ultima istanza configurata).
func (c *Config) SaveHost(host string, hc HostConfig) error {
	if err := c.writeYAML(c.HostPath(host), hc); err != nil {
		return err
	}
	return c.writeYAML(filepath.Join(c.dir, "config.yaml"), mainFile{DefaultHost: host})
}

// SetDefaultHost cambia l'istanza predefinita.
func (c *Config) SetDefaultHost(host string) error {
	return c.writeYAML(filepath.Join(c.dir, "config.yaml"), mainFile{DefaultHost: host})
}

// DeleteHost toglie la configurazione di una istanza. Se era la predefinita,
// default_host passa a un'altra istanza rimasta (la prima in ordine alfabetico)
// o si svuota.
func (c *Config) DeleteHost(host string) error {
	if err := os.Remove(c.HostPath(host)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	def, err := c.DefaultHost()
	if err != nil || def != host {
		return err
	}
	next := ""
	if hs, err := c.Hosts(); err == nil && len(hs) > 0 {
		next = hs[0]
	}
	return c.SetDefaultHost(next)
}

// Hosts elenca le istanze configurate (dal nome del file), in ordine.
func (c *Config) Hosts() ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(c.dir, "hosts"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && !e.IsDir() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func readYAML(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (c *Config) writeYAML(path string, v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return c.write(path, b)
}

// write scrive data in path in modo atomico (file temporaneo accanto, poi
// rename) con permessi solo-utente. La cartella di configurazione e hosts/
// sono create 0700 (ACL utente su Windows).
func (c *Config) write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, d := range []string{c.dir, filepath.Join(c.dir, "hosts")} {
		if d == dir || d == c.dir {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				if err := restrict(d, true); err != nil {
					return err
				}
			}
		}
	}
	tmp, err := os.CreateTemp(dir, ".gs-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := restrict(name, false); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

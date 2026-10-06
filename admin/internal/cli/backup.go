package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/backupstate"
	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
)

// ExitRefused è il codice di un restore (o backup) rifiutato con motivo:
// versione diversa, archivio corrotto o cifrato senza chiave.
const ExitRefused = 6

// ExitNotServing è il codice di un restore completato nei dati ma con
// l'istanza che non risponde attraverso l'Ingress entro il timeout
// (/api/healthz o /downloads/ca.crt).
const ExitNotServing = 9

// Attesa dell'Ingress dopo un restore: un tentativo ogni 2 s, ciascuno con
// timeout di 5 s, al massimo 120 s.
const (
	defaultServingTimeout = 120 * time.Second
	defaultServingPoll    = 2 * time.Second
)

// waitServing ripete status.CheckServing finché passa o scade il timeout.
func (a *App) waitServing(ctx context.Context, cfg *config.Config) error {
	timeout, poll := a.ServingTimeout, a.ServingPoll
	if timeout <= 0 {
		timeout = defaultServingTimeout
	}
	if poll <= 0 {
		poll = defaultServingPoll
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last error
	for {
		actx, acancel := context.WithTimeout(ctx, 5*time.Second)
		if a.CheckServing != nil {
			last = a.CheckServing(actx, cfg)
		} else {
			last = status.CheckServing(actx, cfg, a.HTTP)
		}
		acancel()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("nessuna risposta valida entro %s; ultimo errore: %v", timeout.Round(time.Second), last)
		case <-time.After(poll):
		}
	}
}

// defaultConfigDir è la cartella della configurazione dell'installazione.
const defaultConfigDir = "/etc/gitstack"

func (a *App) cluster(cfg *config.Config) (backup.Cluster, error) {
	if a.NewCluster != nil {
		return a.NewCluster(cfg), nil
	}
	look := a.LookPath
	if look == nil {
		look = exec.LookPath
	}
	k := &backup.KubectlCluster{Kubeconfig: cfg.Kubeconfig, Namespace: cfg.Namespace, Release: cfg.Release}
	if p, err := look("kubectl"); err == nil {
		k.Bin = p
	} else if p, err := look("k3s"); err == nil {
		k.Bin, k.Pre = p, []string{"kubectl"}
	} else {
		return nil, errors.New("né kubectl né k3s nel PATH")
	}
	return k, nil
}

func readKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("file della chiave: %w", err)
	}
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) < backup.MinKeyLen {
		return nil, fmt.Errorf("la chiave in %s è troppo corta (almeno %d caratteri): per esempio `openssl rand -base64 32 > chiave`", path, backup.MinKeyLen)
	}
	return b, nil
}

func (a *App) options(cfg *config.Config, cfgPath string, key []byte) *backup.Options {
	dir := a.configDir(cfgPath)
	abs, err := filepath.Abs(cfgPath)
	if err != nil {
		abs = cfgPath
	}
	return &backup.Options{Cfg: cfg, BinaryVersion: a.Version, ConfigDir: dir, ConfigFile: abs, Key: key, Log: a.Stdout}
}

func (a *App) backupFailure(op string, err error) int {
	var ref *backup.RefusedError
	if errors.As(err, &ref) {
		a.errorf("%s rifiutato: %v", op, err)
		return ExitRefused
	}
	a.errorf("%s: %v", op, err)
	return ExitUnexpected
}

func runBackup(ctx context.Context, a *App, args []string) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	dest := fs.String("dest", "", "cartella di destinazione (default backup.destination del config)")
	keyFile := fs.String("key-file", "", "file con la chiave per cifrare l'archivio (AES-256-GCM); senza, l'archivio non è cifrato")
	retention := fs.Int("retention", 0, "numero di backup da conservare (default: backup.retention del config)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if err == nil {
			err = fmt.Errorf("argomento inatteso: %s", fs.Arg(0))
		}
		a.errorf("backup: %v", err)
		a.usage(a.Stderr)
		return ExitUsage
	}
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	var key []byte
	if *keyFile != "" {
		var err error
		if key, err = readKey(*keyFile); err != nil {
			a.errorf("backup: %v", err)
			return ExitUsage
		}
	}
	cl, err := a.cluster(cfg)
	if err != nil {
		a.backupStateWrite(path, false, "", err)
		a.errorf("backup: %v", err)
		return ExitCluster
	}
	o := a.options(cfg, path, key)
	o.Cluster, o.DestDir = cl, *dest
	if *retention > 0 {
		o.Retention = *retention
	}
	if a.Now != nil {
		o.Now = a.Now
	}
	res, err := backup.Backup(ctx, o)
	if err != nil {
		code := a.backupFailure("backup", err)
		a.backupStateWrite(path, false, "", err)
		return code
	}
	_, _ = fmt.Fprintf(a.Stdout, "Backup completato: %s\n  SHA-256: %s\n  Versione: %s\n  Finestra di sola lettura: %.1f s\n",
		res.Path, res.SHA256, res.Manifest.Version, res.Window.Seconds())
	if key == nil {
		_, _ = fmt.Fprintln(a.Stdout, "  Archivio NON cifrato (root-only 0600): contiene le chiavi dell'installazione. Usa --key-file per cifrarlo.")
	}
	a.backupStateWrite(path, true, res.Path, nil)
	return ExitOK
}

// backupStateWrite scrive il file di stato dell'ultimo backup.
// Non fallisce se la scrittura non è possibile: il comando comunque
// segnala l'errore al terminale.
func (a *App) backupStateWrite(cfgPath string, success bool, path string, err error) {
	s := backupstate.State{
		Success: success,
		Path:    path,
		Error:   "",
		At:      time.Now().UTC(),
	}
	if err != nil {
		s.Error = err.Error()
	}
	cfgDir := a.configDir(cfgPath)
	_ = backupstate.Write(cfgDir, s)
}

// publishTLS lancia `gitstack-tls ensure` sulla cartella tls ripristinata,
// senza opzioni: nomi e IP vengono da tls.conf della nuova installazione, quindi
// il certificato del server è riemesso (con la CA del backup) se i SAN o la
// firma non combaciano.
func (a *App) publishTLS(ctx context.Context, configDir string) error {
	tlsDir := a.Getenv("GITSTACK_TLS_DIR")
	if tlsDir == "" {
		tlsDir = filepath.Join(configDir, "tls")
	}
	tlsBin := a.Getenv("GITSTACK_TLS_BIN")
	if tlsBin == "" {
		tlsBin = tlsToolPath
	}
	out, err := a.Runner.Run(ctx, []string{"GITSTACK_TLS_DIR=" + tlsDir}, tlsBin, "ensure")
	if len(out) > 0 {
		_, _ = a.Stdout.Write(out)
	}
	if err != nil {
		return fmt.Errorf("gitstack-tls ensure: %w", err)
	}
	return nil
}

func runRestore(ctx context.Context, a *App, args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	dest := fs.String("dest", "", "cartella dei backup, dove lavora lo stage (default backup.destination del config)")
	keyFile := fs.String("key-file", "", "file con la chiave per decifrare l'archivio")
	// Flag e archivio in qualsiasi ordine: l'archivio è l'unico argomento.
	var archive string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			a.errorf("restore: %v", err)
			a.usage(a.Stderr)
			return ExitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		if archive != "" {
			a.errorf("restore: argomento inatteso: %s", fs.Arg(0))
			return ExitUsage
		}
		archive = fs.Arg(0)
		rest = fs.Args()[1:]
	}
	if archive == "" {
		a.errorf("restore: manca l'archivio. Uso: gitstack restore [--dest D] [--key-file F] <archivio>")
		return ExitUsage
	}
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	var key []byte
	if *keyFile != "" {
		var err error
		if key, err = readKey(*keyFile); err != nil {
			a.errorf("restore: %v", err)
			return ExitUsage
		}
	}
	cl, err := a.cluster(cfg)
	if err != nil {
		a.errorf("restore: %v", err)
		return ExitCluster
	}
	o := a.options(cfg, path, key)
	o.Cluster, o.DestDir = cl, *dest
	// Con TLS internal o custom la CA e il certificato ripristinati su disco
	// vanno pubblicati nel cluster; con insecure e letsencrypt niente.
	if cfg.TLS == "internal" || cfg.TLS == "custom" {
		o.PublishTLS = func(ctx context.Context) error { return a.publishTLS(ctx, o.ConfigDir) }
	}
	o.WaitServing = func(ctx context.Context) error { return a.waitServing(ctx, cfg) }
	if err := backup.Restore(ctx, o, archive); err != nil {
		var se *backup.ServingError
		if errors.As(err, &se) {
			a.errorf("restore: %v", err)
			return ExitNotServing
		}
		return a.backupFailure("restore", err)
	}
	_, _ = fmt.Fprintln(a.Stdout, "Restore completato: database, repo, allegati, Secret e configurazione ripristinati.")
	return ExitOK
}

package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/backupstate"
	"github.com/fathorMB/GitStack/admin/internal/config"
)

// ExitUploadFailed è il codice di un backup locale riuscito il cui
// caricamento su S3 è fallito (N7): l'archivio locale è valido, il servizio
// systemd risulta fallito e la causa è in `gitstack status`.
const ExitUploadFailed = 10

const (
	envS3AccessKey = "GITSTACK_S3_ACCESS_KEY"
	envS3SecretKey = "GITSTACK_S3_SECRET_KEY"
	s3CheckTimeout = 30 * time.Second
	notifyTimeout  = 60 * time.Second
)

func (a *App) newStore(c config.S3) (backup.ObjectStore, error) {
	if a.NewStore != nil {
		return a.NewStore(c)
	}
	return backup.NewS3Store(c)
}

// s3Key legge la chiave di cifratura indicata dalla configurazione S3.
func s3Key(cfg *config.Config) ([]byte, error) {
	if cfg.Backup.S3 == nil {
		return nil, nil
	}
	return readKey(cfg.Backup.S3.KeyFile)
}

// scrubS3 toglie le chiavi S3 da un testo prima che arrivi a un log, a
// `gitstack status` o a una notifica.
func scrubS3(cfg *config.Config, msg string) string {
	if cfg.Backup.S3 == nil {
		return msg
	}
	for _, v := range []string{cfg.Backup.S3.SecretKey, cfg.Backup.S3.AccessKey} {
		if len(v) >= 4 {
			msg = strings.ReplaceAll(msg, v, "***")
		}
	}
	return msg
}

// uploadBackup carica l'archivio appena prodotto. Ritorna nil senza S3
// configurato; altrimenti l'esito (anche in errore) da registrare.
func (a *App) uploadBackup(ctx context.Context, cfg *config.Config, o *backup.Options, archive string) (*backupstate.Upload, error) {
	if cfg.Backup.S3 == nil {
		return nil, nil
	}
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	st, err := a.newStore(*cfg.Backup.S3)
	var res *backup.UploadResult
	if err == nil {
		res, err = backup.Upload(ctx, o, st, archive)
	}
	up := &backupstate.Upload{At: now, Name: baseName(archive)}
	if err != nil {
		up.Error = scrubS3(cfg, err.Error())
		return up, fmt.Errorf("%s", up.Error)
	}
	up.Success = true
	if res.PruneEr != nil {
		// Il caricamento è riuscito: la potatura non riuscita si dice, non si
		// trasforma in un errore del backup.
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: archivio caricato, ma la conservazione su S3 non è riuscita: %s\n", scrubS3(cfg, res.PruneEr.Error()))
	}
	return up, nil
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// notifyUploadFailure avvisa gli amministratori. Scelta (GIT-204): il tool
// gira sull'host, fuori dal cluster e senza credenziali per le API interne
// di core, che non hanno un endpoint per notificare gli admin; aggiungerlo
// vorrebbe dire contratto, core e gateway per un solo evento. Perciò: lo
// stato in `gitstack status`, il servizio systemd che risulta fallito (exit
// 10) e, a scelta dell'amministratore, backup.s3.notify_command (mail,
// ntfy, Slack...).
func (a *App) notifyUploadFailure(ctx context.Context, cfg *config.Config, archive, msg string) {
	cmd := strings.TrimSpace(cfg.Backup.S3.NotifyCommand)
	if cmd == "" {
		return
	}
	nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
	defer cancel()
	env := []string{
		"GITSTACK_BACKUP_EVENT=s3-upload-failed",
		"GITSTACK_BACKUP_ARCHIVE=" + archive,
		"GITSTACK_BACKUP_ERROR=" + msg,
		"GITSTACK_HOST=" + cfg.Host,
	}
	if out, err := a.Runner.Run(nctx, env, "sh", "-c", cmd); err != nil {
		_, _ = fmt.Fprintf(a.Stderr, "ATTENZIONE: notify_command fallito: %s\n", scrubS3(cfg, err.Error()))
		_ = out
	}
}

// restoreFromS3 scarica l'archivio (il più recente se name è vuoto) in una
// cartella di lavoro dentro la cartella dei backup e ritorna il percorso e la
// funzione che la rimuove.
func (a *App) restoreFromS3(ctx context.Context, cfg *config.Config, o *backup.Options, name string) (string, func(), error) {
	st, err := a.newStore(*cfg.Backup.S3)
	if err != nil {
		return "", func() {}, err
	}
	base := o.DestDir
	if base == "" {
		base = cfg.Backup.Destination
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp(base, ".s3-fetch-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	p, err := backup.FetchFromS3(ctx, o, st, name, dir)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return p, cleanup, nil
}

// runConfigSetBackupS3 implementa `gitstack config set backup.s3 …`.
func runConfigSetBackupS3(ctx context.Context, a *App, args []string) int {
	fs := flag.NewFlagSet("config set backup.s3", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", configFlagUsage)
	endpoint := fs.String("endpoint", "", "endpoint S3: host[:porta] o URL con schema (http:// per MinIO in chiaro)")
	bucket := fs.String("bucket", "", "nome del bucket")
	prefix := fs.String("prefix", "", "prefisso (cartella) nel bucket")
	region := fs.String("region", "", "regione (default "+config.DefaultS3Region+")")
	accessKey := fs.String("access-key", "", "chiave di accesso (o $"+envS3AccessKey+")")
	secretKey := fs.String("secret-key", "", "chiave segreta (meglio $"+envS3SecretKey+": gli argomenti si vedono con ps)")
	keyFile := fs.String("key-file", "", "file con la chiave di cifratura degli archivi (la stessa di backup --key-file): obbligatorio")
	retention := fs.String("retention", "", "archivi da tenere su S3 (default 30) oppure «bucket» per lasciar fare al bucket")
	notify := fs.String("notify-command", "", "comando (sh -c) da eseguire quando il caricamento fallisce")
	noCheck := fs.Bool("no-check", false, "non verificare subito la connessione al bucket")
	remove := fs.Bool("remove", false, "togli la configurazione S3: i backup restano solo locali")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		if err == nil {
			err = fmt.Errorf("argomento inatteso: %s", fs.Arg(0))
		}
		a.errorf("config set backup.s3: %v", err)
		return ExitUsage
	}
	path := a.configPath(*cfgPath)
	cfg, code := a.loadConfig(path)
	if cfg == nil {
		return code
	}
	if *remove {
		if err := config.SetBackupS3(path, nil); err != nil {
			a.errorf("config set backup.s3: %v", err)
			return ExitUnexpected
		}
		_, _ = fmt.Fprintln(a.Stdout, "Configurazione S3 rimossa: i backup restano solo locali. Gli archivi già su S3 non sono toccati.")
		return ExitOK
	}
	var s config.S3
	if cfg.Backup.S3 != nil {
		s = *cfg.Backup.S3
	}
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&s.Endpoint, *endpoint)
	set(&s.Bucket, *bucket)
	set(&s.Prefix, *prefix)
	set(&s.Region, *region)
	set(&s.KeyFile, *keyFile)
	set(&s.Retention, *retention)
	set(&s.NotifyCommand, *notify)
	set(&s.AccessKey, a.Getenv(envS3AccessKey))
	set(&s.SecretKey, a.Getenv(envS3SecretKey))
	set(&s.AccessKey, *accessKey)
	set(&s.SecretKey, *secretKey)
	if err := s.Validate(); err != nil {
		a.errorf("config set backup.s3: %v", scrubS3(&config.Config{Backup: config.Backup{S3: &s}}, err.Error()))
		return ExitUsage
	}
	if _, err := readKey(s.KeyFile); err != nil {
		a.errorf("config set backup.s3: %v", err)
		return ExitUsage
	}
	if !*noCheck {
		st, err := a.newStore(s)
		if err == nil {
			cctx, cancel := context.WithTimeout(ctx, s3CheckTimeout)
			err = st.Check(cctx)
			cancel()
		}
		if err != nil {
			a.errorf("config set backup.s3: %s\nNon ho scritto niente. Con --no-check la configurazione si salva senza verificare il bucket.",
				scrubS3(&config.Config{Backup: config.Backup{S3: &s}}, err.Error()))
			return ExitCluster
		}
	}
	if err := config.SetBackupS3(path, &s); err != nil {
		a.errorf("config set backup.s3: %v", err)
		return ExitUnexpected
	}
	keep := "default 30"
	if r := strings.TrimSpace(s.Retention); r != "" {
		keep = r
	}
	_, _ = fmt.Fprintf(a.Stdout, "Backup su S3 configurato: bucket %s (endpoint %s, prefisso %q), conservazione S3: %s. Archivi sempre cifrati con la chiave in %s.\n",
		s.Bucket, s.Endpoint, s.Prefix, keep, s.KeyFile)
	return ExitOK
}

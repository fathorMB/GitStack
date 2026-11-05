package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Backup e ripristino dei database gestiti (v3-d, GIT-553, ADR 0003, punto 5, DB13 e DB15).
//
// Il dump e' `pg_dump -Fc` eseguito con exec nel pod del database (mai la directory dati), cifrato AES-256-GCM
// con la chiave di dump dell'istanza e scritto su databases-data. Il ripristino e' SEMPRE
// `pg_restore --no-owner --no-acl --role=<name>_owner` e i privilegi di `<name>_ro` si riapplicano dopo.

const pgSocketDir = "/var/run/postgresql"

// RestoreOptions sono le scelte di un ripristino.
type RestoreOptions struct {
	// Clean sostituisce gli oggetti del dump gia' presenti (`--clean --if-exists`): il ripristino sul posto.
	Clean bool
}

// Dumper esegue pg_dump e pg_restore contro il database gestito. In produzione e' PodDumper (exec nel pod);
// i test d'integrazione ne danno uno su un Postgres vero.
type Dumper interface {
	// Dump scrive su w il dump `-Fc` del database d.Name.
	Dump(ctx context.Context, d store.ManagedDB, w io.Writer) error
	// Restore legge un dump `-Fc` da r e lo applica al database d.Name con --no-owner --no-acl
	// --role=<name>_owner, in una sola transazione: un errore lascia il database com'era.
	Restore(ctx context.Context, d store.ManagedDB, r io.Reader, o RestoreOptions) error
}

// PodDumper realizza Dumper con pg_dump e pg_restore dentro il pod del database, come gitstack_admin sul socket
// locale (stesso canale di PsqlRunner). Il dump esce in streaming, il ripristino entra da stdin.
type PodDumper struct {
	Exec StreamExecutor
}

// DumpArgs e' la riga di comando di pg_dump (esportata per i test e per admin, che usa lo stesso comando).
func DumpArgs(d store.ManagedDB) []string {
	return []string{"pg_dump", "-Fc", "-h", pgSocketDir, "-U", AdminRole, "-d", d.Name}
}

// RestoreArgs e' la riga di comando di pg_restore (DB15): mai owner e ACL del dump. `--single-transaction`
// rende il ripristino tutto-o-niente; lock_timeout evita di restare in coda dietro le sessioni delle app.
func RestoreArgs(d store.ManagedDB, o RestoreOptions) []string {
	args := []string{"env", "PGOPTIONS=-c lock_timeout=60s", "pg_restore", "-h", pgSocketDir, "-U", AdminRole, "-d", d.Name,
		"--no-owner", "--no-acl", "--role=" + OwnerRole(d.Name), "--single-transaction", "--exit-on-error"}
	if o.Clean {
		args = append(args, "--clean", "--if-exists")
	}
	return args
}

// Dump implementa Dumper.
func (p *PodDumper) Dump(ctx context.Context, d store.ManagedDB, w io.Writer) error {
	return p.Exec.ExecStream(ctx, Namespace(d.ID), PodName, WorkloadName, DumpArgs(d), nil, w)
}

// Restore implementa Dumper.
func (p *PodDumper) Restore(ctx context.Context, d store.ManagedDB, r io.Reader, o RestoreOptions) error {
	return p.Exec.ExecStream(ctx, Namespace(d.ID), PodName, WorkloadName, RestoreArgs(d, o), r, io.Discard)
}

// Errori di backup e ripristino.
var (
	// ErrBackupUnavailable: mancano chiave di dump, cartella dei dump o esecutore (databases-data non montato).
	ErrBackupUnavailable = errors.New("i backup dei database non sono configurati: mancano la chiave di dump o la cartella databases-data")
	// ErrBackupFileMissing: il file del dump non c'e' piu' (volume perso o database sorgente eliminato).
	ErrBackupFileMissing = errors.New("il file del dump non c'e' piu'")
)

// Durata e conteggi di default.
const (
	defaultBackupEvery      = 24 * time.Hour
	defaultBackupRetryEvery = time.Hour
	// staleBackupAfter: un dump `in_progress` senza processo che lo esegua e piu' vecchio di cosi' e' interrotto.
	staleBackupAfter = 2 * time.Minute
	maxErrorLen      = 600
)

func (c *Controller) backupConfigured() bool {
	return c.dumper != nil && len(c.cfg.DumpKey) >= MinDumpKeyLen && c.cfg.DataDir != ""
}

func (c *Controller) keepFor(kind string) int {
	switch kind {
	case store.BackupScheduled:
		if c.cfg.KeepScheduled > 0 {
			return c.cfg.KeepScheduled
		}
		return store.KeepScheduled
	case store.BackupOnDemand:
		if c.cfg.KeepOnDemand > 0 {
			return c.cfg.KeepOnDemand
		}
		return store.KeepOnDemand
	}
	if c.cfg.KeepSafety > 0 {
		return c.cfg.KeepSafety
	}
	return store.KeepSafety
}

func dumpDirFor(id uuid.UUID) string { return filepath.Join("dumps", id.String()) }

// dumpPath risolve un percorso relativo salvato nel modello dentro DataDir; rifiuta tutto cio' che ne esce.
func (c *Controller) dumpPath(rel string) (string, error) {
	if c.cfg.DataDir == "" {
		return "", ErrBackupUnavailable
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("percorso del dump non valido: %q", rel)
	}
	return filepath.Join(c.cfg.DataDir, clean), nil
}

func shortErr(err error) string {
	s := strings.TrimSpace(err.Error())
	if len(s) > maxErrorLen {
		s = s[:maxErrorLen] + "…"
	}
	return s
}

// BeginBackup registra un dump `in_progress` e ritorna subito (l'API risponde 202). Chi ha chiamato lo esegue con
// RunBackup. ErrNotReady se il database non e' `running`; ErrBackupInProgress se ce n'e' gia' uno.
func (c *Controller) BeginBackup(ctx context.Context, d store.ManagedDB, kind string, actor *store.AppActor) (store.ManagedDBBackup, error) {
	if !c.backupConfigured() {
		return store.ManagedDBBackup{}, ErrBackupUnavailable
	}
	if d.Status != store.DBRunning && d.Status != store.DBRestoring && d.Status != store.DBUpdating {
		return store.ManagedDBBackup{}, ErrNotReady
	}
	rec, err := c.st.BeginManagedDBBackup(ctx, d, kind, actor, c.cfg.Now())
	if err != nil {
		return store.ManagedDBBackup{}, err
	}
	c.mu.Lock()
	c.liveBackups[rec.ID] = true
	c.mu.Unlock()
	return rec, nil
}

// StartBackup avvia un dump su richiesta in background (POST .../backups): 202 con il dump `in_progress`.
func (c *Controller) StartBackup(ctx context.Context, d store.ManagedDB, actor *store.AppActor) (store.ManagedDBBackup, error) {
	rec, err := c.BeginBackup(ctx, d, store.BackupOnDemand, actor)
	if err != nil {
		return rec, err
	}
	bg := context.WithoutCancel(ctx)
	go func() { _ = c.RunBackup(bg, d, rec) }()
	return rec, nil
}

// Backup esegue un dump in modo sincrono (programmato e di sicurezza) e ritorna il dump completato.
func (c *Controller) Backup(ctx context.Context, d store.ManagedDB, kind string, actor *store.AppActor) (store.ManagedDBBackup, error) {
	rec, err := c.BeginBackup(ctx, d, kind, actor)
	if err != nil {
		return rec, err
	}
	if err := c.RunBackup(ctx, d, rec); err != nil {
		return rec, err
	}
	return c.st.GetManagedDBBackup(ctx, d.ID, rec.ID)
}

// RunBackup esegue il dump registrato da BeginBackup: pg_dump nel pod, cifratura, file, esito nel modello e retention.
// Un errore chiude il dump come `failed` col motivo e lo scrive in backup_error del database.
func (c *Controller) RunBackup(ctx context.Context, d store.ManagedDB, rec store.ManagedDBBackup) error {
	defer func() { c.mu.Lock(); delete(c.liveBackups, rec.ID); c.mu.Unlock() }()
	rel := filepath.ToSlash(filepath.Join(dumpDirFor(d.ID), rec.ID.String()+".dump.enc"))
	size, err := c.writeDump(ctx, d, rel)
	// Il contesto puo' essere gia' annullato (arresto di core): l'esito si scrive comunque.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err != nil {
		if p, perr := c.dumpPath(rel); perr == nil {
			_ = os.Remove(p)
			_ = os.Remove(p + ".part")
		}
		if ferr := c.st.FailManagedDBBackup(wctx, d, rec, shortErr(err), c.cfg.Now()); ferr != nil {
			c.cfg.Log.Error("databases: esito del backup non scritto", "db", d.ID, "err", ferr)
		}
		c.cfg.Log.Warn("databases: backup non riuscito", "db", d.ID, "kind", rec.Kind, "err", err)
		return err
	}
	if err := c.st.CompleteManagedDBBackup(wctx, d, rec, rel, size, c.cfg.Now()); err != nil {
		if p, perr := c.dumpPath(rel); perr == nil {
			_ = os.Remove(p)
		}
		return fmt.Errorf("esito del backup non scritto: %w", err)
	}
	files, err := c.st.PruneManagedDBBackups(wctx, d.ID, rec.Kind, c.keepFor(rec.Kind), c.cfg.Now())
	if err != nil {
		c.cfg.Log.Warn("databases: retention dei backup non applicata", "db", d.ID, "err", err)
		return nil
	}
	for _, f := range files {
		if p, perr := c.dumpPath(f); perr == nil {
			_ = os.Remove(p)
		}
	}
	return nil
}

// pgdmpMagic inizia ogni archivio `pg_dump -Fc`: un output diverso (un messaggio d'errore su stdout) non e' un dump.
var pgdmpMagic = []byte("PGDMP")

type magicWriter struct {
	w    io.Writer
	head []byte
	n    int64
}

func (m *magicWriter) Write(p []byte) (int, error) {
	if len(m.head) < len(pgdmpMagic) {
		m.head = append(m.head, p[:min(len(p), len(pgdmpMagic)-len(m.head))]...)
	}
	n, err := m.w.Write(p)
	m.n += int64(n)
	return n, err
}

// writeDump scrive il dump cifrato in un file temporaneo e lo rinomina; ritorna la dimensione del file cifrato.
func (c *Controller) writeDump(ctx context.Context, d store.ManagedDB, rel string) (int64, error) {
	p, err := c.dumpPath(rel)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return 0, fmt.Errorf("cartella dei dump: %w", err)
	}
	f, err := os.OpenFile(p+".part", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("file del dump: %w", err)
	}
	var cleanup = true
	defer func() {
		if cleanup {
			_ = f.Close()
			_ = os.Remove(p + ".part")
		}
	}()
	enc, err := NewDumpEncryptWriter(f, c.cfg.DumpKey)
	if err != nil {
		return 0, err
	}
	mw := &magicWriter{w: enc}
	if err := c.dumper.Dump(ctx, d, mw); err != nil {
		return 0, fmt.Errorf("pg_dump: %w", err)
	}
	if string(mw.head) != string(pgdmpMagic) {
		return 0, errors.New("pg_dump non ha prodotto un archivio valido")
	}
	if err := enc.Close(); err != nil {
		return 0, err
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	cleanup = false
	if err := f.Close(); err != nil {
		_ = os.Remove(p + ".part")
		return 0, err
	}
	if err := os.Rename(p+".part", p); err != nil {
		_ = os.Remove(p + ".part")
		return 0, err
	}
	return st.Size(), nil
}

// openDump apre un dump cifrato e ne ritorna il flusso in chiaro.
func (c *Controller) openDump(rel string) (io.Reader, func(), error) {
	p, err := c.dumpPath(rel)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrBackupFileMissing
	}
	if err != nil {
		return nil, nil, err
	}
	r, err := NewDumpDecryptReader(f, c.cfg.DumpKey)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return r, func() { _ = f.Close() }, nil
}

// DumpExists dice se il file di un dump completato c'e' ancora (l'API rifiuta un ripristino senza file).
func (c *Controller) DumpExists(rel string) bool {
	p, err := c.dumpPath(rel)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// removeDumps toglie i dump del database dal disco (eliminazione, DB19).
func (c *Controller) removeDumps(id uuid.UUID) {
	if c.cfg.DataDir == "" {
		return
	}
	if err := os.RemoveAll(filepath.Join(c.cfg.DataDir, dumpDirFor(id))); err != nil {
		c.cfg.Log.Warn("databases: dump non rimossi", "db", id, "err", err)
	}
}

// scheduledDue dice se il database ha bisogno del dump giornaliero: l'ultimo programmato (riuscito o no) e' piu'
// vecchio di BackupEvery; dopo un fallimento si riprova ogni BackupRetryEvery, non a ogni giro.
func (c *Controller) scheduledDue(ctx context.Context, d store.ManagedDB) bool {
	every, retryEvery := c.cfg.BackupEvery, c.cfg.BackupRetryEvery
	if every <= 0 {
		every = defaultBackupEvery
	}
	if retryEvery <= 0 {
		retryEvery = defaultBackupRetryEvery
	}
	last, err := c.st.LatestManagedDBBackup(ctx, d.ID, store.BackupScheduled)
	if errors.Is(err, store.ErrBackupNotFound) {
		return c.cfg.Now().Sub(d.CreatedAt) >= every
	}
	if err != nil {
		return false
	}
	switch last.Status {
	case store.BackupInProgress:
		return false
	case store.BackupFailed:
		return c.cfg.Now().Sub(last.CreatedAt) >= retryEvery
	}
	return c.cfg.Now().Sub(last.CreatedAt) >= every
}

// maybeScheduledBackup esegue il dump giornaliero se serve. Senza chiave o cartella non fa niente (e non
// finge: l'API dei backup risponde che non sono configurati).
func (c *Controller) maybeScheduledBackup(ctx context.Context, d store.ManagedDB) {
	if !c.backupConfigured() || !c.scheduledDue(ctx, d) {
		return
	}
	// Errore gia' registrato nel modello (backup_error, evento, audit): qui basta non perderlo.
	if _, err := c.Backup(ctx, d, store.BackupScheduled, nil); err != nil && !errors.Is(err, store.ErrBackupInProgress) {
		c.cfg.Log.Warn("databases: backup programmato non riuscito", "db", d.ID, "err", err)
	}
}

// failStaleBackups chiude i dump rimasti `in_progress` per un riavvio di core.
func (c *Controller) failStaleBackups(ctx context.Context) {
	c.mu.Lock()
	live := make([]uuid.UUID, 0, len(c.liveBackups))
	for id := range c.liveBackups {
		live = append(live, id)
	}
	c.mu.Unlock()
	now := c.cfg.Now()
	if _, err := c.st.FailStaleManagedDBBackups(ctx, live, now.Add(-staleBackupAfter), now); err != nil && ctx.Err() == nil {
		c.cfg.Log.Warn("databases: dump interrotti non chiusi", "err", err)
	}
}

// RestoreBackup mette in coda il ripristino del dump `b` nel database `d` (stesso database o uno nuovo gia'
// creato dall'API): lo esegue il controller. `same` richiede che il database sia `running`.
func (c *Controller) RestoreBackup(ctx context.Context, d store.ManagedDB, b store.ManagedDBBackup, mode string, actor *store.AppActor) error {
	if !c.backupConfigured() {
		return ErrBackupUnavailable
	}
	if b.Status != store.BackupCompleted || b.File == nil {
		return ErrBackupFileMissing
	}
	if !c.DumpExists(*b.File) {
		return ErrBackupFileMissing
	}
	if err := c.st.RequestManagedDBRestore(ctx, d, b.ID, *b.File, mode, actor, c.cfg.Now()); err != nil {
		return err
	}
	c.Kick()
	return nil
}

// runRestore esegue il ripristino pendente (DB15) e ritorna lo stato con cui il database torna a girare. Sul posto
// fa prima un dump di sicurezza: se non riesce non tocca niente. Un errore e' in restore_error del database.
func (c *Controller) runRestore(ctx context.Context, d store.ManagedDB, p store.PendingRestore) (string, *string) {
	finish := func(err error) (string, *string) {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if ferr := c.st.FinishManagedDBRestore(wctx, d, p, err, c.cfg.Now()); ferr != nil {
			c.cfg.Log.Error("databases: esito del ripristino non scritto", "db", d.ID, "err", ferr)
		}
		if err != nil {
			c.cfg.Log.Warn("databases: ripristino non riuscito", "db", d.ID, "err", err)
		}
		return store.DBRunning, nil
	}
	if !c.backupConfigured() {
		return finish(ErrBackupUnavailable)
	}
	if d.Status != store.DBRestoring {
		if err := c.st.SetManagedDBStatus(ctx, d.ID, store.DBRestoring, nil, c.cfg.Now()); err != nil {
			return store.DBRunning, nil
		}
	}
	safety := ""
	if p.Mode == store.RestoreSame {
		rec, err := c.Backup(ctx, d, store.BackupSafety, p.RequestedBy)
		if err != nil {
			return finish(fmt.Errorf("dump di sicurezza non riuscito, il database non e' stato toccato: %s", shortErr(err)))
		}
		safety = rec.ID.String()
	}
	r, closeFn, err := c.openDump(p.File)
	if err != nil {
		return finish(err)
	}
	defer closeFn()
	if err := c.dumper.Restore(ctx, d, r, RestoreOptions{Clean: p.Mode == store.RestoreSame}); err != nil {
		msg := "pg_restore: " + shortErr(err)
		if safety != "" {
			msg += " (il ripristino e' una sola transazione: il database e' com'era; dump di sicurezza " + safety + ")"
		}
		return finish(errors.New(msg))
	}
	if c.sql != nil {
		if err := ReapplyROPrivileges(ctx, c.sql, d); err != nil {
			return finish(fmt.Errorf("privilegi di %s non riapplicati: %s", ReadOnlyRole(d.Name), shortErr(err)))
		}
	}
	if c.cfg.AfterRestore != nil {
		if err := c.cfg.AfterRestore(ctx, d); err != nil {
			return finish(fmt.Errorf("dati ripristinati, ma i collegamenti non sono stati ricreati: %s", shortErr(err)))
		}
	}
	return finish(nil)
}

package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Database gestiti nel backup e nel restore (v3-d, GIT-553, ADR 0003 punto 5, DB14 e DB15).
//
// Ad ogni `gitstack backup` l'archivio riceve un dump FRESCO (`pg_dump -Fc`, exec nel pod) di ogni database
// gestito in `databases/<db-id>/<nome>.dump`. L'archivio e' cifrato come sempre (obbligatorio con S3), quindi i
// dump seguono retention e copia S3 senza altro codice. La directory dati dei database (PVC) non entra mai: i
// volumi dei database stanno in gitstack-db-*, non in gitstack-app-*, e non sono nell'elenco dei volumi delle app.
// La coerenza e' per database, non fra database diversi ne' con il database di core.
//
// In restore, dopo che core e i servizi sono tornati su, il controller di core ricrea le istanze vuote
// (namespace, StatefulSet, ruoli e database, GIT-551): qui si aspetta che siano pronte e si applica
// `pg_restore --no-owner --no-acl --role=<nome>_owner` (stessa sequenza di DB15) con i privilegi di `<nome>_ro`
// riapplicati. Nessun segreto di database viaggia nel backup: le istanze ripartono con password nuove.

// DatabasesPrefix inizia il nome delle voci dei database nell'archivio: databases/<db-id>/<nome>.dump.
const DatabasesPrefix = "databases/"

const (
	dbNamespacePrefix = "gitstack-db-"
	// Come services/core/internal/databases: pod pg-0, container pg, superuser gitstack_admin sul socket locale.
	dbPod       = "pg-0"
	dbContainer = "pg"
	dbAdminRole = "gitstack_admin"
	dbSocketDir = "/var/run/postgresql"
)

var dbNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,28}[a-z0-9])?$`)

// DatabaseRef e' un database gestito incluso nel backup.
type DatabaseRef struct {
	ID      string `json:"id"`
	Owner   string `json:"owner"`
	Name    string `json:"name"`
	PgMajor int    `json:"pg_major"`
	// Entry e' il nome della voce nell'archivio.
	Entry string `json:"entry"`
	// Status e' lo stato nel modello al momento del backup (solo informativo: si fa il dump dei `running`).
	Status string `json:"status,omitempty"`
}

// Namespace e' il namespace Kubernetes del database.
func (d DatabaseRef) Namespace() string { return dbNamespacePrefix + d.ID }

func (d DatabaseRef) entryName() string { return DatabasesPrefix + d.ID + "/" + d.Name + ".dump" }

func (d DatabaseRef) ownerRole() string { return d.Name + "_owner" }

func (d DatabaseRef) roRole() string { return d.Name + "_ro" }

// validate rifiuta id e nomi che non siano ben formati: finiscono in percorsi e comandi kubectl.
func (d DatabaseRef) validate() error {
	if !repoIDRe.MatchString(d.ID) {
		return fmt.Errorf("database: id non valido %q", d.ID)
	}
	if !dbNameRe.MatchString(d.Name) {
		return fmt.Errorf("database: nome non valido %q", d.Name)
	}
	return nil
}

// DatabaseCluster e' la parte di Cluster che serve ai database gestiti. KubectlCluster la realizza; un Cluster che
// non la realizza non puo' fare dump ne' ripristino dei database.
type DatabaseCluster interface {
	// DumpDatabase scrive su w il dump `pg_dump -Fc` del database (exec nel pod).
	DumpDatabase(ctx context.Context, d DatabaseRef, w io.Writer) error
	// DatabaseReady dice se l'istanza e' pronta a ricevere il ripristino: pod Ready e ruoli creati dal controller.
	DatabaseReady(ctx context.Context, d DatabaseRef) (bool, error)
	// RestoreDatabase applica un dump `-Fc` all'istanza vuota con --no-owner --no-acl --role=<nome>_owner e
	// riapplica i privilegi di <nome>_ro (DB15).
	RestoreDatabase(ctx context.Context, d DatabaseRef, r io.Reader) error
}

// databasesCmd legge i database gestiti: una riga per database, id|owner|nome|major|stato. Se la tabella non
// esiste (database di una versione senza database gestiti) l'elenco e' vuoto.
const databasesCmd = `exec_psql() { psql -q -At -F '|' -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"; }
if [ "$(exec_psql "SELECT to_regclass('core.managed_dbs') IS NOT NULL")" = t ]; then
  exec_psql "SELECT id, owner_name, name, pg_major, status FROM core.managed_dbs ORDER BY owner_name, name"
fi`

func parseDatabaseRow(line string) (DatabaseRef, error) {
	f := strings.Split(line, "|")
	if len(f) != 5 {
		return DatabaseRef{}, fmt.Errorf("riga dei database non valida (%d campi)", len(f))
	}
	var major int
	if _, err := fmt.Sscan(f[3], &major); err != nil {
		return DatabaseRef{}, fmt.Errorf("riga dei database non valida: major %q", f[3])
	}
	d := DatabaseRef{ID: strings.ToLower(f[0]), Owner: f[1], Name: f[2], PgMajor: major, Status: f[4]}
	if err := d.validate(); err != nil {
		return DatabaseRef{}, err
	}
	d.Entry = d.entryName()
	return d, nil
}

// databases ritorna i database gestiti: dal hook Databases se c'e' (test), altrimenti dal database di core.
func (o *Options) databases(ctx context.Context) ([]DatabaseRef, error) {
	if o.Databases != nil {
		return o.Databases(ctx)
	}
	var out bytes.Buffer
	if err := o.Cluster.PGExec(ctx, nil, &out, "sh", "-c", databasesCmd); err != nil {
		return nil, fmt.Errorf("elenco dei database gestiti: %w", err)
	}
	var dbs []DatabaseRef
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		d, err := parseDatabaseRow(line)
		if err != nil {
			return nil, err
		}
		dbs = append(dbs, d)
	}
	return dbs, nil
}

// planDatabases decide quali database entrano: quelli `running` (un dump di un'istanza che non gira non e'
// possibile). Gli altri si dichiarano nel log con lo stato: nessun database sparisce in silenzio dal backup.
func (o *Options) planDatabases(ctx context.Context) ([]DatabaseRef, error) {
	all, err := o.databases(ctx)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}
	if _, ok := o.Cluster.(DatabaseCluster); !ok {
		return nil, errors.New("questo cluster non sa fare il dump dei database gestiti")
	}
	var run []DatabaseRef
	for _, d := range all {
		if d.Status != "" && d.Status != "running" {
			o.logf("ATTENZIONE: il database %s/%s e' in stato %s: non incluso nel backup", d.Owner, d.Name, d.Status)
			continue
		}
		run = append(run, d)
	}
	return run, nil
}

// stageDatabases scrive il dump di ogni database nell'archivio. Un dump che fallisce fa fallire il backup: un
// database che manca all'archivio non passa in silenzio.
func (o *Options) stageDatabases(ctx context.Context, st *stager, dbs []DatabaseRef) ([]DatabaseRef, error) {
	if len(dbs) == 0 {
		return nil, nil
	}
	dc := o.Cluster.(DatabaseCluster)
	var refs []DatabaseRef
	for _, d := range dbs {
		o.logf("dump del database %s/%s (pg_dump -Fc)", d.Owner, d.Name)
		if err := st.write(d.Entry, func(w io.Writer) error {
			chk := &pgdmpCheck{w: w}
			if err := dc.DumpDatabase(ctx, d, chk); err != nil {
				return err
			}
			if string(chk.head) != "PGDMP" {
				return errors.New("pg_dump non ha prodotto un archivio valido")
			}
			return nil
		}); err != nil {
			return nil, fmt.Errorf("dump del database %s/%s: %w", d.Owner, d.Name, err)
		}
		refs = append(refs, d)
	}
	return refs, nil
}

// pgdmpCheck lascia passare il dump e ne controlla l'inizio: un archivio `-Fc` comincia con PGDMP, un messaggio
// d'errore su stdout no.
type pgdmpCheck struct {
	w    io.Writer
	head []byte
}

func (p *pgdmpCheck) Write(b []byte) (int, error) {
	if len(p.head) < 5 {
		p.head = append(p.head, b[:min(len(b), 5-len(p.head))]...)
	}
	return p.w.Write(b)
}

// restoreDatabasesTail toglie dal database di core appena ripristinato cio' che puo' essere vero solo sul disco
// del cluster di prima: i file dei dump programmati stanno su databases-data e non entrano nell'archivio, quindi
// le loro righe, i ripristini pendenti e le richieste di aggiornamento in coda non hanno piu' niente da fare.
// Idempotente e innocuo se le tabelle non ci sono.
const restoreDatabasesTail = `
DO $$ BEGIN
  IF to_regclass('core.managed_db_backups') IS NOT NULL THEN
    DELETE FROM core.managed_db_backups;
  END IF;
  IF to_regclass('core.managed_dbs') IS NOT NULL THEN
    UPDATE core.managed_dbs SET restore_backup_id = NULL, restore_file = NULL, restore_mode = NULL, restore_requested_by = NULL,
      upgrade_requested_at = NULL, upgrade_requested_by = NULL, backup_error = NULL, backup_error_kind = NULL, last_backup_at = NULL;
  END IF;
END $$;
`

const (
	// databaseReadyTimeout e' l'attesa massima, per database, che il controller ricrei l'istanza vuota.
	databaseReadyTimeout = 10 * time.Minute
	databaseReadyPoll    = 3 * time.Second
)

// restoreDatabases rimette i dump nelle istanze ricreate dal controller. Va chiamato con core gia' ripartito.
func (o *Options) restoreDatabases(ctx context.Context, stage string, dbs []DatabaseRef, has map[string]bool) error {
	if len(dbs) == 0 {
		return nil
	}
	dc, ok := o.Cluster.(DatabaseCluster)
	if !ok {
		return errors.New("questo cluster non sa ripristinare i database gestiti")
	}
	sorted := append([]DatabaseRef(nil), dbs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Entry < sorted[j].Entry })
	timeout, poll := databaseReadyTimeout, databaseReadyPoll
	if o.DatabaseReadyTimeout > 0 {
		timeout = o.DatabaseReadyTimeout
	}
	if o.DatabaseReadyPoll > 0 {
		poll = o.DatabaseReadyPoll
	}
	for _, d := range sorted {
		if err := d.validate(); err != nil {
			return refused("archivio non valido: %v", err)
		}
		if d.Entry != d.entryName() || !has[d.Entry] {
			return refused("archivio incompleto: manca %s", d.entryName())
		}
	}
	for _, d := range sorted {
		o.logf("ripristino del database %s/%s: attendo che il controller ricrei l'istanza", d.Owner, d.Name)
		if err := waitDatabaseReady(ctx, dc, d, timeout, poll); err != nil {
			return fmt.Errorf("database %s/%s: %w (i dati di core sono ripristinati; quando l'istanza e' pronta rilancia il restore dello stesso archivio)", d.Owner, d.Name, err)
		}
		f, err := os.Open(filepath.Join(stage, filepath.FromSlash(d.Entry)))
		if err != nil {
			return err
		}
		err = dc.RestoreDatabase(ctx, d, f)
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("ripristino del database %s/%s: %w", d.Owner, d.Name, err)
		}
		o.logf("database %s/%s ripristinato (le credenziali sono nuove: ruota la password dal portale o con gs)", d.Owner, d.Name)
	}
	return nil
}

func waitDatabaseReady(ctx context.Context, dc DatabaseCluster, d DatabaseRef, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		ready, err := dc.DatabaseReady(ctx, d)
		if err == nil && ready {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			if last != nil {
				return fmt.Errorf("non pronto entro %s: %w", timeout, last)
			}
			return fmt.Errorf("non pronto entro %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

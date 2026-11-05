package status

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// ManagedDatabaseIssue e' un problema aperto di un database gestito (v3-d, GIT-553): un backup, un ripristino o
// un aggiornamento minor non riusciti. Un backup fallito non passa in silenzio: `gitstack status` lo stampa e il
// portale lo mostra (operations.backupError del database).
type ManagedDatabaseIssue struct {
	// Database e' owner/nome.
	Database string `json:"database"`
	// Kind: "backup", "ripristino" o "aggiornamento".
	Kind string `json:"kind"`
	// Reason e' il motivo registrato dal controller (mai contenuti del dump).
	Reason string `json:"reason"`
}

// managedIssuesQuery legge i problemi aperti dal database di core: una riga per problema, tipo|owner/nome|motivo.
// Senza la tabella (versione senza database gestiti) non stampa niente.
const managedIssuesQuery = `exec_psql() { psql -q -At -F '|' -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "$1"; }
if [ "$(exec_psql "SELECT to_regclass('core.managed_dbs') IS NOT NULL")" = t ]; then
  echo "count|$(exec_psql "SELECT count(*) FROM core.managed_dbs")"
fi
if [ "$(exec_psql "SELECT to_regclass('core.managed_dbs') IS NOT NULL")" = t ] && [ "$(exec_psql "SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'managed_dbs' AND column_name = 'backup_error'")" = 1 ]; then
  exec_psql "SELECT 'backup', owner_name || '/' || name, replace(backup_error, E'\n', ' ') FROM core.managed_dbs WHERE backup_error IS NOT NULL UNION ALL SELECT 'ripristino', owner_name || '/' || name, replace(restore_error, E'\n', ' ') FROM core.managed_dbs WHERE restore_error IS NOT NULL UNION ALL SELECT 'aggiornamento', owner_name || '/' || name, replace(upgrade_error, E'\n', ' ') FROM core.managed_dbs WHERE upgrade_error IS NOT NULL ORDER BY 2, 1"
fi`

// ManagedSummary e' cio' che `gitstack status` sa dei database gestiti: quanti sono e i problemi aperti.
type ManagedSummary struct {
	// Count e' il numero di database dell'istanza (core.managed_dbs).
	Count  int
	Issues []ManagedDatabaseIssue
}

// FailedBackups e' il numero di database con l'ultimo backup fallito.
func (m ManagedSummary) FailedBackups() int {
	n := 0
	for _, i := range m.Issues {
		if i.Kind == "backup" {
			n++
		}
	}
	return n
}

// parseManagedCount legge la riga `count|N` dell'output di managedIssuesQuery (0 se manca).
func parseManagedCount(out string) int {
	for _, line := range strings.Split(out, "\n") {
		if n, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "count|"); ok {
			v, err := strconv.Atoi(strings.TrimSpace(n))
			if err == nil && v > 0 {
				return v
			}
		}
	}
	return 0
}

// parseManagedIssues legge l'output di managedIssuesQuery; ignora le righe che non hanno la forma attesa.
func parseManagedIssues(out string) []ManagedDatabaseIssue {
	var issues []ManagedDatabaseIssue
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), "|", 3)
		if len(f) != 3 || strings.Count(f[1], "/") != 1 {
			continue
		}
		switch f[0] {
		case "backup", "ripristino", "aggiornamento":
			issues = append(issues, ManagedDatabaseIssue{Kind: f[0], Database: f[1], Reason: strings.TrimSpace(f[2])})
		}
	}
	return issues
}

// managedIssues sono i problemi aperti dei database gestiti (vedi managedSummary).
func (c *Collector) managedIssues(ctx context.Context, cfg *config.Config) []ManagedDatabaseIssue {
	m, _ := c.managedSummary(ctx, cfg)
	return m.Issues
}

// managedSummary interroga il database di core (nel pod di Postgres, o nel pod client con il database esterno).
// Un errore di lettura non e' un problema dei database: ok=false e non si mostra niente.
func (c *Collector) managedSummary(ctx context.Context, cfg *config.Config) (ManagedSummary, bool) {
	bin, pre, err := c.kubectlCommand()
	if err != nil {
		return ManagedSummary{}, false
	}
	target := "statefulset/" + cfg.Release + "-postgres"
	if cfg.Postgres.External {
		target = "deployment/" + cfg.Release + "-pgclient"
	}
	args := append(append([]string{}, pre...), "exec", "-n", cfg.Namespace, target, "--", "sh", "-c", managedIssuesQuery)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := c.Runner.Run(cctx, []string{"KUBECONFIG=" + cfg.Kubeconfig}, bin, args...)
	if err != nil {
		return ManagedSummary{}, false
	}
	return ManagedSummary{Count: parseManagedCount(string(out)), Issues: parseManagedIssues(string(out))}, true
}

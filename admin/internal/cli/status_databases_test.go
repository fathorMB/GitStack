package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/status"
)

// GIT-553: un backup (o ripristino, o aggiornamento minor) fallito di un database gestito si legge in
// `gitstack status`, col motivo; senza problemi la sezione non compare.
func TestStatus_StampaIProblemiDeiDatabaseGestiti(t *testing.T) {
	rep := &status.Report{Host: "h", Release: "gs", Namespace: "ns", APIHealthy: true, LastBackup: "mai",
		Services: []status.Service{{Name: "gs-core", Desired: 1, Ready: 1}}}
	var ok bytes.Buffer
	printReport(&ok, "v1", rep)
	if strings.Contains(ok.String(), "Database gestiti") {
		t.Fatalf("senza problemi la sezione non deve comparire:\n%s", ok.String())
	}
	rep.DatabaseIssues = []status.ManagedDatabaseIssue{
		{Database: "alice/shop", Kind: "backup", Reason: "pg_dump: connessione rifiutata"},
		{Database: "alice/shop", Kind: "ripristino", Reason: "pg_restore: errore"},
		{Database: "acme/erp", Kind: "aggiornamento", Reason: "dump di sicurezza non riuscito"},
	}
	var bad bytes.Buffer
	printReport(&bad, "v1", rep)
	for _, want := range []string{"Database gestiti: problemi aperti", "alice/shop: BACKUP FALLITO: pg_dump: connessione rifiutata",
		"alice/shop: RIPRISTINO FALLITO: pg_restore: errore", "acme/erp: AGGIORNAMENTO MINOR NON ESEGUITO: dump di sicurezza non riuscito"} {
		if !strings.Contains(bad.String(), want) {
			t.Errorf("manca %q in:\n%s", want, bad.String())
		}
	}
	if !strings.Contains(bad.String(), "Stato: sano") {
		t.Errorf("un backup fallito e' un avviso, non un guasto del servizio:\n%s", bad.String())
	}
}

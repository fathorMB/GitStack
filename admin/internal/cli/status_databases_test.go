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
	if strings.Contains(ok.String(), "Database gestiti: problemi aperti") {
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

// GIT-557 (DB23): lo stato dei database gestiti c'e' sempre: spento di default, acceso con il numero dei
// database e dei backup falliti; config.yaml e cluster discordi si segnalano.
func TestStatus_StatoDeiDatabaseGestiti(t *testing.T) {
	base := func() *status.Report {
		return &status.Report{Host: "h", Release: "gs", Namespace: "ns", APIHealthy: true, LastBackup: "mai",
			Services: []status.Service{{Name: "gs-core", Desired: 1, Ready: 1}}}
	}
	render := func(r *status.Report) string {
		var b bytes.Buffer
		printReport(&b, "v1", r)
		return b.String()
	}
	off := render(base())
	if !strings.Contains(off, "Database gestiti: spento") || !strings.Contains(off, "gitstack upgrade --enable-databases") {
		t.Errorf("spento di default:\n%s", off)
	}
	r := base()
	r.DatabasesEnabled, r.DatabasesLive, r.DatabasesCount, r.DatabasesFailedBackups, r.DatabasesPortRange = true, "on", 3, 1, "15432-15531"
	on := render(r)
	for _, want := range []string{"Database gestiti: acceso", "Database: 3; backup falliti: 1", "Porte LAN dei database: 15432-15531"} {
		if !strings.Contains(on, want) {
			t.Errorf("manca %q in:\n%s", want, on)
		}
	}
	r = base()
	r.DatabasesEnabled, r.DatabasesLive = true, "off"
	if got := render(r); !strings.Contains(got, "Database gestiti: spento (config.yaml dice acceso") {
		t.Errorf("config acceso ma core spento:\n%s", got)
	}
	r = base()
	r.DatabasesLive = "on"
	if got := render(r); !strings.Contains(got, "Database gestiti: acceso (ma config.yaml dice spento") {
		t.Errorf("config spento ma core acceso:\n%s", got)
	}
}

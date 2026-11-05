package status

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

// GIT-553: un backup fallito di un database gestito si legge in `gitstack status` (Report.DatabaseIssues), insieme a
// ripristini e aggiornamenti minor non riusciti; le righe che non hanno la forma attesa si ignorano.
func TestManagedIssues_LeggeIProblemiAperti(t *testing.T) {
	r := &execRunner{out: "backup|alice/shop|pg_dump: connessione rifiutata\naggiornamento|acme/erp|dump di sicurezza non riuscito: |disco pieno\nrumore\nbackup|senza-slash|x\n"}
	c := &Collector{Runner: r, LookPath: func(string) (string, error) { return "/usr/bin/kubectl", nil }}
	issues := c.managedIssues(context.Background(), &config.Config{Release: "gs", Namespace: "ns"})
	if len(issues) != 2 {
		t.Fatalf("problemi: %+v", issues)
	}
	if issues[0] != (ManagedDatabaseIssue{Database: "alice/shop", Kind: "backup", Reason: "pg_dump: connessione rifiutata"}) {
		t.Errorf("primo: %+v", issues[0])
	}
	if issues[1].Kind != "aggiornamento" || !strings.HasSuffix(issues[1].Reason, "|disco pieno") {
		t.Errorf("secondo (il motivo puo' contenere |): %+v", issues[1])
	}
	if !strings.Contains(strings.Join(r.args, " "), "exec -n ns statefulset/gs-postgres") {
		t.Errorf("deve interrogare il Postgres di core: %v", r.args)
	}
	// Con il database esterno si usa il pod client.
	r.args = nil
	c.managedIssues(context.Background(), extCfg())
	if !strings.Contains(strings.Join(r.args, " "), "deployment/gs-pgclient") {
		t.Errorf("database esterno: %v", r.args)
	}
	// Un errore di lettura non inventa problemi.
	c2 := &Collector{Runner: &execRunner{err: errors.New("boom")}, LookPath: func(string) (string, error) { return "/usr/bin/kubectl", nil }}
	if got := c2.managedIssues(context.Background(), &config.Config{Release: "gs", Namespace: "ns"}); got != nil {
		t.Errorf("errore di lettura: %+v", got)
	}
	// Non rendono l'installazione non sana: sono un avviso, non un guasto.
	rep := &Report{APIHealthy: true, Services: []Service{{Name: "gs-gateway", Desired: 1, Ready: 1}}, DatabaseIssues: issues}
	if !rep.Healthy() {
		t.Error("un backup fallito non deve far fallire la salute (gitstack upgrade la usa per decidere)")
	}
}

package backup

import (
	"context"
	"strings"
	"testing"
)

func indexOf(calls []string, s string) int {
	for i, c := range calls {
		if c == s {
			return i
		}
	}
	return -1
}

// natsRestoreEnv: archivio con il Secret gs-nats-auth del vecchio cluster e
// installazione nuova (stesso nome, altre credenziali) con lo StatefulSet NATS.
func natsRestoreEnv(t *testing.T, withNATS bool) (string, *env) {
	t.Helper()
	src := newEnv(t)
	src.f.secrets = append(src.f.secrets, Secret{Name: "gs-nats-auth", Raw: []byte(`{"data":{"password":"VECCHIA"}}`)})
	res, err := Backup(context.Background(), src.o)
	if err != nil {
		t.Fatal(err)
	}
	r := newEnv(t)
	r.o.DestDir = t.TempDir()
	if withNATS {
		r.f.statefulSets = map[string]bool{"gs-nats": true}
	}
	return res.Path, r
}

func TestRestoreRestartsNATSAfterSecretsBeforeServices(t *testing.T) {
	archive, r := natsRestoreEnv(t, true)
	if err := Restore(context.Background(), r.o, archive); err != nil {
		t.Fatal(err)
	}
	if indexOf(r.f.applied, "gs-nats-auth") < 0 {
		t.Fatalf("Secret nats-auth non ripristinato: %v", r.f.applied)
	}
	c := r.f.calls
	psql, rst, rdy := indexOf(c, "psql"), indexOf(c, "restart-sts gs-nats"), indexOf(c, "ready-sts gs-nats")
	if psql < 0 || rst < 0 || rdy < 0 {
		t.Fatalf("chiamate: %v", c)
	}
	// ApplySecret avviene tra psql e il restart di NATS (le chiamate a
	// ApplySecret non finiscono in calls): NATS si riavvia dopo il database
	// e il restore dei Secret, e si attende prima di ogni riaccensione.
	if psql >= rst || rst >= rdy {
		t.Errorf("ordine sbagliato: %v", c)
	}
	for _, d := range []string{"gs-identity", "gs-core", "gs-git", "gs-gateway"} {
		up := -1
		for i, x := range c {
			if strings.HasPrefix(x, "scale "+d+" ") && !strings.HasSuffix(x, " 0") {
				up = i
			}
		}
		if up < 0 || up < rdy {
			t.Errorf("%s va riacceso dopo l'attesa di NATS: %v", d, c)
		}
		if indexOf(c, "ready "+d) < up {
			t.Errorf("WaitReady di %s prima della riaccensione: %v", d, c)
		}
	}
	if !strings.Contains(r.log.String(), "riavvio NATS per le credenziali ripristinate") {
		t.Errorf("manca la riga di log: %s", r.log)
	}
	// Postgres non si riavvia (Secret non ripristinato) e il web non legge Secret.
	for _, x := range c {
		if strings.Contains(x, "postgres") || strings.HasPrefix(x, "restart ") {
			t.Errorf("nessun riavvio atteso: %v", c)
		}
	}
}

func TestRestoreSkipsNATSWhenAbsent(t *testing.T) {
	archive, r := natsRestoreEnv(t, false)
	if err := Restore(context.Background(), r.o, archive); err != nil {
		t.Fatal(err)
	}
	for _, x := range r.f.calls {
		if strings.Contains(x, "-sts ") {
			t.Errorf("NATS assente: nessuna chiamata attesa: %v", r.f.calls)
		}
	}
	if strings.Contains(r.log.String(), "riavvio NATS") {
		t.Errorf("log inatteso: %s", r.log)
	}
}

func TestRestoreNATSRestartFailureKeepsServicesStopped(t *testing.T) {
	archive, r := natsRestoreEnv(t, true)
	r.f.failSTSRestart = true
	err := Restore(context.Background(), r.o, archive)
	if err == nil || !strings.Contains(err.Error(), "rollout restart statefulset/gs-nats") || !strings.Contains(err.Error(), "FERMI") {
		t.Fatalf("errore con il comando a mano atteso, ottenuto %v", err)
	}
	for _, d := range []string{"gs-gateway", "gs-git", "gs-core", "gs-identity"} {
		if r.f.replicas[d] != 0 {
			t.Errorf("%s riacceso con NATS non riavviato", d)
		}
	}
}

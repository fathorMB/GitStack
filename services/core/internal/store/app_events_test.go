package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// GIT-384 (H12): le istantanee del webhook. InBackup segue H4 (attiva e precedente); un'app con repo
// archiviato è ferma e non ha copie pronte.

func TestVersionSnapshot_InBackupAttivaEPrecedente(t *testing.T) {
	for _, tc := range []struct {
		number, active int
		want           bool
	}{{1, 3, false}, {2, 3, true}, {3, 3, true}, {1, 1, true}} {
		got := versionSnapshot(AppVersion{Number: tc.number, Trigger: "manual", State: "serving", CreatedAt: time.Now()}, tc.active)
		if got.InBackup != tc.want {
			t.Errorf("versione %d con attiva %d: inBackup=%v (voluto %v)", tc.number, tc.active, got.InBackup, tc.want)
		}
	}
}

func TestAppSnapshot_RepoArchiviatoFermaELeCopie(t *testing.T) {
	archived := time.Now()
	a := App{ID: uuid.New(), Name: "web", Owner: "alice", RepoName: "web", Status: "running",
		ReadyCopies: 2, RepoArchivedAt: &archived, Config: AppConfig{Copies: 2}}
	got := appSnapshot(a)
	if got.Status != "stopped" || got.StoppedReason == nil || *got.StoppedReason != "repo_archived" {
		t.Errorf("stato dell'istantanea: %s %v", got.Status, got.StoppedReason)
	}
	if got.ReadyCopies != 0 {
		t.Errorf("copie pronte di un'app ferma = %d (voluto 0)", got.ReadyCopies)
	}
}

func TestActorUser_NilEAgente(t *testing.T) {
	if actorUser(nil) != nil {
		t.Fatal("senza attore niente avviso")
	}
	id := uuid.New()
	u := actorUser(&AppActor{ID: id, Username: "bot", Type: "agent"})
	if u.ID != id.String() || u.Type != "agent" {
		t.Errorf("attore agente: %+v", u)
	}
}

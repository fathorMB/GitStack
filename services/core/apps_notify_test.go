package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-354 (H13): se il deploy di un agente fallisce, gli admin del repo ricevono la notifica; chi non ha
// admin sul repo (per esempio l'agente stesso, con solo write) no.

type fakeManagers struct {
	owners map[uuid.UUID][]uuid.UUID
	admins map[uuid.UUID]bool
}

func (f fakeManagers) OrgOwners(_ context.Context, org uuid.UUID) ([]uuid.UUID, error) {
	return f.owners[org], nil
}
func (f fakeManagers) HasRole(_ context.Context, u, _ uuid.UUID, role string) (bool, error) {
	return role == "admin" && f.admins[u], nil
}

func testNotifier(mgr fakeManagers, ownerType string, ownerID uuid.UUID, sent *[]notify.OccurrenceInput, to *[]uuid.UUID) *deployNotify {
	return &deployNotify{
		mgr:   mgr,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		owner: func(context.Context, uuid.UUID) (string, uuid.UUID, error) { return ownerType, ownerID, nil },
		send: func(_ context.Context, r []uuid.UUID, o notify.OccurrenceInput) error {
			*sent = append(*sent, o)
			*to = append(*to, r...)
			return nil
		},
	}
}

func TestDeployFallito_AvvisaGliAdminDelRepo(t *testing.T) {
	alice, bot, org := uuid.New(), uuid.New(), uuid.New()
	repo := uuid.New()
	app := store.App{ID: uuid.New(), RepoID: repo, Name: "web", Owner: "acme", RepoName: "handbook"}
	v := store.AppVersion{Number: 43, Trigger: "manual", CreatedBy: &store.AppActor{ID: bot, Username: "bot", Type: "agent"}}

	// Repo di una persona: l'owner ha admin e riceve l'avviso; il deploy è dell'agente.
	var sent []notify.OccurrenceInput
	var to []uuid.UUID
	n := testNotifier(fakeManagers{admins: map[uuid.UUID]bool{alice: true}}, "user", alice, &sent, &to)
	n.failed(context.Background(), app, v, "health check failed for 2m")
	if len(sent) != 1 || len(to) != 1 || to[0] != alice {
		t.Fatalf("destinatari %v, inviati %d", to, len(sent))
	}
	o := sent[0]
	if o.EventName != EventAppDeployFailed || o.RepoID != repo || o.Actor != uuid.Nil {
		t.Fatalf("occorrenza: %+v", o)
	}
	if o.Data["app"] != "web" || o.Data["version"] != 43 || o.Data["triggeredBy"] != "bot" || o.Data["triggeredByType"] != "agent" || o.Data["reason"] != "health check failed for 2m" {
		t.Fatalf("dati: %+v", o.Data)
	}

	// Organizzazione: solo gli owner che hanno ancora admin; l'agente (o chi non è admin) non riceve nulla.
	sent, to = nil, nil
	n = testNotifier(fakeManagers{owners: map[uuid.UUID][]uuid.UUID{org: {alice, bot}}, admins: map[uuid.UUID]bool{alice: true}}, "organization", org, &sent, &to)
	n.failed(context.Background(), app, v, "x")
	if len(to) != 1 || to[0] != alice {
		t.Fatalf("destinatari %v, voluto solo alice", to)
	}

	// Nessun admin: nessun avviso (e nessun errore).
	sent, to = nil, nil
	n = testNotifier(fakeManagers{admins: map[uuid.UUID]bool{}}, "user", alice, &sent, &to)
	n.failed(context.Background(), app, v, "x")
	if len(sent) != 0 {
		t.Fatalf("avviso inatteso: %+v", sent)
	}
}

func TestDeployFallito_SenzaMotoreNonSiAvvisa(t *testing.T) {
	if f := newDeployFailedNotifier(nil, nil, nil, slog.Default()); f != nil {
		t.Fatal("senza motore delle notifiche non deve esserci un notificatore")
	}
}

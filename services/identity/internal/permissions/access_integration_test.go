//go:build integration

package permissions_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/permissions"
	"github.com/google/uuid"
)

func findAccess(items []permissions.ResourceAccess, id uuid.UUID) *permissions.ResourceAccess {
	for i := range items {
		if items[i].ResourceID == id {
			return &items[i]
		}
	}
	return nil
}

func hasSource(ra *permissions.ResourceAccess, want permissions.Source) bool {
	for _, s := range ra.Sources {
		if s == want {
			return true
		}
	}
	return false
}

func TestUserAccess_FontiELivelloPiuAlto(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bob := e.user(t, "bob", false)
	boss := e.user(t, "boss", false)
	acme := e.org(t, "acme")
	web := e.team(t, acme, "web")
	ops := e.team(t, acme, "ops")
	e.orgMember(t, acme, bob, "member")
	e.teamMember(t, acme, web, bob)
	e.orgMember(t, acme, boss, "owner")

	direct, viaTeam, internal, sum, none, personal := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	e.grant(t, direct, permissions.SubjectUser, bob, permissions.RoleWrite)
	e.grant(t, viaTeam, permissions.SubjectTeam, web, permissions.RoleRead)
	e.grant(t, sum, permissions.SubjectUser, bob, permissions.RoleRead)
	e.grant(t, sum, permissions.SubjectTeam, web, permissions.RoleWrite)
	e.grant(t, none, permissions.SubjectTeam, ops, permissions.RoleAdmin) // bob non è nel team ops
	for _, r := range []struct {
		id  uuid.UUID
		vis permissions.Visibility
	}{{internal, permissions.VisibilityInternal}, {sum, permissions.VisibilityInternal}, {none, permissions.VisibilityPrivate}} {
		if err := e.svc.SetAttributes(ctx, r.id, permissions.OwnerOrganization, acme, r.vis); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.SetAttributes(ctx, personal, permissions.OwnerUser, bob, permissions.VisibilityPrivate); err != nil {
		t.Fatal(err)
	}

	admin, items, err := e.svc.UserAccess(ctx, bob)
	if err != nil || admin {
		t.Fatalf("UserAccess bob: admin=%v err=%v", admin, err)
	}
	if ra := findAccess(items, direct); ra == nil || ra.Role != permissions.RoleWrite ||
		len(ra.Sources) != 1 || ra.Sources[0].Kind != permissions.SourceDirect {
		t.Errorf("grant diretto: %+v", ra)
	}
	if ra := findAccess(items, viaTeam); ra == nil || ra.Role != permissions.RoleRead ||
		!hasSource(ra, permissions.Source{Kind: permissions.SourceTeam, Role: permissions.RoleRead, Organization: "acme", Team: "web"}) {
		t.Errorf("via team: %+v", ra)
	}
	if ra := findAccess(items, internal); ra == nil || ra.Role != permissions.RoleRead ||
		len(ra.Sources) != 1 || ra.Sources[0].Kind != permissions.SourceInternal {
		t.Errorf("internal: %+v", ra)
	}
	// Fonti che si sommano: read diretto + write via team + read internal = write.
	ra := findAccess(items, sum)
	if ra == nil || ra.Role != permissions.RoleWrite || len(ra.Sources) != 3 || ra.Sources[0].Role != permissions.RoleWrite {
		t.Errorf("fonti sommate: %+v", ra)
	}
	if ra := findAccess(items, personal); ra == nil || ra.Role != permissions.RoleAdmin ||
		!hasSource(ra, permissions.Source{Kind: permissions.SourceOwner, Role: permissions.RoleAdmin}) {
		t.Errorf("repo personale: %+v", ra)
	}
	if findAccess(items, none) != nil {
		t.Error("risorsa di un team estraneo e privata: non deve comparire")
	}
	// Il ruolo coincide con EffectiveRole per ogni risorsa elencata.
	for _, it := range items {
		if got := e.role(t, bob, it.ResourceID); got != it.Role {
			t.Errorf("risorsa %s: UserAccess %q, EffectiveRole %q", it.ResourceID, it.Role, got)
		}
	}

	// Owner dell'organizzazione: admin sul repo dell'org e write via team ereditato.
	_, items, err = e.svc.UserAccess(ctx, boss)
	if err != nil {
		t.Fatal(err)
	}
	if ra := findAccess(items, none); ra == nil || ra.Role != permissions.RoleAdmin ||
		!hasSource(ra, permissions.Source{Kind: permissions.SourceOwner, Role: permissions.RoleAdmin, Organization: "acme"}) ||
		!hasSource(ra, permissions.Source{Kind: permissions.SourceTeam, Role: permissions.RoleAdmin, Organization: "acme", Team: "ops"}) {
		t.Errorf("owner di organizzazione: %+v", ra)
	}
}

func TestUserAccess_AdminDisattivatoInesistente(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	root := e.user(t, "root", true)
	gone := e.user(t, "gone", false)
	res := uuid.New()
	e.grant(t, res, permissions.SubjectUser, gone, permissions.RoleAdmin)

	admin, items, err := e.svc.UserAccess(ctx, root)
	if err != nil || !admin || len(items) != 0 {
		t.Errorf("admin di sistema: admin=%v items=%d err=%v", admin, len(items), err)
	}
	e.exec(t, `UPDATE identity.users SET is_active = false WHERE id = $1`, gone)
	admin, items, err = e.svc.UserAccess(ctx, gone)
	if err != nil || admin || len(items) != 0 {
		t.Errorf("disattivato: admin=%v items=%d err=%v", admin, len(items), err)
	}
	if _, _, err := e.svc.UserAccess(ctx, uuid.New()); !errors.Is(err, permissions.ErrUserNotFound) {
		t.Errorf("inesistente: %v", err)
	}
}

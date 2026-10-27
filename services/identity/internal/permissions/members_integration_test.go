//go:build integration

package permissions_test

import (
	"context"
	"sort"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/permissions"
	"github.com/google/uuid"
)

func ids(list []uuid.UUID) []string {
	out := make([]string, 0, len(list))
	for _, id := range list {
		out = append(out, id.String())
	}
	sort.Strings(out)
	return out
}

func sameUsers(t *testing.T, label string, got []uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	g, w := ids(got), ids(want)
	if len(g) != len(w) {
		t.Fatalf("%s: %v (voluti %v)", label, g, w)
	}
	for i := range g {
		if g[i] != w[i] {
			t.Fatalf("%s: %v (voluti %v)", label, g, w)
		}
	}
}

// GIT-404: UsersWithRole segue le stesse regole di EffectiveRole (HasRole).
func TestUsersWithRole_ComeIlRuoloEffettivo(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	sys := e.user(t, "sys", true)
	direct := e.user(t, "direct", false)
	viaTeam := e.user(t, "viateam", false)
	writer := e.user(t, "writer", false)
	reader := e.user(t, "reader", false)
	boss := e.user(t, "boss", false)
	gone := e.user(t, "gone", false)
	acme := e.org(t, "acme")
	ops := e.team(t, acme, "ops")
	e.orgMember(t, acme, boss, "owner")
	e.teamMember(t, acme, ops, viaTeam)

	repo := uuid.New()
	e.grant(t, repo, permissions.SubjectUser, direct, permissions.RoleAdmin)
	e.grant(t, repo, permissions.SubjectTeam, ops, permissions.RoleAdmin)
	e.grant(t, repo, permissions.SubjectUser, writer, permissions.RoleWrite)
	e.grant(t, repo, permissions.SubjectUser, reader, permissions.RoleRead)
	e.grant(t, repo, permissions.SubjectUser, gone, permissions.RoleAdmin)
	e.exec(t, `UPDATE identity.users SET is_active = false WHERE id = $1`, gone)

	// Il grant al team arriva anche all'owner dell'organizzazione (ereditarietà), come in HasRole.
	sameUsers(t, "admin", mustMembers(t, e, repo, permissions.RoleAdmin), sys, direct, viaTeam, boss)
	sameUsers(t, "write", mustMembers(t, e, repo, permissions.RoleWrite), sys, direct, viaTeam, boss, writer)
	sameUsers(t, "read", mustMembers(t, e, repo, permissions.RoleRead), sys, direct, viaTeam, boss, writer, reader)

	// Ogni utente dell'elenco ha davvero il ruolo, e gli altri no.
	for _, u := range []uuid.UUID{sys, direct, viaTeam, writer, reader, boss, gone} {
		in := false
		for _, x := range mustMembers(t, e, repo, permissions.RoleAdmin) {
			in = in || x == u
		}
		if has := e.role(t, u, repo).AtLeast(permissions.RoleAdmin); has != in {
			t.Errorf("utente %s: EffectiveRole admin=%v ma elenco=%v", u, has, in)
		}
	}

	// P1/P6: owner dell'organizzazione e proprietario del repo personale.
	orgRepo, personal := uuid.New(), uuid.New()
	if err := e.svc.SetAttributes(ctx, orgRepo, permissions.OwnerOrganization, acme, permissions.VisibilityPrivate); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetAttributes(ctx, personal, permissions.OwnerUser, writer, permissions.VisibilityInternal); err != nil {
		t.Fatal(err)
	}
	sameUsers(t, "org", mustMembers(t, e, orgRepo, permissions.RoleAdmin), sys, boss)
	sameUsers(t, "personale", mustMembers(t, e, personal, permissions.RoleAdmin), sys, writer)

	if _, err := e.svc.UsersWithRole(ctx, repo, permissions.RoleNone); err == nil {
		t.Error("un ruolo non valido va rifiutato")
	}
}

func mustMembers(t *testing.T, e *env, res uuid.UUID, role permissions.Role) []uuid.UUID {
	t.Helper()
	list, err := e.svc.UsersWithRole(context.Background(), res, role)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

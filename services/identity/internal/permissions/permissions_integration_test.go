//go:build integration

package permissions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/permissions"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type env struct {
	pool  *pgxpool.Pool
	svc   *permissions.Service
	users *users.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	now := func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	return &env{pool: pool, svc: permissions.New(pool, now), users: users.New(pool, now)}
}

func (e *env) user(t *testing.T, name string, admin bool) uuid.UUID {
	t.Helper()
	u, err := e.users.Create(context.Background(), users.CreateInput{
		Username: name, Email: name + "@example.com", DisplayName: name, Password: "una password lunga e buona", IsAdmin: admin,
	})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return u.ID
}

func (e *env) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (e *env) org(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO identity.organizations (id, name) VALUES ($1, $2)`, id, name)
	return id
}

func (e *env) team(t *testing.T, org uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO identity.teams (id, org_id, name) VALUES ($1, $2, $3)`, id, org, name)
	return id
}

func (e *env) orgMember(t *testing.T, org, user uuid.UUID, role string) {
	t.Helper()
	e.exec(t, `INSERT INTO identity.org_members (org_id, user_id, role) VALUES ($1, $2, $3)`, org, user, role)
}

func (e *env) teamMember(t *testing.T, org, team, user uuid.UUID) {
	t.Helper()
	e.exec(t, `INSERT INTO identity.team_members (team_id, org_id, user_id) VALUES ($1, $2, $3)`, team, org, user)
}

func (e *env) grant(t *testing.T, res uuid.UUID, st permissions.SubjectType, subject uuid.UUID, role permissions.Role) permissions.Grant {
	t.Helper()
	g, err := e.svc.Create(context.Background(), permissions.CreateInput{ResourceID: res, SubjectType: st, SubjectID: subject, Role: role})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	return g
}

func (e *env) role(t *testing.T, user, res uuid.UUID) permissions.Role {
	t.Helper()
	r, err := e.svc.EffectiveRole(context.Background(), user, res)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Ereditarietà org → team → utente e massimo fra le fonti.
func TestEffectiveRole(t *testing.T) {
	e := newEnv(t)
	res, other := uuid.New(), uuid.New()

	alice := e.user(t, "alice", false) // grant diretto
	bob := e.user(t, "bob", false)     // via team
	carol := e.user(t, "carol", false) // owner dell'organizzazione
	dave := e.user(t, "dave", false)   // membro semplice dell'organizzazione
	erin := e.user(t, "erin", false)   // nessun grant
	root := e.user(t, "root", true)    // admin di sistema
	frank := e.user(t, "frank", false) // diretto read + team write
	gina := e.user(t, "gina", false)   // team di un'altra organizzazione
	_ = erin

	acme := e.org(t, "acme")
	web := e.team(t, acme, "web")
	e.orgMember(t, acme, bob, "member")
	e.teamMember(t, acme, web, bob)
	e.orgMember(t, acme, carol, "owner")
	e.orgMember(t, acme, dave, "member")
	e.orgMember(t, acme, frank, "member")
	e.teamMember(t, acme, web, frank)

	other1 := e.org(t, "other")
	ops := e.team(t, other1, "ops")
	e.orgMember(t, other1, gina, "member")
	e.teamMember(t, other1, ops, gina)

	e.grant(t, res, permissions.SubjectUser, alice, permissions.RoleWrite)
	e.grant(t, res, permissions.SubjectTeam, web, permissions.RoleWrite)
	e.grant(t, res, permissions.SubjectUser, frank, permissions.RoleRead)
	e.grant(t, other, permissions.SubjectTeam, ops, permissions.RoleAdmin)

	cases := []struct {
		name string
		user uuid.UUID
		res  uuid.UUID
		want permissions.Role
	}{
		{"grant diretto", alice, res, permissions.RoleWrite},
		{"via team", bob, res, permissions.RoleWrite},
		{"via owner dell'organizzazione (org → team → utente)", carol, res, permissions.RoleWrite},
		{"membro semplice dell'org senza team: niente", dave, res, permissions.RoleNone},
		{"nessun grant", erin, res, permissions.RoleNone},
		{"admin di sistema senza grant", root, res, permissions.RoleAdmin},
		{"massimo fra diretto read e team write", frank, res, permissions.RoleWrite},
		{"team di un'altra organizzazione: niente", gina, res, permissions.RoleNone},
		{"il grant vale solo per la sua risorsa", alice, other, permissions.RoleNone},
		{"team admin su un'altra risorsa", gina, other, permissions.RoleAdmin},
		{"owner dell'org ereditato solo sulle risorse dei suoi team", carol, other, permissions.RoleNone},
		{"utente inesistente", uuid.New(), res, permissions.RoleNone},
	}
	for _, c := range cases {
		if got := e.role(t, c.user, c.res); got != c.want {
			t.Errorf("%s: ruolo %q, voluto %q", c.name, got, c.want)
		}
	}

	ok, eff, err := e.svc.Check(context.Background(), bob, res, permissions.RoleWrite)
	if err != nil || !ok || eff != permissions.RoleWrite {
		t.Errorf("Check write per bob: ok=%v eff=%q err=%v", ok, eff, err)
	}
	ok, _, _ = e.svc.Check(context.Background(), bob, res, permissions.RoleAdmin)
	if ok {
		t.Error("Check admin per bob: concesso")
	}
}

// Un utente disattivato non ha più permessi, nemmeno se admin di sistema.
func TestEffectiveRole_UtenteDisattivato(t *testing.T) {
	e := newEnv(t)
	res := uuid.New()
	alice := e.user(t, "alice", false)
	root := e.user(t, "root", true)
	e.grant(t, res, permissions.SubjectUser, alice, permissions.RoleAdmin)
	e.exec(t, `UPDATE identity.users SET is_active = false WHERE id IN ($1, $2)`, alice, root)
	if r := e.role(t, alice, res); r != permissions.RoleNone {
		t.Errorf("alice disattivata: %q", r)
	}
	if r := e.role(t, root, res); r != permissions.RoleNone {
		t.Errorf("root disattivato: %q", r)
	}
}

// Un grant revocato o abbassato vale subito; togliere l'utente dal team
// toglie il ruolo ereditato.
func TestEffectiveRole_Cambiamenti(t *testing.T) {
	e := newEnv(t)
	res := uuid.New()
	bob := e.user(t, "bob", false)
	acme := e.org(t, "acme")
	web := e.team(t, acme, "web")
	e.orgMember(t, acme, bob, "member")
	e.teamMember(t, acme, web, bob)
	g := e.grant(t, res, permissions.SubjectTeam, web, permissions.RoleAdmin)
	if r := e.role(t, bob, res); r != permissions.RoleAdmin {
		t.Fatalf("prima: %q", r)
	}
	if _, err := e.svc.UpdateRole(context.Background(), res, g.ID, permissions.RoleRead); err != nil {
		t.Fatal(err)
	}
	if r := e.role(t, bob, res); r != permissions.RoleRead {
		t.Errorf("dopo l'abbassamento: %q", r)
	}
	e.exec(t, `DELETE FROM identity.team_members WHERE team_id = $1 AND user_id = $2`, web, bob)
	if r := e.role(t, bob, res); r != permissions.RoleNone {
		t.Errorf("dopo l'uscita dal team: %q", r)
	}
	e.teamMember(t, acme, web, bob)
	if err := e.svc.Delete(context.Background(), res, g.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.role(t, bob, res); r != permissions.RoleNone {
		t.Errorf("dopo la revoca: %q", r)
	}
}

func TestGrantCRUD(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	res := uuid.New()
	alice := e.user(t, "alice", false)
	acme := e.org(t, "acme")
	web := e.team(t, acme, "web")

	g := e.grant(t, res, permissions.SubjectUser, alice, permissions.RoleRead)
	if g.ResourceID != res || g.SubjectType != permissions.SubjectUser || g.SubjectID != alice || g.Role != permissions.RoleRead {
		t.Errorf("grant = %+v", g)
	}
	tg := e.grant(t, res, permissions.SubjectTeam, web, permissions.RoleWrite)
	if tg.SubjectType != permissions.SubjectTeam || tg.SubjectID != web {
		t.Errorf("grant team = %+v", tg)
	}

	// Il secondo grant per lo stesso soggetto è un conflitto.
	_, err := e.svc.Create(ctx, permissions.CreateInput{ResourceID: res, SubjectType: permissions.SubjectUser, SubjectID: alice, Role: permissions.RoleAdmin})
	if !errors.Is(err, permissions.ErrConflict) {
		t.Errorf("secondo grant: %v", err)
	}
	// Soggetto inesistente e dati non validi.
	_, err = e.svc.Create(ctx, permissions.CreateInput{ResourceID: res, SubjectType: permissions.SubjectTeam, SubjectID: uuid.New(), Role: permissions.RoleRead})
	if !errors.Is(err, permissions.ErrSubjectNotFound) {
		t.Errorf("team inesistente: %v", err)
	}
	_, err = e.svc.Create(ctx, permissions.CreateInput{ResourceID: res, SubjectType: permissions.SubjectUser, SubjectID: alice, Role: "owner"})
	var ve *permissions.ValidationError
	if !errors.As(err, &ve) || ve.Fields["role"] == "" {
		t.Errorf("ruolo non valido: %v", err)
	}
	// Un utente con lo stesso id di un team non scambia il tipo di soggetto.
	_, err = e.svc.Create(ctx, permissions.CreateInput{ResourceID: res, SubjectType: permissions.SubjectUser, SubjectID: web, Role: permissions.RoleRead})
	if !errors.Is(err, permissions.ErrSubjectNotFound) {
		t.Errorf("id di un team come utente: %v", err)
	}

	list, total, err := e.svc.List(ctx, res, 1, 10)
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("list: %v total=%d len=%d", err, total, len(list))
	}
	list, total, _ = e.svc.List(ctx, res, 2, 1)
	if total != 2 || len(list) != 1 {
		t.Errorf("seconda pagina: total=%d len=%d", total, len(list))
	}
	if _, total, _ := e.svc.List(ctx, uuid.New(), 1, 10); total != 0 {
		t.Errorf("altra risorsa: total=%d", total)
	}

	// Un grant non si tocca passando da un'altra risorsa.
	if _, err := e.svc.UpdateRole(ctx, uuid.New(), g.ID, permissions.RoleAdmin); !errors.Is(err, permissions.ErrNotFound) {
		t.Errorf("update su altra risorsa: %v", err)
	}
	if err := e.svc.Delete(ctx, uuid.New(), g.ID); !errors.Is(err, permissions.ErrNotFound) {
		t.Errorf("delete su altra risorsa: %v", err)
	}
	up, err := e.svc.UpdateRole(ctx, res, g.ID, permissions.RoleAdmin)
	if err != nil || up.Role != permissions.RoleAdmin {
		t.Errorf("update: %+v %v", up, err)
	}
	if _, err := e.svc.UpdateRole(ctx, res, g.ID, "x"); !errors.As(err, &ve) {
		t.Errorf("update con ruolo non valido: %v", err)
	}
	if err := e.svc.Delete(ctx, res, g.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := e.svc.Delete(ctx, res, g.ID); !errors.Is(err, permissions.ErrNotFound) {
		t.Errorf("secondo delete: %v", err)
	}
}

func (e *env) readable(t *testing.T, user uuid.UUID) (bool, map[uuid.UUID]bool) {
	t.Helper()
	all, ids, err := e.svc.ReadableResources(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if ids == nil {
		t.Fatal("ids nil: voluto un elenco (anche vuoto)")
	}
	set := map[uuid.UUID]bool{}
	for _, id := range ids {
		if set[id] {
			t.Errorf("id %s ripetuto", id)
		}
		set[id] = true
	}
	return all, set
}

func TestReadableResources(t *testing.T) {
	e := newEnv(t)
	admin := e.user(t, "root", true)
	alice, bob, carol, dave, erin, gone := e.user(t, "alice", false), e.user(t, "bob", false), e.user(t, "carol", false), e.user(t, "dave", false), e.user(t, "erin", false), e.user(t, "gone", false)
	acme, other := e.org(t, "acme"), e.org(t, "altra")
	web, ops := e.team(t, acme, "web"), e.team(t, other, "ops")
	e.orgMember(t, acme, carol, "owner")
	e.orgMember(t, acme, bob, "member")
	e.teamMember(t, acme, web, bob)
	e.orgMember(t, other, erin, "owner")
	r1, r2, r3, r4 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	e.grant(t, r1, permissions.SubjectUser, alice, permissions.RoleRead)
	e.grant(t, r1, permissions.SubjectTeam, web, permissions.RoleWrite) // bob la legge anche per questa via
	e.grant(t, r2, permissions.SubjectTeam, web, permissions.RoleRead)
	e.grant(t, r3, permissions.SubjectTeam, ops, permissions.RoleAdmin) // altra organizzazione
	e.grant(t, r4, permissions.SubjectUser, gone, permissions.RoleRead)
	e.exec(t, `UPDATE identity.users SET is_active = false WHERE id = $1`, gone)

	for name, tc := range map[string]struct {
		user    uuid.UUID
		wantAll bool
		want    []uuid.UUID
	}{
		"diretto":              {alice, false, []uuid.UUID{r1}},
		"via team (due grant)": {bob, false, []uuid.UUID{r1, r2}},
		"via owner dell'org":   {carol, false, []uuid.UUID{r1, r2}},
		"owner dell'altra org": {erin, false, []uuid.UUID{r3}},
		"senza grant":          {dave, false, nil},
		"admin di sistema":     {admin, true, nil},
		"utente disattivato":   {gone, false, nil},
		"utente inesistente":   {uuid.New(), false, nil},
	} {
		t.Run(name, func(t *testing.T) {
			all, set := e.readable(t, tc.user)
			if all != tc.wantAll || len(set) != len(tc.want) {
				t.Fatalf("all=%v ids=%v, voluti %v", all, set, tc.want)
			}
			for _, id := range tc.want {
				if !set[id] {
					t.Errorf("manca %s", id)
				}
			}
		})
	}
}

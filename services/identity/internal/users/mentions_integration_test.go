//go:build integration

package users_test

import (
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/orgs"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

// ResolveMentions (M-06, I8): utenti e agenti attivi, team espansi nei membri
// attivi, nomi sconosciuti assenti, maiuscole ignorate.
func TestResolveMentions(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	s := users.New(pool, nil)
	o := orgs.New(pool, nil)
	ctx := context.Background()

	mk := func(name, kind string) users.User {
		in := users.CreateInput{Username: name, Kind: kind, Email: name + "@example.com"}
		if kind == "" || kind == "human" {
			in.Password = pw
		}
		u, err := s.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	alice, bob, carol := mk("alice", "human"), mk("bob", "human"), mk("carol", "human")
	bot := mk("botty", "agent")

	org, err := o.Create(ctx, orgs.CreateOrgInput{Name: "acme"}, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	team, err := o.CreateTeam(ctx, org.ID, orgs.CreateTeamInput{Name: "devs"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := o.CreateTeam(ctx, org.ID, orgs.CreateTeamInput{Name: "vuoto"})
	if err != nil {
		t.Fatal(err)
	}
	_ = empty
	for _, u := range []users.User{bob, carol, bot} {
		if _, err := o.SetOrgMember(ctx, org.ID, u.ID, orgs.SetOrgMemberInput{Role: "member"}); err != nil {
			t.Fatal(err)
		}
		if _, err := o.SetTeamMember(ctx, org.ID, team.ID, u.ID, orgs.SetTeamMemberInput{Role: "member"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE identity.users SET is_active = false WHERE id = $1`, carol.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.ResolveMentions(ctx, []string{"Alice", "botty", "carol", "nessuno", "ACME/Devs", "acme/vuoto", "acme/ignoto", "ignota/devs", "a/b/c", "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Users) != 2 || got.Users["Alice"].ID != alice.ID || got.Users["botty"].Kind != "agent" {
		t.Errorf("utenti = %+v (carol è disattivata, nessuno non esiste)", got.Users)
	}
	devs, ok := got.Teams["ACME/Devs"]
	if !ok || len(devs.Members) != 2 || devs.Members[0].Username != "bob" || devs.Members[1].Username != "botty" {
		t.Errorf("team devs = %+v (attesi bob e botty, non la disattivata carol)", devs)
	}
	if v, ok := got.Teams["acme/vuoto"]; !ok || len(v.Members) != 0 {
		t.Errorf("il team vuoto esiste senza membri: %+v %v", v, ok)
	}
	if len(got.Teams) != 2 {
		t.Errorf("team = %+v, attesi solo devs e vuoto", got.Teams)
	}
}

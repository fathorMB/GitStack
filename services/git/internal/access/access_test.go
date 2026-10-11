package access

import (
	"context"
	"errors"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/repostore"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

type fakeIdentity struct{ writeRole bool }

func (fakeIdentity) VerifyToken(context.Context, string) (Principal, bool, error) {
	return Principal{}, false, nil
}

func (f fakeIdentity) HasRole(_ context.Context, _, _, role string) (bool, error) {
	return role == "read" || f.writeRole, nil
}

type fakeCore struct{}

func (fakeCore) ResolveRepo(context.Context, trust.Identity, string, string) (RepoRef, error) {
	return RepoRef{ID: "r1"}, nil
}

type fakeDisk struct{}

func (fakeDisk) Get(context.Context, string) (repostore.Info, error) { return repostore.Info{}, nil }
func (fakeDisk) RepoPath(string) (string, error)                      { return "/tmp/r1.git", nil }

// GIT-190: gli scope si valutano con la regola unica di pkg/scopes, come
// nell'API: write:resource implica read:resource.
func TestAuthorizeImplicazioniDegliScope(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		write  bool
		want   error
	}{
		{"read legge", []string{ScopeRead}, false, nil},
		{"write legge", []string{ScopeWrite}, false, nil},
		{"write+read legge", []string{ScopeRead, ScopeWrite}, false, nil},
		{"read non scrive", []string{ScopeRead}, true, ErrForbidden},
		{"write scrive", []string{ScopeWrite}, true, nil},
		{"senza scope resource non legge", []string{"read:user"}, false, ErrForbidden},
		{"write:user non basta", []string{"write:user"}, true, ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Authorizer{Identity: fakeIdentity{writeRole: true}, Core: fakeCore{}, Disk: fakeDisk{}}
			_, err := a.Authorize(context.Background(), Principal{UserID: "u", Username: "alice", Scopes: tt.scopes}, "alice", "r", tt.write)
			if !errors.Is(err, tt.want) {
				t.Errorf("Authorize(%v, write=%v) = %v, want %v", tt.scopes, tt.write, err, tt.want)
			}
		})
	}
}

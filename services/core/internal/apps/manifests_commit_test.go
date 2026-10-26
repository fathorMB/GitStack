package apps

import "testing"

// GIT-386 (H14): GITSTACK_COMMIT_SHA viene da AppVersion.CommitSha, solo se non è nil; il
// controller non chiama nessuno.
func TestBuildDeployment_CommitSha(t *testing.T) {
	const commit = "3f9a1c2d4e5b6a7f8091a2b3c4d5e6f708192a3b"
	empty := ""
	cases := []struct {
		name   string
		commit *string
		want   string
		wantOK bool
	}{
		{name: "nil: la variabile manca", commit: nil},
		{name: "stringa vuota: la variabile manca", commit: &empty},
		{name: "commit letto dalla versione: la variabile c'è", commit: ptrString(commit), want: commit, wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, ver := testApp()
			ver.CommitSha = tc.commit
			env := envValues(BuildDeployment(app, ver, BuildOptions{Replicas: 1}).Spec.Template.Spec.Containers[0].Env)
			got, ok := env[EnvCommitSha]
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("GITSTACK_COMMIT_SHA = %q (presente %v), atteso %q (presente %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func ptrString(s string) *string { return &s }

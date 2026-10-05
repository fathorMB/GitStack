package receiverules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/git/internal/access"
)

func TestNilRulesNessunaRegola(t *testing.T) {
	var r *Rules
	if r.GitArgs() != nil || r.Env("main") != nil {
		t.Fatal("il valore nil non deve produrre argomenti né ambiente")
	}
}

func TestInstallScriveLHook(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	r, err := Install(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "pre-receive"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "#!/bin/sh\n") || strings.Contains(string(b), "\r") {
		t.Fatal("l'hook deve avere shebang e fine riga LF")
	}
	args := strings.Join(r.GitArgs(), " ")
	if !strings.Contains(args, "core.hooksPath=") {
		t.Fatalf("args = %s", args)
	}
	env := strings.Join(r.Env("main"), " ")
	for _, want := range []string{"GITSTACK_RULE_MAX_BLOB_BYTES=104857600", "GITSTACK_RULE_WARN_REPO_BYTES=5368709120", "GITSTACK_RULE_PROTECT_BRANCH=main"} {
		if !strings.Contains(env, want) {
			t.Errorf("manca %s in %s", want, env)
		}
	}
	if _, err := Install(dir, Limits{MaxBlobBytes: -1}); err == nil {
		t.Fatal("soglia negativa accettata")
	}
	if _, err := Install("", DefaultLimits()); err == nil {
		t.Fatal("directory vuota accettata")
	}
}

func TestProtectedBranch(t *testing.T) {
	cases := []struct {
		ref  access.RepoRef
		want string
	}{
		{access.RepoRef{DefaultBranch: "trunk", ProtectDefaultBranch: true}, "trunk"},
		{access.RepoRef{DefaultBranch: "trunk"}, ""},
		{access.RepoRef{ProtectDefaultBranch: true}, "main"},
	}
	for _, c := range cases {
		if got := ProtectedBranch(c.ref); got != c.want {
			t.Errorf("ProtectedBranch(%+v) = %q, atteso %q", c.ref, got, c.want)
		}
	}
}

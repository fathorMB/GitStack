package gsrepo

import (
	"context"
	"errors"
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := []struct {
		in   string
		want Remote
	}{
		{"https://git.acme.test/acme/api.git", Remote{"git.acme.test", "acme", "api"}},
		{"https://git.acme.test/acme/api", Remote{"git.acme.test", "acme", "api"}},
		{"https://git.acme.test/acme/api/", Remote{"git.acme.test", "acme", "api"}},
		{"https://user:pw@git.acme.test:8443/acme/api.git", Remote{"git.acme.test:8443", "acme", "api"}},
		{"http://localhost:3000/acme/api.git", Remote{"localhost:3000", "acme", "api"}},
		{"git@git.acme.test:acme/api.git", Remote{"git.acme.test", "acme", "api"}},
		{"git@git.acme.test:acme/api", Remote{"git.acme.test", "acme", "api"}},
		{"ssh://git@git.acme.test/acme/api.git", Remote{"git.acme.test", "acme", "api"}},
		{"ssh://git@git.acme.test:2222/acme/api.git", Remote{"git.acme.test", "acme", "api"}},
		{"ssh://git@GIT.Acme.test:2222/acme/my.repo.git\n", Remote{"git.acme.test", "acme", "my.repo"}},
		// R11: le maiuscole del nome del repo si conservano come scritte.
		{"https://git.acme.test/acme/GitStack.git", Remote{"git.acme.test", "acme", "GitStack"}},
		{"ssh://git@git.acme.test:2222/acme/GITSTACK.git", Remote{"git.acme.test", "acme", "GITSTACK"}},
	}
	for _, c := range cases {
		got, err := ParseRemote(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: %+v, atteso %+v", c.in, got, c.want)
		}
	}
	if (Remote{Owner: "a", Repo: "b"}).FullName() != "a/b" {
		t.Error("FullName")
	}
}

func TestParseRemoteErrori(t *testing.T) {
	for _, in := range []string{
		"", "/srv/git/x.git", "https://h.test/solo", "https://h.test/a/b/c", "ftp://h.test/a/b",
		"git@h.test:solo", "https:///a/b",
	} {
		if _, err := ParseRemote(in); err == nil {
			t.Errorf("%q: atteso un errore", in)
		}
	}
}

type fake struct {
	url string
	err error
}

func (f fake) RemoteURL(_ context.Context, name string) (string, error) {
	if name != "origin" {
		return "", errors.New("solo origin")
	}
	return f.url, f.err
}

func TestOriginRemote(t *testing.T) {
	r, err := OriginRemote(context.Background(), fake{url: "git@h.test:acme/api.git"})
	if err != nil || r.Owner != "acme" || r.Host != "h.test" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := OriginRemote(context.Background(), fake{err: ErrNoRemote}); !errors.Is(err, ErrNoRemote) {
		t.Errorf("atteso ErrNoRemote, ho %v", err)
	}
}

func TestParseRepoFlag(t *testing.T) {
	o, r, err := ParseRepoFlag(" acme/api ")
	if err != nil || o != "acme" || r != "api" {
		t.Fatalf("%s/%s %v", o, r, err)
	}
	// R11: --repo conserva le maiuscole del nome.
	if o, r, err := ParseRepoFlag("acme/GitStack"); err != nil || o != "acme" || r != "GitStack" {
		t.Fatalf("%s/%s %v", o, r, err)
	}
	for _, in := range []string{"", "acme", "a/b/c", "/b"} {
		if _, _, err := ParseRepoFlag(in); err == nil {
			t.Errorf("%q: atteso errore", in)
		}
	}
}

func TestExecGitFuoriDaUnRepo(t *testing.T) {
	if _, err := (ExecGit{Dir: t.TempDir()}).RemoteURL(context.Background(), "origin"); !errors.Is(err, ErrNoRemote) {
		t.Errorf("atteso ErrNoRemote fuori da un repo, ho %v", err)
	}
}

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// gitrefGit imita il servizio git: commits?ref= e commits/{sha}/resolve
// risolvono come gitref.Resolve, cioè aggiungono da sé refs/heads/ e
// refs/tags/ (prima il branch) e accettano uno sha solo se esadecimale. Un
// nome già completo ("refs/heads/main") non si trova.
type gitrefGit struct {
	gitclient.Git
	branches, tags map[string]string
	shas           map[string]bool
}

func (g *gitrefGit) ReadJSON(_ context.Context, _ trust.Identity, _ uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	notFound := &gitclient.APIError{Status: 404, Code: "not_found", Message: "Riferimento non trovato."}
	list := func(m map[string]string) json.RawMessage {
		items := []map[string]any{}
		for n, sha := range m {
			items = append(items, map[string]any{"name": n, "commit": map[string]string{"sha": sha}})
		}
		b, _ := json.Marshal(map[string]any{"items": items, "total": len(items)})
		return b
	}
	switch {
	case path == "branches":
		return list(g.branches), nil
	case path == "tags":
		return list(g.tags), nil
	case strings.HasSuffix(path, "/resolve"):
		sha := strings.TrimSuffix(strings.TrimPrefix(path, "commits/"), "/resolve")
		if g.shas[sha] {
			return json.RawMessage(`{"sha":"` + sha + `"}`), nil
		}
		return nil, notFound
	case path == "commits":
		ref := q.Get("ref")
		for _, m := range []map[string]string{g.branches, g.tags} {
			if sha, ok := m[ref]; ok {
				return json.RawMessage(`{"items":[{"sha":"` + sha + `"}]}`), nil
			}
		}
		return nil, notFound
	}
	return nil, notFound
}

func (g *gitrefGit) OpenStream(context.Context, trust.Identity, uuid.UUID, string, url.Values) (*gitclient.Stream, error) {
	return nil, errors.New("non usato")
}

func TestCiResolveRef_NomiCompleti(t *testing.T) {
	sha := func(c string) string { return strings.Repeat(c, 40) }
	g := &gitrefGit{
		branches: map[string]string{"main": sha("a"), "feat/x": sha("b"), "dup": sha("c"), "deadbeef": sha("d")},
		tags:     map[string]string{"v1": sha("e"), "dup": sha("f")},
		shas:     map[string]bool{sha("a"): true},
	}
	s := &apiServer{git: g}
	for _, tc := range []struct{ in, ref, sha string }{
		{"main", "refs/heads/main", sha("a")},
		{"refs/heads/main", "refs/heads/main", sha("a")},
		{"feat/x", "refs/heads/feat/x", sha("b")},
		{"v1", "refs/tags/v1", sha("e")},
		{"refs/tags/v1", "refs/tags/v1", sha("e")},
		{"dup", "refs/heads/dup", sha("c")}, // vince il branch, come in gitref.Resolve
		{"refs/tags/dup", "refs/tags/dup", sha("f")},
		{"deadbeef", "refs/heads/deadbeef", sha("d")}, // un branch esadecimale resta un branch
		{sha("a"), sha("a"), sha("a")},
	} {
		ref, got, err := s.ciResolveRef(context.Background(), uuid.New(), tc.in)
		if err != nil || ref != tc.ref || got != tc.sha {
			t.Errorf("ciResolveRef(%q) = %q, %q, %v; voluti %q, %q", tc.in, ref, got, err, tc.ref, tc.sha)
		}
	}
	for _, in := range []string{"nessuno", "refs/heads/v1", "refs/tags/main", "refs/pull/1/head", "cafebabe1", strings.Repeat("9", 40)} {
		if ref, _, err := s.ciResolveRef(context.Background(), uuid.New(), in); !isGitNotFound(err) {
			t.Errorf("ciResolveRef(%q) = %q, %v; voluto not found", in, ref, err)
		}
	}
}

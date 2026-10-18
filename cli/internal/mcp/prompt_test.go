package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/mcp/promptgen"
)

const skillsDir = "../../../skills"

func norm(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }

// M4: i prompt sono generati dalle skills. Se una SKILL.md cambia senza
// rigenerare prompts_gen.go (cd cli && go generate ./internal/mcp/...), il test fallisce.
func TestPromptGeneratiDalleSkills(t *testing.T) {
	want, err := promptgen.Generate(skillsDir, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("prompts_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if norm(got) != norm(want) {
		t.Fatal("prompts_gen.go diverge da skills/: rigenera con `cd cli && go generate ./internal/mcp/...`")
	}
}

func TestOgniPromptHaIlCorpoDellaSuaSkill(t *testing.T) {
	if len(Prompts) != len(promptgen.Defs) {
		t.Fatalf("prompt %d, definizioni %d", len(Prompts), len(promptgen.Defs))
	}
	have := map[string]string{}
	for _, p := range Prompts {
		have[p.Name] = p.Skill
		raw, err := os.ReadFile(filepath.Join(skillsDir, p.Skill, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(norm(raw), p.Body) || len(p.Body) < 200 {
			t.Errorf("%s: il corpo non è quello di %s", p.Name, p.Skill)
		}
	}
	// i tre flussi di M4
	for name, skill := range map[string]string{
		"issue_to_pr": "gitstack-issue-to-commit", "issue_triage": "gitstack-issue-triage", "pr_review": "gitstack-pr-review",
	} {
		if have[name] != skill {
			t.Errorf("prompt %s: skill %q, attesa %q", name, have[name], skill)
		}
	}
}

func TestPromptsListEGet(t *testing.T) {
	c := newClient(t)
	i := c.send("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	l := c.send("prompts/list", nil)
	g := c.send("prompts/get", map[string]any{"name": "pr_review", "arguments": map[string]string{"repo": "acme/web", "number": "7"}})
	miss := c.send("prompts/get", map[string]any{"name": "pr_review", "arguments": map[string]string{"repo": "acme/web"}})
	unk := c.send("prompts/get", map[string]any{"name": "nope"})
	extra := c.send("prompts/get", map[string]any{"name": "issue_triage", "arguments": map[string]string{"repo": "a/b", "x": "1"}})
	res := c.run(false)

	// M4: prompt sì, risorse no.
	if !strings.Contains(string(res[i].Result), `"prompts":{"listChanged":false}`) || strings.Contains(string(res[i].Result), "resources") {
		t.Errorf("capabilities: %s", res[i].Result)
	}
	var ls struct {
		Prompts []struct {
			Name string `json:"name"`
		} `json:"prompts"`
	}
	if err := json.Unmarshal(res[l].Result, &ls); err != nil || len(ls.Prompts) != len(Prompts) {
		t.Fatalf("prompts/list: %v %s", err, res[l].Result)
	}
	var gt struct {
		Messages []struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(res[g].Result, &gt); err != nil || len(gt.Messages) != 1 || gt.Messages[0].Role != "user" {
		t.Fatalf("prompts/get: %v %s", err, res[g].Result)
	}
	txt := gt.Messages[0].Content.Text
	if !strings.Contains(txt, "- repo: acme/web") || !strings.Contains(txt, "- number: 7") || !strings.Contains(txt, "review di una Pull Request") {
		t.Errorf("testo del prompt: %.300s", txt)
	}
	for _, id := range []int{miss, unk, extra} {
		if res[id].Error == nil || res[id].Error.Code != codeInvalidParams {
			t.Errorf("richiesta %d: atteso -32602, %+v", id, res[id])
		}
	}
}

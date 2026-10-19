//go:build integration

package stackitest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// GIT-273 (v1.1): il ciclo completo di una Pull Request sullo stack vero, per
// le due vie dell'agente. bob (write) è l'agente: apre la issue, spinge un ramo,
// apre la PR con `fixes #n`; alice (admin, persona) approva; la PR si unisce e
// la issue si chiude come completed (PR1-PR3, PR6, PR8, PR10, PR12).
//
//   - TestGsPullRequestCiclo: tutto con `gs pr` (G1, PR10).
//   - TestGsMcpPullRequestCiclo: l'agente parla MCP con `gs mcp` (M1-M4); la
//     persona approva con gs. Anche la sola lettura (M3) e l'api_request.

func (g *gsEnv) prRepo(name string) (string, string) {
	g.t.Helper()
	id := g.createRepo("alice", name, "private")
	want(g.t, g.gw("POST", "/resources/"+id+"/grants", map[string]any{"subjectType": "user", "subjectId": g.userID["bob"], "role": "write"}, g.bearer("alice"), nil), 201, "")
	seed := g.initWork(g.httpsURL("alice", g.tokens["alice"], "/alice/"+name+".git"), map[string]string{"README.md": "# " + name + "\n"})
	g.mustGit(seed, "", "push", "-q", "origin", "main")
	return id, seed
}

// agentBranch spinge su origin (come bob) un ramo con un file in più.
func (g *gsEnv) agentBranch(repo, branch, file, msg string) {
	g.t.Helper()
	w := g.clone(g.httpsURL("bob", g.tokens["bob"], "/alice/"+repo+".git"), "")
	g.mustGit(w, "", "switch", "-q", "-c", branch)
	g.commitFile(w, file, "contenuto di "+file+"\n", msg)
	g.mustGit(w, "", "push", "-q", "origin", branch)
}

func (g *gsEnv) issueIs(repo string, n int, state, reason string) {
	g.t.Helper()
	r := g.ok("alice", repo, "", "issue", "view", fmt.Sprint(n), "--json", "state,closeReason")
	m := jsonMap(g.t, r.out)
	if m["state"] != state || (reason != "" && m["closeReason"] != reason) {
		g.t.Fatalf("issue #%d: %v, attesa %s/%s", n, m, state, reason)
	}
}

func TestGsPullRequestCiclo(t *testing.T) {
	g := newGsEnv(t)
	const repo = "ciclo-pr"
	g.prRepo(repo)
	R := repo
	bob := func(args ...string) gsRun { return g.ok("bob", R, "", args...) }
	alice := func(args ...string) gsRun { return g.ok("alice", R, "", args...) }

	n := int(jsonMap(t, bob("issue", "create", "-t", "Manca la guida", "-b", "da scrivere", "--json", "number").out)["number"].(float64))
	g.agentBranch(repo, fmt.Sprintf("agent/%d-guida", n), "GUIDA.md", fmt.Sprintf("Aggiunge la guida\n\nfixes #%d", n))

	// bozza con `fixes #n`: non si unisce ancora (PR6, PR8)
	r := bob("pr", "create", "--head", fmt.Sprintf("agent/%d-guida", n), "--title", "Aggiunge la guida", "--body", fmt.Sprintf("fixes #%d", n), "--draft", "--json", "number,draft,closingIssues,state")
	pr := jsonMap(t, r.out)
	pn := int(pr["number"].(float64))
	if pr["draft"] != true || pr["state"] != "open" || fmt.Sprint(pr["closingIssues"]) != fmt.Sprintf("[%d]", n) {
		t.Fatalf("PR creata: %v", pr)
	}
	if pn == n {
		t.Fatalf("la PR e la issue condividono la numerazione: PR #%d e issue #%d devono differire", pn, n)
	}
	if r := g.gs("bob", R, "", "pr", "merge", fmt.Sprint(pn), "--squash", "--yes"); r.code == 0 {
		t.Fatalf("merge di una bozza riuscito: %s", r.out)
	}
	g.issueIs(repo, n, "open", "")

	// pronta, con la review chiesta ad alice (PR10, PR12c)
	rd := jsonMap(t, bob("pr", "ready", fmt.Sprint(pn), "--reviewer", "alice", "--json", "draft,requestedReviewers").out)
	if rd["draft"] != false {
		t.Fatalf("pr ready: %v", rd)
	}
	if got := jsonMap(t, bob("pr", "view", fmt.Sprint(pn), "--json", "reviewDecision").out); got["reviewDecision"] == "approved" {
		t.Fatalf("approvata prima della review: %v", got)
	}

	// l'autore non approva la propria PR (PR2); la persona sì
	if r := g.gs("bob", R, "", "pr", "review", fmt.Sprint(pn), "--approve"); r.code == 0 {
		t.Fatal("bob ha approvato la propria PR")
	}
	rv := jsonMap(t, alice("pr", "review", fmt.Sprint(pn), "--approve", "--body", "Ok", "--json", "state,countsAsApproval").out)
	if rv["state"] != "approved" || rv["countsAsApproval"] != true {
		t.Fatalf("review della persona: %v", rv)
	}
	if got := jsonMap(t, bob("pr", "view", fmt.Sprint(pn), "--json", "reviewDecision,approvals").out); got["reviewDecision"] != "approved" {
		t.Fatalf("dopo l'approvazione: %v", got)
	}
	bob("pr", "comment", fmt.Sprint(pn), "--body", "Grazie, unisco.")

	// merge con squash, issue chiusa come completed (PR3, PR8)
	m := jsonMap(t, bob("pr", "merge", fmt.Sprint(pn), "--squash", "--delete-branch", "--yes", "--json", "state,mergeMethod,mergedBy").out)
	if m["state"] != "merged" || m["mergeMethod"] != "squash" {
		t.Fatalf("merge: %v", m)
	}
	g.issueIs(repo, n, "closed", "completed")
	if l := alice("pr", "list", "--state", "merged", "--json", "number").out; !strings.Contains(l, fmt.Sprint(pn)) {
		t.Fatalf("pr list --state merged: %s", l)
	}
	// il README del ramo principale ora ha la guida
	w := g.clone(g.httpsURL("alice", g.tokens["alice"], "/alice/"+repo+".git"), "")
	if out := g.mustGit(w, "", "ls-files", "GUIDA.md"); strings.TrimSpace(out) != "GUIDA.md" {
		t.Fatalf("GUIDA.md non è sul ramo principale: %q", out)
	}
}

// mcpSession è un client MCP minimo su `gs mcp` (stdio).
type mcpSession struct {
	t   *testing.T
	in  io.WriteCloser
	out *bufio.Reader
	cmd *exec.Cmd
	id  int
}

func (g *gsEnv) startMCP(token string, args ...string) *mcpSession {
	g.t.Helper()
	cmd := exec.Command(g.bin, append([]string{"--hostname", g.proxy, "mcp"}, args...)...)
	cmd.Dir = g.t.TempDir()
	cmd.Env = append(cleanEnv(), "GS_CONFIG_DIR="+g.t.TempDir(), "GS_HOST="+g.proxy, "GS_TOKEN="+token, "GS_NO_KEYRING=1", "HOME="+g.home, "USERPROFILE="+g.home)
	in, err := cmd.StdinPipe()
	if err != nil {
		g.t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		g.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		g.t.Fatal(err)
	}
	s := &mcpSession{t: g.t, in: in, out: bufio.NewReader(out), cmd: cmd}
	g.t.Cleanup(func() { _ = in.Close(); _ = cmd.Wait() })
	s.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	_, _ = io.WriteString(in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	return s
}

func (s *mcpSession) rpc(method string, params any) json.RawMessage {
	s.t.Helper()
	s.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": s.id, "method": method, "params": params})
	if _, err := s.in.Write(append(b, '\n')); err != nil {
		s.t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() { l, _ := s.out.ReadString('\n'); done <- l }()
	select {
	case line := <-done:
		var r struct {
			ID    int             `json:"id"`
			Res   json.RawMessage `json:"result"`
			Error *struct{ Message string }
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil || r.ID != s.id {
			s.t.Fatalf("risposta MCP a %s: %q (%v)", method, line, err)
		}
		if r.Error != nil {
			s.t.Fatalf("%s: errore JSON-RPC %s", method, r.Error.Message)
		}
		return r.Res
	case <-time.After(60 * time.Second):
		s.t.Fatalf("%s: nessuna risposta da gs mcp in 60 s", method)
	}
	return nil
}

// call chiama uno strumento; ritorna (testo, isError).
func (s *mcpSession) call(tool string, args map[string]any) (string, bool) {
	s.t.Helper()
	var cr struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(s.rpc("tools/call", map[string]any{"name": tool, "arguments": args}), &cr); err != nil || len(cr.Content) == 0 {
		s.t.Fatalf("%s: risultato non valido: %v", tool, err)
	}
	return cr.Content[0].Text, cr.IsError
}

func (s *mcpSession) must(tool string, args map[string]any) map[string]any {
	s.t.Helper()
	text, isErr := s.call(tool, args)
	if isErr {
		s.t.Fatalf("%s: %s", tool, text)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(text), &m)
	return m
}

func TestGsMcpPullRequestCiclo(t *testing.T) {
	g := newGsEnv(t)
	const repo = "ciclo-mcp"
	g.prRepo(repo)
	R := repo

	mcp := g.startMCP(g.tokens["bob"])
	// M2/M4: strumenti e prompt del flusso issue → PR
	var tl struct{ Tools []struct{ Name string } }
	_ = json.Unmarshal(mcp.rpc("tools/list", nil), &tl)
	have := map[string]bool{}
	for _, tt := range tl.Tools {
		have[tt.Name] = true
	}
	for _, name := range []string{"issue_create", "pr_create", "pr_ready", "pr_merge", "review_create", "api_request"} {
		if !have[name] {
			t.Fatalf("strumento %s assente: %v", name, have)
		}
	}
	if !strings.Contains(string(mcp.rpc("prompts/list", nil)), "issue_to_pr") {
		t.Error("manca il prompt del flusso issue → PR")
	}

	issue := mcp.must("issue_create", map[string]any{"owner": "alice", "repo": repo, "body": map[string]any{"title": "Manca la guida MCP", "body": "da scrivere"}})
	n := int(issue["number"].(float64))
	g.agentBranch(repo, fmt.Sprintf("agent/%d-guida", n), "GUIDA.md", fmt.Sprintf("Aggiunge la guida\n\nfixes #%d", n))
	pr := mcp.must("pr_create", map[string]any{"owner": "alice", "repo": repo, "body": map[string]any{
		"title": "Aggiunge la guida", "sourceBranch": fmt.Sprintf("agent/%d-guida", n), "body": fmt.Sprintf("fixes #%d", n), "draft": true,
	}})
	pn := int(pr["number"].(float64))
	if pr["draft"] != true {
		t.Fatalf("pr_create: %v", pr)
	}
	// una bozza non si unisce
	if text, isErr := mcp.call("pr_merge", map[string]any{"owner": "alice", "repo": repo, "number": pn, "body": map[string]any{"method": "squash"}}); !isErr {
		t.Fatalf("merge di una bozza riuscito: %s", text)
	}
	mcp.must("pr_ready", map[string]any{"owner": "alice", "repo": repo, "number": pn})

	// la persona approva con gs (PR2)
	rv := jsonMap(t, g.ok("alice", R, "", "pr", "review", fmt.Sprint(pn), "--approve", "--json", "state,countsAsApproval").out)
	if rv["state"] != "approved" || rv["countsAsApproval"] != true {
		t.Fatalf("review della persona: %v", rv)
	}

	merged := mcp.must("pr_merge", map[string]any{"owner": "alice", "repo": repo, "number": pn, "body": map[string]any{"method": "merge"}})
	if merged["state"] != "merged" {
		t.Fatalf("pr_merge: %v", merged)
	}
	g.issueIs(repo, n, "closed", "completed")

	// M3: la sola lettura rifiuta le scritture, anche via api_request, e lascia leggere
	ro := g.startMCP(g.tokens["bob"], "--read-only")
	if text, isErr := ro.call("issue_create", map[string]any{"owner": "alice", "repo": repo, "body": map[string]any{"title": "no"}}); !isErr {
		t.Fatalf("issue_create in sola lettura riuscito: %s", text)
	}
	if text, isErr := ro.call("api_request", map[string]any{"method": "POST", "path": "/repos/alice/" + repo + "/issues", "body": map[string]any{"title": "no"}}); !isErr {
		t.Fatalf("api_request POST in sola lettura riuscito: %s", text)
	}
	if text, isErr := ro.call("pr_view", map[string]any{"owner": "alice", "repo": repo, "number": pn}); isErr || !strings.Contains(text, "merged") {
		t.Fatalf("pr_view in sola lettura: %v %s", isErr, text)
	}
}

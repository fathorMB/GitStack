package api

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// Estensione `x-mcp` (M2, M3; GIT-258): ogni operazione del tag `pulls` dice se
// e' esposta come strumento MCP, con quale nome `oggetto_azione` e se e'
// distruttiva. Il test legge openapi.yaml riga per riga (il modulo api non ha
// dipendenze), quindi si appoggia alla forma fissa del documento:
// percorsi a 2 spazi, metodi a 4, proprieta' dell'operazione a 6.

type specOp struct {
	method, path, id string
	pulls            bool
	mcp              bool
	expose, destr    string
	name             string
}

func readOps(t *testing.T) []*specOp {
	t.Helper()
	var ops []*specOp
	var path string
	var cur *specOp
	inMCP := false
	inPaths := false
	method := regexp.MustCompile(`^    (get|put|post|delete|patch):\s*$`)
	sc := bufio.NewScanner(bytes.NewReader(Spec))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "paths:") {
			inPaths = true
			continue
		}
		if inPaths && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			break // fine di paths
		}
		if !inPaths {
			continue
		}
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			path = strings.TrimSuffix(strings.TrimSpace(line), ":")
			cur = nil
			continue
		}
		if m := method.FindStringSubmatch(line); m != nil {
			cur = &specOp{method: strings.ToUpper(m[1]), path: path}
			ops = append(ops, cur)
			inMCP = false
			continue
		}
		if cur == nil || !strings.HasPrefix(line, "      ") || strings.HasPrefix(line, "       ") && !inMCP {
			continue
		}
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "      x-mcp:"):
			cur.mcp, inMCP = true, true
		case inMCP && strings.HasPrefix(line, "        expose:"):
			cur.expose = strings.TrimSpace(strings.TrimPrefix(trim, "expose:"))
		case inMCP && strings.HasPrefix(line, "        name:"):
			cur.name = strings.TrimSpace(strings.TrimPrefix(trim, "name:"))
		case inMCP && strings.HasPrefix(line, "        destructive:"):
			cur.destr = strings.TrimSpace(strings.TrimPrefix(trim, "destructive:"))
		case strings.HasPrefix(line, "      operationId:"):
			cur.id = strings.TrimSpace(strings.TrimPrefix(trim, "operationId:"))
			inMCP = false
		case strings.HasPrefix(line, "      tags:"):
			cur.pulls = strings.Contains(trim, "pulls")
			inMCP = false
		case !strings.HasPrefix(line, "        "):
			inMCP = false
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return ops
}

func TestMCPExtensionOnPullOperations(t *testing.T) {
	nameRe := regexp.MustCompile(`^[a-z]+(_[a-z]+)+$`)
	names := map[string]string{}
	count := 0
	exposed := 0
	for _, op := range readOps(t) {
		if !op.pulls {
			continue
		}
		count++
		where := op.method + " " + op.path + " (" + op.id + ")"
		if !op.mcp {
			t.Errorf("%s: manca l'estensione x-mcp", where)
			continue
		}
		if op.expose != "true" && op.expose != "false" {
			t.Errorf("%s: x-mcp.expose deve essere true o false, trovato %q", where, op.expose)
		}
		if op.destr != "true" && op.destr != "false" {
			t.Errorf("%s: x-mcp.destructive deve essere true o false, trovato %q", where, op.destr)
		}
		if !nameRe.MatchString(op.name) {
			t.Errorf("%s: x-mcp.name %q non e' nella forma oggetto_azione", where, op.name)
		}
		if prev, dup := names[op.name]; dup {
			t.Errorf("%s: x-mcp.name %q gia' usato da %s", where, op.name, prev)
		}
		names[op.name] = op.id
		if op.expose == "true" {
			exposed++
		}
		// M3: chiusure, merge ed eliminazioni sono distruttive; le letture no.
		wantDestructive := op.method == "DELETE" ||
			strings.HasPrefix(op.id, "close") || strings.HasPrefix(op.id, "merge") || strings.HasPrefix(op.id, "delete")
		if wantDestructive && op.destr != "true" {
			t.Errorf("%s: chiusure, merge ed eliminazioni sono distruttive (M3)", where)
		}
		if op.method == "GET" && op.destr != "false" {
			t.Errorf("%s: una lettura non e' distruttiva", where)
		}
		// M3: nessuno strumento dedicato per le impostazioni.
		if strings.Contains(op.id, "Settings") && op.expose != "false" {
			t.Errorf("%s: le impostazioni non hanno uno strumento dedicato (M3)", where)
		}
	}
	if count < 30 {
		t.Fatalf("operazioni del tag pulls trovate: %d, attese almeno 30", count)
	}
	if exposed == 0 {
		t.Fatal("nessuna operazione esposta in MCP")
	}
}

// Le operazioni di PR1-PR12 che il contratto deve descrivere (GIT-258).
func TestPullOperationsPresent(t *testing.T) {
	want := []string{
		"listPullRequests", "createPullRequest", "getPullRequest", "updatePullRequest",
		"markPullRequestReady", "markPullRequestDraft", "closePullRequest", "reopenPullRequest",
		"listPullRequestCommits", "listPullRequestFiles", "setPullRequestFileViewed",
		"getPullRequestMergeability", "mergePullRequest", "deletePullRequestBranch",
		"listPullRequestReviews", "createPullRequestReview",
		"listPullRequestComments", "createPullRequestComment", "updatePullRequestComment", "deletePullRequestComment",
		"listPullRequestThreads", "resolvePullRequestThread", "unresolvePullRequestThread",
		"requestPullRequestReviewers", "removePullRequestReviewer",
		"listCommitStatuses", "createCommitStatus", "getCombinedCommitStatus",
		"getBranchSettings", "updateBranchSettings",
	}
	have := map[string]bool{}
	for _, op := range readOps(t) {
		have[op.id] = true
	}
	for _, id := range want {
		if !have[id] {
			t.Errorf("operazione %s assente dal contratto", id)
		}
	}
	for _, schema := range []string{"WebhookPullRequestPayload", "WebhookPullRequestReviewPayload", "WebhookCommitStatusPayload", "PullRequestMergeability"} {
		if !bytes.Contains(Spec, []byte("\n    "+schema+":")) {
			t.Errorf("schema %s assente", schema)
		}
	}
}

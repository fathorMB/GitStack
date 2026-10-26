package mcp

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// GIT-354 (H13, mockup H10 «MCP tools»): gli strumenti delle app escono dal contratto, e le operazioni da
// admin che modificano qualcosa portano l'annotazione «distruttiva».

const appsSpecPath = "../../../api/openapi.yaml"

// adminOps trova nel contratto le operazioni del tag apps con x-repo-permission: admin.
var adminOps = regexp.MustCompile(`\n    (get|post|put|patch|delete):\n      operationId: (\w+)\n      tags: \[apps\]\n      x-required-scopes: [^\n]*\n      x-repo-permission: admin\n      x-mcp:\n        expose: (\w+)\n        name: (\S+)\n        destructive: (\w+)`)

func TestOperazioniAdminDelleAppSonoDistruttive(t *testing.T) {
	raw, err := os.ReadFile(appsSpecPath)
	if err != nil {
		t.Fatal(err)
	}
	spec := strings.ReplaceAll(string(raw), "\r\n", "\n")
	found := 0
	for _, m := range adminOps.FindAllStringSubmatch(spec, -1) {
		method, op, expose, name, destructive := m[1], m[2], m[3], m[4], m[5]
		found++
		if method == "get" {
			if destructive != "false" {
				t.Errorf("%s (%s): una lettura da admin non è distruttiva", op, name)
			}
			continue
		}
		if destructive != "true" {
			t.Errorf("%s (%s): operazione da admin che modifica, attesa x-mcp.destructive: true", op, name)
		}
		_ = expose
	}
	if found < 12 {
		t.Fatalf("operazioni admin delle app trovate: %d; la regex non legge più il contratto?", found)
	}
}

// La tabella «MCP tools» del mockup: lettura, scrittura (write) e admin distruttivo.
func TestStrumentiMCPDelleAppConLeAnnotazioniGiuste(t *testing.T) {
	c := newClient(t)
	l := c.send("tools/list", nil)
	res := c.run(false)
	var ls listed
	_ = json.Unmarshal(res[l].Result, &ls)
	by := map[string]int{}
	for k, tl := range ls.Tools {
		by[tl.Name] = k
	}
	want := map[string]string{
		"app_list": "read", "app_get": "read", "app_version_list": "read", "app_logs": "read",
		"app_deploy": "write", "app_rollback": "write", "app_restart": "write", "app_scale": "write",
		"app_create": "destructive", "app_delete": "destructive", "app_set_access": "destructive",
		"app_access_member_add": "destructive", "app_access_member_remove": "destructive",
		// GIT-359 (H3): volumi persistenti, dal contratto di GIT-356.
		"app_volume_list": "read", "app_volume_create": "destructive", "app_volume_delete": "destructive",
	}
	for name, kind := range want {
		i, ok := by[name]
		if !ok {
			t.Errorf("strumento %s assente", name)
			continue
		}
		a := ls.Tools[i].Annotations
		switch kind {
		case "read":
			if !a.ReadOnlyHint || a.DestructiveHint {
				t.Errorf("%s: %+v, attesa lettura", name, a)
			}
		case "write":
			if a.ReadOnlyHint || a.DestructiveHint {
				t.Errorf("%s: %+v, attesa scrittura non distruttiva", name, a)
			}
		case "destructive":
			if a.ReadOnlyHint || !a.DestructiveHint {
				t.Errorf("%s: %+v, attesa distruttiva", name, a)
			}
		}
	}
}

// Gli strumenti mandano la richiesta al percorso del contratto, con il corpo: app_deploy diventa un POST su
// /deploys del repo, con l'identità del token di chi chiama (l'agente).
func TestAppDeployMCPChiamaIlContratto(t *testing.T) {
	var tool Tool
	for _, tl := range Tools {
		if tl.Name == "app_deploy" {
			tool = tl
		}
	}
	if tool.Method != "POST" || tool.Path != "/repos/{owner}/{repo}/apps/{app}/deploys" || !tool.Body || tool.Destructive {
		t.Fatalf("app_deploy: %+v", tool)
	}
	if strings.Join(tool.PathParams, ",") != "app,owner,repo" {
		t.Errorf("parametri di percorso: %v", tool.PathParams)
	}
}

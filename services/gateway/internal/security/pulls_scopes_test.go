package security

import "testing"

// GIT-261 (v1.1): le operazioni delle PR di questo item chiedono gli ambiti
// del contratto: leggere vuole read:resource, aprire e gestire write:resource,
// quindi un token di sola lettura non apre né chiude una PR.
func TestRoutes_AmbitiDelleOperazioniPR(t *testing.T) {
	want := map[string]string{
		"listPullRequests":       "read:resource",
		"getPullRequest":         "read:resource",
		"listPullRequestCommits": "read:resource",
		"listPullRequestFiles":   "read:resource",
		"listPullRequestEvents":  "read:resource",
		"createPullRequest":      "write:resource",
		"updatePullRequest":      "write:resource",
		"markPullRequestReady":   "write:resource",
		"markPullRequestDraft":   "write:resource",
		"closePullRequest":       "write:resource",
		"reopenPullRequest":      "write:resource",
	}
	seen := 0
	for _, r := range Routes {
		scope, ok := want[r.OperationID]
		if !ok {
			continue
		}
		seen++
		if len(r.Scopes) != 1 || r.Scopes[0] != scope {
			t.Errorf("%s: ambiti %v, voluto %s", r.OperationID, r.Scopes, scope)
		}
		if !Grants([]string{"read:resource"}, scope) != (scope == "write:resource") {
			t.Errorf("%s: un token read:resource %s l'operazione", r.OperationID, map[bool]string{true: "ammette", false: "esclude"}[scope == "write:resource"])
		}
		if !Grants([]string{"write:resource"}, scope) {
			t.Errorf("%s: write:resource deve bastare", r.OperationID)
		}
	}
	if seen != len(want) {
		t.Errorf("operazioni trovate = %d, volute %d", seen, len(want))
	}
}

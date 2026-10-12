//go:build integration

package httpserver_test

import (
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
)

// MP1 (GIT-201): il servizio git risponde con ref divergenti e un testo in
// `error` (divergedText) anche se il fetch è riuscito. Per core è comunque un
// esito `diverged` (mirror `in_sync`, nessun ritentativo), non un errore.
func TestPullMirror_RefDivergenti_ConTestoDErrore_NonEUnErrore(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", true)
	e.enable("app", pullURL, "")
	e.puller.setAnswer(func(int, gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error) {
		return gitclient.MirrorPullResult{
			OK: false, Diverged: true,
			Error: "ref divergenti, lasciati come sono: refs/heads/main (non-fast-forward)",
			Refs: []gitclient.MirrorPullRef{
				{Ref: "refs/heads/main", Status: "diverged", SHA: strings.Repeat("a", 40), Origin: strings.Repeat("b", 40), Reason: "non-fast-forward"},
				{Ref: "refs/heads/dev", Status: "updated", SHA: strings.Repeat("c", 40)},
			},
		}, nil
	})
	e.processPull()

	got := e.getPull("app")
	if got.State != "in_sync" || len(got.DivergedRefs) != 1 || got.Attempts != 0 || got.LastError != nil {
		t.Fatalf("mirror con un ref divergente: %+v", got)
	}
	if runs := e.pullRuns("app"); runs.Items[0].Outcome != "diverged" {
		t.Fatalf("registro: %+v", runs.Items[0])
	}

	// Un ref in errore resta un errore, anche se un altro diverge.
	e.syncNow("app")
	e.puller.setAnswer(func(int, gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error) {
		return gitclient.MirrorPullResult{
			OK: false, Diverged: true, Error: "ref divergenti, lasciati come sono",
			Refs: []gitclient.MirrorPullRef{
				{Ref: "refs/heads/main", Status: "diverged", SHA: strings.Repeat("a", 40), Origin: strings.Repeat("b", 40)},
				{Ref: "refs/heads/dev", Status: "error", Reason: "update-ref fallito"},
			},
		}, nil
	})
	e.processPull()
	if got := e.getPull("app"); got.State != "error" {
		t.Fatalf("con un ref in errore lo stato doveva essere error: %+v", got)
	}
}

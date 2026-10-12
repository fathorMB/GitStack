package cli

import (
	"context"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/status"
	"github.com/fathorMB/GitStack/admin/internal/upgrade"
)

// k3sAvailable completa il report con la versione di k3s dichiarata dal ramo
// di destinazione (deploy/k3s-version, N10) e il tipo di aggiornamento.
// Best effort: senza rete o senza file lo stato resta quello locale.
func (a *App) k3sAvailable(ctx context.Context, rep *status.Report) {
	if rep.K3sVersion == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	declared, err := upgrade.DeclaredK3sVersion(cctx, a.HTTP, a.Getenv("GITSTACK_RAW_URL"), a.Getenv("GITSTACK_REPO"), a.Getenv("GITSTACK_REF"))
	if err != nil {
		return
	}
	plan, err := upgrade.PlanK3s(rep.K3sVersion, declared, true)
	if err != nil {
		// più vecchia dell'installata, o salto di più minori: niente da proporre
		if plan.Action != upgrade.K3sMinor {
			return
		}
	}
	rep.K3sDeclared = declared
	switch plan.Action {
	case upgrade.K3sPatch:
		rep.K3sUpdate = "patch"
	case upgrade.K3sMinor:
		rep.K3sUpdate = "minor"
	}
}

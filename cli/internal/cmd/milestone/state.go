package milestone

import (
	"fmt"
	"strconv"
	"time"

	gitstack "github.com/fathorMB/GitStack/client/go"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// confirmDelete è la conferma G8 delle operazioni distruttive: riscrivere
// il numero della milestone. Con --yes procede; senza terminale e senza --yes è
// un uso errato (exit 2) e non si legge stdin.
func confirmDelete(f *cmdutil.Factory, yes bool, n int64) error {
	prompt := fmt.Sprintf("Eliminare la milestone #%d. Riscrivi `%d` per confermare:", n, n)
	return cmdutil.ConfirmOrYes(f.IO, yes, prompt, strconv.FormatInt(n, 10))
}

// milestoneOut è una milestone con il suo indirizzo web e l'avanzamento.
type milestoneOut struct {
	gitstack.Milestone
	URL      string `json:"url"`
	Progress string `json:"progress"`
}

func (e *env) printMilestone(jo *output.JSONOptions, m *gitstack.Milestone) error {
	if jo.Enabled() {
		out := milestoneOut{Milestone: *m, URL: e.webURL(m.Number), Progress: milestoneProgress(*m)}
		return jo.Write(e.f.IO.Out, out, e.f.IO.OutTTY)
	}
	fmt.Fprintf(e.f.IO.Out, "#%d\t%s\t%s\t%s\n", m.Number, m.Title, m.State, milestoneProgress(*m))
	return nil
}

// milestoneProgress calcola la percentuale di avanzamento (issue chiuse / totali).
func milestoneProgress(m gitstack.Milestone) string {
	total := m.OpenIssues + m.ClosedIssues
	if total == 0 {
		return "0%"
	}
	pct := m.ClosedIssues * 100 / total
	return fmt.Sprintf("%d%%", pct)
}

// parseDate converte una stringa nel formato AAAA-MM-GG in un openapi_types.Date.
func parseDate(s string) (openapi_types.Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return openapi_types.Date{}, cmdutil.UsageErrorf("formato data non valido: %q (atteso AAAA-MM-GG)", s)
	}
	return openapi_types.Date{Time: t}, nil
}

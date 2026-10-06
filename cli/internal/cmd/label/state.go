package label

import (
	"fmt"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// confirmDelete è la conferma G8 delle operazioni distruttive: riscrivere
// il nome dell'etichetta. Con --yes procede; senza terminale e senza --yes è
// un uso errato (exit 2) e non si legge stdin.
func confirmDelete(f *cmdutil.Factory, yes bool, name string) error {
	prompt := fmt.Sprintf("Eliminare l'etichetta %s. Riscrivi `%s` per confermare:", name, name)
	return cmdutil.ConfirmOrYes(f.IO, yes, prompt, name)
}

// labelOut è un'etichetta con il suo indirizzo web.
type labelOut struct {
	gitstack.Label
	URL string `json:"url"`
}

func (e *env) printLabel(jo *output.JSONOptions, l *gitstack.Label) error {
	if jo.Enabled() {
		out := labelOut{Label: *l, URL: e.webURL(l.Name)}
		return jo.Write(e.f.IO.Out, out, e.f.IO.OutTTY)
	}
	fmt.Fprintf(e.f.IO.Out, "%s\t%s\t%s\t%s\n", l.Name, l.Color, l.Description, e.webURL(l.Name))
	return nil
}

func validateColor(c string) error {
	if len(c) != 6 {
		return fmt.Errorf("deve essere esadecimale a sei cifre")
	}
	for _, b := range c {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			return fmt.Errorf("carattere non esadecimale: %c", b)
		}
	}
	return nil
}

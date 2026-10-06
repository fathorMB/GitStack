package cmdutil

import "github.com/spf13/cobra"

// NoArgs rifiuta gli argomenti posizionali con un UsageError (exit 2);
// cobra.NoArgs darebbe un errore generico (exit 1).
func NoArgs(c *cobra.Command, args []string) error {
	if len(args) > 0 {
		return UsageErrorf("argomento inatteso %q per %q", args[0], c.CommandPath())
	}
	return nil
}

// NArgs restituisce un validatore Args che richiede esattamente n argomenti.
// A differenza di cobra.ExactArgs, restituisce un UsageError (exit 2).
func NArgs(n int) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if len(args) < n {
			return UsageErrorf("almeno %d argomento/i richiesti per %q", n, c.CommandPath())
		}
		if len(args) > n {
			return UsageErrorf("al massimo %d argomento/i per %q, ricevuto %d", n, c.CommandPath(), len(args))
		}
		return nil
	}
}

// GroupRun è il RunE dei comandi che raggruppano sottocomandi: senza
// argomenti mostra l'aiuto (exit 0), con un argomento che non è un
// sottocomando è un uso errato (exit 2). Rende il gruppo "disponibile" per
// cobra, quindi elencato in `gs --help`.
func GroupRun(c *cobra.Command, args []string) error {
	if len(args) > 0 {
		return UsageErrorf("comando sconosciuto %q per %q", args[0], c.CommandPath())
	}
	return c.Help()
}

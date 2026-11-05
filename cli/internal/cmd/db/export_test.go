package db

import "github.com/fathorMB/GitStack/cli/internal/cmdutil"

// SetPsql sostituisce la ricerca e il lancio di psql nei test e restituisce il ripristino.
func SetPsql(look func(string) (string, error), run func(path string, args, env []string, f *cmdutil.Factory) error) func() {
	old := psql
	psql = psqlRunner{lookPath: look, run: run}
	return func() { psql = old }
}

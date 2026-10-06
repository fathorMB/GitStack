// Package cli è il dispatcher dei comandi di `gitstack`, lo strumento di
// amministrazione dell'host (non `gs`, la CLI degli utenti).
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
)

// Codici di uscita, documentati nel README del modulo.
const (
	ExitOK          = 0 // comando riuscito (status: tutto sano)
	ExitUnhealthy   = 1 // status: almeno un servizio o l'API non è sano
	ExitUsage       = 2 // comando o opzione sconosciuti
	ExitConfig      = 3 // configurazione assente o non valida
	ExitCluster     = 4 // status: il cluster non si può interrogare
	ExitNeedsRoot   = 5 // il comando richiede root
	ExitUnexpected  = 70
	envConfigPath   = "GITSTACK_CONFIG"
	configFlagUsage = "percorso del file di configurazione (default " + config.DefaultPath + ", o $" + envConfigPath + ")"
)

// App contiene le dipendenze dei comandi: nei test si sostituiscono.
type App struct {
	Version string
	Stdout  io.Writer
	Stderr  io.Writer
	Getenv  func(string) string
	Geteuid func() int
	Runner  status.Runner
	HTTP    *http.Client
	// LookPath, se non nil, sostituisce exec.LookPath nella ricerca di kubectl.
	LookPath func(string) (string, error)
	// NewCluster, se non nil, sostituisce il cluster kubectl (test di backup e restore).
	NewCluster func(*config.Config) backup.Cluster
}

// NewApp è l'App di produzione.
func NewApp(version string) *App {
	return &App{
		Version: version,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Getenv:  os.Getenv,
		Geteuid: os.Geteuid,
		Runner:  status.ExecRunner{},
	}
}

type command struct {
	name      string
	summary   string
	needsRoot bool // cambia lo stato dell'host: chiede root
	run       func(ctx context.Context, a *App, args []string) int
}

// cmds è la tabella dei comandi (una variabile, perché i test vi aggiungono
// un comando che cambia lo stato).
var cmds []command

func init() {
	cmds = []command{
		{name: "status", summary: "versione, host, salute dei servizi, ultimo backup", run: runStatus},
		{name: "backup", summary: "archivio coerente di database, repo, allegati e configurazione", needsRoot: true, run: runBackup},
		{name: "restore", summary: "ripristina un archivio su un'installazione pulita della stessa versione", needsRoot: true, run: runRestore},
		{name: "version", summary: "versione del binario", run: runVersion},
	}
}

// Run esegue la riga di comando args (senza il nome del programma) e
// restituisce il codice di uscita.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.usage(a.Stderr)
		return ExitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		a.usage(a.Stdout)
		return ExitOK
	case "--version":
		return runVersion(ctx, a, nil)
	}
	for _, c := range cmds {
		if c.name != args[0] {
			continue
		}
		if c.needsRoot && a.Geteuid() != 0 {
			a.errorf("'%s' cambia lo stato dell'host: servono i permessi di root. Rilancia con sudo: sudo gitstack %s", c.name, strings.Join(args, " "))
			return ExitNeedsRoot
		}
		return c.run(ctx, a, args[1:])
	}
	a.errorf("comando sconosciuto: %s", args[0])
	a.usage(a.Stderr)
	return ExitUsage
}

func (a *App) usage(w io.Writer) {
	cs := append([]command(nil), cmds...)
	sort.Slice(cs, func(i, j int) bool { return cs[i].name < cs[j].name })
	_, _ = fmt.Fprintln(w, "Uso: gitstack <comando> [opzioni]")
	_, _ = fmt.Fprintln(w, "\nStrumento di amministrazione dell'host GitStack (per gli utenti c'è `gs`).")
	_, _ = fmt.Fprintln(w, "\nComandi:")
	for _, c := range cs {
		_, _ = fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
	}
	_, _ = fmt.Fprintln(w, "\nOpzioni comuni: --config PERCORSO (o $"+envConfigPath+"), -h/--help.")
	_, _ = fmt.Fprintln(w, "Codici di uscita: 0 ok, 1 non sano, 2 uso, 3 configurazione, 4 cluster, 5 serve root, 6 rifiutato.")
}

func (a *App) errorf(format string, args ...any) {
	_, _ = fmt.Fprintf(a.Stderr, "ERRORE: "+format+"\n", args...)
}

func (a *App) configPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if p := a.Getenv(envConfigPath); p != "" {
		return p
	}
	return config.DefaultPath
}

// loadConfig carica la configurazione e traduce l'errore in codice di uscita.
func (a *App) loadConfig(path string) (*config.Config, int) {
	cfg, err := config.Load(path)
	if err == nil {
		return cfg, ExitOK
	}
	switch {
	case errors.Is(err, config.ErrPermission):
		a.errorf("%v. Il file è root-only: rilancia con sudo.", err)
		return nil, ExitNeedsRoot
	case errors.Is(err, config.ErrNotFound):
		a.errorf("%v. GitStack non risulta installato con deploy/install.sh (che scrive il file): rilancia l'installer.", err)
	default:
		a.errorf("%v", err)
	}
	return nil, ExitConfig
}

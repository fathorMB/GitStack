// Package db è il gruppo `gs db` (GIT-556, v3-d): i database PostgreSQL gestiti di un owner. create, list, view,
// resize, delete; link e unlink con le app; backup list|run|restore; accesso dalla LAN; rotazione delle credenziali;
// connect con psql. Stessa API della UI (DB1-DB24), con --json e codici di uscita G3.
package db

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs db`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "db",
		Aliases: []string{"database"},
		Short:   "Gestisci i database PostgreSQL gestiti",
		Long: "Database PostgreSQL di un owner (utente o organizzazione), collegabili alle app dello stesso owner. " +
			"Un database si indica con `owner/nome`, oppure con il solo nome e --owner (o l'owner di --repo). " +
			"Le operazioni richiedono `admin` sull'owner; le password si vedono solo alla creazione o alla rotazione. " +
			"Se l'amministratore non ha acceso la funzione, l'API risponde `databases_disabled`.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newListCmd(f), newCreateCmd(f), newViewCmd(f), newResizeCmd(f), newDeleteCmd(f),
		newLinkCmd(f), newUnlinkCmd(f), newBackupCmd(f), newAccessCmd(f), newCredentialsCmd(f), newConnectCmd(f))
	return cmd
}

// env è quello che un sottocomando usa: client generato e istanza.
type env struct {
	f   *cmdutil.Factory
	gen *gitstack.ClientWithResponses
}

func newEnv(f *cmdutil.Factory) (*env, error) {
	host, err := f.Host()
	if err != nil {
		return nil, err
	}
	tok, err := f.Token(host)
	if err != nil {
		return nil, err
	}
	cl, err := api.New(api.Options{Host: host, Token: tok, Version: f.Version, HTTP: f.HTTPClient()})
	if err != nil {
		return nil, err
	}
	return &env{f: f, gen: cl.Generated()}, nil
}

func (e *env) check(status int, body []byte) error { return api.CheckStatus(status, body) }

func unexpected(status int) error {
	return fmt.Errorf("risposta inattesa dal gateway (HTTP %d)", status)
}

// ownerFlag è il flag --owner, comune ai comandi che nominano un database o un elenco.
type ownerFlag struct{ v string }

func (o *ownerFlag) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.v, "owner", "", "Owner del database (utente o organizzazione); predefinito: l'owner di --repo")
}

// owner risolve l'owner: --owner, altrimenti quello del repo (-R o remote origin).
func (o *ownerFlag) owner(f *cmdutil.Factory) (string, error) {
	if s := strings.TrimSpace(o.v); s != "" {
		return s, nil
	}
	if ow, _, err := f.BaseRepo(); err == nil && ow != "" {
		return ow, nil
	}
	return "", cmdutil.UsageErrorf("indica l'owner: usa owner/nome, --owner o --repo owner/repo")
}

// dbRef risolve `owner/nome` o `nome`.
func (o *ownerFlag) dbRef(f *cmdutil.Factory, arg string) (owner, name string, err error) {
	arg = strings.TrimSpace(arg)
	if i := strings.Index(arg, "/"); i >= 0 {
		owner, name = arg[:i], arg[i+1:]
		if owner == "" || name == "" || strings.Contains(name, "/") {
			return "", "", cmdutil.UsageErrorf("%q non è un database valido: usa owner/nome", arg)
		}
		return owner, strings.ToLower(name), nil
	}
	if arg == "" {
		return "", "", cmdutil.UsageErrorf("il nome del database non può essere vuoto")
	}
	owner, err = o.owner(f)
	if err != nil {
		return "", "", err
	}
	return owner, strings.ToLower(arg), nil
}

// parseSize legge 512MB, 10GB, 1.5GiB o un numero di byte; le unità sono binarie (1 GB = 1024 MiB), come la UI.
func parseSize(flag, s string) (int64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"gib", 1 << 30}, {"mib", 1 << 20}, {"kib", 1 << 10}, {"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}, {"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10}, {"b", 1}} {
		if strings.HasSuffix(t, u.suffix) {
			mult = u.mult
			t = strings.TrimSpace(strings.TrimSuffix(t, u.suffix))
			break
		}
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil || v <= 0 {
		return 0, cmdutil.UsageErrorf("%s: la dimensione %q non è valida: usa per esempio 512MB o 2GB", flag, s)
	}
	return int64(v * float64(mult)), nil
}

// parseCPU legge 500m (millesimi di vCPU) o 0.5 / 2 (vCPU).
func parseCPU(s string) (int, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if strings.HasSuffix(t, "m") {
		n, err := strconv.Atoi(strings.TrimSuffix(t, "m"))
		if err != nil || n < 1 {
			return 0, cmdutil.UsageErrorf("--cpu: %q non è valida: usa per esempio 500m o 0.5", s)
		}
		return n, nil
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil || v <= 0 {
		return 0, cmdutil.UsageErrorf("--cpu: %q non è valida: usa per esempio 500m o 0.5", s)
	}
	n := int(v*1000 + 0.5)
	if n < 1 {
		return 0, cmdutil.UsageErrorf("--cpu: %q è troppo piccola", s)
	}
	return n, nil
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GB", n>>30)
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d B", n)
}

func humanCPU(m int) string {
	if m%1000 == 0 {
		return strconv.Itoa(m / 1000)
	}
	return fmt.Sprintf("%dm", m)
}

func day(t time.Time) string { return t.Local().Format(time.DateOnly) }

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// apiCode è il codice di errore dell'API, vuoto se l'errore non viene dall'API.
func apiCode(err error) string {
	var ae *cmdutil.APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

// maxOneArg accetta zero o un argomento, con un UsageError (exit 2) altrimenti.
func maxOneArg(c *cobra.Command, args []string) error {
	if len(args) > 1 {
		return cmdutil.UsageErrorf("al massimo 1 argomento per %q, ricevuto %d", c.CommandPath(), len(args))
	}
	return nil
}

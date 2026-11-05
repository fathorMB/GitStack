package db

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// LanFields sono i campi di --json di `gs db access lan` (schema DatabaseLan).
var LanFields = []string{"enabled", "port", "host", "caCertificate", "ufwCommand", "connections"}

func newAccessCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Accesso al database dall'esterno",
		Args:  cmdutil.NoArgs,
		RunE:  cmdutil.GroupRun,
	}
	cmd.AddCommand(newLanCmd(f))
	return cmd
}

func newLanCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo output.JSONOptions
		ow ownerFlag
	)
	cmd := &cobra.Command{
		Use:   "lan <db> [on|off]",
		Short: "Legge, accende o spegne l'accesso dalla LAN",
		Long: "Senza on/off mostra lo stato. Acceso, il database ascolta su una porta TCP dell'intervallo configurato, solo TLS " +
			"(scram-sha-256, sslmode=verify-full con la CA): il comando mostra indirizzo, porta e il comando `ufw` da eseguire " +
			"sull'host, perché GitStack non tocca il firewall. `port_unavailable` se la porta è già usata. Spegnendo, se Kubernetes " +
			"non riesce a togliere Service e policy l'API risponde 503 `lan_close_failed`: la chiusura è pendente, la porta resta " +
			"riservata e il comando esce con 1; ripeti `off` per completarla. Serve `admin` sull'owner.",
		Example: "  gs db access lan acme/shop\n  gs db access lan acme/shop on\n  gs db access lan shop off --owner acme",
		Args: func(c *cobra.Command, args []string) error {
			if len(args) < 1 || len(args) > 2 {
				return cmdutil.UsageErrorf("servono <db> e, per cambiare, on oppure off")
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			owner, name, err := ow.dbRef(f, args[0])
			if err != nil {
				return err
			}
			set, want := false, false
			if len(args) == 2 {
				switch strings.ToLower(args[1]) {
				case "on":
					set, want = true, true
				case "off":
					set = true
				default:
					return cmdutil.UsageErrorf("%q non è valido: usa on oppure off", args[1])
				}
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			var lan *gitstack.DatabaseLan
			if set {
				resp, err := e.gen.SetDatabaseLanWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name, gitstack.DatabaseLanInput{Enabled: want})
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					if apiCode(err) == "lan_close_failed" {
						return &cmdutil.ExitError{Code: cmdutil.ExitGeneric, ErrCode: "lan_close_failed", Err: fmt.Errorf(
							"la LAN di %s/%s risulta spenta ma la chiusura è pendente: Service o NetworkPolicy sono ancora presenti e la porta resta riservata. Ripeti `gs db access lan %s/%s off` per completarla", owner, name, owner, name)}
					}
					return err
				}
				lan = resp.JSON200
			} else {
				resp, err := e.gen.GetDatabaseLanWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name)
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				lan = resp.JSON200
			}
			if lan == nil {
				return unexpected(200)
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, lan, f.IO.OutTTY)
			}
			printLan(f, *lan)
			return nil
		},
	}
	ow.add(cmd)
	output.AddJSONFlags(cmd, &jo, LanFields)
	return cmd
}

func printLan(f *cmdutil.Factory, l gitstack.DatabaseLan) {
	out := f.IO.Out
	if !l.Enabled {
		_, _ = fmt.Fprintln(out, "lan: off")
		return
	}
	_, _ = fmt.Fprintln(out, "lan: on")
	if l.Host != nil && l.Port != nil {
		_, _ = fmt.Fprintf(out, "indirizzo: %s:%d\n", *l.Host, *l.Port)
	}
	if l.Connections != nil {
		_, _ = fmt.Fprintf(out, "connessioni: %d\n", *l.Connections)
	}
	if l.UfwCommand != nil {
		_, _ = fmt.Fprintf(out, "firewall (da eseguire sull'host): %s\n", *l.UfwCommand)
	}
	_, _ = fmt.Fprintln(out, "solo TLS: sslmode=verify-full con la CA (gs db connect la usa da sola)")
}

func newCredentialsCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "credentials",
		Aliases: []string{"creds"},
		Short:   "Credenziali del proprietario del database",
		Args:    cmdutil.NoArgs,
		RunE:    cmdutil.GroupRun,
	}
	cmd.AddCommand(newRotateCmd(f))
	return cmd
}

func newRotateCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo output.JSONOptions
		ow ownerFlag
	)
	cmd := &cobra.Command{
		Use:   "rotate <db>",
		Short: "Ruota la password dell'utente proprietario",
		Long: "Genera una nuova password per `<nome>_owner` e la mostra una sola volta: non si rilegge. Le connessioni aperte con " +
			"la vecchia password restano valide finché non si chiudono. Le app collegate usano il proprio ruolo e non cambiano " +
			"(per quelle serve ruotare il collegamento). Serve `admin` sull'owner.",
		Example: "  gs db credentials rotate acme/shop",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			owner, name, err := ow.dbRef(f, args[0])
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.RotateDatabaseCredentialsWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, resp.JSON200, f.IO.OutTTY)
			}
			_, _ = fmt.Fprintf(f.IO.Out, "ruotata la password di %s/%s\n", owner, name)
			printCredentials(f, resp.JSON200.Credentials)
			return nil
		},
	}
	ow.add(cmd)
	output.AddJSONFlags(cmd, &jo, CredentialsResultFields)
	return cmd
}

// psqlRunner è il punto dove `gs db connect` lancia psql: i test lo sostituiscono.
type psqlRunner struct {
	lookPath func(string) (string, error)
	run      func(path string, args, env []string, f *cmdutil.Factory) error
}

var psql = psqlRunner{
	lookPath: exec.LookPath,
	run: func(path string, args, env []string, f *cmdutil.Factory) error {
		cmd := exec.Command(path, args...)
		cmd.Env = env
		cmd.Stdin, cmd.Stdout, cmd.Stderr = f.IO.In, f.IO.Out, f.IO.ErrOut
		return cmd.Run()
	},
}

func newConnectCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		ow           ownerFlag
		user, dbname string
		caOut        string
	)
	cmd := &cobra.Command{
		Use:   "connect <db> [-- argomenti di psql]",
		Short: "Apri psql sul database dalla LAN, con TLS e la CA",
		Long: "Legge indirizzo, porta e CA pubblica dalla LAN del database (deve essere accesa: `gs db access lan <db> on`) e lancia " +
			"psql con sslmode=verify-full e la CA salvata in un file temporaneo. La password non passa da gs: psql la chiede, o " +
			"leggila da PGPASSWORD. L'utente predefinito è `<nome>_owner` e il database `<nome>`. Serve psql nel PATH: se manca il " +
			"comando esce con 1 e scrive i parametri di connessione. Con --ca-out la CA resta nel file indicato.",
		Example: "  gs db connect acme/shop\n  gs db connect acme/shop -- -c 'select 1'",
		Args:    cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) < 1 {
				return cmdutil.UsageErrorf("serve <db>")
			}
			owner, name, err := ow.dbRef(f, args[0])
			if err != nil {
				return err
			}
			extra := args[1:]
			if len(extra) > 0 && c.ArgsLenAtDash() != 1 {
				return cmdutil.UsageErrorf("gli argomenti di psql vanno dopo `--`")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.GetDatabaseLanWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			l := resp.JSON200
			if l == nil {
				return unexpected(resp.StatusCode())
			}
			if !l.Enabled || l.Host == nil || l.Port == nil || l.CaCertificate == nil {
				return &cmdutil.ExitError{Code: cmdutil.ExitGeneric, ErrCode: "lan_disabled", Err: fmt.Errorf(
					"la LAN di %s/%s è spenta: accendila con `gs db access lan %s/%s on`", owner, name, owner, name)}
			}
			if user == "" {
				user = name + "_owner"
			}
			if dbname == "" {
				dbname = name
			}
			caPath := caOut
			if caPath == "" {
				d, err := os.MkdirTemp("", "gs-db-ca-")
				if err != nil {
					return err
				}
				defer func() { _ = os.RemoveAll(d) }()
				caPath = filepath.Join(d, "ca.pem")
			}
			if err := os.WriteFile(caPath, []byte(*l.CaCertificate), 0o600); err != nil {
				return err
			}
			conn := fmt.Sprintf("host=%s port=%d dbname=%s user=%s sslmode=verify-full sslrootcert=%s", *l.Host, *l.Port, dbname, user, caPath)
			path, err := psql.lookPath("psql")
			if err != nil {
				if caOut != "" {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "CA salvata in %s\n", caOut)
				}
				return &cmdutil.ExitError{Code: cmdutil.ExitGeneric, ErrCode: "psql_not_found", Err: fmt.Errorf(
					"psql non è nel PATH: installa il client PostgreSQL oppure connettiti con altri strumenti a %s:%d (utente %s, database %s, sslmode=verify-full, CA con --ca-out)", *l.Host, *l.Port, user, dbname)}
			}
			if err := psql.run(path, append([]string{conn}, extra...), os.Environ(), f); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					return &cmdutil.ExitError{Code: ee.ExitCode(), ErrCode: "psql_failed", Err: fmt.Errorf("psql è uscito con codice %d", ee.ExitCode())}
				}
				return err
			}
			return nil
		},
	}
	ow.add(cmd)
	cmd.Flags().StringVar(&user, "user", "", "Utente (predefinito: <nome>_owner)")
	cmd.Flags().StringVar(&dbname, "dbname", "", "Database PostgreSQL (predefinito: il nome)")
	cmd.Flags().StringVar(&caOut, "ca-out", "", "Salva la CA in questo file invece che in uno temporaneo")
	return cmd
}

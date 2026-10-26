package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// EnvFields sono i campi di --json di `gs app env list` (schema AppVariable).
var EnvFields = []string{"name", "value", "managed", "updatedAt", "updatedBy", "version"}

// SecretFields sono i campi di --json di `gs app secret list` (schema AppSecret: mai i valori, H8).
var SecretFields = []string{"name", "updatedAt", "updatedBy", "version"}

func newEnvCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "env",
		Aliases: []string{"vars"},
		Short:   "Gestisci le variabili d'ambiente dell'app",
		Long: "Le variabili sono visibili a chi ha `write`; crearle, cambiarle o toglierle richiede `admin`. Ogni modifica " +
			"crea una nuova versione e riavvia le copie una alla volta (H8). Quelle impostate da GitStack sono in sola lettura.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newEnvListCmd(f), newEnvSetCmd(f), newEnvUnsetCmd(f))
	return cmd
}

func newEnvListCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "list <app>",
		Aliases: []string{"ls"},
		Short:   "Elenca le variabili dell'app",
		Example: "  gs app env list web --repo acme/handbook",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.ListAppVariablesWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app))
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
				return jo.Write(f.IO.Out, resp.JSON200.Items, f.IO.OutTTY)
			}
			for _, v := range resp.JSON200.Items {
				note := ""
				if v.Managed {
					note = "\t(gestita da GitStack)"
				}
				_, _ = fmt.Fprintf(f.IO.Out, "%s=%s%s\n", v.Name, v.Value, note)
			}
			return nil
		},
	}
	output.AddJSONFlags(cmd, &jo, EnvFields)
	return cmd
}

func newEnvSetCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "set <app> NOME=valore...",
		Short: "Crea o sostituisce variabili",
		Long: "Crea o sostituisce una o più variabili. Ogni variabile crea una nuova versione e riavvia le copie. Serve " +
			"`admin` sul repo. Per i valori riservati usa `gs app secret set`.",
		Example: "  gs app env set web SEARCH_INDEX=handbook-v2 --repo acme/handbook",
		Args:    cmdutil.MinArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			type pair struct{ name, value string }
			var pairs []pair
			for _, a := range args[1:] {
				k, v, ok := strings.Cut(a, "=")
				if !ok || strings.TrimSpace(k) == "" {
					return cmdutil.UsageErrorf("%q non è nella forma NOME=valore", a)
				}
				pairs = append(pairs, pair{strings.TrimSpace(k), v})
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			var saved []gitstack.AppVariable
			for _, p := range pairs {
				resp, err := e.gen.PutAppVariableWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), p.name, gitstack.AppVariableInput{Value: p.value})
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return unexpected(resp.StatusCode())
				}
				saved = append(saved, *resp.JSON200)
				if !jo.Enabled() {
					ver := ""
					if resp.JSON200.Version != nil {
						ver = fmt.Sprintf(" (v%d)", *resp.JSON200.Version)
					}
					_, _ = fmt.Fprintf(f.IO.Out, "set %s%s\n", p.name, ver)
				}
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, saved, f.IO.OutTTY)
			}
			return nil
		},
	}
	output.AddJSONFlags(cmd, &jo, EnvFields)
	return cmd
}

func newEnvUnsetCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "unset <app> NOME...",
		Aliases: []string{"rm"},
		Short:   "Elimina variabili",
		Long: "Elimina una o più variabili e crea una nuova versione. Chiede la conferma (G8): senza terminale serve --yes. " +
			"Serve `admin` sul repo.",
		Example: "  gs app env unset web SEARCH_INDEX --yes",
		Args:    cmdutil.MinArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			names := args[1:]
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Eliminare %s da %s. Riscrivi `%s` per confermare:", strings.Join(names, ", "), app, app)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, app); err != nil {
				return err
			}
			for _, n := range names {
				resp, err := e.gen.DeleteAppVariableWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), n)
				if err != nil {
					return err
				}
				if err := e.check(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(f.IO.Out, "unset %s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}

func newSecretCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Gestisci i segreti dell'app",
		Long: "I segreti sono cifrati e non si rileggono mai: `list` mostra solo i nomi. Servono i permessi `admin` sul repo. " +
			"Sono separati dai segreti della CI.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newSecretListCmd(f), newSecretSetCmd(f), newSecretRmCmd(f))
	return cmd
}

func newSecretListCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "list <app>",
		Aliases: []string{"ls"},
		Short:   "Elenca i nomi dei segreti (mai i valori)",
		Example: "  gs app secret list web --repo acme/handbook",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.ListAppSecretsWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app))
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
				return jo.Write(f.IO.Out, resp.JSON200.Items, f.IO.OutTTY)
			}
			for _, s := range resp.JSON200.Items {
				_, _ = fmt.Fprintln(f.IO.Out, s.Name)
			}
			return nil
		},
	}
	output.AddJSONFlags(cmd, &jo, SecretFields)
	return cmd
}

func newSecretSetCmd(f *cmdutil.Factory) *cobra.Command {
	var value string
	cmd := &cobra.Command{
		Use:   "set <app> <NOME>",
		Short: "Crea o sostituisce un segreto",
		Long: "Il valore si legge dallo standard input (consigliato: non resta nella cronologia della shell) oppure da " +
			"--value. Crea una nuova versione. Il valore non torna mai nelle risposte e nei log è mascherato.",
		Example: "  printf %s \"$DB_PASSWORD\" | gs app secret set web DB_PASSWORD --repo acme/handbook",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			name := strings.TrimSpace(args[1])
			if name == "" {
				return cmdutil.UsageErrorf("il nome non può essere vuoto")
			}
			val := value
			if !c.Flags().Changed("value") {
				if f.IO.InTTY {
					return cmdutil.UsageErrorf("passa il valore dallo standard input o con --value")
				}
				b, err := io.ReadAll(io.LimitReader(f.IO.In, 1<<20))
				if err != nil {
					return err
				}
				val = strings.TrimRight(string(b), "\r\n")
			}
			if val == "" {
				return cmdutil.UsageErrorf("il valore del segreto non può essere vuoto")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.PutAppSecretWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), name, gitstack.AppSecretInput{Value: &val})
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.Out, "set secret %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&value, "value", "", "Valore del segreto (altrimenti dallo standard input)")
	return cmd
}

func newSecretRmCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <app> <NOME>",
		Aliases: []string{"delete"},
		Short:   "Elimina un segreto",
		Long:    "Elimina il segreto e crea una nuova versione. Chiede la conferma (G8): senza terminale serve --yes.",
		Example: "  gs app secret rm web DB_PASSWORD --yes",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			name := strings.TrimSpace(args[1])
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Eliminare il segreto %s da %s. Riscrivi `%s` per confermare:", name, app, name)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, name); err != nil {
				return err
			}
			resp, err := e.gen.DeleteAppSecretWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), name)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.Out, "removed secret %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}

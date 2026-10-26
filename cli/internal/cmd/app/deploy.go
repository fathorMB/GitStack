package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// pollInterval è ogni quanto `--wait` interroga la versione; i test lo accorciano.
var pollInterval = 2 * time.Second

// VersionFields sono i campi di --json di deploy, rollback e restart.
var VersionFields = []string{"number", "state", "stateReason", "trigger", "image", "createdBy", "createdAt", "rollbackOf", "summary"}

func (e *env) version(ctx context.Context, app string, n int) (*gitstack.AppVersion, error) {
	resp, err := e.gen.GetAppVersionWithResponse(ctx, gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), gitstack.AppVersionParam(n))
	if err != nil {
		return nil, err
	}
	if err := e.check(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, unexpected(resp.StatusCode())
	}
	return resp.JSON200, nil
}

// failed è l'errore di un deploy che non è andato a buon fine: exit 1 (G3), la riga ✗ è già su stdout.
func failed(format string, args ...any) error {
	return &cmdutil.ExitError{Code: cmdutil.ExitGeneric, ErrCode: "deploy_failed", Err: fmt.Errorf(format, args...)}
}

// wait aspetta che la versione smetta di essere in corso e stampa l'esito come nel mockup H10. Restituisce
// l'errore di uscita 1 se la versione non serve il traffico.
func (e *env) wait(ctx context.Context, app string, v gitstack.AppVersion, timeout time.Duration, quiet bool) (*gitstack.AppVersion, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cur := v
	for cur.State == gitstack.AppDeployStateInProgress {
		select {
		case <-ctx.Done():
			if !quiet {
				_, _ = fmt.Fprintf(e.f.IO.Out, "✗ v%d ancora in corso dopo %s\n", v.Number, timeout)
			}
			return &cur, failed("timeout: v%d non ha finito il deploy entro %s", v.Number, timeout)
		case <-time.After(pollInterval):
		}
		n, err := e.version(ctx, app, v.Number)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			return &cur, err
		}
		cur = *n
	}
	out := e.f.IO.Out
	switch cur.State {
	case gitstack.AppDeployStateServing:
		a, err := e.getApp(ctx, app)
		if err != nil {
			return &cur, err
		}
		if !quiet {
			_, _ = fmt.Fprintf(out, "✓ v%d healthy · %d/%d copies\n", cur.Number, a.ReadyCopies, a.Copies)
		}
		return &cur, nil
	case gitstack.AppDeployStateRolledBack:
		a, err := e.getApp(context.WithoutCancel(ctx), app)
		prev := ""
		if err == nil {
			prev = fmt.Sprintf(" · rolled back to v%d", a.ActiveVersion)
		}
		if !quiet {
			_, _ = fmt.Fprintf(out, "✗ %s%s\n", reason(cur), prev)
		}
		return &cur, failed("v%d: %s%s", cur.Number, reason(cur), prev)
	default:
		if !quiet {
			_, _ = fmt.Fprintf(out, "✗ v%d %s: %s\n", cur.Number, cur.State, reason(cur))
		}
		return &cur, failed("v%d %s: %s", cur.Number, cur.State, reason(cur))
	}
}

func reason(v gitstack.AppVersion) string {
	if v.StateReason != nil && strings.TrimSpace(*v.StateReason) != "" {
		return *v.StateReason
	}
	return "health check failed"
}

// announce stampa la riga d'avvio del mockup: «deploying v43 to web (2 copies) · image signed by …».
func announce(f *cmdutil.Factory, verb, app string, v gitstack.AppVersion, copies int) {
	_, _ = fmt.Fprintf(f.IO.Out, "%s v%d to %s (%d %s) · image %s\n", verb, v.Number, app, copies, plural(copies, "copy", "copies"), trustEN(v.Image))
}

func newDeployCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		image   string
		doWait  bool
		timeout time.Duration
		jo      output.JSONOptions
	)
	cmd := &cobra.Command{
		Use:   "deploy <app>",
		Short: "Mette in produzione un'immagine (nuova versione)",
		Long: "Crea una nuova versione con l'immagine indicata (un tag del registry del repo, `nome:tag`, un digest o un " +
			"riferimento esterno) e la avvia. Con --wait aspetta il controllo di salute: esce 0 se la versione serve il " +
			"traffico, 1 se il deploy fallisce (con il ritorno alla versione precedente, H12). Serve `write` sul repo; " +
			"un agente lo fa con la sua identità, che compare nello storico delle versioni.",
		Example: "  gs app deploy web --repo acme/handbook --image sha-91be20d --wait\n  gs app deploy web --image sha-91be20d --wait --json number,state",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			if strings.TrimSpace(image) == "" {
				return cmdutil.UsageErrorf("--image è obbligatoria")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.DeployAppWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), gitstack.AppDeployInput{Image: image})
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return unexpected(resp.StatusCode())
			}
			v := *resp.JSON202
			quiet := jo.Enabled()
			if !quiet {
				copies := 1
				if v.Config != nil {
					copies = v.Config.Copies
				}
				announce(f, "deploying", app, v, copies)
			}
			var werr error
			if doWait {
				var n *gitstack.AppVersion
				n, werr = e.wait(c.Context(), app, v, timeout, quiet)
				if n != nil {
					v = *n
				}
			}
			if quiet {
				if err := jo.Write(f.IO.Out, v, f.IO.OutTTY); err != nil {
					return err
				}
			}
			return werr
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&image, "image", "", "Immagine da mettere in produzione")
	fl.BoolVar(&doWait, "wait", false, "Aspetta il controllo di salute (exit 1 se il deploy fallisce)")
	fl.DurationVar(&timeout, "timeout", 10*time.Minute, "Quanto aspettare con --wait")
	output.AddJSONFlags(cmd, &jo, VersionFields)
	return cmd
}

func newRestartCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "restart <app>",
		Short:   "Riavvia le copie dell'app, una alla volta",
		Long:    "Riavvia le copie una alla volta (senza interruzione con due o più copie). Non crea una versione. Serve `write` sul repo.",
		Example: "  gs app restart web --repo acme/handbook",
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
			resp, err := e.gen.RestartAppWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app))
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return unexpected(resp.StatusCode())
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, toRow(*resp.JSON202), f.IO.OutTTY)
			}
			_, _ = fmt.Fprintf(f.IO.Out, "restarting %s (%d %s)\n", app, resp.JSON202.Copies, plural(resp.JSON202.Copies, "copy", "copies"))
			return nil
		},
	}
	output.AddJSONFlags(cmd, &jo, ListFields)
	return cmd
}

func newRollbackCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		to      int
		pause   bool
		doWait  bool
		waitFor time.Duration
		jo      output.JSONOptions
	)
	cmd := &cobra.Command{
		Use:   "rollback <app> --to <versione>",
		Short: "Torna a una versione precedente",
		Long: "Crea una nuova versione con l'immagine e la configurazione della versione scelta (ripristina immagine, " +
			"variabili e nomi dei segreti, mai i loro valori storici). --pause-auto-deploy sospende i deploy automatici " +
			"dal branch seguito, altrimenti la prossima immagine della CI sostituisce il ritorno. Serve `write` sul repo.",
		Example: "  gs app rollback web --to 42 --repo acme/handbook\n  gs app rollback web --to 42 --pause-auto-deploy --wait",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			if !c.Flags().Changed("to") || to < 1 {
				return cmdutil.UsageErrorf("--to <versione> è obbligatoria")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			in := gitstack.AppRollbackInput{Version: to}
			if pause {
				in.PauseAutoDeploy = &pause
			}
			resp, err := e.gen.RollbackAppWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), in)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON202 == nil {
				return unexpected(resp.StatusCode())
			}
			v := *resp.JSON202
			quiet := jo.Enabled()
			if !quiet {
				_, _ = fmt.Fprintf(f.IO.Out, "rolling %s back to v%d as v%d\n", app, to, v.Number)
			}
			var werr error
			if doWait {
				var n *gitstack.AppVersion
				n, werr = e.wait(c.Context(), app, v, waitFor, quiet)
				if n != nil {
					v = *n
				}
			}
			if quiet {
				if err := jo.Write(f.IO.Out, v, f.IO.OutTTY); err != nil {
					return err
				}
			}
			return werr
		},
	}
	fl := cmd.Flags()
	fl.IntVar(&to, "to", 0, "Versione da ripristinare come nuova versione")
	fl.BoolVar(&pause, "pause-auto-deploy", false, "Sospende il deploy automatico dal branch seguito")
	fl.BoolVar(&doWait, "wait", false, "Aspetta il controllo di salute (exit 1 se fallisce)")
	fl.DurationVar(&waitFor, "timeout", 10*time.Minute, "Quanto aspettare con --wait")
	output.AddJSONFlags(cmd, &jo, VersionFields)
	return cmd
}

func newScaleCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		copies int
		yes    bool
		jo     output.JSONOptions
	)
	cmd := &cobra.Command{
		Use:   "scale <app> --copies <n>",
		Short: "Imposta il numero di copie",
		Long: "Scala a mano, entro la quota dell'owner (niente scala automatica). Con --copies 0 ferma l'app: chiede la " +
			"conferma (G8), o --yes senza terminale. Non crea una versione. Serve `write` sul repo.",
		Example: "  gs app scale web --copies 3 --repo acme/handbook\n  gs app scale web --copies 0 --yes",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			if !c.Flags().Changed("copies") || copies < 0 {
				return cmdutil.UsageErrorf("--copies <n> è obbligatoria (0 o più)")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if copies == 0 {
				prompt := fmt.Sprintf("Fermare %s (0 copie). Riscrivi `%s` per confermare:", app, app)
				if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, app); err != nil {
					return err
				}
			}
			resp, err := e.gen.ScaleAppWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), gitstack.AppScaleInput{Copies: copies})
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				var ae *cmdutil.APIError
				if errors.As(err, &ae) && ae.Code == "insufficient_capacity" && !jo.Enabled() {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "! %s\n", ae.Message)
				}
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, toRow(*resp.JSON200), f.IO.OutTTY)
			}
			_, _ = fmt.Fprintf(f.IO.Out, "%s: %d %s\n", app, resp.JSON200.Copies, plural(resp.JSON200.Copies, "copy", "copies"))
			return nil
		},
	}
	fl := cmd.Flags()
	fl.IntVar(&copies, "copies", 0, "Numero di copie (0 ferma l'app)")
	fl.BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	output.AddJSONFlags(cmd, &jo, ListFields)
	return cmd
}

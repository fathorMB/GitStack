// Package ci implementa i comandi CLI per workflow ed esecuzioni CI.
package ci

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	"github.com/fathorMB/GitStack/cli/internal/repoenv"
	gitstack "github.com/fathorMB/GitStack/client/go"
)

var workflowFields = []string{"id", "name", "path", "state"}
var runFields = []string{"id", "workflowId", "status", "conclusion", "event", "ref", "sha", "createdAt", "startedAt", "completedAt", "jobs"}

func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "run", Short: "Segui le esecuzioni CI", Args: cmdutil.NoArgs, RunE: cmdutil.GroupRun}
	cmd.AddCommand(newListRun(f), newViewRun(f), newWatchRun(f), newRerun(f), newCancel(f))
	return cmd
}

func NewWorkflowCmd(f *cmdutil.Factory) *cobra.Command {
	wf := &cobra.Command{Use: "workflow", Short: "Gestisci i workflow CI", Args: cmdutil.NoArgs, RunE: cmdutil.GroupRun}
	wf.AddCommand(newListWorkflow(f), newDispatchWorkflow(f))
	return wf
}

func newListRun(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	c := &cobra.Command{Use: "list", Short: "Elenca le esecuzioni CI", Args: cmdutil.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		e, err := repoenv.New(f)
		if err != nil {
			return err
		}
		r, err := e.Gen.ListWorkflowRunsWithResponse(c.Context(), e.Owner, e.Repo)
		if err != nil {
			return err
		}
		if err := repoenv.Check(r.StatusCode(), r.Body); err != nil {
			return err
		}
		if r.JSON200 == nil {
			return repoenv.Unexpected(r.StatusCode())
		}
		items := r.JSON200.Items
		if items == nil {
			items = []gitstack.WorkflowRun{}
		}
		if jo.Enabled() {
			return jo.Write(f.IO.Out, items, f.IO.OutTTY)
		}
		for _, x := range items {
			_, _ = fmt.Fprintf(f.IO.Out, "%s\t%s\t%s\t%s\n", x.Id, x.Status, conclusion(&x), x.WorkflowId)
		}
		return nil
	}}
	output.AddJSONFlags(c, &jo, runFields)
	return c
}

func newViewRun(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	var logs bool
	c := &cobra.Command{Use: "view <id>", Short: "Mostra un'esecuzione CI", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		e, err := repoenv.New(f)
		if err != nil {
			return err
		}
		id, err := runID(args[0])
		if err != nil {
			return err
		}
		r, err := e.Gen.GetWorkflowRunWithResponse(c.Context(), e.Owner, e.Repo, id)
		if err != nil {
			return err
		}
		if err := repoenv.Check(r.StatusCode(), r.Body); err != nil {
			return err
		}
		if r.JSON200 == nil {
			return repoenv.Unexpected(r.StatusCode())
		}
		if logs {
			logOut := f.IO.Out
			if jo.Enabled() {
				logOut = f.IO.ErrOut
			}
			if err := failedLogs(c, e, r.JSON200, logOut); err != nil {
				return err
			}
		}
		if jo.Enabled() {
			return jo.Write(f.IO.Out, r.JSON200, f.IO.OutTTY)
		}
		_, _ = fmt.Fprintf(f.IO.Out, "%s\t%s\t%s\t%s\n", r.JSON200.Id, r.JSON200.Status, conclusion(r.JSON200), r.JSON200.Ref)
		return nil
	}}
	c.Flags().BoolVar(&logs, "log-failed", false, "Stampa i log dei job falliti")
	output.AddJSONFlags(c, &jo, runFields)
	return c
}

func newWatchRun(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	var logs, exitStatus bool
	var interval time.Duration
	c := &cobra.Command{Use: "watch <id>", Short: "Attende la fine di un'esecuzione CI", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if interval <= 0 {
			return cmdutil.UsageErrorf("--interval deve essere maggiore di zero")
		}
		id, err := runID(args[0])
		if err != nil {
			return err
		}
		e, err := repoenv.New(f)
		if err != nil {
			return err
		}
		var r *gitstack.WorkflowRun
		for {
			resp, err := e.Gen.GetWorkflowRunWithResponse(c.Context(), e.Owner, e.Repo, id)
			if err != nil {
				return err
			}
			if err := repoenv.Check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return repoenv.Unexpected(resp.StatusCode())
			}
			r = resp.JSON200
			if r.Status == "completed" || r.Status == "cancelled" {
				break
			}
			timer := time.NewTimer(interval)
			select {
			case <-c.Context().Done():
				timer.Stop()
				return c.Context().Err()
			case <-timer.C:
			}
		}
		if logs {
			logOut := f.IO.Out
			if jo.Enabled() {
				logOut = f.IO.ErrOut
			}
			if err := failedLogs(c, e, r, logOut); err != nil {
				return err
			}
		}
		if jo.Enabled() {
			if err := jo.Write(f.IO.Out, r, f.IO.OutTTY); err != nil {
				return err
			}
		} else {
			_, _ = fmt.Fprintf(f.IO.Out, "%s\t%s\t%s\n", r.Id, r.Status, conclusion(r))
		}
		if exitStatus && conclusion(r) != "success" {
			return fmt.Errorf("esecuzione conclusa con %s", conclusion(r))
		}
		return nil
	}}
	c.Flags().BoolVar(&logs, "log-failed", false, "Stampa i log dei job falliti")
	c.Flags().BoolVar(&exitStatus, "exit-status", false, "Esce con codice 1 se l'esecuzione non ha successo")
	c.Flags().DurationVar(&interval, "interval", 2*time.Second, "Intervallo di polling")
	output.AddJSONFlags(c, &jo, runFields)
	return c
}

func newRerun(f *cmdutil.Factory) *cobra.Command {
	return runMutation(f, "rerun", "Riesegue un workflow", false)
}
func newCancel(f *cmdutil.Factory) *cobra.Command {
	return runMutation(f, "cancel", "Annulla un'esecuzione CI", true)
}

func runMutation(f *cmdutil.Factory, action, short string, cancel bool) *cobra.Command {
	var jo output.JSONOptions
	var yes bool
	c := &cobra.Command{Use: action + " <id>", Short: short, Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		id, err := runID(args[0])
		if err != nil {
			return err
		}
		e, err := repoenv.New(f)
		if err != nil {
			return err
		}
		var status int
		var body []byte
		if cancel {
			// G8: annullare un'esecuzione fa perdere il lavoro in corso; la conferma va prima di ogni chiamata HTTP.
			prompt := fmt.Sprintf("Stai per annullare l'esecuzione %s di %s/%s: il lavoro in corso va perso.", id, e.Owner, e.Repo)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, ""); err != nil {
				return err
			}
			r, er := e.Gen.CancelWorkflowRunWithResponse(c.Context(), e.Owner, e.Repo, id)
			if er != nil {
				return er
			}
			status, body = r.StatusCode(), r.Body
			if err := repoenv.Check(status, body); err != nil {
				return err
			}
			if r.JSON202 == nil {
				return repoenv.Unexpected(status)
			}
			return writeRun(f, &jo, r.JSON202)
		}
		r, er := e.Gen.RerunWorkflowRunWithResponse(c.Context(), e.Owner, e.Repo, id)
		if er != nil {
			return er
		}
		status, body = r.StatusCode(), r.Body
		if err := repoenv.Check(status, body); err != nil {
			return err
		}
		if r.JSON202 == nil {
			return repoenv.Unexpected(status)
		}
		return writeRun(f, &jo, r.JSON202)
	}}
	if cancel {
		c.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	}
	output.AddJSONFlags(c, &jo, runFields)
	return c
}

func newListWorkflow(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	c := &cobra.Command{Use: "list", Short: "Elenca i workflow", Args: cmdutil.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		e, err := repoenv.New(f)
		if err != nil {
			return err
		}
		r, err := e.Gen.ListWorkflowsWithResponse(c.Context(), e.Owner, e.Repo)
		if err != nil {
			return err
		}
		if err := repoenv.Check(r.StatusCode(), r.Body); err != nil {
			return err
		}
		if r.JSON200 == nil {
			return repoenv.Unexpected(r.StatusCode())
		}
		if r.JSON200.Items == nil {
			r.JSON200.Items = []gitstack.Workflow{}
		}
		if jo.Enabled() {
			return jo.Write(f.IO.Out, r.JSON200.Items, f.IO.OutTTY)
		}
		for _, w := range r.JSON200.Items {
			_, _ = fmt.Fprintf(f.IO.Out, "%s\t%s\t%s\t%s\n", w.Id, w.State, w.Name, w.Path)
		}
		return nil
	}}
	output.AddJSONFlags(c, &jo, workflowFields)
	return c
}

func newDispatchWorkflow(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	var ref string
	var inputs []string
	c := &cobra.Command{Use: "run <workflow>", Short: "Avvia manualmente un workflow", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if strings.TrimSpace(ref) == "" {
			return cmdutil.UsageErrorf("--ref è obbligatorio")
		}
		values := map[string]string{}
		for _, item := range inputs {
			k, v, ok := strings.Cut(item, "=")
			if !ok || k == "" {
				return cmdutil.UsageErrorf("input non valido %q: atteso nome=valore", item)
			}
			values[k] = v
		}
		e, err := repoenv.New(f)
		if err != nil {
			return err
		}
		body := gitstack.WorkflowDispatchInput{Ref: ref}
		if len(values) > 0 {
			body.Inputs = &values
		}
		r, err := e.Gen.DispatchWorkflowWithResponse(c.Context(), e.Owner, e.Repo, args[0], body)
		if err != nil {
			return err
		}
		if err := repoenv.Check(r.StatusCode(), r.Body); err != nil {
			return err
		}
		if r.JSON202 == nil {
			return repoenv.Unexpected(r.StatusCode())
		}
		return writeRun(f, &jo, r.JSON202)
	}}
	c.Flags().StringVar(&ref, "ref", "", "Branch o ref da eseguire (obbligatorio)")
	c.Flags().StringArrayVarP(&inputs, "input", "f", nil, "Input workflow nome=valore (ripetibile)")
	output.AddJSONFlags(c, &jo, runFields)
	return c
}

func runID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, cmdutil.UsageErrorf("id esecuzione non valido: %q", s)
	}
	return id, nil
}

func failedLogs(c *cobra.Command, e *repoenv.Env, run *gitstack.WorkflowRun, out io.Writer) error {
	for _, job := range run.Jobs {
		if job.Conclusion == nil || (*job.Conclusion != "failure" && *job.Conclusion != "timed_out") {
			continue
		}
		resp, err := e.Gen.GetWorkflowJobLogWithResponse(c.Context(), e.Owner, e.Repo, job.Id, nil)
		if err != nil {
			return err
		}
		if err := repoenv.Check(resp.StatusCode(), resp.Body); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "--- %s ---\n", job.Name)
		_, _ = out.Write(resp.Body)
	}
	return nil
}

func conclusion(r *gitstack.WorkflowRun) string {
	if r.Conclusion == nil {
		return ""
	}
	return *r.Conclusion
}

func writeRun(f *cmdutil.Factory, jo *output.JSONOptions, run *gitstack.WorkflowRun) error {
	if jo.Enabled() {
		return jo.Write(f.IO.Out, run, f.IO.OutTTY)
	}
	_, err := fmt.Fprintf(f.IO.Out, "%s\t%s\t%s\n", run.Id, run.Status, conclusion(run))
	return err
}

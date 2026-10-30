package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// ListFields sono i campi di --json di `gs app list` e `gs app status` (la schermata CLI del mockup H10 usa
// name, status, version, verified).
var ListFields = []string{"name", "status", "version", "verified", "image", "address", "access", "copies", "readyCopies", "follow", "lastDeploy", "createdBy"}

// row è la proiezione di un'app che `list` e `status` stampano in JSON: i nomi del mockup (version, verified)
// al posto di quelli annidati del contratto.
type row struct {
	Name        string                  `json:"name"`
	Status      gitstack.AppStatus      `json:"status"`
	Version     int                     `json:"version"`
	Verified    bool                    `json:"verified"`
	Image       string                  `json:"image"`
	Address     *string                 `json:"address,omitempty"`
	Access      gitstack.AppAccessMode  `json:"access"`
	Copies      int                     `json:"copies"`
	ReadyCopies int                     `json:"readyCopies"`
	Follow      *gitstack.AppFollow     `json:"follow,omitempty"`
	LastDeploy  *gitstack.AppLastDeploy `json:"lastDeploy,omitempty"`
	CreatedBy   *gitstack.AppActor      `json:"createdBy,omitempty"`
}

func toRow(a gitstack.App) row {
	return row{Name: a.Name, Status: a.Status, Version: a.ActiveVersion, Verified: a.Image.Verified, Image: a.Image.Reference,
		Address: a.Address, Access: a.Access, Copies: a.Copies, ReadyCopies: a.ReadyCopies, Follow: a.Follow,
		LastDeploy: a.LastDeploy, CreatedBy: a.CreatedBy}
}

func (e *env) getApp(ctx context.Context, app string) (*gitstack.App, error) {
	resp, err := e.gen.GetAppWithResponse(ctx, gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app))
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

func newListCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Elenca le app del repo",
		Long:    "Elenca le app del repo con stato, versione attiva, copie e se l'immagine è verificata. Serve `read` sul repo.",
		Example: "  gs app list --repo acme/handbook\n  gs app list --repo acme/handbook --json name,status,version,verified",
		Args:    cmdutil.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.ListAppsWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo))
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			rows := make([]row, 0, len(resp.JSON200.Items))
			for _, a := range resp.JSON200.Items {
				rows = append(rows, toRow(a))
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, rows, f.IO.OutTTY)
			}
			if len(rows) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "Nessuna app in %s/%s\n", e.owner, e.repo)
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "NOME", "STATO", "VERSIONE", "COPIE", "IMMAGINE", "INDIRIZZO")
			for _, r := range rows {
				verified := "non verificata"
				if r.Verified {
					verified = "verificata"
				}
				addr := ""
				if r.Address != nil {
					addr = *r.Address
				}
				t.AddRow(r.Name, string(r.Status), fmt.Sprintf("v%d", r.Version), fmt.Sprintf("%d/%d", r.ReadyCopies, r.Copies), verified, addr)
			}
			return t.Render(f.IO.Out)
		},
	}
	output.AddJSONFlags(cmd, &jo, ListFields)
	return cmd
}

func newStatusCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "status <app>",
		Short: "Mostra stato e salute di un'app",
		Long: "Mostra stato, versione attiva, copie pronte, indirizzo e ultimo deploy (anche fallito, con il ritorno " +
			"automatico alla versione precedente). Serve `read` sul repo.",
		Example: "  gs app status web --repo acme/handbook\n  gs app status web --json status,version,readyCopies",
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
			a, err := e.getApp(c.Context(), app)
			if err != nil {
				return err
			}
			r := toRow(*a)
			if jo.Enabled() {
				return jo.Write(f.IO.Out, r, f.IO.OutTTY)
			}
			out := f.IO.Out
			_, _ = fmt.Fprintf(out, "%s · %s · v%d · %d/%d copie pronte\n", a.Name, a.Status, a.ActiveVersion, a.ReadyCopies, a.Copies)
			if a.Address != nil {
				_, _ = fmt.Fprintf(out, "indirizzo: %s\n", *a.Address)
			}
			_, _ = fmt.Fprintf(out, "immagine: %s (%s)\n", a.Image.Reference, imageTrust(a.Image))
			if a.Follow != nil {
				state := "attivo"
				if a.Follow.Paused {
					state = "sospeso"
				}
				switch {
				case a.Follow.TagPattern != nil:
					_, _ = fmt.Fprintf(out, "deploy automatico: tag %s (%s)\n", *a.Follow.TagPattern, state)
				case a.Follow.Branch != nil:
					_, _ = fmt.Fprintf(out, "deploy automatico: branch %s (%s)\n", *a.Follow.Branch, state)
				}
			}
			if d := a.LastDeploy; d != nil {
				line := fmt.Sprintf("ultimo deploy: v%d %s", d.Version, d.State)
				if d.Reason != nil && *d.Reason != "" {
					line += " · " + *d.Reason
				}
				_, _ = fmt.Fprintln(out, line)
			}
			if a.RunningPreviousVersion != nil && *a.RunningPreviousVersion {
				_, _ = fmt.Fprintln(out, "serve ancora la versione precedente")
			}
			return nil
		},
	}
	output.AddJSONFlags(cmd, &jo, ListFields)
	return cmd
}

func imageTrust(i gitstack.AppImage) string {
	if !i.Verified {
		return "non verificata"
	}
	if i.SignedBy != nil && *i.SignedBy != "" {
		return "firmata da " + *i.SignedBy
	}
	return "verificata"
}

func newCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		image, branch, tagPattern, healthPath string
		port, copies                          int
		restricted                            bool
		jo                                    output.JSONOptions
	)
	cmd := &cobra.Command{
		Use:   "create <app>",
		Short: "Crea un'app nel repo e la mette in esecuzione",
		Long: "Crea l'app (interna, con il login GitStack) con la versione 1 e avvia il deploy iniziale. Senza --port l'app " +
			"non ha indirizzo e parla solo con le app dello stesso repo. Serve `admin` sul repo.",
		Example: "  gs app create web --image sha-91be20d --port 8080 --health-path /healthz --copies 2 --repo acme/handbook",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			if strings.TrimSpace(image) == "" {
				return cmdutil.UsageErrorf("--image è obbligatoria")
			}
			in := gitstack.AppCreateInput{Name: app, Image: image}
			fl := c.Flags()
			if fl.Changed("port") {
				in.Port = &port
			}
			if fl.Changed("copies") {
				if copies < 1 {
					return cmdutil.UsageErrorf("--copies deve essere almeno 1")
				}
				in.Copies = &copies
			}
			if fl.Changed("follow-branch") {
				in.FollowBranch = &branch
			}
			if fl.Changed("follow-tag") {
				in.FollowTagPattern = &tagPattern
			}
			if fl.Changed("follow-branch") && fl.Changed("follow-tag") {
				return cmdutil.UsageErrorf("--follow-branch e --follow-tag non vanno insieme: un'app segue un branch oppure una regola sui tag")
			}
			if fl.Changed("restricted") {
				in.Restricted = &restricted
			}
			if fl.Changed("health-path") {
				in.HealthCheck = &gitstack.AppHealthCheck{Path: healthPath, IntervalSeconds: 10}
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.CreateAppWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), in)
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
				return jo.Write(f.IO.Out, toRow(resp.JSON202.App), f.IO.OutTTY)
			}
			a, v := resp.JSON202.App, resp.JSON202.Version
			_, _ = fmt.Fprintf(f.IO.Out, "creating %s v%d (%d %s) · image %s\n", a.Name, v.Number, a.Copies, plural(a.Copies, "copy", "copies"), trustEN(v.Image))
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&image, "image", "", "Immagine: tag del registry del repo, `nome:tag` o riferimento esterno completo")
	fl.IntVar(&port, "port", 0, "Porta del container (senza: app senza indirizzo)")
	fl.IntVar(&copies, "copies", 1, "Numero di copie")
	fl.StringVar(&branch, "follow-branch", "", "Branch da seguire per il deploy automatico (H7)")
	fl.StringVar(&tagPattern, "follow-tag", "", "Regola sui tag per il deploy automatico, per esempio 'v*-test.*' (H7); non insieme a --follow-branch")
	fl.StringVar(&healthPath, "health-path", "", "Percorso HTTP del controllo di salute, per esempio /healthz")
	fl.BoolVar(&restricted, "restricted", false, "Rifiuta immagini che girano come root")
	output.AddJSONFlags(cmd, &jo, ListFields)
	return cmd
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// trustEN è la frase d'immagine delle righe di deploy, nella lingua del mockup H10 («signed by acme/handbook CI»).
func trustEN(i gitstack.AppImage) string {
	if !i.Verified {
		return "not verified"
	}
	if i.SignedBy != nil && *i.SignedBy != "" {
		return "signed by " + *i.SignedBy
	}
	return "verified"
}

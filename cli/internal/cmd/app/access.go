package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// AccessFields sono i campi di --json di `gs app access list` e `add` (schema AppAccessMember dell'API).
var AccessFields = []string{"id", "kind", "name", "email", "memberCount", "addedBy", "addedAt"}

// InviteFields sono i campi di --json di `gs app access invite` (schema AppAccessInvite dell'API).
var InviteFields = []string{"guideUrl", "fingerprintSha256", "address", "caMode", "text"}

func newAccessCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Gestisci la lista di accesso di un'app",
		Long: "La lista di accesso (H6) dice chi, oltre a chi ha read sul repo, può aprire l'app: utenti e team " +
			"GitStack, mai accesso anonimo. Chi entra solo dalla lista apre l'app e nient'altro: non vede " +
			"codice, issues o la scheda Apps e non riceve nessun ruolo sul repo. Serve il permesso admin sul repo.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newAccessListCmd(f), newAccessAddCmd(f), newAccessRemoveCmd(f), newAccessInviteCmd(f))
	return cmd
}

func appName(args []string) (string, error) {
	if strings.TrimSpace(args[0]) == "" {
		return "", cmdutil.UsageErrorf("il nome dell'app non può essere vuoto")
	}
	return args[0], nil
}

func (e *env) listMembers(ctx context.Context, app string) ([]gitstack.AppAccessMember, error) {
	resp, err := e.gen.ListAppAccessMembersWithResponse(ctx, gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app))
	if err != nil {
		return nil, err
	}
	if err := e.check(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, unexpected(resp.StatusCode())
	}
	return resp.JSON200.Items, nil
}

func newAccessListCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "list <app>",
		Aliases: []string{"ls"},
		Short:   "Elenca la lista di accesso dell'app",
		Long:    "Elenca utenti e team nella lista di accesso dell'app. Serve il permesso admin sul repo.",
		Example: "  gs app access list sito\n  gs app access list sito --json name,kind",
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
			items, err := e.listMembers(c.Context(), app)
			if err != nil {
				return err
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, items, f.IO.OutTTY)
			}
			if len(items) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "Nessuno nella lista di accesso di %s\n", app)
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "TIPO", "NOME", "DETTAGLIO", "AGGIUNTO DA", "DATA")
			for _, m := range items {
				detail := ""
				if m.Email != nil {
					detail = *m.Email
				}
				if m.MemberCount != nil {
					detail = fmt.Sprintf("%d persone", *m.MemberCount)
				}
				by := ""
				if m.AddedBy != nil {
					by = m.AddedBy.Username
				}
				t.AddRow(string(m.Kind), m.Name, detail, by, m.AddedAt.Local().Format(time.DateOnly))
			}
			return t.Render(f.IO.Out)
		},
	}
	output.AddJSONFlags(cmd, &jo, AccessFields)
	return cmd
}

func newAccessAddCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "add <app> <utente|organizzazione/team>",
		Short: "Aggiungi un utente o un team alla lista di accesso",
		Long: "Aggiunge un utente GitStack, oppure un team scritto organizzazione/team, alla lista di accesso " +
			"dell'app. Non assegna nessun ruolo sul repo. Serve il permesso admin sul repo.",
		Example: "  gs app access add sito gverdi\n  gs app access add sito retail/business",
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
			kind := gitstack.AppAccessMemberInputKindUser
			if strings.Contains(name, "/") {
				kind = gitstack.AppAccessMemberInputKindTeam
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.AddAppAccessMemberWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app),
				gitstack.AddAppAccessMemberJSONRequestBody{Kind: kind, Name: name})
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			if jo.Enabled() {
				return jo.Write(f.IO.Out, resp.JSON201, f.IO.OutTTY)
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Aggiunto %s alla lista di accesso di %s\n", resp.JSON201.Name, app)
			return nil
		},
	}
	output.AddJSONFlags(cmd, &jo, AccessFields)
	return cmd
}

func newAccessRemoveCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <app> <utente|organizzazione/team>",
		Aliases: []string{"rm"},
		Short:   "Togli un utente o un team dalla lista di accesso",
		Long: "Toglie un utente o un team dalla lista di accesso dell'app: da quel momento chi non ha read sul repo " +
			"non apre più l'app. Chiede di riscrivere il nome (G8): senza terminale serve --yes, altrimenti exit 2. " +
			"Serve il permesso admin sul repo.",
		Example: "  gs app access remove sito gverdi\n  gs app access remove sito retail/business --yes",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			name := strings.ToLower(strings.TrimSpace(args[1]))
			if name == "" {
				return cmdutil.UsageErrorf("il nome non può essere vuoto")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Togliere %s dalla lista di accesso di %s. Riscrivi `%s` per confermare:", name, app, name)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, name); err != nil {
				return err
			}
			items, err := e.listMembers(c.Context(), app)
			if err != nil {
				return err
			}
			var id *uuid.UUID
			for _, m := range items {
				if strings.EqualFold(m.Name, name) {
					u := uuid.UUID(m.Id)
					id = &u
					break
				}
			}
			if id == nil {
				return fmt.Errorf("%s non è nella lista di accesso di %s", name, app)
			}
			resp, err := e.gen.RemoveAppAccessMemberWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), *id)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Tolto %s dalla lista di accesso di %s\n", name, app)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}

func newAccessInviteCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "invite <app>",
		Short: "Stampa il testo dell'invito ai tester",
		Long: "Stampa il testo dell'invito, già composto, da mandare ai tester: link alla guida del certificato con l'app " +
			"come parametro, impronta SHA-256 da controllare, indirizzo dell'app e il rimando a «Report a problem». " +
			"È lo stesso testo del pulsante «Invita i tester». GitStack non manda email: lo spedisci tu con i tuoi " +
			"strumenti. Serve il permesso admin sul repo.",
		Example: "  gs app access invite sito\n  gs app access invite sito --json fingerprintSha256,address",
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
			resp, err := e.gen.GetAppAccessInviteWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app))
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
			_, _ = fmt.Fprintln(f.IO.Out, resp.JSON200.Text)
			return nil
		},
	}
	output.AddJSONFlags(cmd, &jo, InviteFields)
	return cmd
}

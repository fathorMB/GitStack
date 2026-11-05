package db

import (
	"fmt"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// BackupFields sono i campi di --json dei backup (schema DatabaseBackup).
var BackupFields = []string{"id", "kind", "status", "sizeBytes", "error", "createdAt", "completedAt", "createdBy"}

func newBackupCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backup",
		Aliases: []string{"backups"},
		Short:   "Backup e ripristino di un database",
		Long: "Dump `pg_dump -Fc` cifrati: il programmato ne tiene gli ultimi 7, quello su richiesta gli ultimi 5. I contenuti non " +
			"si scaricano, servono al ripristino. Serve `admin` sull'owner.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newBackupListCmd(f), newBackupRunCmd(f), newBackupRestoreCmd(f))
	return cmd
}

func newBackupListCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo output.JSONOptions
		ow ownerFlag
	)
	cmd := &cobra.Command{
		Use:     "list <db>",
		Aliases: []string{"ls"},
		Short:   "Elenca i backup del database",
		Example: "  gs db backup list acme/shop\n  gs db backup list shop --owner acme --json id,status",
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
			resp, err := e.gen.ListDatabaseBackupsWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name)
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
			if len(resp.JSON200.Items) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "%s/%s non ha backup\n", owner, name)
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "ID", "TIPO", "STATO", "DIMENSIONE", "CREATO", "ERRORE")
			for _, b := range resp.JSON200.Items {
				sz, errMsg := "-", ""
				if b.SizeBytes != nil {
					sz = humanSize(*b.SizeBytes)
				}
				if b.Error != nil {
					errMsg = *b.Error
				}
				t.AddRow(b.Id.String(), string(b.Kind), string(b.Status), sz, b.CreatedAt.Local().Format("2006-01-02 15:04"), errMsg)
			}
			return t.Render(f.IO.Out)
		},
	}
	ow.add(cmd)
	output.AddJSONFlags(cmd, &jo, BackupFields)
	return cmd
}

func newBackupRunCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo output.JSONOptions
		ow ownerFlag
	)
	cmd := &cobra.Command{
		Use:     "run <db>",
		Aliases: []string{"create"},
		Short:   "Avvia un backup su richiesta",
		Long: "Avvia un dump cifrato (202): il backup parte `in_progress` e l'esito si legge con `gs db backup list`. Se un dump è " +
			"già in corso l'API risponde `backup_in_progress`. Serve `admin` sull'owner.",
		Example: "  gs db backup run acme/shop",
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
			resp, err := e.gen.CreateDatabaseBackupWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name)
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
				return jo.Write(f.IO.Out, resp.JSON202, f.IO.OutTTY)
			}
			_, _ = fmt.Fprintf(f.IO.Out, "backup %s avviato (%s)\n", resp.JSON202.Id, resp.JSON202.Status)
			return nil
		},
	}
	ow.add(cmd)
	output.AddJSONFlags(cmd, &jo, BackupFields)
	return cmd
}

func newBackupRestoreCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo      output.JSONOptions
		ow      ownerFlag
		yes     bool
		asNew   bool
		newName string
	)
	cmd := &cobra.Command{
		Use:   "restore <db> <backup-id> [--new [--name <nome>]]",
		Short: "Ripristina un backup",
		Long: "Senza --new il backup SOSTITUISCE il database: il controller fa prima un dump di sicurezza, ricrea i ruoli e i " +
			"collegamenti (password nuove) e riavvia le app collegate. Con --new nasce un database nuovo, con --name anche con un " +
			"nome diverso (se è già usato: `name_conflict`). Chiede la conferma (G8): per la sostituzione riscrivi `owner/nome`, " +
			"per --new basta sì; --yes senza terminale. I GRANT personalizzati non sopravvivono. Serve `admin` sull'owner.",
		Example: "  gs db backup restore acme/shop 6a1f... --yes\n  gs db backup restore acme/shop 6a1f... --new --name shop-copia --yes",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			owner, name, err := ow.dbRef(f, args[0])
			if err != nil {
				return err
			}
			var id gitstack.DatabaseBackupIdParam
			if err := id.UnmarshalText([]byte(args[1])); err != nil {
				return cmdutil.UsageErrorf("%q non è un id di backup valido", args[1])
			}
			if !asNew && c.Flags().Changed("name") {
				return cmdutil.UsageErrorf("--name vale solo con --new")
			}
			full := owner + "/" + name
			in := gitstack.DatabaseRestoreInput{Target: gitstack.DatabaseRestoreTargetSame, Confirm: &full}
			var prompt, expected string
			if asNew {
				in.Target, in.Confirm = gitstack.DatabaseRestoreTargetNew, nil
				if c.Flags().Changed("name") {
					in.Name = &newName
				}
				prompt = fmt.Sprintf("Ripristinare il backup %s di %s in un database nuovo? [s/N]", id, full)
			} else {
				prompt = fmt.Sprintf("Il backup %s SOSTITUISCE il database %s e i suoi dati attuali. Riscrivi `%s` per confermare:", id, full, full)
				expected = full
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, expected); err != nil {
				return err
			}
			resp, err := e.gen.RestoreDatabaseBackupWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name, id, in)
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
				return jo.Write(f.IO.Out, resp.JSON202, f.IO.OutTTY)
			}
			d := resp.JSON202
			_, _ = fmt.Fprintf(f.IO.Out, "ripristino avviato in %s/%s (%s)\n", d.Owner, d.Name, d.Status)
			return nil
		},
	}
	ow.add(cmd)
	cmd.Flags().BoolVar(&asNew, "new", false, "Ripristina in un database nuovo invece di sostituire questo")
	cmd.Flags().StringVar(&newName, "name", "", "Con --new: nome del database nuovo (predefinito: lo stesso nome)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	output.AddJSONFlags(cmd, &jo, DatabaseFields)
	return cmd
}

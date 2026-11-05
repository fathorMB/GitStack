package db

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// DatabaseFields sono i campi di --json di list, view e resize (schema Database).
var DatabaseFields = []string{"id", "owner", "name", "pgMajor", "status", "sizeBytes", "cpuMillis", "memoryBytes", "lanEnabled", "lanPort", "linkCount", "usage", "operations", "createdAt", "createdBy", "updatedAt"}

// CredentialsResultFields sono i campi di --json di create e di credentials rotate.
var CredentialsResultFields = []string{"database", "credentials"}

func newListCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo output.JSONOptions
		ow ownerFlag
	)
	cmd := &cobra.Command{
		Use:     "list [owner]",
		Aliases: []string{"ls"},
		Short:   "Elenca i database di un owner",
		Long:    "Elenca i database gestiti dell'owner: stato, major, dimensione, CPU/RAM, LAN e collegamenti. Serve `admin` sull'owner.",
		Example: "  gs db list acme\n  gs db list --owner acme --json name,status,sizeBytes",
		Args:    maxOneArg,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 1 {
				ow.v = args[0]
			}
			owner, err := ow.owner(f)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.ListDatabasesWithResponse(c.Context(), gitstack.RepoOwnerParam(owner))
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
					_, _ = fmt.Fprintf(f.IO.ErrOut, "%s non ha database\n", owner)
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "NOME", "STATO", "PG", "DIMENSIONE", "CPU", "RAM", "LAN", "LINK")
			for _, d := range resp.JSON200.Items {
				t.AddRow(d.Name, string(d.Status), strconv.Itoa(int(d.PgMajor)), humanSize(d.SizeBytes), humanCPU(d.CpuMillis), humanSize(d.MemoryBytes), lanCell(d), strconv.Itoa(d.LinkCount))
			}
			return t.Render(f.IO.Out)
		},
	}
	ow.add(cmd)
	output.AddJSONFlags(cmd, &jo, DatabaseFields)
	return cmd
}

func lanCell(d gitstack.Database) string {
	if d.LanEnabled && d.LanPort != nil {
		return strconv.Itoa(*d.LanPort)
	}
	return onOff(d.LanEnabled)
}

func newCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo          output.JSONOptions
		ow          ownerFlag
		major       int
		size, cpu   string
		memory      string
		sizeB, memB int64
		cpuM        int
	)
	cmd := &cobra.Command{
		Use:   "create <nome> --pg <16|17>",
		Short: "Crea un database PostgreSQL gestito",
		Long: "Crea un database (202: nasce `creating` e il controller lo porta `running`). Dimensione, CPU e RAM contano nella " +
			"quota dell'owner: oltre il tetto l'API risponde `insufficient_capacity`. La password dell'utente `<nome>_owner` si " +
			"vede solo in questa risposta e non si rilegge: salvala subito. Serve `admin` sull'owner.",
		Example: "  gs db create shop --pg 17 --owner acme\n  gs db create acme/shop --pg 16 --size 5GB --cpu 500m --memory 512MB",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			owner, name, err := ow.dbRef(f, args[0])
			if err != nil {
				return err
			}
			if major != 16 && major != 17 {
				return cmdutil.UsageErrorf("--pg deve essere 16 o 17")
			}
			in := gitstack.DatabaseCreateInput{Name: name, PgMajor: gitstack.DatabasePgMajor(major)}
			fl := c.Flags()
			if fl.Changed("size") {
				if sizeB, err = parseSize("--size", size); err != nil {
					return err
				}
				in.SizeBytes = &sizeB
			}
			if fl.Changed("cpu") {
				if cpuM, err = parseCPU(cpu); err != nil {
					return err
				}
				in.CpuMillis = &cpuM
			}
			if fl.Changed("memory") {
				if memB, err = parseSize("--memory", memory); err != nil {
					return err
				}
				in.MemoryBytes = &memB
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.CreateDatabaseWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), in)
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
			d := resp.JSON202.Database
			_, _ = fmt.Fprintf(f.IO.Out, "creato %s/%s · PostgreSQL %d · %s\n", d.Owner, d.Name, int(d.PgMajor), d.Status)
			printCredentials(f, resp.JSON202.Credentials)
			return nil
		},
	}
	ow.add(cmd)
	cmd.Flags().IntVar(&major, "pg", 0, "Major di PostgreSQL: 16 o 17 (obbligatoria)")
	cmd.Flags().StringVar(&size, "size", "", "Dimensione dichiarata, per esempio 5GB (predefinita: 1 GB)")
	cmd.Flags().StringVar(&cpu, "cpu", "", "CPU richiesta, per esempio 500m o 0.5 (predefinita: 100m)")
	cmd.Flags().StringVar(&memory, "memory", "", "RAM richiesta, per esempio 512MB (predefinita: 256 MB)")
	output.AddJSONFlags(cmd, &jo, CredentialsResultFields)
	return cmd
}

// printCredentials scrive le credenziali una tantum (DB22).
func printCredentials(f *cmdutil.Factory, c gitstack.DatabaseCredentials) {
	out := f.IO.Out
	_, _ = fmt.Fprintf(out, "utente: %s\npassword: %s\ndatabase: %s\nsslmode: %s\n", c.Username, c.Password, c.Database, c.SslMode)
	_, _ = fmt.Fprintln(f.IO.ErrOut, "La password si vede solo ora e non si rilegge: salvala.")
}

func newViewCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo output.JSONOptions
		ow ownerFlag
	)
	cmd := &cobra.Command{
		Use:     "view <db>",
		Aliases: []string{"get", "status"},
		Short:   "Mostra un database",
		Long:    "Stato, major, dimensione, uso del disco con gli avvisi all'80% e al 100%, LAN, collegamenti e ultimo backup. Serve `admin` sull'owner.",
		Example: "  gs db view acme/shop\n  gs db view shop --owner acme --json status,usage",
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
			resp, err := e.gen.GetDatabaseWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name)
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
			printDatabase(f, *resp.JSON200)
			return nil
		},
	}
	ow.add(cmd)
	output.AddJSONFlags(cmd, &jo, DatabaseFields)
	return cmd
}

func printDatabase(f *cmdutil.Factory, d gitstack.Database) {
	out := f.IO.Out
	_, _ = fmt.Fprintf(out, "%s/%s · %s · PostgreSQL %d\n", d.Owner, d.Name, d.Status, int(d.PgMajor))
	_, _ = fmt.Fprintf(out, "risorse: %s disco · %s CPU · %s RAM\n", humanSize(d.SizeBytes), humanCPU(d.CpuMillis), humanSize(d.MemoryBytes))
	if d.Usage != nil {
		_, _ = fmt.Fprintf(out, "uso: %s (%s)\n", humanSize(d.Usage.DatabaseBytes), d.Usage.DiskState)
	}
	_, _ = fmt.Fprintf(out, "lan: %s\n", lanCell(d))
	_, _ = fmt.Fprintf(out, "collegamenti: %d\n", d.LinkCount)
	if op := d.Operations; op != nil {
		if op.LastBackupAt != nil {
			_, _ = fmt.Fprintf(out, "ultimo backup: %s\n", op.LastBackupAt.Local().Format("2006-01-02 15:04"))
		}
		if op.BackupError != nil {
			_, _ = fmt.Fprintf(out, "errore backup: %s\n", *op.BackupError)
		}
		if op.RestoreInProgress != nil && *op.RestoreInProgress {
			_, _ = fmt.Fprintln(out, "ripristino in corso")
		}
		if op.RestoreError != nil {
			_, _ = fmt.Fprintf(out, "errore ripristino: %s\n", *op.RestoreError)
		}
		if op.MinorUpdateAvailable {
			_, _ = fmt.Fprintln(out, "aggiornamento minor disponibile")
		}
	}
}

func newResizeCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		jo                output.JSONOptions
		ow                ownerFlag
		size, cpu, memory string
	)
	cmd := &cobra.Command{
		Use:   "resize <db> [--size S] [--cpu C] [--memory M]",
		Short: "Ridimensiona un database",
		Long: "Cambia la dimensione dichiarata, la CPU e la RAM. La dimensione è un limite morbido (avvisi all'80% e al 100%, " +
			"le scritture non si bloccano). Il totale conta nella quota dell'owner: oltre il tetto `insufficient_capacity`. " +
			"Serve almeno uno fra --size, --cpu e --memory e `admin` sull'owner.",
		Example: "  gs db resize acme/shop --size 10GB\n  gs db resize shop --owner acme --cpu 1 --memory 1GB",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			owner, name, err := ow.dbRef(f, args[0])
			if err != nil {
				return err
			}
			var in gitstack.DatabaseUpdateInput
			fl := c.Flags()
			if !fl.Changed("size") && !fl.Changed("cpu") && !fl.Changed("memory") {
				return cmdutil.UsageErrorf("serve almeno uno fra --size, --cpu e --memory")
			}
			if fl.Changed("size") {
				n, err := parseSize("--size", size)
				if err != nil {
					return err
				}
				in.SizeBytes = &n
			}
			if fl.Changed("cpu") {
				n, err := parseCPU(cpu)
				if err != nil {
					return err
				}
				in.CpuMillis = &n
			}
			if fl.Changed("memory") {
				n, err := parseSize("--memory", memory)
				if err != nil {
					return err
				}
				in.MemoryBytes = &n
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.UpdateDatabaseWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name, in)
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
			d := resp.JSON200
			_, _ = fmt.Fprintf(f.IO.Out, "ridimensionato %s/%s: %s disco · %s CPU · %s RAM (%s)\n", d.Owner, d.Name, humanSize(d.SizeBytes), humanCPU(d.CpuMillis), humanSize(d.MemoryBytes), d.Status)
			return nil
		},
	}
	ow.add(cmd)
	cmd.Flags().StringVar(&size, "size", "", "Nuova dimensione dichiarata, per esempio 10GB")
	cmd.Flags().StringVar(&cpu, "cpu", "", "Nuova CPU, per esempio 500m o 1")
	cmd.Flags().StringVar(&memory, "memory", "", "Nuova RAM, per esempio 1GB")
	output.AddJSONFlags(cmd, &jo, DatabaseFields)
	return cmd
}

func newDeleteCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		yes bool
		ow  ownerFlag
		jo  output.JSONOptions
	)
	cmd := &cobra.Command{
		Use:     "delete <db>",
		Aliases: []string{"rm"},
		Short:   "Elimina un database e i suoi dati",
		Long: "Elimina subito e senza cestino il database con i suoi dati, i collegamenti (le app collegate perdono le variabili e " +
			"riavviano), la porta LAN e i backup programmati e su richiesta: non si annulla. Chiede la conferma (G8): riscrivi " +
			"`owner/nome`, o --yes senza terminale. Serve `admin` sull'owner.",
		Example: "  gs db delete acme/shop\n  gs db delete shop --owner acme --yes",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			owner, name, err := ow.dbRef(f, args[0])
			if err != nil {
				return err
			}
			full := owner + "/" + name
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Eliminare il database %s con tutti i dati, i collegamenti e i backup. Non si annulla. Riscrivi `%s` per confermare:", full, full)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, full); err != nil {
				return err
			}
			resp, err := e.gen.DeleteDatabaseWithResponse(c.Context(), gitstack.RepoOwnerParam(owner), name, &gitstack.DeleteDatabaseParams{Confirm: &full})
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.Out, "deleted %s\n", full)
			return nil
		},
	}
	ow.add(cmd)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	output.AddJSONFlags(cmd, &jo, []string{"owner", "name", "deleted"})
	return cmd
}

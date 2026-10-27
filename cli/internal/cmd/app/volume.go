package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// VolumeFields sono i campi di --json di `gs app volume list` e `add` (schema AppVolume, H3).
var VolumeFields = []string{"name", "mountPath", "sizeBytes", "version", "createdAt", "createdBy", "includeInBackup", "looksLikeDatabase"}

const minVolumeBytes = 1 << 20 // 1 MiB, come il contratto

func newVolumeCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "volume",
		Aliases: []string{"volumes"},
		Short:   "Gestisci i volumi persistenti dell'app",
		Long: "I volumi hanno dimensione fissa e contano nella quota disco dell'owner (H3, H11). Un'app con volumi gira in " +
			"una sola copia. Elencarli richiede `read`, aggiungerli o toglierli `admin`.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newVolumeListCmd(f), newVolumeAddCmd(f), newVolumeRemoveCmd(f), newVolumeBackupCmd(f))
	return cmd
}

// parseSize legge 512MB, 10GB, 1.5GiB o un numero di byte. Le unità sono binarie (1 GB = 1024 MiB), come la UI.
func parseSize(s string) (int64, error) {
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
		return 0, cmdutil.UsageErrorf("la dimensione %q non è valida: usa per esempio 512MB o 10GB", s)
	}
	n := int64(v * float64(mult))
	if n < minVolumeBytes {
		return 0, cmdutil.UsageErrorf("la dimensione minima di un volume è 1 MiB")
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

func newVolumeListCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "list <app>",
		Aliases: []string{"ls"},
		Short:   "Elenca i volumi dell'app",
		Example: "  gs app volume list wiki --repo acme/handbook\n  gs app volume list wiki --json name,mountPath,sizeBytes",
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
			resp, err := e.gen.ListAppVolumesWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app))
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
					_, _ = fmt.Fprintf(f.IO.ErrOut, "%s non ha volumi\n", app)
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "NOME", "MOUNT", "DIMENSIONE", "VERSIONE", "CREATO", "BACKUP")
			for _, v := range resp.JSON200.Items {
				t.AddRow(string(v.Name), v.MountPath, humanSize(v.SizeBytes), strconv.Itoa(v.Version), v.CreatedAt.Local().Format(time.DateOnly), inBackup(v.IncludeInBackup))
			}
			return t.Render(f.IO.Out)
		},
	}
	output.AddJSONFlags(cmd, &jo, VolumeFields)
	return cmd
}

func newVolumeAddCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		mount, size string
		jo          output.JSONOptions
	)
	cmd := &cobra.Command{
		Use:   "add <app> <nome> --mount <percorso> --size <dimensione>",
		Short: "Aggiungi un volume all'app",
		Long: "Aggiunge un volume di dimensione fissa (almeno 1 MiB) montato in --mount. La dimensione conta nella quota " +
			"disco dell'owner: se non ci sta il comando esce con l'errore `insufficient_capacity`. Crea una nuova " +
			"versione e distribuisce l'app con aggiornamento Recreate; con i volumi l'app gira in una sola copia. " +
			"I volumi sono inclusi nei backup (H4); per un database pianifica un dump nel volume. Serve `admin` sul repo.",
		Example: "  gs app volume add wiki data --mount /wiki/data --size 10GB",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			name := strings.ToLower(strings.TrimSpace(args[1]))
			if name == "" {
				return cmdutil.UsageErrorf("il nome del volume non può essere vuoto")
			}
			if mount == "" || size == "" {
				return cmdutil.UsageErrorf("servono --mount e --size")
			}
			if !strings.HasPrefix(mount, "/") {
				return cmdutil.UsageErrorf("--mount deve essere un percorso assoluto, per esempio /wiki/data")
			}
			bytes, err := parseSize(size)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.CreateAppVolumeWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app),
				gitstack.AppVolumeInput{Name: gitstack.AppVolumeName(name), MountPath: mount, SizeBytes: bytes})
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
				return jo.Write(f.IO.Out, resp.JSON202.Volume, f.IO.OutTTY)
			}
			_, _ = fmt.Fprintf(f.IO.Out, "aggiunto %s → %s (%s), versione %d\n", name, mount, humanSize(bytes), resp.JSON202.Version.Number)
			return nil
		},
	}
	cmd.Flags().StringVar(&mount, "mount", "", "Percorso di mount nel container (assoluto, obbligatorio)")
	cmd.Flags().StringVar(&size, "size", "", "Dimensione fissa, per esempio 512MB o 10GB (obbligatoria)")
	output.AddJSONFlags(cmd, &jo, VolumeFields)
	return cmd
}

func inBackup(b bool) string {
	if b {
		return "sì"
	}
	return "no"
}

func newVolumeBackupCmd(f *cmdutil.Factory) *cobra.Command {
	var include, exclude bool
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "backup <app> <nome> (--include | --exclude)",
		Short: "Includi o escludi un volume dal backup",
		Long: "Di default ogni volume entra in `gitstack backup` (anche verso S3) e torna con `gitstack restore` (H3, D19). " +
			"--exclude lo toglie, --include lo rimette. È una scelta sul dato: non crea versioni né deploy. Un volume escluso " +
			"ritorna vuoto dopo un restore. Per un database il backup copia i file con l'app accesa e la coerenza non è " +
			"garantita: pianifica un dump in un altro volume (docs/operations.md, «Volumes and databases»). Serve `admin` sul repo.",
		Example: "  gs app volume backup wiki cache --exclude\n  gs app volume backup wiki cache --include",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if include == exclude {
				return cmdutil.UsageErrorf("serve esattamente una scelta fra --include e --exclude")
			}
			app, err := appName(args)
			if err != nil {
				return err
			}
			name := strings.ToLower(strings.TrimSpace(args[1]))
			if name == "" {
				return cmdutil.UsageErrorf("il nome del volume non può essere vuoto")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.UpdateAppVolumeWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), gitstack.AppVolumeParam(name),
				gitstack.AppVolumeUpdate{IncludeInBackup: include})
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
			if include {
				_, _ = fmt.Fprintf(f.IO.Out, "%s di %s è nei backup\n", name, app)
			} else {
				_, _ = fmt.Fprintf(f.IO.Out, "%s di %s è escluso dai backup\n", name, app)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&include, "include", false, "Metti il volume nei backup")
	cmd.Flags().BoolVar(&exclude, "exclude", false, "Togli il volume dai backup")
	output.AddJSONFlags(cmd, &jo, VolumeFields)
	return cmd
}

func newVolumeRemoveCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <app> <nome>",
		Aliases: []string{"rm"},
		Short:   "Elimina un volume e i suoi dati",
		Long: "Elimina il volume e i dati che contiene: non si annulla. Crea una nuova versione senza il volume e libera la " +
			"quota disco. Chiede di riscrivere il nome del volume (G8): senza terminale serve --yes, altrimenti exit 2. " +
			"Serve `admin` sul repo.",
		Example: "  gs app volume remove wiki data\n  gs app volume rm wiki data --yes",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			name := strings.ToLower(strings.TrimSpace(args[1]))
			if name == "" {
				return cmdutil.UsageErrorf("il nome del volume non può essere vuoto")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Eliminare il volume %s di %s e tutti i suoi dati. Riscrivi `%s` per confermare:", name, app, name)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, name); err != nil {
				return err
			}
			resp, err := e.gen.DeleteAppVolumeWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), gitstack.AppVolumeParam(name))
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.Out, "eliminato %s da %s\n", name, app)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}

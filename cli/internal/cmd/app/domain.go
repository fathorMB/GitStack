package app

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// DomainFields sono i campi di --json dei comandi `gs app domain` (schema AppDomain dell'API, v3-c).
var DomainFields = []string{"id", "domain", "status", "statusReason", "dns", "certificate", "lastCheckedAt", "verifiedAt", "createdAt", "createdBy"}

// AccessModeFields sono i campi di --json di `gs app access <app> --mode` (schema AppAccess).
var AccessModeFields = []string{"mode", "publicAppsEnabled", "address", "certificate", "identityHeaders", "changedAt", "changedBy"}

func newDomainCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "domain",
		Aliases: []string{"domains"},
		Short:   "Gestisci i domini propri di un'app pubblica",
		Long: "I domini propri (v3-c) sono nomi tuoi (es. shop.retail.it) che puntano a un'app con accesso `public`. " +
			"Il giro è: `gs app access <app> --mode public`, `gs app domain add`, creare il record DNS indicato, " +
			"`gs app domain verify`, poi il certificato (Let's Encrypt automatico, oppure il tuo con `gs app domain cert upload`). " +
			"Serve il permesso admin sul repo.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newDomainListCmd(f), newDomainAddCmd(f), newDomainVerifyCmd(f), newDomainRemoveCmd(f), newDomainCertCmd(f))
	return cmd
}

func domainArg(s string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(s))
	if d == "" {
		return "", cmdutil.UsageErrorf("il dominio non può essere vuoto")
	}
	return d, nil
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format(time.DateOnly)
}

func dnsText(d gitstack.AppDomain) string {
	s := fmt.Sprintf("%s %s -> %s", d.Dns.Type, d.Dns.Name, d.Dns.Value)
	for _, a := range d.Dns.Alternatives {
		s += fmt.Sprintf(" (oppure %s %s)", a.Type, a.Value)
	}
	return s
}

func certText(c gitstack.AppDomainCertificate) string {
	s := fmt.Sprintf("%s/%s", c.Mode, c.State)
	if c.ValidUntil != nil {
		s += " fino al " + fmtTime(c.ValidUntil)
	}
	if c.Error != nil && *c.Error != "" {
		s += " (" + *c.Error + ")"
	}
	return s
}

func statusText(d gitstack.AppDomain) string {
	s := string(d.Status)
	if d.StatusReason != nil && *d.StatusReason != "" {
		s += " (" + *d.StatusReason + ")"
	}
	return s
}

// printDomain stampa il dettaglio di un dominio, o il JSON se richiesto.
func printDomain(f *cmdutil.Factory, jo *output.JSONOptions, verb string, d *gitstack.AppDomain) error {
	if jo.Enabled() {
		return jo.Write(f.IO.Out, d, f.IO.OutTTY)
	}
	_, _ = fmt.Fprintf(f.IO.Out, "%s %s\n", verb, d.Domain)
	_, _ = fmt.Fprintf(f.IO.Out, "stato:       %s\n", statusText(*d))
	_, _ = fmt.Fprintf(f.IO.Out, "record DNS:  %s\n", dnsText(*d))
	_, _ = fmt.Fprintf(f.IO.Out, "certificato: %s\n", certText(d.Certificate))
	return nil
}

func newDomainListCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:     "list <app>",
		Aliases: []string{"ls"},
		Short:   "Elenca i domini propri dell'app",
		Long:    "Elenca i domini propri dell'app con stato del DNS, record atteso e stato del certificato. L'indirizzo automatico non compare. Serve admin sul repo.",
		Example: "  gs app domain list sito\n  gs app domain list sito --json domain,status",
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
			resp, err := e.gen.ListAppDomainsWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app))
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			items := resp.JSON200.Items
			if jo.Enabled() {
				return jo.Write(f.IO.Out, items, f.IO.OutTTY)
			}
			if len(items) == 0 {
				if f.IO.OutTTY {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "Nessun dominio proprio per %s\n", app)
				}
				return nil
			}
			t := output.NewTable(f.IO.OutTTY, f.IO.Width, "DOMINIO", "STATO", "RECORD DNS", "CERTIFICATO")
			for _, d := range items {
				t.AddRow(d.Domain, statusText(d), dnsText(d), certText(d.Certificate))
			}
			return t.Render(f.IO.Out)
		},
	}
	output.AddJSONFlags(cmd, &jo, DomainFields)
	return cmd
}

func newDomainAddCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "add <app> <dominio>",
		Short: "Aggiungi un dominio proprio all'app",
		Long: "Aggiunge un dominio proprio a un'app con accesso `public`. Il dominio nasce `pending_dns` e il comando " +
			"stampa il record DNS da creare; poi `gs app domain verify`. Errori: 409 se il dominio è già di un'app o l'app " +
			"non è pubblica, 422 se il dominio è riservato (dominio di GitStack, delle app, un IP) o non valido.",
		Example: "  gs app domain add sito shop.retail.it",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			dom, err := domainArg(args[1])
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.AddAppDomainWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app),
				gitstack.AddAppDomainJSONRequestBody{Domain: dom})
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return unexpected(resp.StatusCode())
			}
			return printDomain(f, &jo, "aggiunto", resp.JSON201)
		},
	}
	output.AddJSONFlags(cmd, &jo, DomainFields)
	return cmd
}

func newDomainVerifyCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	cmd := &cobra.Command{
		Use:   "verify <app> <dominio>",
		Short: "Ricontrolla subito il DNS del dominio",
		Long: "Rifà subito il controllo del DNS senza aspettare quello periodico. Esce 0 anche se il DNS non risponde " +
			"ancora: lo stato (`verified`, `pending_dns`, `error`) è nell'output.",
		Example: "  gs app domain verify sito shop.retail.it --json status,statusReason",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			dom, err := domainArg(args[1])
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.RecheckAppDomainWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), dom)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			return printDomain(f, &jo, "ricontrollato", resp.JSON200)
		},
	}
	output.AddJSONFlags(cmd, &jo, DomainFields)
	return cmd
}

func newDomainRemoveCmd(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <app> <dominio>",
		Aliases: []string{"remove"},
		Short:   "Togli un dominio proprio dall'app",
		Long: "Toglie la route e il certificato del dominio (anche quello caricato da te): il dominio torna libero. " +
			"L'indirizzo automatico non cambia. Chiede di riscrivere il dominio (G8, H13): senza terminale serve --yes, altrimenti exit 2.",
		Example: "  gs app domain rm sito shop.retail.it\n  gs app domain rm sito shop.retail.it --yes",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			dom, err := domainArg(args[1])
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Togliere il dominio %s da %s, con route e certificato. Riscrivi `%s` per confermare:", dom, app, dom)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, dom); err != nil {
				return err
			}
			resp, err := e.gen.RemoveAppDomainWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), dom)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(f.IO.ErrOut, "Tolto il dominio %s da %s\n", dom, app)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	return cmd
}

func newDomainCertCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Gestisci il certificato di un dominio proprio",
		Long: "Di default il certificato è emesso e rinnovato da Let's Encrypt. `upload` carica il tuo (modalità `custom`), " +
			"`rm` lo toglie e torna a Let's Encrypt.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(newDomainCertUploadCmd(f), newDomainCertRemoveCmd(f))
	return cmd
}

func readPEM(f *cmdutil.Factory, flag, path string, stdinUsed *bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", cmdutil.UsageErrorf("--%s è obbligatorio", flag)
	}
	var b []byte
	var err error
	if path == "-" {
		if *stdinUsed {
			return "", cmdutil.UsageErrorf("solo uno fra --cert e --key può leggere da stdin (-)")
		}
		*stdinUsed = true
		b, err = io.ReadAll(f.IO.In)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("lettura di --%s: %w", flag, err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", cmdutil.UsageErrorf("--%s: il file è vuoto", flag)
	}
	return string(b), nil
}

func newDomainCertUploadCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	var certPath, keyPath string
	cmd := &cobra.Command{
		Use:   "upload <app> <dominio> --cert <file> --key <file>",
		Short: "Carica il certificato del cliente per un dominio",
		Long: "Porta il dominio in modalità `custom`: Let's Encrypt non emette né rinnova più. Catena in PEM con la foglia per " +
			"prima, chiave privata in PEM senza passphrase (non viene mai stampata né riletta). `-` legge da stdin, per uno solo dei due. " +
			"Errore 422 se la chiave non corrisponde, il certificato non copre il dominio o è scaduto.",
		Example: "  gs app domain cert upload sito shop.retail.it --cert fullchain.pem --key privkey.pem",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			dom, err := domainArg(args[1])
			if err != nil {
				return err
			}
			stdin := false
			cert, err := readPEM(f, "cert", certPath, &stdin)
			if err != nil {
				return err
			}
			key, err := readPEM(f, "key", keyPath, &stdin)
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			resp, err := e.gen.UploadAppDomainCertificateWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), dom,
				gitstack.UploadAppDomainCertificateJSONRequestBody{Certificate: cert, PrivateKey: &key})
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			return printDomain(f, &jo, "certificato caricato per", resp.JSON200)
		},
	}
	cmd.Flags().StringVar(&certPath, "cert", "", "File PEM del certificato con la catena (obbligatorio)")
	cmd.Flags().StringVar(&keyPath, "key", "", "File PEM della chiave privata, senza passphrase (obbligatorio)")
	output.AddJSONFlags(cmd, &jo, DomainFields)
	return cmd
}

func newDomainCertRemoveCmd(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <app> <dominio>",
		Aliases: []string{"remove"},
		Short:   "Togli il certificato del cliente e torna a Let's Encrypt",
		Long: "Cancella certificato e chiave caricati e riavvia l'emissione con Let's Encrypt. 409 se il dominio è già in " +
			"`letsencrypt`. Chiede conferma: --yes senza terminale.",
		Example: "  gs app domain cert rm sito shop.retail.it --yes",
		Args:    cmdutil.NArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			dom, err := domainArg(args[1])
			if err != nil {
				return err
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Togliere il certificato caricato per %s e tornare a Let's Encrypt. Riscrivi `%s` per confermare:", dom, dom)
			if err := cmdutil.ConfirmOrYes(f.IO, yes, prompt, dom); err != nil {
				return err
			}
			resp, err := e.gen.RemoveAppDomainCertificateWithResponse(c.Context(), gitstack.RepoOwnerParam(e.owner), gitstack.RepoNameParam(e.repo), gitstack.AppParam(app), dom)
			if err != nil {
				return err
			}
			if err := e.check(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return unexpected(resp.StatusCode())
			}
			return printDomain(f, &jo, "certificato tolto per", resp.JSON200)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere (obbligatorio senza terminale)")
	output.AddJSONFlags(cmd, &jo, DomainFields)
	return cmd
}

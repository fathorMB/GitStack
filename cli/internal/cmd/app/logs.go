package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
)

// LogFields sono i campi di --json di `gs app logs` (schema AppLogLine): una riga JSON per riga di log.
var LogFields = []string{"time", "copy", "source", "message"}

func newLogsCmd(f *cmdutil.Factory) *cobra.Command {
	var (
		since, until, grep, copyName, source string
		follow                               bool
		limit                                int
		jo                                   output.JSONOptions
	)
	cmd := &cobra.Command{
		Use:   "logs <app>",
		Short: "Legge i log dell'app, in diretta o dalla cronologia",
		Long: "Righe di tutte le copie (o di una, --copy) mescolate agli eventi della piattaforma (deploy, salute, " +
			"riavvii). --follow tiene aperta la connessione. I segreti sono mascherati ***. Con --json una riga JSON per " +
			"riga di log. Serve `write` sul repo: i log possono contenere dati dell'app.",
		Example: "  gs app logs web --since 5m --grep panic --repo acme/handbook\n  gs app logs web --follow --copy web-2",
		Args:    cmdutil.NArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			app, err := appName(args)
			if err != nil {
				return err
			}
			switch source {
			case "", "all", "app", "platform":
			default:
				return cmdutil.UsageErrorf("--source deve essere all, app o platform")
			}
			if limit < 0 {
				return cmdutil.UsageErrorf("--limit non può essere negativo")
			}
			e, err := newEnv(f)
			if err != nil {
				return err
			}
			q := url.Values{"format": {"ndjson"}}
			set := func(k, v string) {
				if v != "" {
					q.Set(k, v)
				}
			}
			set("since", since)
			set("until", until)
			set("grep", grep)
			set("copy", copyName)
			set("source", source)
			if follow {
				q.Set("follow", "true")
			}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			path := fmt.Sprintf("/repos/%s/%s/apps/%s/logs?%s", url.PathEscape(e.owner), url.PathEscape(e.repo), url.PathEscape(app), q.Encode())
			resp, err := e.raw.Do(c.Context(), http.MethodGet, path, nil, http.Header{"Accept": {"application/x-ndjson"}})
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 64*1024), 1<<20)
			for sc.Scan() {
				raw := sc.Bytes()
				if len(raw) == 0 {
					continue
				}
				var l gitstack.AppLogLine
				if err := json.Unmarshal(raw, &l); err != nil {
					return fmt.Errorf("riga di log non valida: %w", err)
				}
				if jo.Enabled() {
					if err := jo.Write(f.IO.Out, l, f.IO.OutTTY); err != nil {
						return err
					}
					continue
				}
				who := "platform"
				if l.Copy != nil && *l.Copy != "" {
					who = *l.Copy
				}
				_, _ = fmt.Fprintf(f.IO.Out, "%s %s  %s\n", l.Time.Local().Format("15:04:05"), who, l.Message)
			}
			if err := sc.Err(); err != nil && c.Context().Err() == nil {
				return err
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&since, "since", "", "Durata (5m, 1h, 7d) o istante RFC 3339")
	fl.StringVar(&until, "until", "", "Istante RFC 3339 di fine")
	fl.StringVar(&grep, "grep", "", "Mostra solo le righe che contengono il testo")
	fl.StringVar(&copyName, "copy", "", "Solo una copia (per esempio web-1)")
	fl.StringVar(&source, "source", "", "all, app o platform")
	fl.BoolVarP(&follow, "follow", "f", false, "Resta in ascolto di nuove righe")
	fl.IntVar(&limit, "limit", 0, "Al massimo N righe, le più recenti")
	output.AddJSONFlags(cmd, &jo, LogFields)
	return cmd
}

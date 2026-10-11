package auth

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/config"
)

// newGitCredentialCmd è il credential helper di git (`gs auth git-credential
// get|store|erase`): lo chiama git, non l'utente. `get` risponde con il token
// dell'istanza configurata che corrisponde a host e protocollo; `store` e
// `erase` non fanno niente (il token si gestisce con login e logout). Per un
// host sconosciuto non risponde, così git passa al helper successivo.
func newGitCredentialCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:    "git-credential <get|store|erase>",
		Short:  "Credential helper di git (uso interno)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if args[0] != "get" {
				return nil
			}
			req := readCredentialRequest(f)
			if req["host"] == "" {
				return nil
			}
			proto := req["protocol"]
			if proto == "" {
				proto = "https"
			}
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			hosts, err := cfg.Hosts()
			if err != nil {
				return err
			}
			// GS_HOST + GS_TOKEN valgono anche senza configurazione.
			if h := normalizeHost(f.Getenv(cmdutil.EnvHost)); h != "" && f.Getenv(config.EnvToken) != "" && !contains(hosts, h) {
				hosts = append(hosts, h)
			}
			ts, err := f.Tokens()
			if err != nil {
				return err
			}
			stored, err := f.StoredTokens()
			if err != nil {
				return err
			}
			// GS_TOKEN vale solo per l'istanza di GS_HOST (o la predefinita), mai per le altre.
			envTarget := normalizeHost(f.Getenv(cmdutil.EnvHost))
			if envTarget == "" {
				envTarget, _ = cfg.DefaultHost()
			}
			for _, h := range hosts {
				if !hostMatches(h, proto, req["host"]) {
					continue
				}
				var tok string
				if f.Getenv(config.EnvToken) != "" && h == envTarget {
					tok, _, err = ts.Token(h)
				} else {
					tok, _, err = stored.Token(h)
				}
				if err != nil || tok == "" {
					return nil
				}
				user := "gs"
				if hc, ok, err := cfg.Host(h); err == nil && ok && hc.User != "" {
					user = hc.User
				}
				_, _ = fmt.Fprintf(f.IO.Out, "username=%s\npassword=%s\n", user, tok)
				return nil
			}
			return nil
		},
	}
}

// readCredentialRequest legge le righe chiave=valore che git manda su stdin,
// fino alla riga vuota.
func readCredentialRequest(f *cmdutil.Factory) map[string]string {
	req := map[string]string{}
	sc := bufio.NewScanner(f.IO.In)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			req[k] = v
		}
	}
	return req
}

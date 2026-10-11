// Package auth è il gruppo `gs auth`: login (token, --with-token, --web),
// status, logout, setup-git e il credential helper di git.
package auth

import (
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// NewCmd restituisce il comando padre `gs auth`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Autenticazione: login, logout, stato e token",
		Long: "Autenticazione: login, logout, stato e token.\n\n" +
			"Per agenti e CI non serve nessun login: bastano le variabili d'ambiente\n" +
			"GS_HOST (istanza) e GS_TOKEN (token), che hanno la precedenza su tutto.",
		Args: cmdutil.NoArgs,
		RunE: cmdutil.GroupRun,
	}
	cmd.AddCommand(
		newLoginCmd(f),
		newStatusCmd(f),
		newLogoutCmd(f),
		newSetupGitCmd(f),
		newGitCredentialCmd(f),
	)
	return cmd
}

// normalizeHost porta un'istanza digitata dall'utente alla forma conservata:
// host[:porta] in minuscolo, senza percorso; lo schema https:// si toglie
// (è il predefinito), http:// si conserva.
func normalizeHost(s string) string {
	s = strings.TrimSpace(s)
	scheme := ""
	switch {
	case strings.HasPrefix(strings.ToLower(s), "https://"):
		s = s[len("https://"):]
	case strings.HasPrefix(strings.ToLower(s), "http://"):
		s = s[len("http://"):]
		scheme = "http://"
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return ""
	}
	return scheme + strings.ToLower(s)
}

// splitHost scompone un'istanza conservata in schema (https o http) e
// host[:porta].
func splitHost(h string) (scheme, hostport string) {
	if rest, ok := strings.CutPrefix(h, "http://"); ok {
		return "http", rest
	}
	return "https", strings.TrimPrefix(h, "https://")
}

// webURL è l'indirizzo base (schema://host) di un'istanza.
func webURL(h string) string {
	scheme, hp := splitHost(h)
	return scheme + "://" + hp
}

// hostMatches dice se l'istanza conservata h risponde a una richiesta git
// con quel protocollo e quell'host[:porta].
func hostMatches(h, protocol, hostport string) bool {
	scheme, hp := splitHost(h)
	return scheme == protocol && strings.EqualFold(hp, hostport)
}

// recommendedScopes sono gli scope che `gs` usa di norma, dal catalogo
// TokenScope di api/openapi.yaml.
var recommendedScopes = []string{"read:user", "read:org", "read:resource", "write:resource"}

// newTokenURL è la pagina della UI per creare un token precompilato (percorso
// fissato in web/README.md, M-07).
func newTokenURL(h string) string {
	q := url.Values{}
	q.Set("name", "gs")
	q.Set("scopes", strings.Join(recommendedScopes, ","))
	q.Set("expires", "90")
	// Le virgole e i due punti restano leggibili nella query.
	enc := strings.NewReplacer("%2C", ",", "%3A", ":").Replace(q.Encode())
	return webURL(h) + "/settings/tokens/new?" + enc
}

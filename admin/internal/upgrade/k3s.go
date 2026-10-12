package upgrade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Regola N10: ogni versione di GitStack dichiara la versione di k3s con cui è
// provata in deploy/k3s-version (una riga, es. v1.36.4+k3s1). Lo leggono
// deploy/install.sh e `gitstack upgrade`.
//
//   - cambia solo la patch (o la revisione k3sN): `gitstack upgrade` la
//     aggiorna da solo, dopo il backup preventivo;
//   - cambia la minore: serve `gitstack upgrade --k3s`, una minore alla
//     volta; senza il flag l'upgrade è rifiutato con la spiegazione;
//   - il ritorno indietro di k3s non è automatico (docs/operations.md).

// K3sVersionFile è il percorso del file nel repository sorgente.
const K3sVersionFile = "deploy/k3s-version"

// K3sInstallURL è lo script ufficiale di installazione di k3s.
const K3sInstallURL = "https://get.k3s.io"

// K3sInstallExec è INSTALL_K3S_EXEC con cui install.sh installa k3s: va
// ripetuto identico, o l'installer riscriverebbe l'unità systemd senza
// --write-kubeconfig-mode 644. Un test lo confronta con deploy/install.sh.
const K3sInstallExec = "server --write-kubeconfig-mode 644"

var k3sVersionRe = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)\+k3s(\d+)`)

// K3sVersion è una versione di k3s: v<major>.<minor>.<patch>+k3s<rev>.
type K3sVersion struct {
	Major, Minor, Patch, Rev int
}

// ParseK3sVersion legge una versione nuda (v1.36.4+k3s1) oppure l'output di
// `k3s --version` ("k3s version v1.36.4+k3s1 (abcdef12)").
func ParseK3sVersion(s string) (K3sVersion, error) {
	m := k3sVersionRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return K3sVersion{}, fmt.Errorf("versione di k3s non riconosciuta: %q (attesa vX.Y.Z+k3sN)", strings.TrimSpace(s))
	}
	var v K3sVersion
	for i, p := range []*int{&v.Major, &v.Minor, &v.Patch, &v.Rev} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return K3sVersion{}, err
		}
		*p = n
	}
	return v, nil
}

// String è la forma di INSTALL_K3S_VERSION.
func (v K3sVersion) String() string {
	return fmt.Sprintf("v%d.%d.%d+k3s%d", v.Major, v.Minor, v.Patch, v.Rev)
}

// MinorLine è la linea minore, es. "1.36".
func (v K3sVersion) MinorLine() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

func (v K3sVersion) cmp(o K3sVersion) int {
	for _, p := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}, {v.Rev, o.Rev}} {
		switch {
		case p[0] < p[1]:
			return -1
		case p[0] > p[1]:
			return 1
		}
	}
	return 0
}

// K3sAction è cosa fare di k3s in questo upgrade.
type K3sAction int

// Valori di K3sAction.
const (
	K3sKeep  K3sAction = iota // stessa versione, o nessuna dichiarata: niente
	K3sPatch                  // solo patch/revisione: in automatico
	K3sMinor                  // cambia la minore: con --k3s
)

func (a K3sAction) String() string {
	switch a {
	case K3sPatch:
		return "patch"
	case K3sMinor:
		return "minore"
	}
	return "nessuna"
}

// K3sPlan è la decisione.
type K3sPlan struct {
	Action   K3sAction
	From, To K3sVersion
}

// Needed dice se k3s va aggiornato.
func (p K3sPlan) Needed() bool { return p.Action != K3sKeep }

// PlanK3s decide cosa fare di k3s, in funzione pura: installed è la versione in
// esecuzione, declared quella dichiarata dalla destinazione ("" = la
// destinazione non ne dichiara: k3s non si tocca), allowMinor è --k3s.
// Gli errori sono RefusedError: nessuna modifica è stata fatta.
func PlanK3s(installed, declared string, allowMinor bool) (K3sPlan, error) {
	if strings.TrimSpace(declared) == "" {
		return K3sPlan{}, nil
	}
	to, err := ParseK3sVersion(declared)
	if err != nil {
		return K3sPlan{}, refused("la destinazione dichiara una versione di k3s non valida in %s: %v", K3sVersionFile, err)
	}
	from, err := ParseK3sVersion(installed)
	if err != nil {
		return K3sPlan{}, refused("versione di k3s installata non leggibile: %v", err)
	}
	p := K3sPlan{From: from, To: to}
	switch c := from.cmp(to); {
	case c == 0:
		return p, nil
	case c > 0:
		return p, refused("la destinazione è provata con k3s %s, ma è installato %s, più recente: k3s non si porta indietro da solo. "+
			"Per tornare a una versione precedente segui la procedura «Rolling back k3s» in docs/operations.md", to, from)
	}
	if from.Major == to.Major && from.Minor == to.Minor {
		p.Action = K3sPatch
		return p, nil
	}
	p.Action = K3sMinor
	if from.Major != to.Major || to.Minor-from.Minor != 1 {
		return p, refused("la destinazione richiede k3s %s, installato %s: k3s si aggiorna una minore alla volta (%s → %s → …). "+
			"Aggiorna prima a una versione di GitStack provata con la minore intermedia, con `gitstack upgrade --k3s`", to, from, from.MinorLine(), to.MinorLine())
	}
	if !allowMinor {
		return p, refused("la destinazione richiede l'aggiornamento di k3s da %s a %s (cambia la minore, %s → %s). "+
			"Non lo faccio senza il tuo consenso: rilancia con `gitstack upgrade --k3s` (backup preventivo, poi k3s, poi GitStack; "+
			"il servizio si interrompe per alcuni minuti). Con --dry-run --k3s vedi cosa farebbe. Il ritorno indietro di k3s non è automatico: docs/operations.md",
			from, to, from.MinorLine(), to.MinorLine())
	}
	return p, nil
}

// Step è un passo dell'aggiornamento mutante.
type Step string

// Passi, nell'ordine in cui girano.
const (
	StepBackup Step = "backup"
	StepK3s    Step = "k3s"
	StepChart  Step = "gitstack"
)

// Steps è l'ordine dei passi che modificano il sistema: il backup viene
// sempre per primo, poi (se serve) k3s, poi GitStack. Run lo segue
// alla lettera.
func Steps(p K3sPlan) []Step {
	if p.Needed() {
		return []Step{StepBackup, StepK3s, StepChart}
	}
	return []Step{StepBackup, StepChart}
}

// installerScript è il comando che installa la versione dichiarata. La
// versione è validata da ParseK3sVersion: nessun carattere di shell.
func installerScript(url string, to K3sVersion) string {
	return fmt.Sprintf("curl -sfL %s | INSTALL_K3S_VERSION=%s INSTALL_K3S_EXEC='%s' sh -", shellQuote(url), to, K3sInstallExec)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// installedK3s legge la versione di k3s in esecuzione (`k3s --version`).
func (o *Options) installedK3s(ctx context.Context) (string, error) {
	out, err := o.Runner.Run(ctx, nil, "k3s", "--version")
	if err != nil {
		return "", fmt.Errorf("k3s --version: %w", err)
	}
	v, err := ParseK3sVersion(string(out))
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// updateK3s installa la versione e aspetta che i servizi tornino sani. Il
// restart di k3s ferma per poco tempo tutti i servizi.
func (o *Options) updateK3s(ctx context.Context, p K3sPlan) error {
	url := o.K3sInstallURL
	if url == "" {
		url = K3sInstallURL
	}
	out, err := o.Runner.Run(ctx, nil, "sh", "-c", installerScript(url, p.To))
	if err != nil {
		return fmt.Errorf("installazione di k3s %s: %w: %s", p.To, err, tail(string(out)))
	}
	o.logf("==> Aspetto che k3s e tutti i servizi siano di nuovo sani (al massimo %s)", o.timeout())
	if err := o.waitHealthy(ctx, o.timeout()); err != nil {
		return fmt.Errorf("dopo l'aggiornamento di k3s: %w", err)
	}
	if got, err := o.installedK3s(ctx); err != nil {
		return fmt.Errorf("k3s installato, ma la versione non è verificabile: %w", err)
	} else if got != p.To.String() {
		return fmt.Errorf("k3s risulta %s invece di %s", got, p.To)
	}
	return nil
}

// DeclaredK3sVersion legge deploy/k3s-version del ref dato (per `gitstack
// status`): mai un errore bloccante, quindi restituisce "" se non risponde.
func DeclaredK3sVersion(ctx context.Context, hc *http.Client, rawBase, repo, ref string) (string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	if rawBase == "" {
		rawBase = "https://raw.githubusercontent.com"
	}
	if repo == "" {
		repo = DefaultRepo
	}
	if ref == "" {
		ref = DefaultRef
	}
	if !refRe.MatchString(ref) {
		return "", fmt.Errorf("riferimento %q non valido", ref)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(rawBase, "/")+"/"+repo+"/"+ref+"/"+K3sVersionFile, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d", K3sVersionFile, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	v, err := ParseK3sVersion(string(b))
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

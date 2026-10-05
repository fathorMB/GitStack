// Package receiverules applica lato server le regole alla ricezione di un
// push (M-03/J), uguali per HTTPS e SSH perché stanno in un hook pre-receive
// di git, non nei due trasporti:
//
//   - R6: push rifiutato se contiene un blob oltre la soglia, con un messaggio
//     che nomina file e dimensione; avviso (non blocco) se il repo supera la
//     soglia di avviso;
//   - R9: sul branch principale, se la protezione è attiva, rifiutati
//     force-push ed eliminazione; gli altri branch sono liberi.
//
// R10 (repo archiviato) è prima, in access.Authorizer: il push non arriva
// nemmeno a git.
//
// L'hook è uno script sh unico, installato una volta in una directory e
// attivato con `-c core.hooksPath=<dir>` sul comando receive-pack: non si
// scrive niente dentro ai repo. Le soglie e il branch protetto arrivano con
// variabili d'ambiente GITSTACK_* (le GIT_* si tolgono dall'ambiente del
// servizio). L'hook guarda solo i blob di un push, non i contenuti: i file
// puntatore di Git LFS (R8) sono piccoli e passano, quindi non è
// d'ostacolo a un LFS futuro.
package receiverules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fathorMB/GitStack/services/git/internal/access"
)

// Valori di default (R6).
const (
	// DefaultMaxBlobBytes: 100 MiB per file.
	DefaultMaxBlobBytes int64 = 100 << 20
	// DefaultWarnRepoBytes: avviso oltre 5 GiB di repo.
	DefaultWarnRepoBytes int64 = 5 << 30
)

// Variabili d'ambiente lette dall'hook.
const (
	envMaxBlob = "GITSTACK_RULE_MAX_BLOB_BYTES"
	envWarn    = "GITSTACK_RULE_WARN_REPO_BYTES"
	envProtect = "GITSTACK_RULE_PROTECT_BRANCH"
)

// Limits sono le soglie dell'installazione; zero = regola disattivata.
type Limits struct {
	MaxBlobBytes  int64
	WarnRepoBytes int64
}

// DefaultLimits sono le soglie di prodotto.
func DefaultLimits() Limits {
	return Limits{MaxBlobBytes: DefaultMaxBlobBytes, WarnRepoBytes: DefaultWarnRepoBytes}
}

// Rules è l'hook installato più le soglie. Il valore nil è valido: nessuna
// regola (utile nei test che non le provano).
type Rules struct {
	dir    string
	limits Limits
}

// Install scrive l'hook pre-receive in dir (creata se serve) e ritorna le
// regole. Va chiamata all'avvio: riscrive lo script a ogni avvio, così un
// aggiornamento del servizio aggiorna anche l'hook.
func Install(dir string, l Limits) (*Rules, error) {
	if dir == "" {
		return nil, errors.New("receiverules: directory degli hook vuota")
	}
	if l.MaxBlobBytes < 0 || l.WarnRepoBytes < 0 {
		return nil, errors.New("receiverules: le soglie non possono essere negative")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("receiverules: %w", err)
	}
	// 0o755: git lo esegue, e un hook non eseguibile viene ignorato.
	if err := os.WriteFile(filepath.Join(abs, "pre-receive"), []byte(preReceive), 0o755); err != nil { //nolint:gosec // l'hook deve essere eseguibile
		return nil, fmt.Errorf("receiverules: %w", err)
	}
	return &Rules{dir: abs, limits: l}, nil
}

// GitArgs sono gli argomenti globali di git da mettere prima di
// `receive-pack` perché usi l'hook.
func (r *Rules) GitArgs() []string {
	if r == nil {
		return nil
	}
	// denyDeleteCurrent=ignore: l'eliminazione del branch principale la decide
	// la protezione di GitStack (R9), non il default di git per HEAD.
	return []string{"-c", "core.hooksPath=" + filepath.ToSlash(r.dir), "-c", "receive.denyDeleteCurrent=ignore"}
}

// Env sono le variabili d'ambiente per il processo receive-pack. protected è
// il nome del branch principale se la sua protezione è attiva, altrimenti
// vuoto.
func (r *Rules) Env(protected string) []string {
	if r == nil {
		return nil
	}
	return []string{
		envMaxBlob + "=" + strconv.FormatInt(r.limits.MaxBlobBytes, 10),
		envWarn + "=" + strconv.FormatInt(r.limits.WarnRepoBytes, 10),
		envProtect + "=" + protected,
	}
}

// preReceive è l'hook. Rifiuta con exit 1; i messaggi su stderr arrivano al
// client come «remote: ...» (sideband) sia da HTTPS sia da SSH.
const preReceive = `#!/bin/sh
# Hook pre-receive di GitStack (R6, R9): generato dal servizio git, non modificare.
LC_ALL=C
export LC_ALL
zero=0000000000000000000000000000000000000000
max="${GITSTACK_RULE_MAX_BLOB_BYTES:-0}"
warn="${GITSTACK_RULE_WARN_REPO_BYTES:-0}"
protect="${GITSTACK_RULE_PROTECT_BRANCH:-}"
status=0
tab=$(printf '\t')

while read -r old new ref; do
  # R9: il branch principale non si riscrive e non si elimina.
  if [ -n "$protect" ] && [ "$ref" = "refs/heads/$protect" ]; then
    if [ "$new" = "$zero" ]; then
      echo "gitstack: push rifiutato: il branch principale '$protect' è protetto e non si può eliminare." >&2
      echo "gitstack: chi ha il ruolo admin può disattivare la protezione nelle impostazioni del repository." >&2
      status=1
      continue
    fi
    if [ "$old" != "$zero" ] && ! git merge-base --is-ancestor "$old" "$new" 2>/dev/null; then
      echo "gitstack: push rifiutato: il branch principale '$protect' è protetto e non accetta force-push (la storia non si riscrive)." >&2
      echo "gitstack: chi ha il ruolo admin può disattivare la protezione nelle impostazioni del repository." >&2
      status=1
      continue
    fi
  fi

  # R6: nessun blob oltre la soglia fra gli oggetti nuovi del push.
  if [ "$new" != "$zero" ] && [ "$max" -gt 0 ]; then
    big=$(git rev-list --objects "$new" --not --all 2>/dev/null |
      git cat-file --batch-check='%(objecttype) %(objectsize) %(rest)' 2>/dev/null |
      awk -v max="$max" '$1 == "blob" && $2 + 0 > max { sz = $2; $1 = ""; $2 = ""; sub(/^ +/, ""); printf "%s\t%d\t%.1f\n", $0, sz, sz / 1048576 }' |
      head -n 5)
    if [ -n "$big" ]; then
      limit_mb=$(awk -v m="$max" 'BEGIN { printf "%.1f", m / 1048576 }')
      echo "gitstack: push rifiutato: ci sono file oltre il limite di $limit_mb MB ($max byte) per file:" >&2
      printf '%s\n' "$big" | while IFS="$tab" read -r path size mb; do
        echo "gitstack:   '$path' pesa $mb MB ($size byte)" >&2
      done
      echo "gitstack: togli i file dalla storia (ad esempio con git rm e un nuovo commit, o riscrivendo i commit) e riprova." >&2
      status=1
    fi
  fi
done

if [ "$status" -ne 0 ]; then
  exit 1
fi

# R6: oltre la soglia di avviso il push passa, con un avviso.
if [ "$warn" -gt 0 ]; then
  kb=$(du -sk . 2>/dev/null | cut -f1)
  if [ -n "$kb" ] && [ "$kb" -gt $((warn / 1024)) ]; then
    mb=$((kb / 1024))
    limit=$((warn / 1048576))
    echo "gitstack: avviso: questo repository pesa circa $mb MB, oltre la soglia di $limit MB. Il push è accettato, ma conviene ridurne la dimensione." >&2
  fi
fi
exit 0
`

// ProtectedBranch è il branch da proteggere per il repo (R9): il suo branch
// principale se la protezione è attiva, altrimenti stringa vuota.
func ProtectedBranch(ref access.RepoRef) string {
	if !ref.ProtectDefaultBranch {
		return ""
	}
	if ref.DefaultBranch == "" {
		return "main"
	}
	return ref.DefaultBranch
}

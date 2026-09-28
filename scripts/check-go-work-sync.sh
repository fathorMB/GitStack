#!/usr/bin/env bash
# Controllo di CI: il workspace Go (go.work) deve restare sincronizzato.
# `go work sync` allinea le dipendenze condivise tra tutti i moduli del
# workspace nei rispettivi go.mod/go.sum (ed eventualmente in go.work.sum);
# fallisce se, dopo averlo lanciato, l'albero di lavoro risulta sporco su un
# go.mod, un go.sum o go.work.sum: vuol dire che un modulo nuovo o un cambio
# di dipendenza in un modulo qualsiasi del workspace ha alzato una
# dipendenza indiretta condivisa senza che qualcuno abbia rilanciato
# `go work sync` e committato il risultato.
#
# Deliberatamente separato da scripts/check-api-generated.sh: `go work sync`
# tocca go.mod/go.sum di TUTTI i moduli del workspace, anche quelli che un
# item non ha toccato (succede quando un modulo nuovo o una sua dipendenza
# alza una dipendenza indiretta condivisa) e non ha nulla a che fare con
# l'allineamento del codice generato al contratto OpenAPI. Vedi GIT-20.
#
# Usa `git status --porcelain` (nessun effetto sull'index) invece di
# `git add`, per non toccare lo stato che l'eventuale commit successivo si
# aspetta.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

go work sync

status="$(git status --porcelain -- ':(glob)**/go.mod' ':(glob)**/go.sum' go.work go.work.sum)"
if [ -n "$status" ]; then
  echo
  echo "Il workspace Go non è sincronizzato con go.work." >&2
  echo "Lancia 'go work sync' dalla radice e committa i go.mod/go.sum elencati sotto." >&2
  echo
  echo "$status"
  exit 1
fi

echo "Workspace Go sincronizzato."

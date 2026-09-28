#!/usr/bin/env bash
# Esegue un comando in ciascun modulo Go del workspace, dalla radice del
# monorepo. Un workspace Go multi-modulo non supporta pattern "./..." dalla
# radice (il tool go richiede di stare dentro un modulo): questo script
# risolve il problema iterando sui moduli.
#
# La lista dei moduli non è scritta a mano: viene ricavata da `go list -m`,
# cioè dal workspace stesso (go.work), così README e CI restano allineati a
# go.work anche quando qualcuno aggiunge o toglie un modulo.
set -euo pipefail

if [ "$#" -eq 0 ]; then
  echo "uso: $0 <comando...>" >&2
  exit 2
fi

mapfile -t modules < <(go list -m -f '{{.Dir}}')

if [ "${#modules[@]}" -eq 0 ]; then
  echo "go-each.sh: nessun modulo trovato nel workspace (go list -m non ha restituito nulla)" >&2
  exit 1
fi

status=0
for module_dir in "${modules[@]}"; do
  echo "==> ${module_dir}: $*"
  if ! (cd "${module_dir}" && "$@"); then
    status=1
  fi
done

exit "${status}"

#!/usr/bin/env bash
# Esegue un comando in ciascun modulo Go del workspace (vedi go.work),
# dalla radice del monorepo. Un modulo Go multi-radice non supporta
# pattern "./..." dalla radice (il tool go richiede di stare dentro un
# modulo): questo script è l'unica fonte della lista dei moduli, usata sia
# dal README sia dalla pipeline CI, così restano sempre allineati.
set -euo pipefail

if [ "$#" -eq 0 ]; then
  echo "uso: $0 <comando...>" >&2
  exit 2
fi

modules=(
  cli
  services/core
  services/gateway
  services/git
  services/identity
)

status=0
for module in "${modules[@]}"; do
  echo "==> ${module}: $*"
  if ! (cd "${module}" && "$@"); then
    status=1
  fi
done

exit "${status}"

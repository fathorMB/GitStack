#!/usr/bin/env bash
# Controllo di CI: il codice generato (client/go, client/ts e l'interfaccia
# server di gateway e core in services/gateway e services/core) deve essere
# allineato al contratto OpenAPI (api/openapi.yaml). Rigenera e fallisce se
# l'albero di lavoro
# risulta sporco dopo la rigenerazione: vuol dire che qualcuno ha cambiato il
# contratto (o modificato a mano il codice generato) senza rilanciare
# ./scripts/generate-api.sh. Usa `git status --porcelain` (nessun effetto
# sull'index) invece di `git add`, per non toccare lo stato che l'eventuale
# commit successivo si aspetta.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

./scripts/generate-api.sh

status="$(git status --porcelain -- api client services/gateway services/core)"
if [ -n "$status" ]; then
  echo
  echo "Il codice generato non è allineato al contratto OpenAPI." >&2
  echo "Rilancia ./scripts/generate-api.sh e committa il risultato." >&2
  echo
  echo "$status"
  exit 1
fi

echo "Codice generato allineato al contratto OpenAPI."

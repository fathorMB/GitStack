#!/usr/bin/env bash
# Controllo di CI: il codice generato (client/go, client/ts) deve essere
# allineato al contratto OpenAPI (api/openapi.yaml). Rigenera e fallisce se
# il repository risulta sporco dopo la rigenerazione: vuol dire che qualcuno
# ha cambiato il contratto (o modificato a mano il codice generato) senza
# rilanciare ./scripts/generate-api.sh.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

./scripts/generate-api.sh

git add -A -- api client
if ! git diff --cached --quiet -- api client; then
  echo
  echo "Il codice generato non è allineato al contratto OpenAPI." >&2
  echo "Rilancia ./scripts/generate-api.sh e committa il risultato." >&2
  echo
  git diff --cached --stat -- api client
  exit 1
fi

echo "Codice generato allineato al contratto OpenAPI."

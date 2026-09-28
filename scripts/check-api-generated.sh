#!/usr/bin/env bash
# Controllo di CI: il codice generato (client/go/*.gen.go,
# client/ts/src/generated/**, e internal/openapi/api.gen.go di gateway e
# core) deve essere allineato al contratto OpenAPI (api/openapi.yaml).
# Rigenera e fallisce se l'albero di lavoro risulta sporco dopo la
# rigenerazione, ma SOLO su questi file: vuol dire che qualcuno ha cambiato
# il contratto (o modificato a mano il codice generato) senza rilanciare
# ./scripts/generate-api.sh.
#
# Deliberatamente ristretto al solo codice generato, non a tutte le cartelle
# che generate-api.sh attraversa (client/go, client/ts, services/gateway,
# services/core includono anche go.mod/go.sum, che `go work sync` può
# modificare per ragioni indipendenti dal contratto OpenAPI): vedi
# scripts/check-go-work-sync.sh per quello, e GIT-20.
#
# Usa `git status --porcelain` (nessun effetto sull'index) invece di
# `git add`, per non toccare lo stato che l'eventuale commit successivo si
# aspetta.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

./scripts/generate-api.sh

generated_paths=(
  'client/go/*.gen.go'
  'client/ts/src/generated'
  'services/gateway/internal/openapi/api.gen.go'
  'services/core/internal/openapi/api.gen.go'
)

status="$(git status --porcelain -- "${generated_paths[@]}")"
if [ -n "$status" ]; then
  echo
  echo "Il codice generato non è allineato al contratto OpenAPI." >&2
  echo "Rilancia ./scripts/generate-api.sh e committa il risultato." >&2
  echo
  echo "$status"
  exit 1
fi

echo "Codice generato allineato al contratto OpenAPI."

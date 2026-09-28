#!/usr/bin/env bash
# Rigenera il client Go (client/go) e il client TypeScript (client/ts) a
# partire dall'unico contratto OpenAPI (api/openapi.yaml), e non fa altro:
# il codice generato non si modifica a mano, si rigenera con questo script.
#
# Uso, dalla radice del monorepo:
#   go work sync
#   ./scripts/generate-api.sh
#
# Requisiti: Go (workspace già sincronizzato con `go work sync`), Node.js e
# pnpm. Nessun'altra dipendenza globale: gli strumenti di generazione (Go
# `oapi-codegen`, TS `@hey-api/openapi-ts`) sono dichiarati rispettivamente
# come tool dependency di client/go/go.mod e come devDependency di
# client/ts/package.json, quindi vengono risolti in versione pinnata.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

echo "==> Lint del contratto OpenAPI (api/openapi.yaml)"
npx --yes @redocly/cli@2 lint api/openapi.yaml --config api/redocly.yaml

echo "==> Generazione client Go (client/go)"
(cd client/go && go generate ./...)

echo "==> Generazione client TypeScript (client/ts)"
(cd client/ts && pnpm install --frozen-lockfile && pnpm run generate)

echo "==> Fatto. Verifica con: git status client/go client/ts"

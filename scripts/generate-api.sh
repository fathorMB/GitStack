#!/usr/bin/env bash
# Rigenera il client Go (client/go), il client TypeScript (client/ts) e
# l'interfaccia server di gateway e core (services/gateway, services/core) a
# partire dall'unico contratto OpenAPI (api/openapi.yaml), e non fa altro: il
# codice generato non si modifica a mano, si rigenera con questo script.
#
# Uso, dalla radice del monorepo:
#   go work sync
#   ./scripts/generate-api.sh
#
# Requisiti: Go (workspace già sincronizzato con `go work sync`), Node.js e
# pnpm. Nessun'altra dipendenza globale: gli strumenti di generazione (Go
# `oapi-codegen`, TS `@hey-api/openapi-ts`) sono dichiarati rispettivamente
# come tool dependency di client/go/go.mod, services/gateway/go.mod e
# services/core/go.mod, e come devDependency di client/ts/package.json,
# quindi vengono risolti in versione pinnata. Il lint del contratto
# (`@redocly/cli`) è invece pinnato in questo script a una versione esatta
# (non solo la major), per riproducibilità.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

echo "==> Lint del contratto OpenAPI (api/openapi.yaml)"
npx --yes @redocly/cli@2.54.3 lint api/openapi.yaml --config api/redocly.yaml

echo "==> Generazione client Go (client/go)"
(cd client/go && go generate ./...)

echo "==> Generazione client TypeScript (client/ts)"
(cd client/ts && pnpm install --frozen-lockfile && pnpm run generate)

echo "==> Generazione interfaccia server del gateway (services/gateway)"
(cd services/gateway && go generate ./...)

echo "==> Generazione interfaccia server di core (services/core)"
(cd services/core && go generate ./...)

echo "==> Fatto. Verifica con: git status client/go client/ts/src/generated services/gateway/internal/openapi services/core/internal/openapi"

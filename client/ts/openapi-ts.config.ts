import { defineConfig } from "@hey-api/openapi-ts";

// Rigenera il client TS dal contratto OpenAPI unico (../../api/openapi.yaml).
// Non modificare a mano l'output in src/generated/: rilanciare `pnpm run
// generate` (vedi anche scripts/generate-api.sh nella radice del monorepo).
export default defineConfig({
  input: "../../api/openapi.yaml",
  output: "src/generated",
  plugins: ["@hey-api/client-fetch", "@hey-api/typescript", "@hey-api/sdk"],
});

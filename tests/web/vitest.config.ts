import { defineConfig } from "../../src/factorforge/applications/console/web/node_modules/vitest/dist/config.js";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL("../..", import.meta.url));
export default defineConfig({
  root,
  test: { include: ["tests/web/*.test.ts"], environment: "node" },
  resolve: {
    alias: {
      react: fileURLToPath(
        new URL(
          "../../src/factorforge/applications/console/web/node_modules/react",
          import.meta.url,
        ),
      ),
      "lightweight-charts": fileURLToPath(
        new URL(
          "../../src/factorforge/applications/console/web/node_modules/lightweight-charts/dist/lightweight-charts.production.mjs",
          import.meta.url,
        ),
      ),
    },
  },
});

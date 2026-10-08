import { defineConfig } from "../../src/factorforge/applications/console/web/node_modules/@playwright/test/index.mjs";
export default defineConfig({
  testDir: ".",
  testMatch: "*.browser.ts",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30000,
  outputDir: "../../runtime/web-test-results",
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:18085",
    viewport: { width: 1440, height: 1000 },
    trace: "retain-on-failure",
  },
  webServer: {
    command: "go run ./tests/web/fixture -root . -listen 127.0.0.1:18085",
    cwd: "../..",
    url: "http://127.0.0.1:18085/login",
    reuseExistingServer: false,
    timeout: 30000,
  },
});

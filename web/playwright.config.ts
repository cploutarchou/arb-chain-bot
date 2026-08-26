import { defineConfig } from "@playwright/test";

// E2E critical path (SKILL §72 / MASTER_PLAN T-045). The suite runs the
// real Go backend (built arbd, PAPER mode, in-memory strategy fallback)
// behind the Next dev proxy, so what is tested is the actual login →
// console → control flow, not mocks. Start both with scripts/e2e.sh.
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  retries: 0,
  workers: 1, // one shared backend; keep flows serialized
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://127.0.0.1:3100",
    // The sandbox/CI proxy must not intercept localhost.
    proxy: undefined,
    // Environments with a system Chromium (e.g. a pre-provisioned
    // /opt/pw-browsers/chromium) point here instead of downloading.
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM }
      : {},
  },
  webServer: process.env.E2E_NO_SERVER
    ? undefined
    : {
        command: "npm run dev -- --port 3100",
        url: "http://127.0.0.1:3100",
        reuseExistingServer: true,
        timeout: 120_000,
        env: { ARB_BACKEND_URL: "http://127.0.0.1:18080", NO_PROXY: "*" },
      },
});

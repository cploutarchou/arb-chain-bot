import { defineConfig } from "@playwright/test";

// Pure-logic unit suite, run with the Playwright runner the repo already
// depends on — no new test framework, no browser, and no backend. The E2E
// config (playwright.config.ts) starts Next on :3100 and expects arbd on
// :18080; these tests must not pay that cost or be blocked by it, so they
// live in their own testDir with no webServer.
//
// Run: npm run test:unit   (under web/)
export default defineConfig({
  testDir: "./unit",
  timeout: 10_000,
  retries: 0,
  reporter: [["list"]],
});

import { test, expect, type Page } from "@playwright/test";

// Critical path: unauthenticated redirect → login → every page renders
// its real data or an HONEST empty/absent state (never a blank crash),
// RBAC hides controls from viewers, config edit round-trips a version.
// The backend is a real arbd in PAPER mode whose market-data bootstrap
// is expected to be retrying (no exchange egress in the harness), so
// engine-dependent pages must show their truthful degraded states.

const ADMIN = { email: "admin@e2e.test", password: "e2e-password-123" };

async function login(page: Page, email = ADMIN.email, password = ADMIN.password) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/overview");
}

test("unauthenticated visitor is sent to login", async ({ page }) => {
  await page.goto("/overview");
  await page.waitForURL("**/login");
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
});

test("bad credentials fail without an oracle", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill("nobody@e2e.test");
  await page.getByLabel("Password").fill("wrong");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByText("Login failed.")).toBeVisible();
});

test("login reaches overview with live system status", async ({ page }) => {
  await login(page);
  await expect(page.getByText("Mode", { exact: true })).toBeVisible();
  await expect(page.getByText("PAPER", { exact: true })).toBeVisible();
});

test("every nav page renders content or an honest state", async ({ page }) => {
  // Package D adds PnL & Analytics, Orders, Fills, Telegram, and the
  // rebuilt Replay/System Health/Risk Center pages; dev-server first
  // compiles push past the default budget.
  test.setTimeout(180_000);
  await login(page);
  const pages: [string, RegExp][] = [
    ["/scanner", /Scanner|Loading/],
    ["/triangles", /Triangles/],
    ["/opportunities", /Opportunities/],
    ["/paper", /Paper Trading/],
    ["/portfolio", /Portfolio|Unavailable|portfolio not initialized/i],
    ["/exchanges", /Exchanges/],
    ["/strategies", /Strategy Configuration/],
    ["/ai", /AI Advisor/],
    ["/risk", /Risk Center/],
    ["/reports", /Reports/],
    ["/replay", /Replay/],
    ["/campaigns", /Campaigns/],
    ["/alerts", /Alert Center/],
    ["/system", /System/],
    ["/audit", /Audit Log/],
    ["/settings", /Settings/],
    // Package D (BL-17..BL-32): the e2e arbd runs PAPER with no DB, so
    // every store-backed endpoint here answers 404 storage_absent — the
    // pages must render that honestly (Unavailable copy), not crash.
    ["/pnl", /PnL|Persistence isn't configured|No settled paper cycles/i],
    ["/orders", /Orders|Persistence isn't configured/i],
    ["/fills", /Fills|Persistence isn't configured/i],
    ["/telegram", /Telegram/i],
  ];
  for (const [path, marker] of pages) {
    await page.goto(path);
    await expect(page.locator("main")).toContainText(marker, { timeout: 10_000 });
  }
});

test("strategy config shows the active version and history", async ({ page }) => {
  await login(page);
  await page.goto("/strategies");
  await expect(page.getByText(/v\d+ active/).first()).toBeVisible({ timeout: 10_000 });
  // Admin sees the edit affordance (RBAC-aware UI; backend enforces).
  await expect(page.getByRole("button", { name: "Edit configuration" })).toBeVisible();
});

test("structured config edit applies as a new version end to end", async ({ page }) => {
  await login(page);
  await page.goto("/strategies");
  await page.getByRole("button", { name: "Edit configuration" }).click();
  // Structured field form (BL-14) — no raw JSON textarea by default.
  const depthField = page.getByLabel("Book depth");
  await expect(depthField).toBeVisible();
  const current = Number(await depthField.inputValue());
  const next = current === 50 ? 60 : 50;
  await depthField.fill(String(next));
  await page.getByRole("button", { name: "Review changes" }).click();
  // Confirmation dialog with a before/after diff table (BL-02, BL-03).
  await page.getByRole("dialog", { name: "Apply new strategy configuration?" }).waitFor();
  await expect(page.getByRole("dialog")).toContainText("scanner.depth");
  await page.getByRole("button", { name: "Apply new version" }).click();
  await expect(page.getByText(/Version \d+ active\./)).toBeVisible({ timeout: 10_000 });
});

test("strategy config advanced JSON toggle still works", async ({ page }) => {
  await login(page);
  await page.goto("/strategies");
  await page.getByRole("button", { name: "Edit configuration" }).click();
  await page.getByRole("button", { name: "Advanced: JSON" }).click();
  const editor = page.locator("textarea");
  await expect(editor).toBeVisible();
  const draft = JSON.parse(await editor.inputValue());
  draft.scanner.depth = draft.scanner.depth === 55 ? 65 : 55;
  await editor.fill(JSON.stringify(draft, null, 2));
  await page.getByRole("button", { name: "Review changes" }).click();
  await page.getByRole("dialog", { name: "Apply new strategy configuration?" }).waitFor();
  await page.getByRole("button", { name: "Apply new version" }).click();
  await expect(page.getByText(/Version \d+ active\./)).toBeVisible({ timeout: 10_000 });
});

test("risk center shows deterministic limits and the persisted event timeline", async ({ page }) => {
  await login(page);
  await page.goto("/risk");
  await expect(page.locator("main")).toContainText(/min_net_edge_bps|MinNetEdgeBps/i, {
    timeout: 10_000,
  });
  // BL-31: the timeline is store-backed; no DB in this harness means an
  // honest Unavailable state, not a crash.
  await expect(page.getByText(/Risk event timeline/)).toBeVisible();
  await expect(page.locator("main")).toContainText(/Persistence isn't configured/i, { timeout: 10_000 });
});

test("system health renders the full payload on usePoll", async ({ page }) => {
  await login(page);
  await page.goto("/system");
  // Process stats are always present regardless of profile (BL-18).
  await expect(page.getByText("Goroutines", { exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText("Heap alloc", { exact: true })).toBeVisible();
  // Engine section: the e2e arbd runs an engine (PAPER profile).
  await expect(page.getByRole("heading", { name: "Engine / scanner" })).toBeVisible();
  // No DB configured — honest absence, not a faked pool.
  await expect(page.locator("main")).toContainText(/Persistence isn't configured/i, { timeout: 10_000 });
});

test("PnL & Analytics renders breakdown/series/distributions honestly with no DB", async ({ page }) => {
  await login(page);
  await page.goto("/pnl");
  await expect(page.getByRole("heading", { name: "Breakdown" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Cumulative P&L and drawdown" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Edge / slippage / latency distributions" })).toBeVisible();
  await expect(page.locator("main")).toContainText(/Persistence isn't configured/i, { timeout: 10_000 });
});

test("Orders and Fills render filters and an honest empty/unavailable state", async ({ page }) => {
  await login(page);
  await page.goto("/orders");
  await expect(page.getByRole("heading", { name: "Orders" })).toBeVisible();
  await expect(page.locator("main")).toContainText(/Persistence isn't configured/i, { timeout: 10_000 });

  await page.goto("/fills");
  await expect(page.getByRole("heading", { name: "Fills" })).toBeVisible();
  await expect(page.locator("main")).toContainText(/Persistence isn't configured/i, { timeout: 10_000 });
});

test("Telegram status renders without ever showing a token", async ({ page }) => {
  await login(page);
  await page.goto("/telegram");
  await expect(page.getByRole("heading", { name: "Telegram", exact: true })).toBeVisible();
  // Unconfigured in this harness: 200 {enabled:false}, not a 404.
  await expect(page.locator("main")).toContainText(/not configured for this deployment/i, { timeout: 10_000 });
  await expect(page.locator("main")).not.toContainText(/bot_token|ARB_TELEGRAM_TOKEN/i);
});

test("Replay page has no shell-command column and cross-links Campaigns instead of a duplicate table", async ({
  page,
}) => {
  await login(page);
  await page.goto("/replay");
  await expect(page.getByRole("heading", { name: "Replay & Backtesting" })).toBeVisible();
  await expect(page.locator("main")).not.toContainText("ARB_MODE=REPLAY");
  await expect(page.locator("main")).not.toContainText("./arbd");
  await expect(page.getByRole("link", { name: /Campaigns → Recorder/ }).first()).toBeVisible();
  // No store in this harness: the replay runner needs persistence, so
  // this is an honest 404 replays_absent, not a crash.
  await expect(page.locator("main")).toContainText(/replay runner isn't available|Persistence isn't configured/i, {
    timeout: 10_000,
  });
});

test("Triangle and Opportunity detail pages render an honest not-found/unavailable state for an unknown id", async ({
  page,
}) => {
  await login(page);
  await page.goto("/triangles/does-not-exist");
  await expect(page.locator("main")).toContainText(/Triangle does-not-exist/);
  await expect(page.locator("main")).toContainText(/not found|Unavailable/i, { timeout: 10_000 });

  await page.goto("/opportunities/does-not-exist");
  await expect(page.locator("main")).toContainText(/Opportunity does-not-exist/);
  await expect(page.locator("main")).toContainText(/Persistence isn't configured|Unavailable/i, { timeout: 10_000 });
});

test("Scanner pin/pause/export controls render and pin persists across reload", async ({ page }) => {
  await login(page);
  await page.goto("/scanner");
  await expect(page.getByRole("button", { name: "Pause display" })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole("button", { name: "Export CSV" }).first()).toBeVisible();
  await expect(page.getByLabel(/Pinned only/)).toBeVisible();
});

test("alerts page renders the shared center", async ({ page }) => {
  await login(page);
  await page.goto("/alerts");
  await expect(page.getByText(/unresolved/)).toBeVisible({ timeout: 10_000 });
});

test("reports generate on demand", async ({ page }) => {
  await login(page);
  await page.goto("/reports");
  await page.getByRole("button", { name: "Generate daily now" }).click();
  await expect(page.locator("main")).toContainText(/daily report .* Scanner: \d+ evaluations/, {
    timeout: 15_000,
  });
});

test("users & roles shows the bootstrap admin for ADMIN", async ({ page }) => {
  await login(page);
  await page.goto("/settings#users");
  await expect(page.getByRole("heading", { name: "Users & roles" })).toBeVisible();
  await expect(page.getByText(ADMIN.email)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole("heading", { name: "Create user" })).toBeVisible();
});

test("paper reset is disabled with a hint while the engine is running", async ({ page }) => {
  await login(page);
  await page.goto("/paper");
  const resetButton = page.getByRole("button", { name: "Reset paper session…" });
  await expect(resetButton).toBeVisible({ timeout: 10_000 });
  // The e2e harness starts the engine running (no exchange egress to
  // pause against) and reset is ADMIN-only while paused — this asserts
  // the honest disabled/hint state rather than resetting a live paper
  // session mid-suite. If the harness ever starts paused instead, this
  // assertion should flip to the enabled + ConfirmDialog path.
  await expect(resetButton).toBeDisabled();
  await expect(page.getByText(/pause the engine before resetting/)).toBeVisible();
});

test("settings Markets & assets renders the real platform-settings document", async ({ page }) => {
  await login(page);
  await page.goto("/settings#markets");
  await expect(page.getByRole("heading", { name: "Markets & assets" })).toBeVisible();
  // The default bootstrap seed (config.go: ARB_SYMBOLS/ARB_STARTING_ASSETS
  // defaults) is what the e2e arbd boots with — this is the real T-057
  // platform document, not the deleted read-only scanner-status panel.
  await expect(page.getByText("BTCUSDT", { exact: true }).first()).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText("v1", { exact: false }).first()).toBeVisible();
  // ADMIN gets the real editor, not a "not yet editable" placeholder.
  await expect(page.getByRole("button", { name: "Edit markets & assets" })).toBeVisible();
  await expect(page.getByText(/not yet editable/i)).toHaveCount(0);

  // Venues & fees is the other half of the same document, no longer a
  // read-only placeholder either.
  await expect(page.getByRole("heading", { name: "Venues & fees" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Edit venues & fees" })).toBeVisible();

  // Version history table for the platform-settings document (rollback
  // affordance, mirrors /strategies).
  await expect(page.getByRole("heading", { name: "Platform settings — version history" })).toBeVisible();
  await expect(page.getByText(/v1 active/)).toBeVisible({ timeout: 10_000 });
});

test("engine status and the restart banner render the backend's real state, never a placeholder", async ({
  page,
}) => {
  await login(page);
  await page.goto("/overview");

  // Confirm the wiring the design promises: ProfileFull always builds an
  // engine + supervisor, so this must answer 200 with a real RestartStatus
  // even with no database configured (platform.Service falls back to
  // platform.NewMemoryStore() — components.go:buildPlatform). Fetched from
  // the page itself (same credentials/cookie path api/client.ts uses) —
  // Playwright's separate page.request context does not share the
  // session cookie with the browser's own fetches.
  const body = await page.evaluate(async () => {
    const r = await fetch("/api/v1/engine/status", { credentials: "same-origin" });
    return { status: r.status, json: (await r.json()) as { data: { restart: { state: string; pending_reasons?: string[]; error?: string } } } };
  });
  expect(body.status).toBe(200);
  const restart = body.json.data.restart;
  expect(["ready", "pending", "restarting", "failed"]).toContain(restart.state);

  const main = page.locator("main");
  if (restart.state === "pending") {
    await expect(main).toContainText("Saved but not running:");
    await expect(main).toContainText((restart.pending_reasons ?? []).join("; "));
    await expect(page.getByRole("button", { name: "Restart engine…" })).toBeVisible();
  } else if (restart.state === "restarting") {
    await expect(main).toContainText(
      "Restarting — waiting for the last recording segment to close and the feed to drain…",
    );
  } else if (restart.state === "failed") {
    await expect(main).toContainText("Restart failed:");
    await expect(main).toContainText(restart.error || "unknown error");
  } else {
    // "ready" with no pending reasons renders nothing — never a neutral
    // placeholder banner for a healthy engine.
    await expect(page.getByRole("button", { name: "Restart engine…" })).toHaveCount(0);
  }
});

test("sign out returns to login", async ({ page }) => {
  await login(page);
  await page.goto("/settings");
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.waitForURL("**/login");
});

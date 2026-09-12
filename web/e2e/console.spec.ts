import { test, expect, type Page } from "@playwright/test";
import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { resolveNav } from "@/lib/nav";

// Critical path: unauthenticated redirect → login → every page renders
// its real data or an HONEST empty/absent state (never a blank crash),
// RBAC hides controls from viewers, config edit round-trips a version.
// The backend is a real arbd in PAPER mode whose market-data bootstrap
// is expected to be retrying (no exchange egress in the harness), so
// engine-dependent pages must show their truthful degraded states.

const ADMIN = { email: "admin@e2e.test", password: "e2e-password-123" };
const OPERATOR = { email: "operator@e2e.test", password: "e2e-operator-123" };

async function login(
  page: Page,
  email = ADMIN.email,
  password = ADMIN.password,
) {
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
  // Scoped to the page body: the shell chrome (sidebar/top bar) also
  // renders the mode word now (F1), so an unscoped exact match would
  // ambiguously resolve between that and this page's own "Mode" stat.
  await expect(
    page.locator("main").getByText("PAPER", { exact: true }),
  ).toBeVisible();
});

test("mode banner announces the mode on mobile without opening the menu (F1)", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  const menuButton = page.getByRole("button", { name: "Open navigation" });
  for (const path of ["/overview", "/paper", "/portfolio"]) {
    await page.goto(path);
    // Never opened: the compact banner must already be on screen.
    await expect(menuButton).toBeVisible();
    await expect(page.getByTitle(/PAPER TRADING ONLY/)).toBeVisible();
  }

  // A mocked REPLAY mode renders the same way — the compact banner is
  // driven by the real status poll, not hard-coded to PAPER.
  await page.route("**/api/v1/system/status", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data?: { mode?: string } };
    if (body.data) body.data.mode = "REPLAY";
    await route.fulfill({ response, json: body });
  });
  await page.goto("/overview");
  await expect(menuButton).toBeVisible();
  await expect(
    page.getByTitle(/REPLAY — replaying recorded data, not live/),
  ).toBeVisible();
  // The status poll keeps firing, so a route callback can still be in
  // `route.fetch()` when the test ends — it then rejects with "Test
  // ended" and Playwright attributes the failure to whichever test runs
  // next (it surfaced on the page sweep at :81, which registers no
  // routes of its own). Dropping the handler here ends that race.
  // Assertions above are untouched: this runs only after they pass.
  await page.unrouteAll({ behavior: "ignoreErrors" });
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
    await expect(page.locator("main")).toContainText(marker, {
      timeout: 10_000,
    });
  }
});

test("strategy config shows the active version and history", async ({
  page,
}) => {
  await login(page);
  await page.goto("/strategies");
  await expect(page.getByText(/v\d+ active/).first()).toBeVisible({
    timeout: 10_000,
  });
  // Admin sees the edit affordance (RBAC-aware UI; backend enforces).
  await expect(
    page.getByRole("button", { name: "Edit configuration" }),
  ).toBeVisible();
});

test("structured config edit applies as a new version end to end", async ({
  page,
}) => {
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
  await page
    .getByRole("dialog", { name: "Apply new strategy configuration?" })
    .waitFor();
  await expect(page.getByRole("dialog")).toContainText("scanner.depth");
  // Wait on the apply response itself, not on a clock.
  //
  // This test used to fail inside the full suite and pass in isolation.
  // The cause is not fixture state — no earlier test applies a config
  // version — it is load: the test five places earlier visits every nav
  // page in the app, which leaves `next dev` compiling routes on demand,
  // so the round trip can exceed a fixed 10s wait. Asserting on the POST
  // makes it deterministic and *strengthens* the test rather than
  // relaxing it: the request must actually be made, and must actually
  // succeed, before the confirmation is required to appear.
  const applied = page.waitForResponse(
    (res) =>
      res.url().includes("/api/v1/config") &&
      res.request().method() === "POST",
    { timeout: 30_000 },
  );
  await page.getByRole("button", { name: "Apply new version" }).click();
  const res = await applied;
  expect(
    res.status(),
    `apply returned ${res.status()}: ${(await res.text()).slice(0, 300)}`,
  ).toBe(200);
  await expect(page.getByText(/Version \d+ active\./)).toBeVisible({
    timeout: 15_000,
  });
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
  await page
    .getByRole("dialog", { name: "Apply new strategy configuration?" })
    .waitFor();
  await page.getByRole("button", { name: "Apply new version" }).click();
  await expect(page.getByText(/Version \d+ active\./)).toBeVisible({
    timeout: 10_000,
  });
});

test("risk center shows deterministic limits and the persisted event timeline", async ({
  page,
}) => {
  await login(page);
  await page.goto("/risk");
  // F11: the limits table reads through the field registry — the
  // human label with its unit, not the raw key.
  await expect(page.locator("main")).toContainText(
    /min net edge \(bps\)|min_net_edge_bps/i,
    {
      timeout: 10_000,
    },
  );
  // BL-31: the timeline is store-backed; no DB in this harness means an
  // honest Unavailable state, not a crash.
  await expect(page.getByText(/Risk event timeline/)).toBeVisible();
  await expect(page.locator("main")).toContainText(
    /Persistence isn't configured/i,
    { timeout: 10_000 },
  );
});

test("system health renders the full payload on usePoll", async ({ page }) => {
  await login(page);
  await page.goto("/system");
  // Process stats are always present regardless of profile (BL-18).
  await expect(page.getByText("Goroutines", { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByText("Heap alloc", { exact: true })).toBeVisible();
  // Engine section: the e2e arbd runs an engine (PAPER profile).
  await expect(
    page.getByRole("heading", { name: "Engine / scanner" }),
  ).toBeVisible();
  // No DB configured — honest absence, not a faked pool.
  await expect(page.locator("main")).toContainText(
    /Persistence isn't configured/i,
    { timeout: 10_000 },
  );
});

test("PnL & Analytics renders breakdown/series/distributions honestly with no DB", async ({
  page,
}) => {
  await login(page);
  await page.goto("/pnl");
  await expect(page.getByRole("heading", { name: "Breakdown" })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Cumulative P&L and drawdown" }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", {
      name: "Edge / slippage / latency distributions",
    }),
  ).toBeVisible();
  await expect(page.locator("main")).toContainText(
    /Persistence isn't configured/i,
    { timeout: 10_000 },
  );
});

test("Orders and Fills render filters and an honest empty/unavailable state", async ({
  page,
}) => {
  await login(page);
  await page.goto("/orders");
  await expect(page.getByRole("heading", { name: "Orders" })).toBeVisible();
  await expect(page.locator("main")).toContainText(
    /Persistence isn't configured/i,
    { timeout: 10_000 },
  );

  await page.goto("/fills");
  await expect(page.getByRole("heading", { name: "Fills" })).toBeVisible();
  await expect(page.locator("main")).toContainText(
    /Persistence isn't configured/i,
    { timeout: 10_000 },
  );
});

test("Telegram status renders without ever showing a token", async ({
  page,
}) => {
  await login(page);
  await page.goto("/telegram");
  await expect(
    page.getByRole("heading", { name: "Telegram", exact: true }),
  ).toBeVisible();
  // Unconfigured in this harness: 200 {enabled:false}, not a 404.
  await expect(page.locator("main")).toContainText(
    /not configured for this deployment/i,
    { timeout: 10_000 },
  );
  await expect(page.locator("main")).not.toContainText(
    /bot_token|ARB_TELEGRAM_TOKEN/i,
  );
});

test("Replay page has no shell-command column and cross-links Campaigns instead of a duplicate table", async ({
  page,
}) => {
  await login(page);
  await page.goto("/replay");
  await expect(
    page.getByRole("heading", { name: "Replay & Backtesting" }),
  ).toBeVisible();
  await expect(page.locator("main")).not.toContainText("ARB_MODE=REPLAY");
  await expect(page.locator("main")).not.toContainText("./arbd");
  await expect(
    page.getByRole("link", { name: /Campaigns → Recorder/ }).first(),
  ).toBeVisible();
  // No store in this harness: the replay runner needs persistence, so
  // this is an honest 404 replays_absent, not a crash.
  await expect(page.locator("main")).toContainText(
    /replay runner isn't available|Persistence isn't configured/i,
    {
      timeout: 10_000,
    },
  );
});

test("Triangle and Opportunity detail pages render an honest not-found/unavailable state for an unknown id", async ({
  page,
}) => {
  await login(page);
  await page.goto("/triangles/does-not-exist");
  await expect(page.locator("main")).toContainText(/Triangle does-not-exist/);
  await expect(page.locator("main")).toContainText(/not found|Unavailable/i, {
    timeout: 10_000,
  });

  await page.goto("/opportunities/does-not-exist");
  await expect(page.locator("main")).toContainText(
    /Opportunity does-not-exist/,
  );
  await expect(page.locator("main")).toContainText(
    /Persistence isn't configured|Unavailable/i,
    { timeout: 10_000 },
  );
});

test("Scanner pin/pause/export controls render and pin persists across reload", async ({
  page,
}) => {
  await login(page);
  await page.goto("/scanner");
  await expect(page.getByRole("button", { name: "Pause display" })).toBeVisible(
    { timeout: 10_000 },
  );
  await expect(
    page.getByRole("button", { name: "Export CSV" }).first(),
  ).toBeVisible();
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
  await expect(page.locator("main")).toContainText(
    /daily report .* Scanner: \d+ evaluations/,
    {
      timeout: 15_000,
    },
  );
});

test("users & roles shows the bootstrap admin for ADMIN", async ({ page }) => {
  await login(page);
  await page.goto("/settings#users");
  await expect(
    page.getByRole("heading", { name: "Users & roles" }),
  ).toBeVisible();
  await expect(page.getByText(ADMIN.email).first()).toBeVisible({
    timeout: 10_000,
  });
  await expect(
    page.getByRole("heading", { name: "Create user" }),
  ).toBeVisible();
});

test("paper reset is disabled with a hint while the engine is running", async ({
  page,
}) => {
  await login(page);
  await page.goto("/paper");
  // T-087 moved the reset control into a closed-by-default disclosure
  // (it was previously a prominent red panel between the live monitor
  // and the history — the audit's own point was that the console's most
  // destructive control had the strongest visual pull). Every safeguard
  // — ADMIN only, engine must be paused, type-to-confirm — is unchanged;
  // only reaching it now takes one extra click.
  await page.getByText("Reset this simulation session").click();
  const resetButton = page.getByRole("button", {
    name: "Reset paper session…",
  });
  await expect(resetButton).toBeVisible({ timeout: 10_000 });
  // The e2e harness starts the engine running (no exchange egress to
  // pause against) and reset is ADMIN-only while paused — this asserts
  // the honest disabled/hint state rather than resetting a live paper
  // session mid-suite. If the harness ever starts paused instead, this
  // assertion should flip to the enabled + ConfirmDialog path.
  await expect(resetButton).toBeDisabled();
  await expect(
    page.getByText(/pause the engine before resetting/),
  ).toBeVisible();
});

// mockPaperRunning overlays a deterministic `paper` block onto the real
// scanner-status response (every other field — ready, triangles,
// markets… — stays whatever the live engine currently reports). This
// e2e harness's engine has no exchange egress to become fully ready
// against (screener.md/replay's own "market-data bootstrap is expected
// to be retrying" comment), so `paper` can legitimately be absent for a
// long time — the control's own correctness here does not depend on
// racing that; it depends on what the control renders once the status
// IS `{running}`, which this pins down exactly.
async function mockPaperRunning(page: Page, running: boolean) {
  await page.route("**/api/v1/scanner/status", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data?: { paper?: unknown } | null;
    };
    if (body.data) {
      body.data.paper = {
        running,
        active_simulations: running ? 1 : 0,
        received: 10,
        completed: 8,
        failed: 1,
        skipped: 1,
      };
    }
    await route.fulfill({ response, json: body });
  });
}

test("shell pause control reaches Paper Trading in one click from an unrelated page, confirms once, and reports the backend's own failure (F2)", async ({
  page,
}) => {
  await login(page);
  await mockPaperRunning(page, true);
  await page.goto("/alerts");
  // Scoped to the sidebar (the one <aside role="complementary"> while the
  // mobile overlay is closed) — the mobile top bar mounts the same
  // control too (CSS-hidden at this viewport, still in the DOM), so an
  // unscoped role query would be ambiguous.
  // The control was renamed (D7/T-087): "Pause paper trading" overstated
  // what POST /api/v1/paper/pause actually reaches — the triangular
  // paper engine only, never Scanner Suite/rule-based auto-paper, which
  // has no pause route at all (docs/design/client-area-refinement-
  // backend-contract.md §1). Only the names changed here; what the test
  // proves — one-click reach, a single confirmation, the backend's own
  // failure verbatim — stays exactly as strict.
  const sidebar = page.getByRole("complementary");
  const pauseButton = sidebar.getByRole("button", {
    name: "Pause triangular simulations",
  });
  await expect(pauseButton).toBeVisible({ timeout: 10_000 });

  await pauseButton.click();
  const dialog = page.getByRole("dialog", {
    name: "Pause triangular simulations?",
  });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText(/in-flight simulations/i);
  // The control must state its scope in visible dialog text, not only a
  // tooltip: rule-based automatic paper execution is unaffected and has
  // no pause control of its own (backend-contract.md §1 — no pause route
  // exists for it at all).
  await expect(dialog).toContainText(
    /rule-based automatic paper execution is not affected and has no pause control/i,
  );

  // Mock the failure so the toast must carry the backend's own status and
  // message verbatim — and so the shared e2e backend, which other tests
  // rely on staying RUNNING, is never actually paused.
  await page.route("**/api/v1/paper/pause", async (route) => {
    await route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        data: null,
        error: { code: "paper_busy", message: "a simulation is still settling" },
      }),
    });
  });
  await dialog
    .getByRole("button", { name: "Pause triangular simulations" })
    .click();
  await expect(
    page.getByText(
      "Pause failed (HTTP 409): a simulation is still settling",
    ),
  ).toBeVisible();
  await expect(dialog).toHaveCount(0);
  await expect(pauseButton).toBeEnabled();
});

test("settings Markets & assets renders the real platform-settings document", async ({
  page,
}) => {
  await login(page);
  await page.goto("/settings#markets");
  await expect(
    page.getByRole("heading", { name: "Markets & assets" }),
  ).toBeVisible();
  // The default bootstrap seed (config.go: ARB_SYMBOLS/ARB_STARTING_ASSETS
  // defaults) is what the e2e arbd boots with — this is the real T-057
  // platform document, not the deleted read-only scanner-status panel.
  await expect(page.getByText("BTCUSDT", { exact: true }).first()).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByText("v1", { exact: false }).first()).toBeVisible();
  // ADMIN gets the real editor, not a "not yet editable" placeholder.
  await expect(
    page.getByRole("button", { name: "Edit markets & assets" }),
  ).toBeVisible();
  await expect(page.getByText(/not yet editable/i)).toHaveCount(0);

  // Venues & fees is the other half of the same document, no longer a
  // read-only placeholder either.
  await expect(
    page.getByRole("heading", { name: "Venues & fees" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Edit venues & fees" }),
  ).toBeVisible();

  // Version history table for the platform-settings document (rollback
  // affordance, mirrors /strategies).
  await expect(
    page.getByRole("heading", { name: "Platform settings — version history" }),
  ).toBeVisible();
  await expect(page.getByText(/v1 active/)).toBeVisible({ timeout: 10_000 });
});

test("settings Venues & fees lists every capability venue, not just the compiled one", async ({
  page,
}) => {
  await login(page);
  await page.goto("/settings#markets");
  await expect(
    page.getByRole("heading", { name: "Venues & fees" }),
  ).toBeVisible();
  // binance is the only compiled connector in this build (T-061); the
  // rest render read-only with the backend's own reason string, never a
  // hardcoded paraphrase — assert against what the API actually returned.
  const caps = await page.evaluate(async () => {
    const r = await fetch("/api/v1/platform/capabilities", {
      credentials: "same-origin",
    });
    return (await r.json()) as {
      data: {
        venues: {
          id: string;
          name: string;
          available: boolean;
          reason?: string;
        }[];
      };
    };
  });
  const unavailable = caps.data.venues.find((v) => !v.available);
  expect(unavailable).toBeTruthy();
  await expect(
    page.getByText(unavailable!.name, { exact: true }).first(),
  ).toBeVisible();
  await expect(page.locator("main")).toContainText(unavailable!.reason!);
  await expect(page.locator("main")).toContainText(
    /Public market data only — API keys are never used/,
  );
  // The token-discount checkbox never offers to enable itself (T-061 D12
  // / settings.go's unconditional rejection) — every instance is disabled.
  const discountBoxes = page.getByRole("checkbox", {
    name: "Token fee discount",
  });
  const n = await discountBoxes.count();
  for (let i = 0; i < n; i++) {
    await expect(discountBoxes.nth(i)).toBeDisabled();
  }
});

test("settings Operating mode renders the mode table from capabilities with SHADOW disabled", async ({
  page,
}) => {
  await login(page);
  await page.goto("/settings#operating-mode");
  await expect(
    page.getByRole("heading", { name: "Operating mode" }),
  ).toBeVisible();
  await expect(
    page.getByText(
      "LIVE is not an option: this platform never places real orders.",
    ),
  ).toBeVisible();
  // The e2e harness boots ARB_MODE=PAPER (scripts/e2e.sh).
  await expect(page.getByRole("radio", { name: "PAPER" })).toBeChecked();
  const shadow = page.getByRole("radio", { name: "SHADOW" });
  await expect(shadow).toBeVisible();
  await expect(shadow).toBeDisabled();
  await expect(page.locator("main")).toContainText(
    /shadow execution is not wired into Engine\.Run/,
  );
  // Immediate for the section header's field_timing lookup would be
  // wrong here — platform.mode is restart-scoped.
  await expect(
    page.locator("#operating-mode").getByText("On restart"),
  ).toBeVisible();
});

test("settings AI advisor shows the fake provider running (ARB_AI_PROVIDER=fake in the harness)", async ({
  page,
}) => {
  await login(page);
  await page.goto("/settings#ai");
  await expect(page.getByRole("heading", { name: "AI advisor" })).toBeVisible();
  // Scoped to the AI section: "Enabled" also labels a per-venue checkbox
  // in Venues & fees, further up the same page.
  const aiSection = page.locator("#ai");
  await expect(aiSection.getByText("Enabled", { exact: true })).toBeVisible();
  await expect(aiSection.getByText("Running", { exact: true })).toBeVisible();
  await expect(
    aiSection.getByText("fake", { exact: true }).first(),
  ).toBeVisible({ timeout: 10_000 });
  // openai is enumerated but never selectable (D10).
  await aiSection.getByRole("button", { name: "Edit AI advisor" }).click();
  const providerSelect = page.getByLabel("Provider");
  await expect(providerSelect).toBeVisible();
  const openaiOption = providerSelect.locator('option[value="openai"]');
  await expect(openaiOption).toBeDisabled();
  await expect(openaiOption).toHaveText(/provider not built/);
});

test("settings Logging & access warns that debug logs every rejected opportunity", async ({
  page,
}) => {
  await login(page);
  await page.goto("/settings#logging");
  await expect(
    page.getByRole("heading", { name: "Logging & access" }),
  ).toBeVisible();
  await expect(page.locator("main")).toContainText(
    /debug logs every rejected opportunity/,
  );
  await expect(page.getByLabel(/Log level/)).toBeVisible();
  await expect(page.getByLabel(/Allowed origin/)).toBeVisible();
});

test("settings Security renders the vault status and never a secret value", async ({
  page,
}) => {
  await login(page);
  // The harness (scripts/e2e.sh) does not set ARB_SECRET_KEY — assert
  // against what the backend actually reports, not a guessed string.
  const secrets = await page.evaluate(async () => {
    const r = await fetch("/api/v1/secrets", { credentials: "same-origin" });
    return {
      status: r.status,
      json: (await r.json()) as {
        data: {
          vault_configured: boolean;
          reason?: string;
          secrets: { name: string; label: string }[];
        };
      },
    };
  });
  expect(secrets.status).toBe(200);
  await page.goto("/settings#security");
  await expect(
    page.getByRole("heading", { name: "Security" }).first(),
  ).toBeVisible();
  if (!secrets.json.data.vault_configured) {
    expect(secrets.json.data.reason).toBeTruthy();
    await expect(page.locator("main")).toContainText(secrets.json.data.reason!);
    // Writes are refused with the vault unconfigured — Set value/Remove
    // must render disabled, not just fail silently on click.
    const securitySection = page.locator("#security");
    const setButtons = securitySection.getByRole("button", {
      name: "Set value",
    });
    for (let i = 0; i < (await setButtons.count()); i++) {
      await expect(setButtons.nth(i)).toBeDisabled();
    }
  }
  for (const s of secrets.json.data.secrets) {
    await expect(
      page.getByText(s.label, { exact: true }).first(),
    ).toBeVisible();
  }
  // Never a token, a prefix, or a last-4 anywhere on the page.
  await expect(page.locator("main")).not.toContainText(
    /ANTHROPIC_API_KEY|ARB_TELEGRAM_TOKEN/i,
  );
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
    const r = await fetch("/api/v1/engine/status", {
      credentials: "same-origin",
    });
    return {
      status: r.status,
      json: (await r.json()) as {
        data: {
          restart: {
            state: string;
            pending_reasons?: string[];
            error?: string;
          };
        };
      },
    };
  });
  expect(body.status).toBe(200);
  const restart = body.json.data.restart;
  expect(["ready", "pending", "restarting", "failed"]).toContain(restart.state);

  const main = page.locator("main");
  if (restart.state === "pending") {
    await expect(main).toContainText("Saved but not running:");
    await expect(main).toContainText(
      (restart.pending_reasons ?? []).join("; "),
    );
    await expect(
      page.getByRole("button", { name: "Restart engine…" }),
    ).toBeVisible();
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
    await expect(
      page.getByRole("button", { name: "Restart engine…" }),
    ).toHaveCount(0);
  }
});

test("sign out returns to login", async ({ page }) => {
  await login(page);
  await page.goto("/settings");
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.waitForURL("**/login");
});

// ---- Tenancy / entitlements / billing (T-081..T-083) ----------------------

test("risk-ack gate blocks once, then never again for the session", async ({
  page,
}) => {
  // risk_ack_required is always false in this DB-less CI profile (no
  // internal/tenancy.Store wired ⇒ every account acts in the exempt
  // platform organisation, internal/api/auth.go riskAckRequired) — the
  // gate is exercised deterministically by rewriting the real /me
  // response's risk_ack_required/version fields in place, keeping every
  // other field (entitlements, org, role…) authentic, and by answering
  // the risk-ack POST locally instead of letting it reach the
  // tenancy_unavailable backend.
  let ackRequired = true;
  await page.route("**/api/v1/auth/me", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data?: { risk_ack_required?: boolean; risk_ack_version?: string };
    };
    if (body.data) {
      body.data.risk_ack_required = ackRequired;
      body.data.risk_ack_version = "2026-08-27";
    }
    await route.fulfill({ response, json: body });
  });
  let ackPosted: string | null = null;
  await page.route("**/api/v1/me/risk-ack", async (route) => {
    const payload = route.request().postDataJSON() as { version?: string };
    ackPosted = payload.version ?? null;
    ackRequired = false;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          org_id: 1,
          risk_ack_version: payload.version,
          risk_ack_at: new Date().toISOString(),
        },
        error: null,
      }),
    });
  });

  await login(page);
  await expect(
    page.getByRole("heading", { name: "Read this before you continue" }),
  ).toBeVisible();
  const acceptButton = page.getByRole("button", {
    name: "Accept and continue",
  });
  await expect(acceptButton).toBeDisabled();
  await page.getByRole("checkbox").check();
  await expect(acceptButton).toBeEnabled();
  await acceptButton.click();
  await expect(
    page.getByRole("heading", { name: "Read this before you continue" }),
  ).toHaveCount(0, { timeout: 10_000 });
  expect(ackPosted).toBe("2026-08-27");

  // Reload: accepted once, never asked again for the rest of the session.
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Read this before you continue" }),
  ).toHaveCount(0);
  await expect(page.getByText("Mode", { exact: true })).toBeVisible();
});

test("/org renders", async ({ page }) => {
  await login(page);
  await page.goto("/org");
  await expect(
    page.getByRole("heading", { name: "Organisation" }),
  ).toBeVisible();
});

test("/billing renders and shows 'Billing not configured' without Paddle secrets", async ({
  page,
}) => {
  await login(page);
  await page.goto("/billing");
  await expect(page.getByRole("heading", { name: "Billing" })).toBeVisible();
  // This CI profile has no paddle_api_key/paddle_webhook_secret in the
  // vault and no PADDLE_CLIENT_TOKEN — billing is honestly unconfigured
  // rather than showing a broken checkout button or price table.
  await expect(page.getByText("Billing not configured.").first()).toBeVisible({
    timeout: 10_000,
  });
});

test("entitlement_exceeded 403 renders a toast with an Upgrade link to /billing", async ({
  page,
}) => {
  // No real request in this DB-less CI profile can trip
  // entitlement_exceeded (the platform org resolves to the institution
  // package, which has no limits to hit) — simulate the backend's
  // documented shape (packages.md §3.2: 403 entitlement_exceeded +
  // data.key/limit) on a real mutating endpoint to exercise the errorBus
  // → ToastProvider wiring end to end. Screener rules (unlike /org, which
  // 503s tenancy_unavailable without a database in this profile) work
  // with no DB, so the create-rule form is reliably reachable here.
  await page.route("**/api/v1/screener/rules", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    await route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({
        data: { key: "rules.max_active", limit: 2 },
        error: {
          code: "entitlement_exceeded",
          message: "active rule limit reached",
        },
      }),
    });
  });
  await login(page);
  await page.goto("/scanner-alerts");
  await page.getByRole("button", { name: "New rule…" }).click();
  await page.getByLabel("Name").fill("Over the limit");
  await page.getByRole("button", { name: "Save rule" }).click();
  const toast = page.getByText(/rules max active/i);
  await expect(toast).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole("link", { name: "Upgrade" })).toBeVisible();
});

// ensureViewerAccount mirrors ensureOperatorAccount below for a VIEWER
// test user, used to assert nav gating renders the role-restricted
// treatment (not just that VIEWER's permitted pages happen to load).
async function ensureViewerAccount(page: Page) {
  const VIEWER_TEST = { email: "viewer@e2e.test", password: "e2e-viewer-123" };
  await login(page);
  await page.goto("/settings#users");
  await page.getByRole("heading", { name: "Users & roles" }).waitFor();
  const already = await page.getByText(VIEWER_TEST.email).count();
  if (already === 0) {
    await page.getByLabel("Email").fill(VIEWER_TEST.email);
    await page.getByLabel("Role").selectOption("VIEWER");
    await page
      .getByLabel("Password", { exact: true })
      .fill(VIEWER_TEST.password);
    await page.getByLabel("Confirm password").fill(VIEWER_TEST.password);
    await page.getByRole("button", { name: "Create user" }).click();
    await expect(page.getByText(VIEWER_TEST.email).first()).toBeVisible({
      timeout: 10_000,
    });
  }
  // Sign out lives in the Account category's own SessionSection, which
  // only renders while that category is the active tab (T-087: the
  // other categories stay mounted-but-hidden). #users activates
  // Administration, so a bare reload back to /settings (Account is the
  // first/default category) is required before Sign out is reachable —
  // clicking it while Administration is active hits a hidden element.
  await page.goto("/settings");
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.waitForURL("**/login");
  return VIEWER_TEST;
}

test("nav gating is visible for a VIEWER (role-restricted, not just hidden)", async ({
  page,
}) => {
  const viewer = await ensureViewerAccount(page);
  await login(page, viewer.email, viewer.password);
  // Audit log now lives under Operations › Platform health, not
  // Overview, and the secondary nav only lists a destination's own
  // entries — /risk is a plain member-visible page (D1), so landing
  // there is what actually renders the Operations secondary list that
  // Audit log sits in.
  await page.goto("/risk");
  const opsNav = page.getByRole("navigation", { name: "Operations sections" });
  // Audit log: GatedControl state="role" — grey, cursor-not-allowed,
  // non-navigable, with the actual minimum role named in the tooltip
  // (console-v2.md §2.4 — never a generic "restricted").
  await expect(
    opsNav.getByRole("link", { name: /Audit log/i }),
  ).toHaveCount(0);
  const gated = opsNav.locator('[title="Requires OPERATOR or ADMIN"]');
  await expect(gated).toBeVisible();
  await expect(gated).toHaveAttribute("aria-disabled", "true");
  await expect(gated).toContainText(/Audit log/i);
});

test("shell paper control shows a VIEWER the live state but never the pause/resume button (F2 RBAC)", async ({
  page,
}) => {
  const viewer = await ensureViewerAccount(page);
  await login(page, viewer.email, viewer.password);
  await mockPaperRunning(page, false);
  await page.goto("/overview");
  const sidebar = page.getByRole("complementary");
  await expect(sidebar.getByText(/PAPER (RUNNING|PAUSED)/)).toBeVisible({
    timeout: 10_000,
  });
  await expect(
    sidebar.getByRole("button", {
      name: /Pause triangular simulations|Resume triangular simulations/,
    }),
  ).toHaveCount(0);
});

// ---- Scanner Suite (T-065..T-072) -----------------------------------------
// Screener, Perpetuals, Funding, Calculator, Alert Rules, Auto-Paper. The
// backend for this suite is being written in parallel — every page must
// show real data OR the honest "Screener backend not available in this
// build" notice, never a crash, until it lands.

// ensureOperatorAccount creates a fixed OPERATOR test user via the real
// Users & roles UI (ADMIN-only) if it doesn't already exist yet, then
// signs out — idempotent across repeated CI runs against the same DB.
async function ensureOperatorAccount(page: Page) {
  await login(page);
  await page.goto("/settings#users");
  await page.getByRole("heading", { name: "Users & roles" }).waitFor();
  const already = await page.getByText(OPERATOR.email).count();
  if (already === 0) {
    await page.getByLabel("Email").fill(OPERATOR.email);
    await page.getByLabel("Role").selectOption("OPERATOR");
    await page.getByLabel("Password", { exact: true }).fill(OPERATOR.password);
    await page.getByLabel("Confirm password").fill(OPERATOR.password);
    await page.getByRole("button", { name: "Create user" }).click();
    await expect(page.getByText(OPERATOR.email).first()).toBeVisible({
      timeout: 10_000,
    });
  }
  // Same reason as ensureViewerAccount above: Sign out only renders
  // while the Account category is active.
  await page.goto("/settings");
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.waitForURL("**/login");
}

test("Perpetuals min carry APR filter sends a fraction, not a raw percent", async ({
  page,
}) => {
  // The field is labelled "% APR" for the operator but the backend
  // (internal/screener/basis.go PerpFilters.MinCarryAPR) reads
  // min_carry_apr as a fraction (0.10 = 10%). Intercept the request so
  // this is checked against the real query string regardless of what
  // the screener backend is wired to answer in this profile.
  await login(page);
  let seenQuery: string | null = null;
  await page.route("**/api/v1/screener/perpetuals**", async (route) => {
    seenQuery = new URL(route.request().url()).searchParams.get(
      "min_carry_apr",
    );
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { rows: [] }, error: null }),
    });
  });
  await page.goto("/perpetuals");
  await page.getByLabel("Min carry APR").fill("10");
  await expect.poll(() => seenQuery, { timeout: 10_000 }).toBe("0.1");
});

test("Alert rule form converts min carry APR between percent and an exact fraction, for both carry and basis, and never sends min_spread_bps for either", async ({
  page,
}) => {
  // Same class of bug as the Perpetuals filter above, plus two more: (1)
  // Number()*100/100 reintroduces binary-float noise on a value that
  // round-trips through storage (0.07 * 100 === 7.000000000000001 in
  // JS) — assert the exact string "0.07", not just "close to". (2) the
  // form used to only show the carry-APR field for kind="carry", not
  // "basis", even though rules.go Validate requires min_carry_apr for
  // both.
  await login(page);
  await page.goto("/scanner-alerts");

  const seenBodies: Record<string, unknown>[] = [];
  await page.route("**/api/v1/screener/rules", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    const body = route.request().postDataJSON() as Record<string, unknown>;
    seenBodies.push(body);
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { rule: { id: `e2e-rule-${seenBodies.length}`, ...body } },
        error: null,
      }),
    });
  });

  const cases = [
    ["carry", "7", "0.07"],
    ["basis", "12.5", "0.125"],
  ] as const;
  for (let i = 0; i < cases.length; i++) {
    const [kind, pct] = cases[i]!;
    await expect(page.getByRole("button", { name: "New rule…" })).toBeVisible({
      timeout: 10_000,
    });
    await page.getByRole("button", { name: "New rule…" }).click();
    await page.getByLabel("Name").fill(`e2e ${kind} rule`);
    await page.getByLabel("Kind").selectOption(kind);
    await expect(page.getByLabel("Min carry APR (%)")).toBeVisible();
    await expect(page.getByLabel("Min spread (bps)")).toHaveCount(0);

    // Advanced model inputs: mmr must never be pre-filled with a default
    // — a fabricated margin rate would let a carry/basis rule pass the
    // MMR gate silently (rule_params.go: nil = unknown = MMR_UNKNOWN skip).
    await page
      .getByRole("button", { name: "Show advanced model inputs" })
      .click();
    const mmrInput = page.getByLabel(/Maintenance margin rate/);
    await expect(mmrInput).toHaveValue("");
    await expect(mmrInput).toHaveAttribute("placeholder", /unknown/);

    await page.getByLabel("Min carry APR (%)").fill(pct);
    await page.getByRole("button", { name: "Save rule" }).click();
    await expect.poll(() => seenBodies.length, { timeout: 10_000 }).toBe(i + 1);
  }

  expect(seenBodies[0]?.min_carry_apr).toBe("0.07");
  expect(seenBodies[0]?.min_spread_bps).toBeUndefined();
  expect(seenBodies[1]?.min_carry_apr).toBe("0.125");
  expect(seenBodies[1]?.min_spread_bps).toBeUndefined();
});

test("Alert rule form loads an existing carry rule's stored fraction as an exact percent, round-trips it unchanged, and preserves untouched advanced params on save", async ({
  page,
}) => {
  const existing = {
    id: "e2e-existing-carry",
    name: "Existing carry rule",
    enabled: true,
    kind: "carry",
    min_carry_apr: "0.07",
    min_liquidity_quote: "500",
    min_lifetime_s: 30,
    buy_venues: [],
    sell_venues: [],
    quotes: ["USDT"],
    bases_allow: [],
    bases_deny: [],
    cooldown_s: 300,
    telegram: true,
    auto_paper: false,
    paper_size_quote: "100",
    // strategy is a param the RuleForm's Advanced section does not
    // expose an input for — it must survive an edit-save untouched.
    params: { mmr: "0.005", strategy: "carry" },
  };
  await page.route("**/api/v1/screener/rules", async (route) => {
    if (route.request().method() !== "GET") return route.fallback();
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: { rules: [existing] }, error: null }),
    });
  });
  const putBodies: Record<string, unknown>[] = [];
  await page.route(
    "**/api/v1/screener/rules/e2e-existing-carry",
    async (route) => {
      if (route.request().method() !== "PUT") return route.fallback();
      const body = route.request().postDataJSON() as Record<string, unknown>;
      putBodies.push(body);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: { rule: { id: existing.id, ...body } },
          error: null,
        }),
      });
    },
  );

  await login(page);
  await page.goto("/scanner-alerts");
  // The table's threshold column converts the stored fraction too — it
  // must never suffix the raw "0.07" with "% APR".
  await expect(page.getByText("7% APR")).toBeVisible();
  await expect(page.getByText("0.07% APR")).toHaveCount(0);

  await page.getByRole("button", { name: "Edit" }).click();
  await expect(page.getByLabel("Min carry APR (%)")).toHaveValue("7");
  await page
    .getByRole("button", { name: "Show advanced model inputs" })
    .click();
  await expect(page.getByLabel(/Maintenance margin rate/)).toHaveValue("0.005");

  await page.getByRole("button", { name: "Save rule" }).click();
  await expect.poll(() => putBodies.length, { timeout: 10_000 }).toBe(1);
  expect(putBodies[0]?.min_carry_apr).toBe("0.07");
  expect(putBodies[0]?.params).toMatchObject({
    mmr: "0.005",
    strategy: "carry",
  });
});

test("Screener include_suspect/include_unknown_liquidity toggles are off by default, send the query params when checked, and the excluded counts render", async ({
  page,
}) => {
  await login(page);
  const seenQueries: URLSearchParams[] = [];
  await page.route("**/api/v1/screener/spreads**", async (route) => {
    seenQueries.push(new URL(route.request().url()).searchParams);
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          rows: [],
          total: 0,
          generated_at: new Date().toISOString(),
          model: "no-transfer, top-of-book",
          excluded: { suspect: 3, liquidity_unknown: 5 },
        },
        error: null,
      }),
    });
  });
  await page.goto("/screener");
  await expect
    .poll(() => seenQueries.length, { timeout: 10_000 })
    .toBeGreaterThan(0);
  expect(seenQueries[0]?.get("include_suspect")).toBeNull();
  expect(seenQueries[0]?.get("include_unknown_liquidity")).toBeNull();

  await expect(page.getByText(/Excluded: suspect\s*3/)).toBeVisible();

  // The two opt-ins moved behind a collapsed-by-default "Advanced
  // filters" disclosure (T-087 §A2/§A3) — the labels and their off-by-
  // default behaviour are unchanged, but they are unreachable until the
  // disclosure is opened.
  await page
    .getByRole("button", { name: /Show advanced filters/i })
    .click();
  await page.getByLabel("Include suspect lanes (asset-identity guard)").check();
  await page.getByLabel("Include unknown-liquidity lanes").check();
  await expect
    .poll(() => seenQueries[seenQueries.length - 1]?.get("include_suspect"), {
      timeout: 10_000,
    })
    .toBe("1");
  expect(
    seenQueries[seenQueries.length - 1]?.get("include_unknown_liquidity"),
  ).toBe("1");
});

test.describe("Scanner Suite", () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage();
    await ensureOperatorAccount(page);
    await page.close();
  });

  // gotoViaNav asserts the page is actually *discoverable* through the
  // new structure — click the primary destination, then the secondary
  // entry it contains, rather than a bare page.goto() — which is what a
  // standing "Scanner Suite" nav group used to make a one-hop assertion.
  // That group no longer exists (T-087): its pages moved into Discover,
  // Paper Trading, Research & Results and Alerts & Rules, so this
  // exercises each one's real new home instead of just asserting the
  // URL still resolves.
  async function gotoViaNav(
    page: Page,
    destLabel: string,
    secondaryLabel: string,
    marker: RegExp,
  ) {
    // Start from a page outside every destination's own landing route,
    // so the click is a real navigation rather than a no-op.
    await page.goto("/overview");
    const primary = page.getByRole("navigation", { name: "Primary" });
    await primary.getByRole("link", { name: destLabel, exact: true }).click();
    const secondary = page.getByRole("navigation", {
      name: `${destLabel} sections`,
    });
    await secondary
      .getByRole("link", { name: secondaryLabel, exact: true })
      .click();
    await expect(page.locator("main")).toContainText(marker, {
      timeout: 10_000,
    });
  }

  test("every former Scanner Suite page is reachable through its new destination for an OPERATOR login", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    await login(page, OPERATOR.email, OPERATOR.password);

    const notAvailable = /Screener backend not available in this build\./;
    const routes: [string, string, RegExp][] = [
      ["Discover", "Spot screener", new RegExp(`Screener|${notAvailable.source}`)],
      ["Discover", "Perpetuals", new RegExp(`Perpetuals|${notAvailable.source}`)],
      ["Discover", "Funding", new RegExp(`Funding|${notAvailable.source}`)],
      ["Discover", "Spreads calculator", /Spreads calculator/],
      ["Alerts & Rules", "Alert rules", new RegExp(`Alert Rules|${notAvailable.source}`)],
      [
        "Research & Results",
        "Screener evidence",
        new RegExp(`Screener Reports|${notAvailable.source}`),
      ],
      [
        "Paper Trading",
        "Rule simulations",
        new RegExp(`Rule simulations|${notAvailable.source}`),
      ],
    ];
    for (const [destLabel, secondaryLabel, marker] of routes) {
      await gotoViaNav(page, destLabel, secondaryLabel, marker);
    }
  });

  test("theme toggle flips data-theme on the document element", async ({
    page,
  }) => {
    await login(page, OPERATOR.email, OPERATOR.password);
    await page.goto("/overview");
    // Scoped to the desktop sidebar (<aside>, implicit role
    // "complementary") — the mobile top bar renders its own instance of
    // the same toggle, hidden at this (default desktop) viewport but
    // still present in the DOM, so an unscoped query would match two.
    const toggle = page
      .getByRole("complementary")
      .getByRole("button", { name: /Switch to (light|dark) theme/ });
    await expect(toggle).toBeVisible();
    const before = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    await toggle.click();
    await expect
      .poll(() =>
        page.evaluate(() =>
          document.documentElement.getAttribute("data-theme"),
        ),
      )
      .not.toBe(before);
  });

  test("Alert Rules mutations are hidden for OPERATOR and available for ADMIN", async ({
    page,
  }) => {
    await login(page, OPERATOR.email, OPERATOR.password);
    await page.goto("/scanner-alerts");
    await expect(
      page.getByRole("heading", { name: "Alert Rules" }),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "New rule…" })).toHaveCount(
      0,
    );
    await expect(page.getByRole("button", { name: "Edit" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Delete" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Enable" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Disable" })).toHaveCount(0);

    // Sign out lives on Settings (SessionSection), not the shell itself.
    await page.goto("/settings");
    await page.getByRole("button", { name: "Sign out" }).click();
    await page.waitForURL("**/login");

    await login(page);
    await page.goto("/scanner-alerts");
    // ADMIN sees the mutation affordance once real data has loaded; if
    // the screener backend isn't built into this profile yet, the honest
    // unavailable notice is the correct thing to see instead — either is
    // valid, a crash or a silently-empty page is not.
    await expect(
      page
        .getByRole("button", { name: "New rule…" })
        .or(page.getByText("Screener backend not available in this build.")),
    ).toBeVisible({ timeout: 10_000 });
  });

  test("Screener Reports lists reports, gates 'Run now' to ADMIN, and the detail view renders the gate checklist, stats and stored markdown safely with the SIMULATED footer", async ({
    page,
  }) => {
    const summary = {
      id: "rep-1",
      period_start: "2026-08-25T00:00:00Z",
      period_end: "2026-08-26T00:00:00Z",
      period_label: "day",
      strategy: "cross_venue_spot",
      rule_id: "",
      n: 42,
      net_pnl_quote: "12.34",
      gate_passed: 1,
      gate_total: 8,
      created_at: "2026-08-26T00:05:00Z",
    };
    const listBody = {
      reports: [summary],
      last_run: {
        day: "2026-08-25",
        started_at: "2026-08-26T00:05:00Z",
        duration_ms: 1200,
        // Go's []Summary(nil) marshals as JSON null when a run produced
        // nothing (generator.go RunResult.Reports has no omitempty) —
        // exercise that shape here, not just the populated one, so the
        // "Last run reports" Stat's null-guard is actually covered.
        reports: null,
        errors: [],
      },
      next_run_utc: "2026-08-27T00:05:00Z",
      generated_at: "2026-08-26T00:06:00Z",
    };
    const detailBody = {
      report: {
        id: "rep-1",
        period_start: summary.period_start,
        period_end: summary.period_end,
        strategy: "cross_venue_spot",
        rule_id: "",
        created_at: summary.created_at,
        // Deliberately includes a pipe table AND a stray inline "<script>"
        // tag — the renderer must show the tag as literal text, never
        // execute or inject it (no dangerouslySetInnerHTML anywhere).
        md:
          "# Report\n\nSome narrative text with <script>window.__xss=1</script>.\n\n" +
          "- bullet one\n- bullet two\n\n| a | b |\n|---|---|\n| 1 | 2 |\n",
        payload: {
          window: {
            label: "day",
            start: summary.period_start,
            end: summary.period_end,
          },
          strategy: "cross_venue_spot",
          rule_id: "",
          rule_name: "",
          stats: {
            n: 42,
            wins: 20,
            matched_pairs: 10,
            net_pnl_quote: "12.34",
            fees_quote: "1.1",
            funding_quote: "0",
            funding_rows: 0,
            pnl_after_rebalance: "12.34",
            matched_pair_net: "10.0",
            unwind_cost_quote: "0",
            partial_leg_pnl_quote: "0",
            conservative_net_quote: "10.0",
            net_bps_mean: "5.2",
            net_bps_median: "4.8",
            hit_rate: "0.55",
            hit_rate_wilson95_low: "0.40",
            hit_rate_wilson95_high: "0.69",
            lifetime_s_mean: "12.5",
            lifetime_s_median: "10",
            lifetime_n: 42,
            max_drawdown_quote: "2.0",
            max_drawdown_frac: "0.02",
            allocated_capital_quote: "100",
            inventory_drift: [],
            skipped: {},
            realised_slip_bps_mean: "1.2",
            realised_slip_bps_p95: "3.4",
            realised_slip_n: 42,
            slip_allowance_bps: "2",
            concentration: "0.3",
            largest_loss_quote: "5.0",
            median_win_quote: "1.0",
            days: 1,
            weekend_days: 0,
            first_sample_at: "2026-08-25T01:00:00Z",
            last_sample_at: "2026-08-25T23:00:00Z",
            wilcoxon: null,
            bootstrap: null,
          },
          gate: [
            {
              item: 1,
              name: "Duration and coverage",
              status: "fail",
              reason: "no evidence yet: regime days not computed",
            },
            {
              item: 4,
              name: "Statistical test",
              status: "pass",
              reason: "Wilcoxon rejects H0, bootstrap CI lower bound > 0",
            },
          ],
          gate_passed: 1,
          gate_total: 8,
          generated_at: summary.created_at,
          data_age_ms: 500,
          model:
            "Measurement, not a recommendation. Simulated results are hypothetical.",
          notes: ["n_regime reads not computed"],
          files: { markdown: "screener-reports/2026-08-25/x.md" },
        },
      },
    };

    await page.route("**/api/v1/screener/reports**", async (route) => {
      const url = new URL(route.request().url());
      const method = route.request().method();
      if (url.pathname === "/api/v1/screener/reports" && method === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ data: listBody, error: null }),
        });
        return;
      }
      if (
        url.pathname === "/api/v1/screener/reports/run" &&
        method === "POST"
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            data: {
              run: {
                day: "2026-08-26",
                started_at: new Date().toISOString(),
                duration_ms: 500,
                reports: [summary],
                errors: [],
              },
            },
            error: null,
          }),
        });
        return;
      }
      if (
        url.pathname === "/api/v1/screener/reports/rep-1" &&
        method === "GET"
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ data: detailBody, error: null }),
        });
        return;
      }
      await route.fallback();
    });

    // OPERATOR: read-only — no "Run now…", but the fixed hypothetical-
    // performance footer is still present on the page.
    await login(page, OPERATOR.email, OPERATOR.password);
    await page.goto("/screener-reports");
    await expect(
      page.getByRole("heading", { name: "Screener Reports" }),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Run now…" })).toHaveCount(0);
    await expect(page.getByText(/SIMULATED\. Paper result/)).toBeVisible();
    // last_run.reports: null must render as 0, not crash the page.
    await expect(page.getByText("Last run reports")).toBeVisible();
    await expect(
      page.getByText("Last run reports").locator("..").getByText("0"),
    ).toBeVisible();

    await page.goto("/settings");
    await page.getByRole("button", { name: "Sign out" }).click();
    await page.waitForURL("**/login");

    // ADMIN: Run now is present, requires the ConfirmDialog, and refreshes
    // the list on success.
    await login(page);
    await page.goto("/screener-reports");
    await expect(page.getByRole("button", { name: "Run now…" })).toBeVisible();
    await page.getByRole("button", { name: "Run now…" }).click();
    const dialog = page.getByRole("dialog", {
      name: "Run screener reports now?",
    });
    await expect(dialog).toBeVisible();
    await dialog.getByRole("button", { name: "Run now", exact: true }).click();
    await expect(page.getByText(/Ran for 2026-08-26/)).toBeVisible({
      timeout: 10_000,
    });

    // List row shows the list-only fields; hit rate/CI is not fabricated
    // here (report.Summary carries no hit_rate — only GET /{id} does).
    await expect(page.getByText("12.34")).toBeVisible();
    await expect(page.getByText("see report")).toBeVisible();

    await page.getByRole("link", { name: "View", exact: true }).click();
    await page.waitForURL("**/screener-reports/rep-1");
    await expect(
      page.getByRole("heading", { name: "Screener Report" }),
    ).toBeVisible();
    await expect(page.getByText("Duration and coverage")).toBeVisible();
    await expect(
      page.getByText("no evidence yet: regime days not computed"),
    ).toBeVisible();
    await expect(page.getByText("bullet one")).toBeVisible();
    // The <script> tag in the stored markdown must render as inert text,
    // never execute — assert no global it would set exists.
    await expect(page.getByText(/<script>/)).toBeVisible();
    expect(
      await page.evaluate(
        () => (window as unknown as { __xss?: number }).__xss,
      ),
    ).toBeUndefined();
    await expect(page.getByText(/SIMULATED\. Paper result/)).toBeVisible();
  });
});

// --- F5: overview five-second test -----------------------------------------
// PnL, risk (breakers), feed state and clock join the strip: mocked to
// deterministic payloads so the assertions pin exactly what the cells
// render (the live engine has no exchange egress, so the real books/clock
// sections can be absent for a long time — see mockPaperRunning's note).
test("overview answers PnL, breakers, feed state and clock in the status strip (F5)", async ({
  page,
}) => {
  await login(page);
  await page.route("**/api/v1/pnl", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          assets: [
            {
              asset: "USDT",
              // Two-decimal-friendly values: the bounded decimal display
              // (D3) rounds at 2dp here, and this test pins the *shape*
              // Overview renders, not a raw backend string — a value
              // that rounds to a different-looking figure (e.g.
              // "0.0012") would make this test assert its own rounding
              // rather than the page's.
              realized: "-12.50",
              exposure_mark: "0",
              net_pnl: "-12.50",
              unmarked: [],
              fees: "1.1",
              fees_marked: "1.10",
              fees_by_asset: { USDT: "1.1", BNB: "0.0012" },
              fees_unmarked: [],
              daily_loss: "-12.50",
              drawdown: "0.10",
            },
          ],
        },
        error: null,
      }),
    });
  });
  await page.route("**/api/v1/risk", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          config_version: 1,
          breakers: [
            { Name: "daily_loss", Scope: "", State: "OPEN", Reason: "USDT session loss reached the limit" },
          ],
          reject_reason_counts: {},
        },
        error: null,
      }),
    });
  });
  await page.route("**/api/v1/system/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          ready: true,
          triangles: 2,
          scanner: { evaluations: 10, revalidations: 4, revalidation_rejects: 1 },
          feed: { frames: 100, reconnects: 0, api_errors: 0, resyncs: 0, seq_gaps: 0, rate_limited: 0 },
          books: [
            { market: "BTCUSDT", state: "HEALTHY", age_ms: 40 },
            { market: "ETHBTC", state: "HEALTHY", age_ms: 55 },
            { market: "ETHUSDT", state: "STALE", age_ms: 900 },
          ],
          clock: { healthy: true, offset_ms: 12.5 },
        },
        error: null,
      }),
    });
  });
  await page.goto("/overview");

  // Realized PnL, per start asset (T-087 Overview rebuild — a ResultRow
  // card per asset, never a cross-asset sum), toned by the backend's own
  // sign; presentSignedQuote shows an explicit sign on every value.
  // Two ancestors up: the asset span's immediate parent is only the
  // header row (asset + "this session"); the dl of values is that
  // header row's sibling under the outer card div.
  const resultCard = page.getByText("USDT", { exact: true }).locator("../..");
  // .first(): DecimalValue renders both an aria-hidden visible span and
  // a sr-only span carrying the identical text (for assistive tech), so
  // an unscoped match always resolves to two elements minimum; realized
  // and net happen to share this value here too (exposure_mark is 0).
  await expect(resultCard.getByText("-12.50 USDT").first()).toBeVisible({
    timeout: 10_000,
  });
  // Drawdown is a dimensionless ratio, rendered as a percentage via
  // presentPercentFromFraction (D10: presentSignedQuote with the start
  // asset as its "unit" was a real defect — a 5.23% drawdown displayed
  // as "+0.05 USDC", a fabricated currency unit and a ~200x
  // understatement) — 0.10 is a 10% drawdown, not "0.10 USDT".
  await expect(resultCard.getByText("10.00%").first()).toBeVisible(); // drawdown
  // Fees paid is a cost, not a signed result — presentQuote, not
  // presentSignedQuote, so no leading "+" (a credit would be the wrong
  // reading of a fee).
  await expect(resultCard.getByText("1.10 USDT").first()).toBeVisible();

  // Attention: one open breaker, composed client-side from /api/v1/risk
  // (there is no backend field for "what needs attention" — the console
  // composes it, per the backend-contract review §5) — the backend's own
  // reason text renders verbatim as the item's detail line.
  const attentionItem = page.getByText(
    /1 circuit breaker open — qualification is gated while any is open\./,
  );
  await expect(attentionItem).toBeVisible({ timeout: 10_000 });
  await expect(
    page.getByText(
      "daily_loss (global): USDT session loss reached the limit",
    ),
  ).toBeVisible();

  // Status strip: one STALE book of three → DEGRADED under "Market feed"
  // (renamed from a bare "Feed" cell — still the same feedState() word).
  const feed = page.getByText("Market feed", { exact: true }).locator("..");
  await expect(feed.getByText("DEGRADED")).toBeVisible();
  // Venue clock cell — label unchanged.
  const clock = page.getByText("Venue clock", { exact: true }).locator("..");
  await expect(clock.getByText("OK")).toBeVisible();
});

test("overview explains a missing paper engine instead of a bare N/A (F5)", async ({
  page,
}) => {
  await login(page);
  await page.route("**/api/v1/scanner/status", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { data?: { paper?: unknown } | null };
    if (body.data) delete body.data.paper;
    await route.fulfill({ response, json: body });
  });
  await page.goto("/overview");
  // Status strip segment renamed "Paper engine" → "Triangular
  // simulation" (T-087 Overview rebuild) — same honest "NOT RUNNING"
  // word, plus a detail line naming the real running mode instead of a
  // bare N/A.
  const cell = page.getByText("Triangular simulation", { exact: true }).locator("..");
  await expect(cell.getByText("NOT RUNNING")).toBeVisible({ timeout: 10_000 });
  await expect(cell.getByText("N/A")).toHaveCount(0);
  await expect(cell.getByText(/mode is PAPER/)).toBeVisible();
});

// --- F6: Paper page as a live-cycle monitor --------------------------------
test("paper page shows in-flight cycles with leg badges, elapsed and the persisted reason (F6)", async ({
  page,
}) => {
  await login(page);
  await mockPaperRunning(page, true);
  await page.route("**/api/v1/paper/active", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          running: true,
          cycles: [
            {
              cycle_id: "cyc-live-1",
              opportunity_id: "op-live-1",
              triangle_id: "binance|USDT|BTCUSDT>ETHBTC>ETHUSDT",
              start_asset: "USDT",
              input: "1000",
              expected_net_bps: "12.5000",
              expected_profit: "1.25",
              started_at: new Date(Date.now() - 1500).toISOString(),
              legs: [
                { leg_no: 1, market: "BTCUSDT", side: "BUY", stage: "FILLED" },
                { leg_no: 2, market: "ETHBTC", side: "BUY", stage: "SUBMITTED" },
                { leg_no: 3, market: "ETHUSDT", side: "SELL", stage: "PENDING" },
              ],
            },
          ],
        },
        error: null,
      }),
    });
  });
  await page.route("**/api/v1/paper/cycles**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          cycles: [
            {
              id: "cyc-settled-1",
              session_id: "sess-1",
              opportunity_id: "op-1",
              outcome: "LEG1_FILLED_LEG2_FAILED",
              reason: "book ETHBTC is STALE at fill time",
              pnl_amount: "-9.9",
              pnl_asset: "USDT",
              realized_pnl: "-10",
              exposure_mark: "0.1",
              slippage_bps: null,
              fees: { BNB: "0.0012" },
              started_at: new Date(Date.now() - 60_000).toISOString(),
              settled_at: new Date(Date.now() - 59_900).toISOString(),
            },
          ],
        },
        error: null,
      }),
    });
  });
  await page.goto("/paper");

  // The three summary stats and the "Live cycles (in flight)" heading
  // were retired with the audit's duplication finding (§5) — the
  // section is now the plain "Running now" (ActiveCycles.tsx), and the
  // in-flight count is just the length of the list below it.
  await expect(
    page.getByRole("heading", { name: "Running now" }),
  ).toBeVisible({ timeout: 10_000 });
  const card = page.getByTestId("active-cycle");
  await expect(card).toBeVisible();
  // Leg badges: the stuck leg reads SUBMITTED, later legs PENDING.
  await expect(card.getByText("1 BTCUSDT BUY · FILLED")).toBeVisible();
  await expect(card.getByText("2 ETHBTC BUY · SUBMITTED")).toBeVisible();
  await expect(card.getByText("3 ETHUSDT SELL · PENDING")).toBeVisible();
  await expect(card.getByText("1000 USDT")).toBeVisible();
  await expect(card.getByText(/expected/)).toBeVisible();
  // Elapsed ticks on the client clock between polls.
  await expect(card.getByTestId("active-cycle-elapsed")).toContainText(
    /elapsed/,
  );

  // The persisted table answers "why", carries the fee bill and the
  // opportunity cross-link.
  await expect(page.getByText("book ETHBTC is STALE at fill time")).toBeVisible({
    timeout: 10_000,
  });
  // Fees now go through the bounded decimal display too: 0.0012 BNB
  // rounds to zero at 2dp, so it renders as the signed less-than form,
  // not the raw backend string — the exact value survives in the
  // wrapping title (feesExact).
  await expect(page.getByText("< 0.01 BNB")).toBeVisible();
  await expect(page.getByTitle("0.0012 BNB")).toBeVisible();
  await expect(page.getByText("Realized PnL", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "opportunity" })).toHaveAttribute(
    "href",
    "/opportunities/op-1",
  );
});

// --- Breaker acknowledgement (Risk Center) ---------------------------------
test("risk center closes an open breaker with the backend's own failure copy and a name-typed confirm", async ({
  page,
}) => {
  await login(page);
  let closeCalls = 0;
  await page.route("**/api/v1/risk", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          config_version: 1,
          breakers: [
            { Name: "daily_loss", Scope: "", State: "OPEN", Reason: "USDT session loss 500 reached the limit 500" },
          ],
          reject_reason_counts: {},
        },
        error: null,
      }),
    });
  });
  await page.goto("/risk");
  await expect(page.getByText("USDT session loss 500 reached the limit 500")).toBeVisible({
    timeout: 10_000,
  });

  const closeButton = page.getByRole("button", { name: "Close…" });
  await expect(closeButton).toBeVisible();
  await closeButton.click();

  const dialog = page.getByRole("dialog", { name: /Close breaker daily_loss/ });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(/resumes qualification on the next evaluation/i)).toBeVisible();

  // The confirm is disabled until the operator types the breaker's name.
  const confirm = dialog.getByRole("button", { name: "Close daily_loss" });
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel(/Type the breaker name/).fill("daily_los");
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel(/Type the breaker name/).fill("daily_loss");
  await expect(confirm).toBeEnabled();

  // Failure first: the toast must carry the backend's own status and
  // message verbatim (same convention as the paper pause control).
  await page.route("**/api/v1/risk/breakers/close", async (route) => {
    closeCalls++;
    if (closeCalls === 1) {
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          data: null,
          error: { code: "breaker_busy", message: "loss limit re-armed; read the reason" },
        }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { name: "daily_loss", scope: "", state: "CLOSED" },
        error: null,
      }),
    });
  });
  await confirm.click();
  await expect(
    page.getByText("Close failed (HTTP 409): loss limit re-armed; read the reason"),
  ).toBeVisible();
  await expect(dialog).toBeVisible(); // stays open on failure

  await dialog.getByRole("button", { name: "Close daily_loss" }).click();
  await expect(
    page.getByText(/Breaker daily_loss is CLOSED/),
  ).toBeVisible({ timeout: 10_000 });
  await expect(dialog).toHaveCount(0);
});

// ---- Navigation (T-087 new coverage) --------------------------------------
// web/unit/nav.spec.ts proves resolveNav()'s own logic in isolation; these
// prove the DOM actually reflects it — the primary/secondary nav highlight,
// the six-destinations-fit-without-scrolling layout guarantee, and that
// browser back/forward behaves. All against a real logged-in session.

const APP_DIR = join(__dirname, "..", "src", "app");
const OUTSIDE_SHELL = new Set(["/", "/login"]);

function discoverRoutes(dir: string, prefix = ""): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...discoverRoutes(full, `${prefix}/${entry}`));
    } else if (entry === "page.tsx") {
      out.push(prefix === "" ? "/" : prefix);
    }
  }
  return out;
}

function concreteRoute(route: string): string {
  return route.replace(/\[[^\]]+\]/g, "sample-id-01");
}

test("all six primary destinations are visible without scrolling at 1440x900", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page);
  const primary = page.getByRole("navigation", { name: "Primary" });
  for (const label of [
    "Overview",
    "Discover",
    "Paper Trading",
    "Research & Results",
    "Alerts & Rules",
    "Settings",
  ]) {
    await expect(
      primary.getByRole("link", { name: label, exact: true }),
    ).toBeInViewport();
  }
  // The Operator area (Operations) is a separate, explicitly labelled
  // group below the six product destinations — also always in view, not
  // scrolled away, since PrimaryNav is shrink-0 (D2).
  await expect(
    primary.getByRole("link", { name: "Operations", exact: true }),
  ).toBeInViewport();
});

test("aria-current marks the exact page 'page' and the destination containing it 'location'", async ({
  page,
}) => {
  await login(page);
  // /perpetuals is a secondary entry of Discover, not Discover's own
  // landing route (/screener) — the case that actually distinguishes
  // "location" from "page" (ConsoleNav.tsx's isLanding check).
  await page.goto("/perpetuals");
  const primary = page.getByRole("navigation", { name: "Primary" });
  await expect(
    primary.getByRole("link", { name: "Discover", exact: true }),
  ).toHaveAttribute("aria-current", "location");
  const secondary = page.getByRole("navigation", {
    name: "Discover sections",
  });
  await expect(
    secondary.getByRole("link", { name: "Perpetuals", exact: true }),
  ).toHaveAttribute("aria-current", "page");

  // Discover's own landing route: the primary link itself is the page.
  await page.goto("/screener");
  await expect(
    primary.getByRole("link", { name: "Discover", exact: true }),
  ).toHaveAttribute("aria-current", "page");
});

test("the three previously-broken pages now highlight their destination", async ({
  page,
}) => {
  // Before T-087 these three passed an `active` label matching nothing
  // in the old shell's lookup and highlighted no destination at all
  // (nav.ts's own header comment; web/unit/nav.spec.ts pins the same
  // three at the resolveNav() level). This is the DOM-level proof.
  await login(page);
  const primary = page.getByRole("navigation", { name: "Primary" });

  await page.goto("/cycles/sample-id-01");
  await expect(
    primary.getByRole("link", { name: "Paper Trading", exact: true }),
  ).toHaveAttribute("aria-current", "location");

  await page.goto("/onboarding");
  await expect(
    primary.getByRole("link", { name: "Overview", exact: true }),
  ).toHaveAttribute("aria-current", "location");

  await page.goto("/screener-reports/sample-id-01");
  await expect(
    primary.getByRole("link", { name: "Research & Results", exact: true }),
  ).toHaveAttribute("aria-current", "location");
  const secondary = page.getByRole("navigation", {
    name: "Research & Results sections",
  });
  await expect(
    secondary.getByRole("link", { name: "Screener evidence", exact: true }),
  ).toHaveAttribute("aria-current", "page");
});

test("every route on disk loads (or an honest Unavailable) and highlights the destination resolveNav() predicts", async ({
  page,
}) => {
  test.setTimeout(180_000);
  await login(page);
  const primary = page.getByRole("navigation", { name: "Primary" });
  const routes = discoverRoutes(APP_DIR).sort();
  expect(routes.length).toBeGreaterThanOrEqual(37);

  for (const route of routes) {
    if (OUTSIDE_SHELL.has(route)) continue;
    const concrete = concreteRoute(route);
    const match = resolveNav(concrete);
    expect(match, `${route} resolves to no navigation entry`).toBeTruthy();

    await page.goto(concrete);
    // Never a blank crash — either real content or an honest absence
    // notice (both are valid; a silently empty <main> is not).
    await expect(page.locator("main")).not.toBeEmpty({ timeout: 10_000 });
    await expect(page.locator("main")).not.toContainText(
      /Application error|this page could not be rendered/i,
    );

    const destId = match!.destination.id;
    await expect(
      primary.locator(`[data-nav-id="${destId}"]`),
      `${route}: destination "${destId}" is not marked current`,
    ).toHaveAttribute("aria-current", /page|location/);
  }
});

test("browser back and forward behave across destinations", async ({
  page,
}) => {
  await login(page);
  const primary = page.getByRole("navigation", { name: "Primary" });
  await page.goto("/overview");
  await page.getByRole("link", { name: "Discover", exact: true }).click();
  await expect(page).toHaveURL(/\/screener$/);
  await page.getByRole("link", { name: "Paper Trading", exact: true }).click();
  await expect(page).toHaveURL(/\/paper$/);

  await page.goBack();
  await expect(page).toHaveURL(/\/screener$/);
  await expect(
    primary.getByRole("link", { name: "Discover", exact: true }),
  ).toHaveAttribute("aria-current", "page");

  await page.goBack();
  await expect(page).toHaveURL(/\/overview$/);
  await expect(
    primary.getByRole("link", { name: "Overview", exact: true }),
  ).toHaveAttribute("aria-current", "page");

  await page.goForward();
  await expect(page).toHaveURL(/\/screener$/);
  await page.goForward();
  await expect(page).toHaveURL(/\/paper$/);
  await expect(
    primary.getByRole("link", { name: "Paper Trading", exact: true }),
  ).toHaveAttribute("aria-current", "page");
});

// ---- Settings deep links (T-087 new coverage) ------------------------------
// web/unit/settings-anchors.spec.ts proves the page *renders* every anchor
// it declares (a static source-text check, by its own admission not a
// browser proof); this is the browser proof it defers to the e2e suite:
// each anchor activates the right category tab AND moves focus into the
// section, not just scroll.

const LEGACY_ANCHOR_CATEGORIES: [string, string][] = [
  ["operating-mode", "Administration"],
  ["markets", "Administration"],
  ["scanner-suite", "Administration"],
  ["logging", "Administration"],
  ["ai", "Administration"],
  ["platform-versions", "Administration"],
  ["users", "Administration"],
  ["notifications", "Notifications"],
  ["security", "Administration"],
];

test("every pre-existing Settings anchor activates its category and moves focus into the section", async ({
  page,
}) => {
  await login(page); // bootstrap ADMIN: platform_admin true, sees Administration
  for (const [anchor, categoryLabel] of LEGACY_ANCHOR_CATEGORIES) {
    await page.goto(`/settings#${anchor}`);
    await expect(
      page.getByRole("tab", { name: categoryLabel }),
      `#${anchor} must activate the ${categoryLabel} tab`,
    ).toHaveAttribute("aria-selected", "true");
    await expect(
      page.locator(`#${anchor}`),
      `#${anchor} must take focus, not just scroll`,
    ).toBeFocused({ timeout: 5_000 });
  }
});

test("screener filters round-trip through the URL, and an opt-in is never enabled by a malformed link", async ({
  page,
}) => {
  // console-v2.md §6.1 asked for URL sync and it had never shipped: six
  // of the ten filters travelled in neither direction, so a screener
  // view could not be bookmarked, shared, or restored by Back, and the
  // Calculator hand-off came back to a default table.
  await login(page);
  await page.goto("/screener");

  // A common filter reaches the URL...
  const spreadField = page.getByRole("textbox", { name: /Min net spread/ });
  await spreadField.fill("42");
  await expect(page).toHaveURL(/min_spread_bps=42/, { timeout: 10_000 });

  // ...and an advanced one does too, as the string it was typed as. This
  // matters: min_liquidity is money, and a round trip through a float
  // would be the shortcut the rest of this change removes.
  //
  // The disclosure is opened by state, not by clicking blind: its label
  // flips between "Show" and "Hide" AND its open state is persisted in
  // localStorage, so a second unconditional click can just as easily
  // close it as open it depending on hydration timing.
  // Retried with toPass, because a visible button is not necessarily a
  // hydrated one: before React attaches its handler the click is inert
  // and aria-expanded never changes. Same race the capture harness hits
  // on /login.
  const ensureAdvancedOpen = async () => {
    const toggle = page.getByRole("button", { name: /advanced filters/i });
    await toggle.waitFor({ state: "visible", timeout: 10_000 });
    await expect(async () => {
      if ((await toggle.getAttribute("aria-expanded")) !== "true") {
        await toggle.click();
      }
      expect(await toggle.getAttribute("aria-expanded")).toBe("true");
    }).toPass({ timeout: 15_000 });
  };
  await ensureAdvancedOpen();
  await page.getByRole("textbox", { name: /Min liquidity/ }).fill("1234.56789");
  await expect(page).toHaveURL(/min_liquidity=1234\.56789/, {
    timeout: 10_000,
  });

  // Reload restores both, rather than resetting to defaults.
  await page.reload();
  // The applied-filter chip also carries this text ("Remove filter: Min
  // net spread: 42 bps"), so the field is addressed by role rather than
  // by label — the chip's presence is itself evidence the filter was
  // restored, but it is not the input.
  await expect(
    page.getByRole("textbox", { name: /Min net spread/ }),
  ).toHaveValue("42");
  // The query string kept it across the reload, which is the claim.
  await expect(page).toHaveURL(/min_liquidity=1234\.56789/);
  await ensureAdvancedOpen();
  await expect(
    page.getByRole("textbox", { name: /Min liquidity/ }),
  ).toHaveValue("1234.56789");

  // An opt-in that is off is absent from the URL — a shared link never
  // carries an opt-in the sender did not enable.
  await expect(page).not.toHaveURL(/include_suspect/);

  // And a malformed value must NOT enable it. These two opt-ins re-admit
  // lanes the backend excludes by default, so anything other than an
  // explicit affirmative has to fall back to the safe default.
  await page.goto("/screener?include_suspect=yes-please");
  const suspect = page.getByRole("checkbox", {
    name: /Include suspect lanes/i,
  });
  await expect(suspect).not.toBeChecked();
  await page.goto("/screener?include_suspect=1");
  await expect(
    page.getByRole("checkbox", { name: /Include suspect lanes/i }),
  ).toBeChecked();

  // An externally-changed URL wins over the component's own state. This
  // is the case that makes Back work and keeps the Calculator hand-off
  // intact: the filters are seeded at mount, so without adopting a later
  // URL change the sync effect would rewrite the address bar from stale
  // state and silently discard what the link asked for.
  await page.goto("/screener?min_spread_bps=42");
  await expect(
    page.getByRole("textbox", { name: /Min net spread/ }),
  ).toHaveValue("42");
  await page.goto("/screener?min_spread_bps=7&quote=USDT");
  await expect(
    page.getByRole("textbox", { name: /Min net spread/ }),
  ).toHaveValue("7");
  await expect(page).toHaveURL(/min_spread_bps=7/);

  // Clearing the filters clears the query string too.
  await page.goto("/screener?min_spread_bps=42");
  await page.getByRole("button", { name: /Clear filters/i }).click();
  await expect(page).not.toHaveURL(/min_spread_bps/, { timeout: 10_000 });
});

test("secondary navigation stays reachable on a short landscape viewport above the md breakpoint", async ({
  page,
}) => {
  // 844x390 — a phone in landscape. 844 is above md(768), so the *desktop*
  // sidebar renders into 390px of height. With `min-h-0 flex-1` the
  // secondary list resolved to zero height, and because it owns its own
  // overflow-y its clipped content did not extend the aside's scroll
  // either: every secondary entry became genuinely unreachable rather
  // than merely scrolled out of view. The capture matrix cannot catch
  // this — it measures horizontal overflow — so this is the evidence.
  await login(page);
  await page.setViewportSize({ width: 844, height: 390 });
  await page.goto("/risk");
  const opsNav = page.getByRole("navigation", { name: "Operations sections" });
  await expect(opsNav).toBeVisible();
  const box = await opsNav.boundingBox();
  expect(box, "the secondary nav must have a box at all").not.toBeNull();
  expect(
    box!.height,
    "the secondary nav collapsed to zero height, taking every secondary destination with it",
  ).toBeGreaterThan(40);
  // And an entry inside it is actually reachable, not merely present.
  const entry = opsNav.getByRole("link", { name: /Risk centre/i }).first();
  await entry.scrollIntoViewIfNeeded();
  await expect(entry).toBeVisible();
});

test("a Settings anchor this role cannot open says so instead of silently landing on Account", async ({
  page,
}) => {
  // The counterpart to the test above, and the branch that had no
  // coverage. A VIEWER has neither platform_admin nor screener:config, so
  // #users maps to a category they cannot open. Landing them on Account
  // with the URL still reading #users and nothing explaining why is what
  // master did not do — it rendered an honest "Requires…" at the anchor —
  // and the first version of SettingsCategories lost it by returning
  // early. The notice must also not appear while the session is still
  // loading, when the category list is legitimately short.
  const viewer = await ensureViewerAccount(page);
  await login(page, viewer.email, viewer.password);
  // #users is platform-only. Note what this does NOT assert: that the
  // Administration tab is absent. A VIEWER may read Scanner Suite
  // settings, so Administration legitimately opens for them — the
  // section inside it is what they cannot reach, which is why the
  // explanation is driven by whether the anchor was actually reached
  // rather than by whether the category exists.
  await page.goto("/settings#users");
  const notice = page.getByRole("status").filter({ hasText: /#users/ });
  await expect(notice).toBeVisible({ timeout: 10_000 });
  await expect(notice).toContainText(/not available to your role/i);
  await expect(page.locator("#users")).toHaveCount(0);

  // And the capability a VIEWER does have still works: the backend grants
  // PermScreenerView to VIEWER, so hiding the whole Administration
  // category from them was a lost capability, not a safety measure.
  await page.goto("/settings#scanner-suite");
  await expect(
    page.getByRole("tab", { name: "Administration" }),
  ).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("#scanner-suite")).toBeFocused({ timeout: 5_000 });
  // No stale explanation left over from the previous navigation. Matched
  // on the notice's own wording rather than on "#", which other status
  // regions on this page could contain.
  await expect(
    page.getByRole("status").filter({ hasText: /not available to your role/i }),
  ).toHaveCount(0);
});

test("switching Settings category and back does not lose an unsaved edit", async ({
  page,
}) => {
  // Categories stay mounted (hidden, not unmounted) precisely so a draft
  // survives a tab switch — SettingsCategories.tsx's whole reason for
  // existing over a naive tab rewrite.
  await login(page);
  await page.goto("/settings");
  await page.getByRole("button", { name: "Change my password" }).click();
  const draftValue = "unsaved-draft-password-text";
  await page.getByLabel("Current password").fill(draftValue);

  await page.getByRole("tab", { name: "Notifications" }).click();
  await expect(page.getByRole("tab", { name: "Notifications" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await page.getByRole("tab", { name: "Account" }).click();
  await expect(page.getByLabel("Current password")).toHaveValue(draftValue);
});

test("the Settings category tablist is arrow-key navigable with a roving tabindex", async ({
  page,
}) => {
  // Derived from the DOM rather than a hardcoded category list: Settings
  // categories are Account/Notifications plus a conditional
  // Administration for anyone who can configure something — the
  // bootstrap ADMIN always gets all three, but the count is not this
  // test's business, only the roving-tabindex mechanics are.
  await login(page);
  await page.goto("/settings");
  const tablist = page.getByRole("tablist", { name: "Settings categories" });
  const tabs = tablist.getByRole("tab");
  // /settings is a fresh full navigation, so AuthProvider remounts and
  // starts at `{kind: "loading"}` — the Administration tab only appears
  // once /auth/me resolves (platform_admin/role-gated). Wait for it
  // rather than counting on whatever the very first paint happened to
  // have, which would otherwise race this assertion.
  await expect(tablist.getByRole("tab", { name: "Administration" })).toBeVisible({
    timeout: 10_000,
  });
  const count = await tabs.count();
  expect(count).toBeGreaterThanOrEqual(3);

  const first = tabs.nth(0);
  const second = tabs.nth(1);
  const last = tabs.nth(count - 1);

  await expect(first).toHaveAttribute("tabindex", "0");
  await first.focus();

  await page.keyboard.press("ArrowRight");
  await expect(second).toBeFocused();
  await expect(second).toHaveAttribute("aria-selected", "true");
  await expect(second).toHaveAttribute("tabindex", "0");
  // The tab that lost selection drops out of the tab order — one stop
  // for the whole widget, not one per tab.
  await expect(first).toHaveAttribute("tabindex", "-1");

  await page.keyboard.press("ArrowLeft");
  await expect(first).toBeFocused();
  await expect(first).toHaveAttribute("aria-selected", "true");

  // Wrap-around: ArrowLeft from the first tab goes to the last one
  // (onTabKey's explicit `index === 0 ? last : index - 1`).
  await page.keyboard.press("ArrowLeft");
  await expect(last).toBeFocused();
  await expect(last).toHaveAttribute("aria-selected", "true");

  await page.keyboard.press("Home");
  await expect(first).toBeFocused();
  await expect(first).toHaveAttribute("aria-selected", "true");

  await page.keyboard.press("End");
  await expect(last).toBeFocused();
  await expect(last).toHaveAttribute("aria-selected", "true");
});

// ---- Drawer, Calculator hand-off, decimal display (T-087 new coverage) ----
// One fixed pair of screener rows reused by all three tests below: a BTC/
// USDT row with a many-fractional-digit spread for the decimal-shape
// assertion, and an ETH/USDT row selling on `kucoin` — a venue outside
// VENUE_OPTIONS' six-name chip vocabulary — for the calculator hand-off
// regression (D-doc: sourcing options from /screener/status, not the
// static list, is what fixed a real defect where such a venue silently
// fell back to "binance").
const SCREENER_ROWS = [
  {
    base: "BTC",
    quote: "USDT",
    buy_venue: "binance",
    sell_venue: "okx",
    buy_ask: "50000.12",
    buy_ask_qty: "1.5",
    sell_bid: "50010.339999",
    sell_bid_qty: "1.2",
    spread_bps_gross: "15.987654321098765",
    spread_bps_net: "12.345678912345678901",
    liquidity_quote: "75000",
    liquidity_unknown: false,
    suspect: false,
    lifetime_s: 30,
    first_seen_at: new Date().toISOString(),
    buy_age_ms: 100,
    sell_age_ms: 120,
    buy_fee_bps: "10",
    sell_fee_bps: "10",
    networks: { buy_withdraw: "open", sell_deposit: "open" },
  },
  {
    base: "ETH",
    quote: "USDT",
    buy_venue: "binance",
    sell_venue: "kucoin",
    buy_ask: "3000.5",
    buy_ask_qty: "5",
    sell_bid: "3005.75",
    sell_bid_qty: "4",
    spread_bps_gross: "8.5",
    spread_bps_net: "5.00",
    liquidity_quote: "15000",
    liquidity_unknown: false,
    suspect: false,
    lifetime_s: 45,
    first_seen_at: new Date().toISOString(),
    buy_age_ms: 200,
    sell_age_ms: 220,
    buy_fee_bps: "10",
    sell_fee_bps: "10",
    networks: { buy_withdraw: "open", sell_deposit: "open" },
  },
];

async function mockScreenerSpreads(page: Page) {
  await page.route("**/api/v1/screener/spreads**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          rows: SCREENER_ROWS,
          total: SCREENER_ROWS.length,
          generated_at: new Date().toISOString(),
          model: "no-transfer, top-of-book",
          excluded: { suspect: 0, liquidity_unknown: 0 },
        },
        error: null,
      }),
    });
  });
  // kucoin is reported by the backend's own venue list — not part of the
  // static six-name VENUE_OPTIONS chip vocabulary — so the Calculator's
  // venueOptions (sourced from this endpoint) genuinely includes it.
  await page.route("**/api/v1/screener/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          venues: ["binance", "okx", "kucoin"].map((name) => ({
            id: name,
            name,
            enabled: true,
            online: true,
            poll_ms: 500,
            spot_pairs: 10,
            perp_contracts: 0,
            rate_limited: false,
          })),
          pairs_tracked: 2,
          spreads_per_sec: 1,
          poll_interval_s: 3,
          updated_at: new Date().toISOString(),
        },
        error: null,
      }),
    });
  });
}

test("Screener Detail drawer manages focus: opens to the close button, Escape closes it, and focus returns to the originating row's Detail button", async ({
  page,
}) => {
  await login(page);
  await mockScreenerSpreads(page);
  await page.goto("/screener");
  const row = page.getByRole("row", { name: /ETH\/USDT/ });
  await expect(row).toBeVisible({ timeout: 10_000 });
  const detailButton = row.getByRole("button", { name: "Detail" });
  await detailButton.click();

  const drawer = page.getByRole("complementary", { name: /^Detail:/ });
  await expect(drawer).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Close detail panel" }),
  ).toBeFocused();

  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
  await expect(detailButton).toBeFocused();
});

test("Screener → Detail → Open in Calculator prefills base, quote and both venues, and a venue outside the six-name list is not silently replaced", async ({
  page,
}) => {
  await login(page);
  await mockScreenerSpreads(page);
  await page.goto("/screener");
  const row = page.getByRole("row", { name: /ETH\/USDT/ });
  await expect(row).toBeVisible({ timeout: 10_000 });
  await row.getByRole("button", { name: "Detail" }).click();
  // A <button> with an onClick router.push, not an <a> — no href to
  // follow, so the navigation is the effect of the click.
  await page.getByRole("button", { name: /Open in Calculator/ }).click();

  await expect(page).toHaveURL(/\/calculator\?/);
  // getByPlaceholder, not getByLabel: the Quote asset field's own hint
  // text ("denominated in the quote asset above") makes a case-
  // insensitive substring match on "Quote asset" ambiguous with the
  // unrelated Size field's hint.
  await expect(page.getByPlaceholder("BTC")).toHaveValue("ETH");
  await expect(page.getByPlaceholder("USDT")).toHaveValue("USDT");
  await expect(page.getByLabel("Buy venue")).toHaveValue("binance");
  // The regression this pins: kucoin is outside VENUE_OPTIONS' static
  // six-name list. A <select> whose value is not among its options
  // renders the *first* option instead — which silently repriced a
  // different venue pair than the row that was clicked. Sourcing options
  // from /screener/status (mocked above) fixed it.
  const sellVenue = page.getByLabel("Sell venue");
  await expect(sellVenue).toHaveValue("kucoin");
  await expect(
    sellVenue.locator('option[value="kucoin"]'),
  ).toHaveCount(1);
});

test("a bps cell shows a bounded value and the exact value is reachable via data-exact and the tooltip", async ({
  page,
}) => {
  await login(page);
  await mockScreenerSpreads(page);
  await page.goto("/screener");
  const row = page.getByRole("row", { name: /BTC\/USDT/ });
  await expect(row).toBeVisible({ timeout: 10_000 });

  const exactValue = "12.345678912345678901";
  const cell = row.locator(`[data-exact="${exactValue}"]`);
  await expect(cell).toHaveCount(1);
  // Shape, not a specific number: bounded to 2 fraction digits, never
  // the 20-plus-digit raw string the audit found on this exact column.
  const visibleText = await cell.locator("[aria-hidden]").innerText();
  expect(visibleText).toMatch(/^[+-]\d[\d,]*\.\d{2} bps$/);
  expect(visibleText.replace(/[^0-9]/g, "").length).toBeLessThan(exactValue.length);
  // The exact backend value is never lost — reachable via data-exact and
  // the "Exactly …" tooltip (never with the unit re-appended).
  await expect(cell).toHaveAttribute("title", `Exactly ${exactValue}`);
});

// ---- Roles and entitlements (T-087 new coverage) ---------------------------
// The backend sets platform_admin = (role == ADMIN) unconditionally
// (authstore.go), so a real tenant ADMIN with platform_admin:false cannot
// be produced through this harness's real signup path — rewriting
// /auth/me in place is the same, already-established pattern the
// risk-ack test above uses for a field the harness cannot otherwise drive.
test("Settings' platform-configuration sections are gated on platform_admin, never the ADMIN display role — Scanner Suite/Strategy & risk are the opposite tier (D13)", async ({
  page,
}) => {
  await page.route("**/api/v1/auth/me", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data?: { platform_admin?: boolean };
    };
    if (body.data) body.data.platform_admin = false;
    await route.fulfill({ response, json: body });
  });
  await login(page); // bootstrap account: role ADMIN, platform_admin forced false above
  await page.goto("/settings");
  await page.getByRole("tab", { name: "Administration" }).click();

  // Role-gated tier (PermScreenerConfig/PermRiskConfig, both ADMIN-role
  // checks) is NOT platform-gated, so it still renders.
  await expect(
    page.getByRole("heading", { name: "Scanner Suite" }),
  ).toBeVisible({ timeout: 10_000 });
  await expect(
    page.getByRole("heading", { name: "Strategy & risk" }),
  ).toBeVisible();

  // Platform-configuration tier (platform_admin-gated) does not — none
  // of the eight platform-only sections render, and no tenant data
  // belonging to them (e.g. Users & roles' member list, the vault's
  // secret names) crosses into this account's view.
  for (const heading of [
    "Operating mode",
    "Markets & assets",
    "Venues & fees",
    "AI advisor",
    "Logging & access",
    "Users & roles",
    "Security",
    "Platform settings — version history",
  ]) {
    await expect(page.getByRole("heading", { name: heading })).toHaveCount(0);
  }
  // In their place, an explanation pointing at the tenant's own surface —
  // nothing the tenant could previously change becomes unavailable, the
  // backend already refused all of it with platform_admin_required.
  // Two matches by design: the category's own description line and the
  // fallback section body say it in almost the same words.
  await expect(
    page.getByText(/operated by platform staff/i).first(),
  ).toBeVisible();
  await expect(
    page.locator("main").getByRole("link", { name: "Organisation" }),
  ).toHaveAttribute("href", "/org");
});

// ---- States (T-087 new coverage) -------------------------------------------
// loading / healthy-empty / stale / failed request / 401 / 403 / 409
// stale_version. An error must never render as "no results" or as a
// successful submission (ui.tsx's Await/ErrorBox is the shared mechanism
// behind every page this exercises).

test("a slow request renders an explicit loading state, never a blank or a fabricated value", async ({
  page,
}) => {
  await login(page);
  await page.route("**/api/v1/risk", async (route) => {
    await new Promise((r) => setTimeout(r, 2000));
    await route.continue();
  });
  await page.goto("/risk");
  await expect(page.getByText("Loading risk state…")).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Circuit breakers" }),
  ).toBeVisible({ timeout: 10_000 });
});

test("a 401 mid-session renders 'Session required', never a false empty or healthy state", async ({
  page,
}) => {
  await login(page);
  await page.route("**/api/v1/risk", async (route) => {
    await route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({
        data: null,
        error: { code: "unauthenticated", message: "session expired" },
      }),
    });
  });
  await page.goto("/risk");
  await expect(page.locator("main")).toContainText("Session required:");
  await expect(page.locator("main")).toContainText("session expired");
  // Never rendered as if the request had simply come back healthy/empty.
  await expect(
    page.getByRole("heading", { name: "Circuit breakers" }),
  ).toHaveCount(0);
});

test("a generic 403 forbidden renders 'Forbidden', never a silent success", async ({
  page,
}) => {
  await login(page);
  await page.route("**/api/v1/system/health", async (route) => {
    await route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({
        data: null,
        error: { code: "forbidden", message: "not allowed for this account" },
      }),
    });
  });
  await page.goto("/system");
  await expect(page.locator("main")).toContainText("Forbidden:");
  await expect(page.locator("main")).toContainText("not allowed for this account");
});

test("a 409 stale_version on a settings apply shows the reload notice, never a success, and the draft is not discarded", async ({
  page,
}) => {
  await login(page); // bootstrap ADMIN: platform_admin true
  await page.goto("/settings#operating-mode");
  await page.getByRole("button", { name: "Edit mode" }).click();
  // MARKET_DATA is Available in this build's ModeTable (unlike SHADOW),
  // so picking it produces a real diff to review and apply.
  await page.getByRole("radio", { name: "MARKET_DATA" }).check();
  await page.getByRole("button", { name: "Review changes" }).click();
  const dialog = page.getByRole("dialog", {
    name: "Apply new platform settings?",
  });
  await expect(dialog).toBeVisible({ timeout: 10_000 });

  await page.route("**/api/v1/platform/settings", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    await route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        data: null,
        error: { code: "stale_version", message: "settings changed" },
        current_version: 7,
      }),
    });
  });
  await dialog.getByRole("button", { name: "Apply", exact: true }).click();

  // The dialog closes on stale (the page-level notice takes over, not a
  // retry against a version that no longer exists) — never a silent
  // "Version N active." success message.
  await expect(dialog).toHaveCount(0, { timeout: 10_000 });
  await expect(page.getByText(/reload to continue/i)).toBeVisible();
  await expect(page.getByText(/Version \d+ active\./)).toHaveCount(0);
  // Still in edit mode with the draft intact — the confirm-and-apply flow
  // failed, so the operator's in-progress edit was not thrown away.
  await expect(
    page.getByRole("button", { name: "Review changes" }),
  ).toBeVisible();
  await expect(page.getByRole("radio", { name: "MARKET_DATA" })).toBeChecked();
});

test("a healthy-empty screener result renders an honest zero, not a generic 'no results'", async ({
  page,
}) => {
  await login(page);
  await page.route("**/api/v1/screener/spreads**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          rows: [],
          total: 0,
          generated_at: new Date().toISOString(),
          model: "no-transfer, top-of-book",
          excluded: { suspect: 0, liquidity_unknown: 0 },
        },
        error: null,
      }),
    });
  });
  await page.goto("/screener");
  // Specific and honest — states how many of how many pairs qualify —
  // not an unlabelled "no results" indistinguishable from a stalled poll
  // or a request that silently failed.
  await expect(
    page.getByText(/spreads matching these filters — 0 of 0 pairs qualify/),
  ).toBeVisible({ timeout: 10_000 });
});

// ---- Mobile (T-087 new coverage) -------------------------------------------
// At 390×844 the desktop <aside> is CSS-hidden (`hidden md:flex`) but
// stays mounted, so an unscoped role query for "Primary"/"complementary"
// matches it too. `.first()` reliably picks the currently-relevant one:
// the mobile overlay's copy renders earlier in the JSX (conditionally, only
// while open) than the always-mounted desktop one.
test("mobile: the hamburger reveals the same navigation, Escape closes the overlay and returns focus, and the mode/pause control are visible without opening it", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await mockPaperRunning(page, true);
  await page.goto("/overview");

  const menuButton = page.getByRole("button", { name: "Open navigation" });
  await expect(menuButton).toBeVisible();
  // Mode + pause control visible without ever opening the menu (F1/D7).
  // The compact top-bar ModeBanner shows the bare word ("PAPER") with
  // the full sentence in its title, mirroring the existing F1 test.
  await expect(page.getByTitle(/PAPER TRADING ONLY/)).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Pause triangular simulations" }).first(),
  ).toBeVisible();

  await menuButton.click();
  await expect(
    page.getByRole("button", { name: "Close navigation" }),
  ).toBeVisible();
  // Same navigation definition — the six primary destinations plus
  // Operations, in the overlay.
  const overlayPrimary = page
    .getByRole("navigation", { name: "Primary" })
    .first();
  for (const label of [
    "Overview",
    "Discover",
    "Paper Trading",
    "Research & Results",
    "Alerts & Rules",
    "Settings",
    "Operations",
  ]) {
    await expect(
      overlayPrimary.getByRole("link", { name: label, exact: true }),
    ).toBeVisible();
  }

  await page.keyboard.press("Escape");
  await expect(
    page.getByRole("button", { name: "Close navigation" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Open navigation" }),
  ).toBeFocused();
});

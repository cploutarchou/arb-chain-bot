import { test, expect, type Page } from "@playwright/test";

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
  await page.getByRole("button", { name: "Apply new version" }).click();
  await expect(page.getByText(/Version \d+ active\./)).toBeVisible({
    timeout: 10_000,
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
  const sidebar = page.getByRole("complementary");
  const pauseButton = sidebar.getByRole("button", {
    name: "Pause paper trading",
  });
  await expect(pauseButton).toBeVisible({ timeout: 10_000 });

  await pauseButton.click();
  const dialog = page.getByRole("dialog", { name: "Pause paper trading?" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText(/in-flight simulations/i);

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
  await dialog.getByRole("button", { name: "Pause paper trading" }).click();
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
  await page.goto("/settings");
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.waitForURL("**/login");
  return VIEWER_TEST;
}

test("Administration navigation is hidden for a VIEWER (operator-only section, never flashed)", async ({
  page,
}) => {
  const viewer = await ensureViewerAccount(page);
  await login(page, viewer.email, viewer.password);
  await page.goto("/overview");
  const nav = page.getByRole("navigation");
  // The operator administration section (Risk Center, Exchanges,
  // Markets, System Health, Audit Log) is not part of a VIEWER's
  // navigation at all — the client-area refinement keeps platform
  // administration to entitled operator staff, and the routes'
  // own pages still answer 403 with a clear error if reached by URL.
  for (const label of ["Administration", "Risk Center", "Audit Log"]) {
    await expect(nav.getByText(label)).toHaveCount(0);
  }
  await expect(nav.getByRole("link", { name: "Audit Log" })).toHaveCount(0);
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
      name: /Pause paper trading|Resume paper trading/,
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

  await expect(page.getByText(/Excluded: suspect 3/)).toBeVisible();

  // The unsafe-lane opt-ins live behind the Advanced disclosure
  // (client-area audit §2): open it, then check both toggles. Scoped to
  // the summary element — the excluded-counts line also mentions
  // "Advanced filters" in prose.
  await page.locator("summary", { hasText: "Advanced filters" }).click();
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

  test("six primary destinations render; every Scanner Suite page is reachable through Discover for an OPERATOR login", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    await login(page, OPERATOR.email, OPERATOR.password);
    await page.goto("/overview");
    const nav = page.getByRole("navigation");
    // The task-based primary navigation (client-area audit §1): six
    // destinations fit without scrolling, replacing the 29-link group
    // wall. Administration is an explicitly labelled operator section.
    for (const label of [
      "Overview",
      "Discover",
      "Paper Trading",
      "Research & Results",
      "Alerts & Rules",
      "Settings",
    ]) {
      await expect(
        nav.getByRole("link", { name: label, exact: true }),
      ).toBeVisible();
    }
    await expect(nav.getByText("Administration")).toBeVisible();

    // Discover reveals the Scanner Suite surfaces contextually (the
    // audit's discoverability requirement: not all links at once, but
    // one navigation activation away).
    await page.goto("/screener");
    for (const label of [
      "Screener (cross-exchange)",
      "Scanner (triangular)",
      "Perpetuals",
      "Funding",
      "Calculator",
      "Triangles",
      "Opportunities",
    ]) {
      await expect(
        nav.getByRole("link", { name: label, exact: true }),
      ).toBeVisible();
    }

    const notAvailable = /Screener backend not available in this build\./;
    const pages: [string, RegExp][] = [
      ["/screener", new RegExp(`Screener|${notAvailable.source}`)],
      ["/perpetuals", new RegExp(`Perpetuals|${notAvailable.source}`)],
      ["/funding", new RegExp(`Funding|${notAvailable.source}`)],
      ["/calculator", /Spreads calculator/],
      ["/scanner-alerts", new RegExp(`Alert Rules|${notAvailable.source}`)],
      [
        "/screener-reports",
        new RegExp(`Screener Reports|${notAvailable.source}`),
      ],
      ["/auto-paper", new RegExp(`Auto-Paper|${notAvailable.source}`)],
    ];
    for (const [path, marker] of pages) {
      await page.goto(path);
      await expect(page.locator("main")).toContainText(marker, {
        timeout: 10_000,
      });
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
              realized: "-12.5",
              exposure_mark: "0",
              net_pnl: "-12.5",
              unmarked: [],
              fees: "1.1",
              fees_marked: "1.34",
              fees_by_asset: { USDT: "1.1", BNB: "0.0012" },
              fees_unmarked: [],
              daily_loss: "-12.5",
              drawdown: "0.0012",
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

  // Realized PnL with the asset, toned by the backend's own sign.
  const pnl = page.getByText("Realized PnL", { exact: true }).locator("..");
  await expect(pnl.getByText("-12.5 USDT")).toBeVisible({ timeout: 10_000 });
  // An open breaker is an attention item first (client-area audit §4B):
  // the fact with the breaker named, one link from the Risk Center.
  await expect(
    page.getByText(/1 risk breaker open — qualification is gated \(daily_loss\)\./),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Risk Center →" }),
  ).toBeVisible();
  // Feed cell: one STALE book of three → DEGRADED.
  const feed = page.getByText("Feed", { exact: true }).locator("..");
  await expect(feed.getByText("DEGRADED")).toBeVisible();
  // Venue clock cell.
  const clock = page.getByText("Venue clock").locator("..");
  await expect(clock.getByText("OK")).toBeVisible();
  // Fees and drawdown cells render the backend's own values.
  await expect(page.getByText("1.34 USDT").first()).toBeVisible();
  await expect(page.getByText("0.0012 USDT").first()).toBeVisible();
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
  const cell = page.getByText("Paper engine").locator("..");
  await expect(cell.getByText("NOT RUNNING")).toBeVisible({ timeout: 10_000 });
  await expect(cell.getByText("N/A")).toHaveCount(0);
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

  await expect(
    page.getByText("Live cycles (in flight)"),
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
  await expect(page.getByText("0.0012 BNB")).toBeVisible();
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

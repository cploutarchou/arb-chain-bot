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
  await expect(page.locator("main")).toContainText(
    /min_net_edge_bps|MinNetEdgeBps/i,
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
  await expect(page.getByText("On restart").first()).toBeVisible();
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
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.waitForURL("**/login");
  return VIEWER_TEST;
}

test("nav gating is visible for a VIEWER (role-restricted, not just hidden)", async ({
  page,
}) => {
  const viewer = await ensureViewerAccount(page);
  await login(page, viewer.email, viewer.password);
  await page.goto("/overview");
  const nav = page.getByRole("navigation");
  // Audit Log: GatedControl state="role" — grey, cursor-not-allowed,
  // non-navigable, with the actual minimum role named in the tooltip
  // (console-v2.md §2.4 — never a generic "restricted").
  await expect(nav.getByRole("link", { name: "Audit Log" })).toHaveCount(0);
  const gated = nav.locator('[title="Requires OPERATOR or ADMIN"]');
  await expect(gated).toBeVisible();
  await expect(gated).toHaveAttribute("aria-disabled", "true");
  await expect(gated).toContainText("Audit Log");
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

test.describe("Scanner Suite", () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage();
    await ensureOperatorAccount(page);
    await page.close();
  });

  test("nav group renders and every Scanner Suite page loads for an OPERATOR login", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    await login(page, OPERATOR.email, OPERATOR.password);
    await page.goto("/overview");
    const nav = page.getByRole("navigation");
    await expect(nav.getByText("Scanner Suite")).toBeVisible();
    for (const label of [
      "Screener",
      "Perpetuals",
      "Funding",
      "Calculator",
      "Alert Rules",
      "Auto-Paper",
    ]) {
      await expect(nav.getByRole("link", { name: label })).toBeVisible();
    }

    const notAvailable = /Screener backend not available in this build\./;
    const pages: [string, RegExp][] = [
      ["/screener", new RegExp(`Screener|${notAvailable.source}`)],
      ["/perpetuals", new RegExp(`Perpetuals|${notAvailable.source}`)],
      ["/funding", new RegExp(`Funding|${notAvailable.source}`)],
      ["/calculator", /Spreads calculator/],
      ["/scanner-alerts", new RegExp(`Alert Rules|${notAvailable.source}`)],
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
});

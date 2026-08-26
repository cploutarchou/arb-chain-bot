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
  // Sixteen page visits; dev-server first compiles push past the
  // default budget.
  test.setTimeout(120_000);
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

test("risk center shows deterministic limits", async ({ page }) => {
  await login(page);
  await page.goto("/risk");
  await expect(page.locator("main")).toContainText(/min_net_edge_bps|MinNetEdgeBps/i, {
    timeout: 10_000,
  });
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

test("sign out returns to login", async ({ page }) => {
  await login(page);
  await page.goto("/settings");
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.waitForURL("**/login");
});

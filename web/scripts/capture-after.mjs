// Capture the "after" state of the seven audited workflows across the
// viewport / theme / zoom matrix, and measure page-level horizontal
// overflow at each stop (T-087 acceptance).
//
// This is a measurement tool, not a test: it writes PNGs plus a JSON
// report of what it measured, so the verification record cites numbers
// rather than impressions. Overflow is the one thing screenshots alone
// cannot prove — a clipped page and a fitting page look identical in a
// viewport-sized capture — so every stop records
// documentElement.scrollWidth vs clientWidth and names the widest
// offending element when they differ.
//
// Credentials come from the environment and are never written to disk,
// into a screenshot, or into the report.
//
// Usage (from web/, against a disposable backend only — never the
// operator's research session on :8080):
//   CAPTURE_BASE_URL=http://127.0.0.1:3100 \
//   CAPTURE_EMAIL=... CAPTURE_PASSWORD=... \
//   node scripts/capture-after.mjs
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

const here = dirname(fileURLToPath(import.meta.url));
const OUT = join(here, "..", "..", "docs", "design", "client-area-refinement-after");

const BASE = process.env.CAPTURE_BASE_URL ?? "http://127.0.0.1:3100";
const EMAIL = process.env.CAPTURE_EMAIL;
const PASSWORD = process.env.CAPTURE_PASSWORD;
if (!EMAIL || !PASSWORD) {
  console.error("CAPTURE_EMAIL and CAPTURE_PASSWORD must be set.");
  process.exit(2);
}
// A capture run must never point at the operator's live stack (T-098).
if (/:8080(\/|$)/.test(BASE) || /:3000(\/|$)/.test(BASE)) {
  console.error(`refusing to capture against ${BASE} — that is the research session`);
  process.exit(2);
}

// The seven audited workflows, in the audit's own order.
const STATES = [
  { id: "01-overview", path: "/overview", label: "Overview" },
  { id: "02-screener", path: "/screener", label: "Discover — spot screener" },
  {
    id: "03-screener-detail",
    path: "/screener",
    label: "Discover — spread detail drawer",
    // Open the first row's detail panel.
    async prepare(page) {
      const detail = page.getByRole("button", { name: "Detail" }).first();
      await detail.waitFor({ state: "visible", timeout: 20_000 });
      await detail.click();
      await page.getByRole("complementary").first().waitFor({ timeout: 10_000 });
    },
  },
  {
    id: "04-calculator",
    path: "/calculator?base=RVN&quote=USDT&buy_venue=binance&sell_venue=kucoin",
    label: "Calculator — prefilled hand-off, calculated",
    async prepare(page) {
      const calc = page.getByRole("button", { name: "Calculate" });
      await calc.waitFor({ state: "visible", timeout: 20_000 });
      await calc.click();
      // Either a result or an honest error — both are valid states to
      // capture; never wait forever for a success that may not come.
      await page
        .getByText(/Liquidity (sufficient|insufficient)|Screener backend|failed/i)
        .first()
        .waitFor({ timeout: 20_000 })
        .catch(() => {});
    },
  },
  { id: "05-paper", path: "/paper", label: "Paper Trading — triangular" },
  { id: "06-auto-paper", path: "/auto-paper", label: "Paper Trading — rule simulations" },
  { id: "07-settings", path: "/settings", label: "Settings" },
];

// The acceptance matrix. The first entry is the primary desktop stop.
const VIEWPORTS = [
  { id: "1440x900", width: 1440, height: 900 },
  { id: "1024x768", width: 1024, height: 768 },
  { id: "768x1024", width: 768, height: 1024 },
  { id: "390x844", width: 390, height: 844 },
  // 844x390 — the same phone in landscape. Added because it is the
  // geometry that broke SecondaryNav: 844 is above the md breakpoint, so
  // the *desktop* sidebar renders into 390px of height, and with
  // `min-h-0 flex-1` the secondary list resolved to zero height and took
  // every secondary destination with it. A short-and-wide stop is the
  // only thing in this matrix that can catch that class of defect.
  { id: "844x390", width: 844, height: 390 },
];
const THEMES = ["dark", "light"];

mkdirSync(OUT, { recursive: true });

// measure reports page-level horizontal overflow and, when there is
// any, the widest element responsible — that is what turns "looks fine"
// into a finding someone can act on. An element inside an explicit
// overflow-x container is allowed to be wider than the page; only the
// document scrolling is a defect.
const MEASURE = () => {
  const de = document.documentElement;
  const overflow = de.scrollWidth - de.clientWidth;
  let widest = null;
  if (overflow > 0) {
    let max = 0;
    for (const el of document.querySelectorAll("body *")) {
      const r = el.getBoundingClientRect();
      const right = r.right;
      if (right > max) {
        max = right;
        widest = {
          tag: el.tagName,
          cls: String(el.className || "").slice(0, 120),
          right: Math.round(right),
        };
      }
    }
  }
  // Count the scroll regions that are *meant* to scroll, so the report
  // can distinguish an intentional table scroller from a broken page.
  const scrollers = [...document.querySelectorAll("[role='region'],.overflow-x-auto")]
    .filter((el) => el.scrollWidth > el.clientWidth)
    .map((el) => ({
      label: el.getAttribute("aria-label"),
      focusable: el.tabIndex >= 0,
      overflow: el.scrollWidth - el.clientWidth,
    }));
  return {
    scrollWidth: de.scrollWidth,
    clientWidth: de.clientWidth,
    overflow,
    widest,
    scrollers,
  };
};

const results = [];
const browser = await chromium.launch();

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const context = await browser.newContext({
      viewport: { width: vp.width, height: vp.height },
      colorScheme: theme,
      // deviceScaleFactor 1 keeps the PNG the viewport's own size, so a
      // reviewer comparing against the audit is comparing like with like.
      deviceScaleFactor: 1,
    });
    const page = await context.newPage();

    // Log in once per context.
    //
    // Retried, because a fixed hydration wait is not a guarantee. Before
    // React attaches its submit handler the button is inert, so a click
    // silently does nothing and this script then sat on /login until the
    // navigation timeout. `next dev` compiles routes on demand, so how
    // long hydration takes depends on what the server is busy with — one
    // capture run died on the fourth viewport for exactly this reason
    // after three had passed. Clicking again is safe: if the first click
    // did register, the URL has already changed and the loop exits.
    for (let attempt = 1; ; attempt++) {
      await page.goto(`${BASE}/login`, { waitUntil: "load" });
      await page.waitForLoadState("networkidle").catch(() => {});
      await page.waitForTimeout(1500);
      await page.locator('input[type="email"]').fill(EMAIL);
      await page.locator('input[type="password"]').fill(PASSWORD);
      await page.getByRole("button", { name: "Sign in" }).click();
      try {
        await page.waitForURL((u) => !u.pathname.endsWith("/login"), {
          timeout: 20_000,
        });
        break;
      } catch (err) {
        if (attempt >= 3) throw err;
        console.warn(
          `login attempt ${attempt} did not navigate (${vp.id}/${theme}); retrying`,
        );
      }
    }
    // The theme toggle persists a choice in localStorage that would
    // override the emulated colorScheme; clear it so the media query
    // stays authoritative and `colorScheme` above is what is captured.
    await page.evaluate(() => {
      try {
        localStorage.removeItem("arb.theme");
      } catch {}
      document.documentElement.removeAttribute("data-theme");
    });

    for (const state of STATES) {
      await page.goto(`${BASE}${state.path}`, { waitUntil: "domcontentloaded" });
      // Let the first poll land so the capture shows real states rather
      // than a page full of loading ellipses.
      await page.waitForTimeout(4000);
      if (state.prepare) {
        try {
          await state.prepare(page);
          await page.waitForTimeout(1500);
        } catch (err) {
          results.push({
            state: state.id,
            theme,
            viewport: vp.id,
            error: `prepare failed: ${String(err).slice(0, 200)}`,
          });
        }
      }
      const measured = await page.evaluate(MEASURE);
      const name = `${state.id}--${theme}--${vp.id}.png`;
      await page.screenshot({ path: join(OUT, name), fullPage: false });
      results.push({
        state: state.id,
        label: state.label,
        theme,
        viewport: vp.id,
        file: name,
        ...measured,
      });
      console.log(
        `${name}  overflow=${measured.overflow}px  scrollers=${measured.scrollers.length}`,
      );
    }
    await context.close();
  }
}

// 200% zoom at the primary desktop stop. Emulated the way a browser
// zoom actually behaves — the CSS viewport halves — rather than by
// scaling the image, which would prove nothing.
{
  const context = await browser.newContext({
    viewport: { width: 720, height: 450 },
    colorScheme: "dark",
    deviceScaleFactor: 2,
  });
  const page = await context.newPage();
  await page.goto(`${BASE}/login`, { waitUntil: "load" });
  // Wait for hydration before touching the form. Before React
  // attaches its submit handler the button is inert, so filling
  // and clicking immediately silently does nothing — the first
  // run of this script sat on /login for 30s for that reason.
  await page.waitForTimeout(3000);
  await page.locator('input[type="email"]').fill(EMAIL);
  await page.locator('input[type="password"]').fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL((u) => !u.pathname.endsWith("/login"), { timeout: 30_000 });
  for (const state of STATES.filter((s) => !s.prepare)) {
    await page.goto(`${BASE}${state.path}`, { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(3500);
    const measured = await page.evaluate(MEASURE);
    const name = `${state.id}--dark--zoom200.png`;
    await page.screenshot({ path: join(OUT, name) });
    results.push({
      state: state.id,
      label: state.label,
      theme: "dark",
      viewport: "1440x900 @ 200% zoom",
      file: name,
      ...measured,
    });
    console.log(`${name}  overflow=${measured.overflow}px`);
  }
  await context.close();
}

await browser.close();

const offenders = results.filter((r) => (r.overflow ?? 0) > 0);
const errors = results.filter((r) => r.error);
writeFileSync(
  join(OUT, "overflow-report.json"),
  JSON.stringify({ generatedAt: new Date().toISOString(), base: BASE, results }, null, 2),
);
console.log(
  `\n${results.length} stops captured, ${offenders.length} with page-level horizontal overflow, ${errors.length} prepare errors`,
);
for (const o of offenders) {
  console.log(`  OVERFLOW ${o.state} ${o.theme} ${o.viewport}: ${o.overflow}px — ${JSON.stringify(o.widest)}`);
}
for (const e of errors) console.log(`  ERROR ${e.state} ${e.theme} ${e.viewport}: ${e.error}`);
process.exit(offenders.length > 0 ? 1 : 0);

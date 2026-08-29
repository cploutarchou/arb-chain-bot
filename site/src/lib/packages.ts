// Capability table — docs/design/packages.md §2 (PROPOSAL), capability and
// limit rows only. Prices are never typed here: the price row reads the
// {{price.*}} tokens from site.config.ts (packages.md §4: Paddle previews).
// Strategy names follow docs/marketing/plan.md §3.3 (no banned words).

export const PACKAGES = ["Watch", "Signal", "Operator", "Desk", "Institution"] as const;
export type PackageName = (typeof PACKAGES)[number];

export interface CapabilityRow {
  label: string;
  cells: [string, string, string, string, string];
}

export const CAPABILITY_ROWS: CapabilityRow[] = [
  {
    label: "Trial",
    cells: [
      "—",
      "14-day Operator trial on sign-up, no card, one per organisation",
      "same",
      "same",
      "pilot by agreement",
    ],
  },
  {
    label: "Venues (screener and perpetuals)",
    cells: [
      "3 fixed",
      "6 (Tier-1 set)",
      "all Tier-1 and Tier-2 (10)",
      "all CEX venues (15)",
      "all, plus venue requests",
    ],
  },
  { label: "Triangular engine venues", cells: ["1", "2", "4", "all supported", "all"] },
  {
    label: "Concurrent alert rules",
    cells: ["2", "8", "25", "80", "250 (soft; raise on request)"],
  },
  { label: "Saved screener templates", cells: ["3", "10", "40", "unlimited", "unlimited"] },
  {
    label: "Screener refresh interval",
    cells: ["30 s", "10 s", "5 s", "3 s", "2 s (collector floor)"],
  },
  {
    label: "Alert channels",
    cells: [
      "web",
      "web, Telegram",
      "web, Telegram, e-mail",
      "web, Telegram, e-mail, webhook",
      "all, multiple Telegram destinations",
    ],
  },
  { label: "Alerts per day", cells: ["20", "200", "1,500", "8,000", "40,000"] },
  {
    label: "Auto-paper strategies",
    cells: [
      "none (manual paper only)",
      "cross-venue spot",
      "cross-venue spot, carry, triangular",
      "all five (adds perpetual-against-perpetual and funding-rate position)",
      "all five",
    ],
  },
  { label: "Open simulated positions (concurrent)", cells: ["0", "5", "30", "150", "600"] },
  { label: "Simulated ledgers per organisation", cells: ["1", "1", "3", "10", "25"] },
  {
    label: "Client API",
    cells: [
      "no",
      "no",
      "read (60 req/min, 2 keys)",
      "read and rule/template writes (300 req/min, 10 keys)",
      "read and write (1,200 req/min, 50 keys), streaming WebSocket",
    ],
  },
  {
    label: "History depth",
    cells: ["24 h", "14 days", "90 days", "400 days", "3 years, plus export to object storage"],
  },
  {
    label: "Data export",
    cells: ["no", "CSV, 14 days", "CSV, 90 days", "CSV and Parquet, full depth", "plus scheduled exports"],
  },
  {
    label: "Evidence reports",
    cells: [
      "public samples only",
      "own rules, weekly",
      "own rules, nightly",
      "nightly, per-strategy comparison",
      "nightly, custom cadence",
    ],
  },
  { label: "Seats", cells: ["1", "1", "3", "12", "40 (more on quote)"] },
  {
    label: "Roles",
    cells: ["owner", "owner", "owner, admin, viewer", "plus operator", "plus custom role names"],
  },
  {
    label: "Support",
    cells: [
      "community docs",
      "e-mail, 2 business days",
      "e-mail, 1 business day",
      "e-mail and shared Telegram channel, 8 business hours",
      "named contact, 4 business hours, quarterly review",
    ],
  },
];

/** The final row, every column, verbatim (packages.md §7, pricing.md). */
export const LIVE_EXECUTION_ROW = "Live execution — not offered";

// Typed API client for the Go backend. Every financial value crosses the
// wire as a string (decimal-safe) and is formatted for display only —
// the frontend NEVER recomputes profitability, fees, or risk.

import { emitEntitlementExceeded, emitRiskAckRequired } from "@/lib/errorBus";

export interface APIError {
  code: string;
  message: string;
  correlation_id?: string;
}

export interface Envelope<T> {
  data: T | null;
  error: APIError | null;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly apiError: APIError | null,
    // data carries the envelope's `data` alongside an error response
    // (e.g. stale_version's {current_version} — WriteErrorData sends
    // both fields together, T-058) — never populated for a plain
    // WriteError response.
    readonly data: unknown = null,
  ) {
    super(apiError?.message ?? `API error (HTTP ${status})`);
    this.name = "ApiError";
  }
}

// isStaleVersion flags the 409 the optimistic-concurrency check (T-058)
// returns when parent_version no longer matches the active version —
// distinct from staleVersion() below, which can return null for a
// stale_version error whose current_version happens to be absent.
export function isStaleVersion(err: unknown): boolean {
  return err instanceof ApiError && err.apiError?.code === "stale_version";
}

// staleVersion reads {current_version} out of a 409 stale_version
// ApiError — the one machine-readable field the optimistic-concurrency
// check needs to render "reload to continue". Returns null when the
// field is unexpectedly absent; callers should still check
// isStaleVersion() first and fall back to the backend's message
// verbatim (never render "version undefined").
export function staleVersion(err: unknown): number | null {
  if (!isStaleVersion(err)) return null;
  const data = (err as ApiError).data as { current_version?: number } | null;
  return typeof data?.current_version === "number"
    ? data.current_version
    : null;
}

// isNotReady flags the 503 a campaign/replay POST can return briefly
// after boot (the runner's background context isn't wired yet) — the
// backend's message already ends in "try again shortly"; callers show
// it verbatim and add a retry affordance, never reword it.
export function isNotReady(err: unknown): boolean {
  return err instanceof ApiError && err.apiError?.code === "not_ready";
}

// CSRF token lives in module state; login and /auth/me both supply it
// (the backend derives it from the session, nothing is persisted here).
// After a full reload the token arrives asynchronously with the /auth/me
// recovery, so mutating requests briefly await it — otherwise a control
// clicked in the first moments after navigation fires tokenless and
// fails (the CI E2E run caught exactly that race on the reports page).
// The wait is bounded: an anonymous session never gets a token, and the
// request then proceeds to its honest 401/403.
let csrfToken = "";
let resolveCsrfReady: (() => void) | null = null;
let csrfReady: Promise<void> = new Promise((r) => {
  resolveCsrfReady = r;
});
export function setCsrfToken(t: string) {
  csrfToken = t;
  if (t) {
    resolveCsrfReady?.();
    resolveCsrfReady = null;
  } else if (resolveCsrfReady === null) {
    // Logout: future mutations wait for the next session's token.
    csrfReady = new Promise((r) => {
      resolveCsrfReady = r;
    });
  }
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init?.headers as Record<string, string>),
  };
  if (init?.method && init.method !== "GET") {
    // Login is the one mutation that legitimately runs tokenless.
    if (!csrfToken && path !== "/api/v1/auth/login") {
      await Promise.race([csrfReady, new Promise((r) => setTimeout(r, 5000))]);
    }
    if (csrfToken) {
      headers["X-CSRF-Token"] = csrfToken;
    }
  }
  const res = await fetch(path, {
    ...init,
    headers,
    credentials: "same-origin",
  });
  let env: Envelope<T> | null = null;
  try {
    env = (await res.json()) as Envelope<T>;
  } catch {
    // non-JSON error body — fall through with env null
  }
  if (!res.ok || env?.error) {
    // Cross-cutting error codes (errorBus): every caller still gets the
    // thrown ApiError for local handling, but auth.tsx and ToastProvider
    // also learn about it without every call site wiring it up.
    if (env?.error?.code === "risk_ack_required") {
      const data = env.data as { required_version?: string } | null;
      emitRiskAckRequired(data?.required_version);
    } else if (env?.error?.code === "entitlement_exceeded") {
      const data = env.data as { key?: string; limit?: unknown } | null;
      emitEntitlementExceeded({
        key: data?.key,
        limit: data?.limit,
        message: env.error.message,
      });
    }
    throw new ApiError(res.status, env?.error ?? null, env?.data ?? null);
  }
  if (env === null || env.data === null) {
    throw new ApiError(res.status, {
      code: "empty_response",
      message: "Empty response",
    });
  }
  return env.data;
}

const get = <T>(path: string) => request<T>(path);
const post = <T>(path: string, body?: unknown) =>
  request<T>(path, {
    method: "POST",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
const put = <T>(path: string, body?: unknown) =>
  request<T>(path, {
    method: "PUT",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
const del = <T>(path: string) => request<T>(path, { method: "DELETE" });

// ---- shapes (mirroring the Go API; decimals stay strings) ----------------

export interface SystemStatus {
  mode: string;
  version: string;
  commit: string;
  uptime_sec: number;
  components: string[];
}

// ---- Tenancy, entitlements and billing (T-081/T-082/T-083, docs/design/
// billing.md, docs/design/packages.md §3) --------------------------------
// The console only READS entitlements; every limit is enforced server-
// side (packages.md §3: "the console only reads them"). Nothing here
// recomputes a limit or a price.

export type OrgRole = "OWNER" | "ADMIN" | "MEMBER" | "VIEWER";
export type CustomerType = "consumer" | "business";

export interface Org {
  id: number;
  name: string;
  package_code: string;
  country?: string;
  customer_type: CustomerType;
  risk_ack_version?: string;
  risk_ack_at?: string;
  trial_ends_at?: string;
  created_at: string;
}

export interface EntitlementVenues {
  screener_max: number; // -1 = unlimited
  screener_tiers: string[];
  screener_fixed: string[];
  triangular_max: number;
  dex_enabled: boolean;
  perps_enabled: boolean;
}

export interface EntitlementRules {
  max_active: number;
  templates_max: number; // -1 = unlimited
  min_refresh_s: number;
  kinds: string[];
}

export interface EntitlementAlerts {
  channels: string[]; // web | telegram | email | webhook
  per_day: number;
  telegram_destinations_max: number;
  min_cooldown_s: number;
}

export interface EntitlementAutoPaper {
  strategies: string[];
  max_open_positions: number;
  ledgers_max: number;
  max_size_quote: string; // decimal string
}

export interface EntitlementAPI {
  enabled: boolean;
  scopes: string[];
  rate_per_min: number;
  burst: number;
  keys_max: number;
  streaming: boolean;
}

export interface EntitlementHistory {
  retention_days: number; // 1 = 24h (Watch)
  export_formats: string[];
  export_scheduled: boolean;
  reports: string;
}

export interface EntitlementSeats {
  max: number;
  roles: string[];
}

export interface EntitlementSupport {
  tier: string;
  response_hours: number;
}

export interface EntitlementExecution {
  paper: true;
  live: false;
}

export interface EntitlementStatus {
  subscription: string; // none|trialing|active|past_due|paused|canceled
  read_only: boolean;
  trial_ends_at?: string;
  effective_package: string;
}

export interface Entitlements {
  schema_version: number;
  package_code: string;
  venues: EntitlementVenues;
  rules: EntitlementRules;
  alerts: EntitlementAlerts;
  auto_paper: EntitlementAutoPaper;
  api: EntitlementAPI;
  history: EntitlementHistory;
  seats: EntitlementSeats;
  support: EntitlementSupport;
  execution: EntitlementExecution;
  white_label?: boolean;
  status?: EntitlementStatus;
}

export interface Me {
  user_id: string;
  role: "ADMIN" | "OPERATOR" | "VIEWER";
  platform_admin: boolean;
  org: Org;
  org_role: OrgRole;
  risk_ack_required: boolean;
  risk_ack_version: string;
  entitlements: Entitlements;
  csrf_token?: string;
}

export interface OrgMember {
  org_id: number;
  user_id: string;
  email?: string;
  role: OrgRole;
  suspended: boolean;
  created_at: string;
}

export interface OrgGetResponse {
  org: Org;
  org_role: OrgRole;
  members: OrgMember[] | null;
  entitlements: Entitlements;
}

export interface BillingSubscription {
  org_id: number;
  paddle_customer_id?: string;
  paddle_subscription_id?: string;
  price_id?: string;
  status: string; // trialing|active|past_due|paused|canceled
  current_period_end?: string;
  cancel_at_period_end: boolean;
  past_due_since?: string;
  scheduled_price_id?: string;
  updated_at: string;
}

export interface BillingSubscriptionResponse {
  org_id: number;
  package_code: string;
  trial_ends_at?: string;
  configured: boolean;
  status?: EntitlementStatus;
  subscription?: BillingSubscription;
}

export interface BillingPrice {
  price_id: string;
  package_code: string;
  billing_interval: "month" | "year";
}

export interface BillingPricesResponse {
  prices: BillingPrice[] | null;
  environment: string;
  client_token: string;
}

export interface BillingCheckoutResult {
  transaction_id?: string;
  client_token?: string;
  environment?: string;
  pending: boolean;
  proration?: string;
  package_code: string;
}

export interface PaperStatus {
  running: boolean;
  active_simulations: number;
  received: number;
  completed: number;
  failed: number;
  skipped: number;
  /** Qualified opportunities refused by a full paper queue, never simulated. */
  dropped?: number;
}

export interface ScannerStatus {
  ready: boolean;
  triangles: number;
  // Nullable: the engine's own zero value before the topology is built
  // (internal/app/engine.go's EngineStatus starts Markets nil and only
  // appends once e.topo is non-nil) marshals as JSON null, not `[]` —
  // callers must not assume an array.
  markets: string[] | null;
  evaluations: number;
  qualified: number;
  rejected: number;
  skipped_unhealthy: number;
  dropped_events: number;
  paper?: PaperStatus;
}

export interface RecentOpportunity {
  id: string;
  triangle_id: string;
  net_bps: string;
  profit: string;
  input: string;
  at: string;
}

export interface OpportunityRow {
  id: string;
  triangle_id: string;
  status: string;
  reason_code?: string;
  starting_asset: string;
  starting_amount: string;
  net_profit?: string;
  net_return_bps?: string;
  data_quality?: string;
  config_version?: number;
  detected_at: string;
  expires_at?: string;
}

export interface CycleRow {
  id: string;
  session_id: string;
  opportunity_id?: string;
  outcome: string;
  pnl_amount?: string;
  pnl_asset?: string;
  slippage_bps?: string;
  started_at: string;
  settled_at?: string;
}

export interface OrderRow {
  id: string;
  cycle_id: string;
  leg_no: number;
  market_id: string;
  side: string;
  status: string;
  qty?: string;
  filled_qty?: string;
  avg_price?: string;
  fee?: string;
  fee_asset?: string;
}

// ---- global Orders/Fills (BL-20) ------------------------------------------
// Distinct shapes from OrderRow/CycleRow above (those are per-cycle views);
// these carry the full fill→order→cycle→triangle→opportunity cross-link
// chain and cursor pagination. Field names mirror
// internal/storage/orders_fills.go exactly (qty_requested/qty_filled, not
// qty/filled_qty; fee_amount, not fee).

export interface OrderListRow {
  id: string;
  cycle_id: string;
  opportunity_id?: string;
  triangle_id?: string;
  symbol?: string;
  leg_no: number;
  side: string;
  status: string;
  qty_requested: string;
  qty_filled: string;
  avg_price?: string;
  fee_amount?: string;
  fee_asset?: string;
  latency_ms?: string;
  created_at: string;
}

export interface OrderPage {
  orders: OrderListRow[] | null;
  next_cursor?: string;
}

export interface FillListRow {
  id: string;
  order_id: string;
  cycle_id: string;
  opportunity_id?: string;
  triangle_id?: string;
  symbol?: string;
  leg_no: number;
  side: string;
  order_status: string;
  price: string;
  qty: string;
  fee_amount?: string;
  fee_asset?: string;
  book_version?: number;
  ts: string;
}

export interface FillPage {
  fills: FillListRow[] | null;
  next_cursor?: string;
}

export interface ListFilter {
  symbol?: string;
  triangle?: string;
  cycle?: string;
  status?: string;
  from?: string;
  to?: string;
  limit?: number;
  cursor?: string;
}

function listFilterQuery(f: ListFilter): string {
  const q = new URLSearchParams();
  if (f.symbol) q.set("symbol", f.symbol);
  if (f.triangle) q.set("triangle", f.triangle);
  if (f.cycle) q.set("cycle", f.cycle);
  if (f.status) q.set("status", f.status);
  if (f.from) q.set("from", f.from);
  if (f.to) q.set("to", f.to);
  if (f.limit) q.set("limit", String(f.limit));
  if (f.cursor) q.set("cursor", f.cursor);
  return q.toString();
}

export interface PortfolioView {
  balances: Record<string, { available: string; reserved: string }>;
  exposure: Record<string, string>;
  equity: Record<string, string>;
  unmarked: string[];
  cycles: number;
  completed: number;
  failed: number;
  at: string;
}

export interface PnLView {
  assets: {
    asset: string;
    realized: string;
    fees: string;
    daily_loss: string;
    drawdown: string;
  }[];
}

// ---- PnL & Analytics (BL-19) -----------------------------------------------
// Every aggregate carries n (sample size); an empty/thin window says so
// rather than showing zeros as fact. by=market has NO net_pnl (a cycle's
// P&L cannot be split across its three legs' markets) and instead carries
// avg_latency_ms — render distinct columns for that dimension.

export interface PnLBreakdownRow {
  key: string;
  n: number;
  net_pnl?: string;
  avg_latency_ms?: string;
}

export type PnLBreakdownBy =
  "exchange" | "triangle" | "asset" | "market" | "hour" | "config_version";

export interface PnLBreakdownResult {
  by: string;
  window_hours: number;
  rows: PnLBreakdownRow[] | null;
  n: number;
  unattributed?: number;
  notes?: string[];
}

export interface PnLPoint {
  at: string;
  cumulative_pnl: string;
  drawdown: string;
}

export interface PnLSeriesResult {
  window_hours: number;
  points: PnLPoint[] | null;
  n: number;
}

export interface HistogramBucket {
  from: string;
  to: string;
  count: number;
}

// Distribution: only `n` is guaranteed; every other field is omitted
// (not zero) when the sample set is empty — an honest empty distribution
// renders as "n: 0", never a zeroed chart.
export interface Distribution {
  n: number;
  min?: string;
  max?: string;
  avg?: string;
  p50?: string;
  p95?: string;
  p99?: string;
  buckets?: HistogramBucket[];
}

export interface DistributionsResult {
  window_hours: number;
  edge_bps: Distribution;
  slippage_bps: Distribution;
  latency_ms: Distribution;
}

export interface RiskView {
  config_version?: number;
  limits?: Record<string, unknown>;
  breakers?: { Name: string; Scope: string; State: string; Reason: string }[];
  reject_reason_counts: Record<string, number>;
}

export interface ConfigSnapshot {
  version: number;
  params: StrategyParams;
  created_by?: string;
  created_at: string;
  parent_version?: number;
}

export interface StrategyParams {
  scanner: Record<string, unknown>;
  risk: Record<string, unknown>;
  notifications: Record<string, unknown>;
}

export interface ConfigVersion {
  version: number;
  created_by?: string;
  created_at: string;
  active: boolean;
  parent_version?: number;
  diff?: Record<string, { old: unknown; new: unknown }>;
}

export interface Alert {
  id: string;
  severity: string;
  source: string;
  key: string;
  title: string;
  body: string;
  first_at: string;
  last_at: string;
  count: number;
  state: "active" | "acked" | "resolved";
  acked_by?: string;
  resolved_by?: string;
}

export interface AIAnalysis {
  id: string;
  kind: string;
  at: string;
  model: string;
  prompt_version: string;
  config_version: number;
  summary: string;
  findings: string[];
  recommendations: AIRecommendation[];
}

export interface AIRecommendation {
  id: string;
  analysis_id: string;
  created_at: string;
  scope: string;
  parameter: string;
  current_value: string;
  recommended_value: string;
  evidence: string;
  reason: string;
  confidence: string;
  expected_effect: string;
  risks: string;
  expires_at: string;
  status: string;
  decided_by?: string;
}

// Report's named sections mirror internal/reporting.Report exactly (BL-32:
// a formatted view renders these fields, not a raw JSON dump). The index
// signature keeps the type forward-compatible with any section the
// backend adds later — an unrecognized key still renders generically.
export interface ReportSystemSection {
  mode: string;
  ready: boolean;
  config_version: number;
  active_alerts: number;
}
export interface ReportExchangeSection {
  exchange: string;
  frames: number;
  reconnects: number;
  api_errors: number;
  resyncs: number;
  sequence_gaps: number;
  books_healthy: number;
  books_total: number;
}
export interface ReportScannerSection {
  evaluations: number;
  qualified: number;
  rejected: number;
  skipped: number;
  dropped: number;
  qualification_rate: string;
}
export interface ReportOppSection {
  qualified_persisted: number;
  best_net_bps?: string;
  avg_net_bps?: string;
  from_memory: boolean;
}
export interface ReportCycleSection {
  total: number;
  success: number;
  failed: number;
  success_rate: string;
}
export interface ReportAssetSection {
  asset: string;
  realized: string;
  fees: string;
  drawdown: string;
}
export interface ReportSlippageSection {
  avg_bps?: string;
  worst_bps?: string;
  samples: number;
}
export interface ReportFailedCycle {
  id: string;
  outcome: string;
}
export interface ReportCapitalSection {
  asset: string;
  available: string;
  reserved: string;
  utilization: string;
}
export interface ReportTriangleStat {
  triangle_id: string;
  cycles: number;
  net_pnl: string;
}
export interface ReportRiskSection {
  breakers_open: number;
  reject_reasons?: Record<string, number>;
}
export interface ReportAISection {
  available: boolean;
  latest_summary?: string;
  proposed_recommendations: number;
}
export interface ReportIncident {
  severity: string;
  title: string;
  count: number;
  last_at: string;
}

export interface Report {
  id: string;
  kind: string;
  period_start: string;
  period_end: string;
  generated_at: string;
  executive_summary: string;
  system_health?: ReportSystemSection;
  exchange_health?: ReportExchangeSection;
  scanner?: ReportScannerSection;
  opportunities?: ReportOppSection;
  paper_cycles?: ReportCycleSection;
  pnl?: ReportAssetSection[];
  slippage?: ReportSlippageSection;
  failed_cycles?: ReportFailedCycle[];
  capital_utilization?: ReportCapitalSection[];
  top_triangles?: ReportTriangleStat[];
  worst_triangles?: ReportTriangleStat[];
  risk_events?: ReportRiskSection;
  ai_findings?: ReportAISection;
  incidents?: ReportIncident[];
  recommended_actions: string[];
  notes?: string[];
  [section: string]: unknown;
}

export interface QualityScore {
  triangle_id: string;
  total: number;
  components: Record<string, string>;
  notes?: string[];
  cycles: number;
}

export interface AuditEvent {
  id: string;
  ts: string;
  actor?: string;
  source: string;
  action: string;
  entity: string;
  entity_id?: string;
}

export interface FeedHealth {
  frames: number;
  reconnects: number;
  api_errors: number;
  resyncs: number;
  seq_gaps: number;
}

export interface HealthView {
  ready: boolean;
  triangles: number;
  scanner: Record<string, number>;
  feed?: FeedHealth;
  books?: { market: string; state: string; age_ms: number }[];
  paper?: PaperStatus;
}

// ---- System Health (BL-18) -------------------------------------------------
// Every section is independently optional — process stats are always
// present; the rest appear only when their backing component exists in
// this profile (engine, store, recorder, supervisor). Render "not running
// in this profile", never a faked zero, for an absent section.

export interface ProcessStats {
  uptime_sec: number;
  goroutines: number;
  heap_alloc_bytes: number;
  heap_sys_bytes: number;
  sys_bytes: number;
  gc_pause_total_ns: number;
  num_gc: number;
}

export interface LatencySnapshot {
  n: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
}

export interface FeedHealthFull {
  frames: number;
  reconnects: number;
  api_errors: number;
  resyncs: number;
  seq_gaps: number;
  msgs_per_sec: number;
  latency_ms?: LatencySnapshot;
}

export interface QueueDepth {
  depth: number;
  capacity: number;
  dropped?: number;
  written?: number;
  /** Records the database refused (outbox only). */
  write_failures?: number;
  /** The last write failed and nothing has succeeded since (outbox only). */
  failing?: boolean;
  /** Cycles persisted without their opportunity row (outbox only). */
  unlinked_cycles?: number;
}

export interface PoolStat {
  acquired_conns: number;
  idle_conns: number;
  constructing_conns: number;
  total_conns: number;
  max_conns: number;
  acquire_count: number;
  empty_acquire_count: number;
  canceled_acquire_count: number;
}

export interface SystemHealthView {
  process: ProcessStats;
  ready?: boolean;
  triangles?: number;
  scanner?: Record<string, number>;
  feed?: FeedHealthFull;
  books?: { market: string; state: string; age_ms: number }[];
  paper?: PaperStatus;
  queues?: { outbox?: QueueDepth; paper?: QueueDepth; recorder?: QueueDepth };
  database?: PoolStat;
  restart?: RestartStatus;
}

// ---- Triangle detail (BL-26) ------------------------------------------------

export interface TriangleLegView {
  leg_no: number;
  market: string;
  side: string;
  from: string;
  to: string;
  book_state?: string;
  book_age_ms?: number;
  top_bid?: string;
  top_ask?: string;
  vwap_price?: string;
  price_impact_bps?: string;
  levels_consumed?: number;
  depth_exhausted?: boolean;
  fee_rate?: string;
  fee_source?: string;
}

export interface TriangleView {
  id: string;
  exchange: string;
  starting_asset: string;
  legs: TriangleLegView[] | null;
}

export interface QualityBreakdown {
  triangle_id: string;
  total: number;
  total_exact: string;
  components: Record<string, string>;
  notes?: string[];
  cycles: number;
}

export interface TriangleDetail {
  triangle?: TriangleView;
  recent_cycles?: CycleRow[];
  quality?: QualityBreakdown;
  notes?: string[];
}

// ---- Opportunity detail (BL-27) --------------------------------------------

export interface OpportunityDecisionView {
  allowed: boolean;
  reason_code?: string;
  checks?: unknown;
  config_version?: number;
  legacy?: boolean;
}

export interface SimulationResultView {
  cycle_id: string;
  outcome: string;
  pnl_amount?: string;
  pnl_asset?: string;
  fees?: unknown;
  slippage_bps?: string;
  exposure?: unknown;
  started_at: string;
  settled_at?: string;
}

export interface OpportunityDetail {
  id: string;
  exchange_id: string;
  triangle_id: string;
  status: string;
  reason_code?: string;
  starting_asset: string;
  starting_amount: string;
  legs: unknown;
  gross_final_amount?: string;
  estimated_final_amount?: string;
  gross_profit?: string;
  net_profit?: string;
  gross_return_bps?: string;
  net_return_bps?: string;
  data_quality?: string;
  config_version?: number;
  detected_at: string;
  expires_at?: string;
  decided_at?: string;
  decision?: OpportunityDecisionView;
  book_versions?: number[];
  simulation?: SimulationResultView;
  notes?: string[];
}

// ---- Replay (BL-17) ---------------------------------------------------------
// Request.speed is accepted and persisted for the audit trail and forward
// compatibility, but has NO effect on execution today: the replay runner
// steps a deterministic, single-threaded discrete-event backtest to the
// next recorded frame or latency deadline, not to wall-clock time, so
// there is no "pace" to scale (internal/replay/execute.go). Surface that
// verbatim in the form, don't imply speed does anything yet.
export interface ReplayRequest {
  recording: string;
  config_version?: number;
  speed?: number;
}

export interface TopOpportunity {
  opportunity_id: string;
  triangle_id: string;
  outcome: string;
  net_bps: string;
  at: string;
}

export interface ReplayRun {
  id: string;
  recording: string;
  request: ReplayRequest;
  status: "queued" | "running" | "done" | "failed";
  done: number;
  total: number;
  step?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  error?: string;
  opportunities: number;
  qualified: number;
  cycles: number;
  top?: TopOpportunity[];
  actor?: string;
}

// ---- Risk events (BL-31) ---------------------------------------------------

export interface RiskEventRow {
  id: string;
  ts: string;
  kind: string;
  subject?: string;
  limit_name?: string;
  observed?: string;
  threshold?: string;
  action?: string;
  breaker_state?: string;
  correlation_id?: string;
}

// ---- Telegram status (BL-21) -----------------------------------------------
// Never carries the bot token — only connectivity/delivery counters and
// chat ids. Answers 200 {enabled:false} when unconfigured, not a 404.

export interface TelegramStatusView {
  enabled: boolean;
  allowlist: number[] | null;
  // Disabled mirrors telegram.disabled (T-059 §4.2: hot mute); reason
  // explains a non-running or muted bot ("disabled in settings" / "no
  // token configured" / "allowlist empty at boot").
  disabled: boolean;
  reason?: string;
  bot_username?: string;
  messages: number;
  errors: number;
  last_poll_at?: string;
  last_poll_ok: boolean;
  last_poll_error?: string;
  last_getme_at?: string;
  last_getme_ok: boolean;
  last_getme_error?: string;
  pushes_sent: number;
  push_errors: number;
  last_pushed_at?: string;
}

export interface RecordingRow {
  id: string;
  exchange_id: string;
  started_at: string;
  ended_at?: string;
  streams: Record<string, string>;
  // Segment metadata varies by recorder version; only the count is
  // displayed, so the shape is kept opaque here.
  segment_files: unknown[];
}

export interface RecorderStatus {
  running: boolean;
  session_id?: string;
  started_at?: string;
  frames_written: number;
  frames_dropped: number;
  segments_closed: number;
  bytes_closed: number;
  symbols?: string[];
  dir?: string;
}

export interface CampaignRequest {
  recording: string;
  assets?: string[];
  balances?: Record<string, string>;
  seeds?: number[];
  fee_maker_bps?: number;
  fee_taker_bps?: number;
  grid?: "full" | "baseline";
}

export interface UserRow {
  id: string;
  email: string;
  role: "ADMIN" | "OPERATOR" | "VIEWER";
  disabled: boolean;
  created_at: string;
}

// ---- platform settings (T-057) --------------------------------------------
// Mirrors internal/platform/settings.go exactly. bps and paper balances are
// decimal strings (shopspring/decimal marshals quoted by default); the
// Telegram allowlist is int64 chat ids, i.e. JSON numbers, not strings.

export interface PlatformFeeOverride {
  maker_bps: string;
  taker_bps: string;
}

export interface PlatformFeeSettings {
  maker_bps: string;
  taker_bps: string;
  overrides?: Record<string, PlatformFeeOverride>;
  token_discount: boolean;
}

export interface PlatformVenueSettings {
  enabled: boolean;
  paper_enabled: boolean;
  symbols: string[];
  starting_assets: string[];
  fees: PlatformFeeSettings;
}

export interface PlatformPaperSettings {
  balances: Record<string, string>;
}

export interface PlatformTelegramSettings {
  // Go's []int64(nil) — the state before any allowlist entry is ever
  // added — marshals as JSON null, not []; always read this through
  // lib/platformFields.ts's allowlistOf(), never directly.
  allowlist: number[] | null;
  // Disabled is NEGATIVE on purpose (settings-expansion §4.2/D3): a
  // document persisted before this field existed unmarshals with the
  // zero value, which must mean "keep delivering". The console renders
  // a toggle labelled "Telegram notifications" whose ON position
  // submits disabled:false — never invert this into an "enabled" field,
  // the wire name and the diff path are both "telegram.disabled".
  disabled: boolean;
}

// PlatformPlatformSettings is the platform.* section (T-059 §2/§4.3):
// mode is restart-scoped, log_level and allowed_origin are hot.
export interface PlatformPlatformSettings {
  mode: string; // MARKET_DATA | RECORD | PAPER (SHADOW enumerated, never settable)
  log_level: string; // debug | info | warn | error
  allowed_origin: string; // scheme://host[:port]
}

export interface PlatformAISchedule {
  hourly_minutes: number; // 0 or 15..1440
  daily_hours: number; // 0 or 1..168
  weekly_hours: number; // 0 or 24..720
}

export interface PlatformAIBudget {
  max_analyses_per_day: number; // 1..96
  max_output_tokens: number; // 256..8192
}

// PlatformAISettings is the ai.* section (T-059 §4.1). Every field is
// hot — ai.Service/ai.Scheduler are always constructed behind an
// ai.Switch, so a runtime enable/provider/schedule/budget change takes
// effect without a restart.
export interface PlatformAISettings {
  enabled: boolean;
  provider: string; // anthropic | fake (openai listed in capabilities, not settable)
  model: string;
  schedule: PlatformAISchedule;
  budget: PlatformAIBudget;
}

export interface PlatformSettingsDoc {
  platform: PlatformPlatformSettings;
  venues: Record<string, PlatformVenueSettings>;
  paper: PlatformPaperSettings;
  telegram: PlatformTelegramSettings;
  ai: PlatformAISettings;
}

// PlatformPlan is the topology one venue's settings would produce
// (internal/platform/catalog.go); computed with the real graph builder, so
// the frontend never guesses a triangle count.
export interface PlatformPlan {
  venue: string;
  markets: number;
  triangles: number;
  rejected_untradeable: number;
  starting_assets: string[];
}

// RestartStatus mirrors internal/app.RestartStatus's wire shape exactly —
// state/pending_reasons/error are rendered verbatim, never re-derived.
export interface RestartStatus {
  state: "ready" | "pending" | "restarting" | "failed";
  settings_version: number;
  pending_version?: number;
  requested_by?: string;
  requested_at?: string;
  ready_at?: string;
  restarts: number;
  pending_reasons?: string[];
  error?: string;
}

// PlatformSnapshotView is the GET/apply/rollback response shape
// (Server.platformView): the settings document plus the backend-computed
// field_timing map (never hardcoded client-side) and, when a catalog is
// wired, the per-venue topology plan.
export interface PlatformSnapshotView {
  version: number;
  created_by?: string;
  created_at: string;
  settings: PlatformSettingsDoc;
  field_timing: Record<string, string>;
  plan?: Record<string, PlatformPlan>;
  restart?: RestartStatus;
  // warnings is populated when the document was accepted but something
  // about it is not fully in effect yet (settings-expansion §4.1) — e.g.
  // ai.enabled with no resolvable provider key. Rendered verbatim, never
  // reworded or softened.
  warnings?: string[];
}

// PlatformSnapshot is the raw stored version (GET .../version/{n}) — no
// field_timing/plan, those are only computed against the CURRENT snapshot.
export interface PlatformSnapshot {
  version: number;
  settings: PlatformSettingsDoc;
  created_by?: string;
  created_at: string;
  parent_version?: number;
}

export interface PlatformVersionInfo {
  version: number;
  created_by?: string;
  created_at: string;
  active: boolean;
  parent_version?: number;
  diff?: Record<string, { old: unknown; new: unknown }>;
}

export interface PlatformPreviewResponse {
  diff: Record<string, { old: unknown; new: unknown }>;
  sections: string[];
  requires_restart: boolean;
  plan?: Record<string, PlatformPlan>;
}

export interface EngineStatusResponse {
  restart: RestartStatus;
}

// ---- capabilities / AI status / secrets (T-059..T-061) -------------------
// Three enumerated tables (modes, venues, AI providers) sharing one shape,
// plus the secrets-vault status and field_timing — all backend-computed so
// the console never hardcodes availability, reasons or timing.

export interface ModeProfile {
  id: string;
  available: boolean;
  reason?: string;
}

// CompiledVenueDiscount is the read-only compiled-in token-discount
// profile for one venue (fees.go constants); modeled is always false
// today — the discount toggle stays disabled everywhere until a
// pay-asset ledger exists.
export interface CompiledVenueDiscount {
  pay_asset: string;
  rate: string;
  applies_to_api: boolean;
  modeled: boolean;
  reason: string;
}

export interface VenueProfile {
  id: string;
  name: string;
  available: boolean;
  reason?: string;
  discount?: CompiledVenueDiscount;
}

export interface AIProviderProfile {
  id: string;
  available: boolean;
  reason?: string;
}

export interface CapabilitiesVaultStatus {
  vault_configured: boolean;
  key_id?: string;
  reason?: string;
}

export interface CapabilitiesResponse {
  modes: ModeProfile[];
  venues: VenueProfile[];
  ai_providers: AIProviderProfile[];
  log_levels: string[];
  secrets: CapabilitiesVaultStatus;
  field_timing?: Record<string, string>;
}

// AIRuntimeStatus is GET /api/v1/ai/status's shape (settings-expansion
// §4.1): configured intent (enabled) vs what is actually installed
// (running), with the reason when they differ.
export interface AIRuntimeStatus {
  enabled: boolean;
  running: boolean;
  reason?: string;
  provider: string;
  model: string;
  key_source?: string; // vault | env
  analyses_today: number;
  max_per_day: number;
  last_analysis?: string;
}

// SecretInfo is one registry row (internal/secrets.Info) — never a value,
// never a prefix, never a last-4.
export interface SecretInfo {
  name: string;
  label: string;
  present: boolean;
  source?: string; // vault | env
  readable: boolean;
  reason?: string;
  updated_at?: string;
  updated_by?: string;
  applies: string; // immediately | process_restart | not_consumed
  group: string; // provider | exchange
  venue?: string; // exchange group: venue id
}

export interface SecretsListResponse {
  vault_configured: boolean;
  key_id?: string;
  reason?: string;
  secrets: SecretInfo[];
}

export interface CampaignRun {
  id: string;
  recording: string;
  request: CampaignRequest;
  status: "queued" | "running" | "done" | "failed";
  done: number;
  total: number;
  step?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  error?: string;
  flags?: Record<string, string[]>;
  // verdicts is the future per-flag-severity companion to `flags`
  // (backend addition tracked as BL-05b); read it when present, else the
  // UI falls back to phrase-matching `flags`.
  verdicts?: Record<string, { text: string; severity: string }[]>;
  report_md?: string;
  report_path?: string;
  json_path?: string;
  actor?: string;
}

// ---- Scanner Suite (T-065..T-072, docs/design/scanner-suite.md §7) --------
// Every bps/price/liquidity/PnL/APR value is a decimal string, rendered
// verbatim — the frontend never recomputes a spread, a fee, or a carry
// number. Public market data only; nothing here ever signs a request or
// resolves an exchange credential.

export interface ScreenerVenueStatus {
  id: string;
  name: string;
  enabled: boolean;
  online: boolean;
  last_poll_at?: string;
  poll_ms: number;
  spot_pairs: number;
  perp_contracts: number;
  rate_limited: boolean;
  error?: string;
}

export interface ScreenerStatusView {
  venues: ScreenerVenueStatus[] | null;
  pairs_tracked: number;
  spreads_per_sec: number;
  poll_interval_s: number;
  updated_at: string;
}

// "open" | "closed" | "unknown" — unknown means the venue requires an API
// key to answer (never inferred from another source, per SKILL.md).
export type ScreenerNetworkState = "open" | "closed" | "unknown";

export interface ScreenerNetworks {
  buy_withdraw: ScreenerNetworkState;
  sell_deposit: ScreenerNetworkState;
  reason?: string;
}

export interface ScreenerSpreadRow {
  base: string;
  quote: string;
  buy_venue: string;
  sell_venue: string;
  buy_ask: string;
  buy_ask_qty: string;
  sell_bid: string;
  sell_bid_qty: string;
  spread_bps_gross: string;
  spread_bps_net: string;
  /** null when either venue publishes no top-of-book size (liquidity_unknown). */
  liquidity_quote: string | null;
  liquidity_unknown: boolean;
  /** Asset-identity guard: same ticker, different asset; excluded unless include_suspect=1. */
  suspect: boolean;
  suspect_reason?: string;
  lifetime_s: number;
  first_seen_at: string;
  buy_age_ms: number;
  sell_age_ms: number;
  buy_fee_bps: string;
  sell_fee_bps: string;
  networks: ScreenerNetworks;
}

export interface ScreenerSpreadsResponse {
  rows: ScreenerSpreadRow[] | null;
  total: number;
  generated_at: string;
  model: string;
  // excluded is always reported (handleScreenerSpreads) so the operator
  // knows how many lanes the safe defaults hid, even when both toggles
  // below are off; optional here only so a build against an older
  // backend that predates this field doesn't crash.
  excluded?: { suspect: number; liquidity_unknown: number };
}

// ScreenerSpreadsQuery mirrors the §7 query params exactly (wire names).
// `base` is the one narrowing param the backend documents; the filter
// card's bases_allow/bases_deny (below) are a client-side convention on
// top of it — bases_allow is sent as this csv `base`, bases_deny is
// applied to the fetched rows in the browser since §7 has no deny slot.
export interface ScreenerSpreadsQuery {
  min_spread_bps?: number;
  min_liquidity?: number;
  min_lifetime_s?: number;
  buy?: string[];
  sell?: string[];
  quote?: string;
  base?: string;
  limit?: number;
  // Both default OFF server-side (handleScreenerSpreads): suspect
  // (asset-identity guard) and unknown-liquidity lanes are excluded
  // unless explicitly opted back in.
  include_suspect?: boolean;
  include_unknown_liquidity?: boolean;
}

function screenerSpreadsQuery(q: ScreenerSpreadsQuery): string {
  const p = new URLSearchParams();
  if (q.min_spread_bps !== undefined)
    p.set("min_spread_bps", String(q.min_spread_bps));
  if (q.min_liquidity !== undefined)
    p.set("min_liquidity", String(q.min_liquidity));
  if (q.min_lifetime_s !== undefined)
    p.set("min_lifetime_s", String(q.min_lifetime_s));
  if (q.buy?.length) p.set("buy", q.buy.join(","));
  if (q.sell?.length) p.set("sell", q.sell.join(","));
  if (q.quote) p.set("quote", q.quote);
  if (q.base) p.set("base", q.base);
  if (q.limit !== undefined) p.set("limit", String(q.limit));
  // queryFlag (backend) only recognizes "1"/"true"/"yes"; omit the key
  // entirely when off rather than sending "0"/"false".
  if (q.include_suspect) p.set("include_suspect", "1");
  if (q.include_unknown_liquidity) p.set("include_unknown_liquidity", "1");
  return p.toString();
}

// ScreenerFilterSet is the filter-card's own shape — saved/loaded as a
// named template. Superset of ScreenerSpreadsQuery: bases_allow maps to
// the `base` query param, bases_deny is client-side only (see above).
export interface ScreenerFilterSet {
  buy_venues?: string[];
  sell_venues?: string[];
  quote?: string;
  min_spread_bps?: number;
  min_liquidity?: number;
  min_lifetime_s?: number;
  bases_allow?: string[];
  bases_deny?: string[];
}

export interface ScreenerTemplate {
  id: string;
  name: string;
  filters: ScreenerFilterSet;
}

export interface ScreenerPerpRow {
  venue: string;
  base: string;
  quote: string;
  spot_mid: string;
  perp_mark: string;
  perp_index: string;
  basis_bps: string;
  funding_rate: string;
  predicted_funding_rate: string;
  funding_interval_h: number;
  next_funding_at: string;
  carry_apr_gross: string;
  carry_apr_net: string;
  spot_fee_bps: string;
  perp_fee_bps: string;
  age_ms: number;
  // hold_days_assumed is not in §7's row shape but is referenced by the
  // task copy ("30-day hold assumption") — render the note only when the
  // backend actually sends it, never a hardcoded day count.
  hold_days_assumed?: number;
}

export interface ScreenerPerpsResponse {
  rows: ScreenerPerpRow[] | null;
  generated_at: string;
}

export interface ScreenerPerpsQuery {
  venue?: string;
  base?: string;
  // Exact decimal fraction as a string, e.g. "0.10" for 10% APR
  // (internal/screener/basis.go PerpFilters.MinCarryAPR, parsed
  // server-side with decimal.NewFromString — never a JSON number).
  // Callers taking a percent input from the operator must convert with
  // lib/decimal's percentToFractionStr, never `Number(percent) / 100`
  // (that division reliably leaves float noise in the querystring).
  min_carry_apr?: string;
  limit?: number;
}

export interface ScreenerFundingPoint {
  at: string;
  rate: string;
}

export interface ScreenerFundingSeries {
  venue: string;
  base: string;
  points: ScreenerFundingPoint[] | null;
}

export interface ScreenerFundingResponse {
  series: ScreenerFundingSeries[] | null;
}

export interface ScreenerCalculatorOverrideFees {
  buy_fee_bps?: string;
  sell_fee_bps?: string;
}

export interface ScreenerCalculatorRequest {
  base: string;
  quote: string;
  buy_venue: string;
  sell_venue: string;
  size_quote: string;
  transfer_fee_quote?: string;
  override_fees?: ScreenerCalculatorOverrideFees;
}

export interface ScreenerCalculatorResult {
  buy_ask: string;
  sell_bid: string;
  size_base: string;
  gross: string;
  fees_buy: string;
  fees_sell: string;
  transfer_fee: string;
  net: string;
  net_bps: string;
  liquidity_ok: boolean;
}

export interface ScreenerVenueSettings {
  enabled: boolean;
  spot_taker_bps: string;
  perp_taker_bps: string;
  perps_enabled: boolean;
}

export interface ScreenerPaperSettings {
  balances: Record<string, Record<string, string>>;
}

export interface ScreenerSettingsDoc {
  poll_interval_s: number;
  min_liquidity_quote: string;
  venues: Record<string, ScreenerVenueSettings>;
  paper: ScreenerPaperSettings;
}

export interface ScreenerSettingsSnapshot {
  version: number;
  created_at: string;
  created_by?: string;
  settings: ScreenerSettingsDoc;
  field_timing: Record<string, string>;
}

export type ScreenerRuleKind = "spread" | "carry" | "basis";

// ScreenerRuleParams mirrors internal/screener/rule_params.go RuleParams
// exactly. Every field is optional on the wire; an absent field means
// "use the documented default" — the defaults themselves live only in
// rule_params.go's Default* vars/comments (there is no GET endpoint that
// serves them), so the console states them in its own copy rather than
// guessing. Decimal fields are strings (shopspring/decimal quoted
// marshaling); exit_k/max_hold_h/max_breakeven_intervals are plain ints
// where the backend's own Rule.MaxHoldH()/ExitK()/MaxBreakevenIntervals()
// treat 0 as "unset, use the default" — never send 0 for those.
export interface ScreenerRuleParams {
  strategy?: "cross_venue_spot" | "carry" | "funding_harvest" | "";
  // §1.3 default 2.
  slip_bps?: string;
  // §1.3 default 1 (fraction of top-of-book qty deemed fillable).
  depth_haircut?: string;
  partial_allowed?: boolean;
  // §2.1 default 5.
  buffer_bps?: string;
  // §2.1 default 0.00001.
  step_size?: string;
  min_notional_quote?: string;
  // §2.4 default 3 × paper_size_quote (computed, not a flat number).
  max_drift_quote?: string;
  // §3.1 default 10.
  min_edge_bps?: string;
  // §3.2 default 0.
  close_bps?: string;
  // §3.2 default 0.
  exit_funding?: string;
  // §3.2 default 2.
  exit_k?: number;
  // §3.2 default 720 (30 days).
  max_hold_h?: number;
  // §3.4 default 0.50.
  margin_stop_frac?: string;
  // §3.4 default 100 bps (BTC/ETH) / 300 bps (other bases).
  basis_stop_bps?: string;
  // §3.4 maintenance margin rate, fraction (0,1]. NO default: nil/absent
  // means "unknown" and every carry/basis rule with auto-paper on skips
  // entry with MMR_UNKNOWN until this is set explicitly — the screener
  // never infers it from another venue or pre-fills a guess.
  mmr?: string;
  // §5.1 default 3.
  min_funding_bps?: string;
  // §5.1 default 12.
  max_breakeven_intervals?: number;
  // §5.1 default 10.
  max_negative_basis_bps?: string;
  // §5.2 default 1.
  exit_funding_bps?: string;
}

export interface ScreenerRule {
  id: string;
  name: string;
  enabled: boolean;
  kind: ScreenerRuleKind;
  min_spread_bps?: string;
  min_carry_apr?: string;
  min_liquidity_quote: string;
  min_lifetime_s: number;
  buy_venues: string[];
  sell_venues: string[];
  quotes: string[];
  bases_allow: string[];
  bases_deny: string[];
  cooldown_s: number;
  telegram: boolean;
  // Opt-in automatic PAPER execution (design §4) — LIVE stays disabled by
  // design regardless of this flag; it only ever books to the paper ledger.
  auto_paper: boolean;
  paper_size_quote: string;
  // Optional strategy-model inputs (strategy-models.md §1–§5); absent
  // keeps every documented default. See ScreenerRuleParams above.
  params?: ScreenerRuleParams;
}

export type ScreenerRuleInput = Omit<ScreenerRule, "id">;

export interface ScreenerEvent {
  id: string;
  rule_id: string;
  kind: string;
  opened_at: string;
  closed_at?: string;
  lifetime_s: number;
  base: string;
  quote: string;
  buy_venue: string;
  sell_venue: string;
  peak_net_bps: string;
  telegram_sent: boolean;
  paper_execution_id?: string;
}

export interface ScreenerAutoPaperRuleSummary {
  rule_id: string;
  alerts: number;
  executed: number;
  skipped: Record<string, number>;
  net_pnl_quote: string;
  hit_rate: string;
  mean_lifetime_s: number;
}

// ScreenerAutoPaperPosition: §7 elides the position row shape
// ("positions: [...]") — every field beyond id is optional and an
// unrecognized backend field still renders via the index signature,
// mirroring the Report interface's forward-compat convention above.
export interface ScreenerAutoPaperPosition {
  id: string;
  rule_id?: string;
  strategy?: string;
  base?: string;
  quote?: string;
  buy_venue?: string;
  sell_venue?: string;
  status?: string;
  opened_at?: string;
  closed_at?: string;
  net_pnl_quote?: string;
  [field: string]: unknown;
}

export interface ScreenerAutoPaperResponse {
  positions: ScreenerAutoPaperPosition[] | null;
  summary: { per_rule: ScreenerAutoPaperRuleSummary[] | null };
}

// ---- Screener paper reports (T-078, internal/screener/report,
// docs/user-guide/reports.md "Screener paper reports") -------------------
// Nightly (00:05 UTC) and on-demand evidence reports for automatic PAPER
// execution: per strategy and per rule, a "day" (previous UTC day) and a
// "cumulative" (since the first ledger row) report, each with the §7
// statistics table and the §8 gate checklist. Every money/rate/bps field
// is a decimal string, rendered verbatim — no report figure is derived
// client-side. rule_id is "" on the per-strategy aggregate.

export interface ScreenerReportWindow {
  label: "day" | "cumulative";
  start: string;
  end: string;
}

export interface ScreenerReportGateItem {
  item: number;
  name: string;
  status: string; // "pass" | "fail" — never a third state (reports.md)
  reason: string;
}

export interface ScreenerDriftRow {
  base: string;
  venue_a: string;
  venue_b: string;
  drift_base: string;
  drift_notional: string;
  mark_age_ms?: number;
  unmatched: number;
}

export interface ScreenerWilcoxonResult {
  n: number;
  w_plus: string;
  mean: string;
  var_w: string;
  z_squared: string;
  rejects_h0: boolean;
}

export interface ScreenerBootstrapResult {
  resamples: number;
  days: number;
  mean_net_bps: string;
  ci95_low: string;
  ci95_high: string;
}

// ScreenerReportStats mirrors internal/screener/report/stats.go Stats
// exactly. Optional (pointer-backed) fields are absent, not zero, when
// the report could not compute them — render "not computed" / "—", never
// a fabricated 0.
export interface ScreenerReportStats {
  n: number;
  wins: number;
  matched_pairs: number;
  net_pnl_quote: string;
  fees_quote: string;
  funding_quote: string;
  funding_rows: number;
  pnl_after_rebalance: string;
  matched_pair_net: string;
  unwind_cost_quote: string;
  partial_leg_pnl_quote: string;
  conservative_net_quote: string;
  net_bps_mean?: string;
  net_bps_median?: string;
  hit_rate?: string;
  hit_rate_wilson95_low?: string;
  hit_rate_wilson95_high?: string;
  lifetime_s_mean?: string;
  lifetime_s_median?: string;
  lifetime_n: number;
  max_drawdown_quote: string;
  max_drawdown_frac?: string;
  allocated_capital_quote: string;
  inventory_drift: ScreenerDriftRow[] | null;
  skipped: Record<string, number> | null;
  realised_slip_bps_mean?: string;
  realised_slip_bps_p95?: string;
  realised_slip_n: number;
  slip_allowance_bps: string;
  concentration?: string;
  largest_loss_quote: string;
  median_win_quote?: string;
  days: number;
  weekend_days: number;
  first_sample_at?: string;
  last_sample_at?: string;
  wilcoxon?: ScreenerWilcoxonResult;
  bootstrap?: ScreenerBootstrapResult;
}

export interface ScreenerReportPayload {
  window: ScreenerReportWindow;
  strategy: string;
  rule_id?: string;
  rule_name?: string;
  stats: ScreenerReportStats;
  gate: ScreenerReportGateItem[] | null;
  gate_passed: number;
  gate_total: number;
  generated_at: string;
  data_age_ms: number | null;
  model: string;
  notes: string[] | null;
  files: { markdown?: string; json?: string };
}

export interface ScreenerReport {
  id: string;
  period_start: string;
  period_end: string;
  strategy: string;
  rule_id: string;
  payload: ScreenerReportPayload;
  // Field name is "md" on the wire (report.Report.Markdown json:"md").
  md?: string;
  created_at: string;
}

// ScreenerReportSummary is the list-view row (report.Report.Summary()) —
// payload and markdown are NOT included here; hit_rate/CI, the gate
// checklist and the model/notes text live only in GET /reports/{id}.
export interface ScreenerReportSummary {
  id: string;
  period_start: string;
  period_end: string;
  period_label: string;
  strategy: string;
  rule_id: string;
  n: number;
  net_pnl_quote: string;
  gate_passed: number;
  gate_total: number;
  created_at: string;
}

export interface ScreenerReportsRunResult {
  day: string;
  started_at: string;
  duration_ms: number;
  // Go's []Summary(nil) — the "no strategy has rules or ledger rows yet"
  // outcome reports.md documents — has no omitempty (generator.go
  // RunResult.Reports) and marshals as JSON null; never assume a run
  // always produced at least one report.
  reports: ScreenerReportSummary[] | null;
  errors?: string[];
  dir?: string;
}

export interface ScreenerReportsListResponse {
  reports: ScreenerReportSummary[] | null;
  last_run: ScreenerReportsRunResult | null;
  next_run_utc: string;
  generated_at: string;
}

// ---- endpoint groups -----------------------------------------------------

export const api = {
  auth: {
    login: (email: string, password: string) =>
      post<{ role: string; csrf_token: string }>("/api/v1/auth/login", {
        email,
        password,
      }),
    logout: () => post<{ status: string }>("/api/v1/auth/logout"),
    me: () => get<Me>("/api/v1/auth/me"),
    // Self-service password change (any authenticated role).
    changePassword: (currentPassword: string, newPassword: string) =>
      post<{ status: string }>("/api/v1/auth/password", {
        current_password: currentPassword,
        new_password: newPassword,
      }),
  },
  // me() is the same handler as auth.me() (GET /api/v1/me is an alias of
  // GET /api/v1/auth/me, internal/api/auth.go handleMe) — a named
  // top-level entry point per the T-081/T-082 wire shape (role,
  // platform_admin, org, org_role, risk_ack_required, entitlements).
  me: () => get<Me>("/api/v1/me"),
  riskAck: {
    // POST /api/v1/me/risk-ack {version} — version must equal the
    // backend's current RiskAckVersion or this 409s with
    // data.required_version (billing.md §1.4); callers should source it
    // from the 403's required_version / me.risk_ack_version, not a
    // hardcoded constant.
    accept: (version: string) =>
      post<{ org_id: number; risk_ack_version: string; risk_ack_at: string }>(
        "/api/v1/me/risk-ack",
        { version },
      ),
  },
  org: {
    get: () => get<OrgGetResponse>("/api/v1/org"),
    members: () =>
      get<{ members: OrgMember[] | null; seats_max: number }>(
        "/api/v1/org/members",
      ),
    // No email-invite endpoint exists: POST /org/members takes an
    // existing account's user_id (orgapi.go handleOrgMemberAdd) and
    // /api/v1/users (email-based account creation) is platform-admin
    // only. Self-service e-mail invites land with T-085.
    addMember: (userId: string, role: OrgRole) =>
      post<{ org_id: number; user_id: string; role: OrgRole }>(
        "/api/v1/org/members",
        { user_id: userId, role },
      ),
    // {id} in these two routes is the member's user_id, not a row id.
    setMemberRole: (userId: string, role: OrgRole) =>
      post<{ user_id: string; role: OrgRole }>(
        `/api/v1/org/members/${encodeURIComponent(userId)}/role`,
        { role },
      ),
    removeMember: (userId: string) =>
      del<{ status: string }>(
        `/api/v1/org/members/${encodeURIComponent(userId)}`,
      ),
  },
  billing: {
    subscription: () =>
      get<BillingSubscriptionResponse>("/api/v1/billing/subscription"),
    prices: () => get<BillingPricesResponse>("/api/v1/billing/prices"),
    checkout: (priceId: string) =>
      post<BillingCheckoutResult>("/api/v1/billing/checkout", {
        price_id: priceId,
      }),
    portal: () => get<{ url: string }>("/api/v1/billing/portal"),
    cancel: () =>
      post<{ status: string; effective: string }>("/api/v1/billing/cancel"),
  },
  users: {
    list: async () =>
      (await request<{ users: UserRow[] | null }>("/api/v1/users")).users ?? [],
    create: (email: string, role: string, password: string) =>
      post<{ user: UserRow }>("/api/v1/users", { email, role, password }).then(
        (r) => r.user,
      ),
    setRole: (id: string, role: string) =>
      post<{ user: UserRow }>(`/api/v1/users/${encodeURIComponent(id)}/role`, {
        role,
      }).then((r) => r.user),
    disable: (id: string) =>
      post<{ user: UserRow }>(
        `/api/v1/users/${encodeURIComponent(id)}/disable`,
      ).then((r) => r.user),
    enable: (id: string) =>
      post<{ user: UserRow }>(
        `/api/v1/users/${encodeURIComponent(id)}/enable`,
      ).then((r) => r.user),
    setPassword: (id: string, password: string) =>
      post<{ user: UserRow }>(
        `/api/v1/users/${encodeURIComponent(id)}/password`,
        { password },
      ).then((r) => r.user),
  },
  system: {
    status: () => get<SystemStatus>("/api/v1/system/status"),
    health: () => get<HealthView>("/api/v1/system/health"),
    // BL-18: the same endpoint, typed for the full payload (process, DB
    // pool, queue depths, per-exchange feed stats, restart state) that
    // the System Health page renders — health() above stays the
    // Exchanges page's narrower view so it is not disturbed.
    healthFull: () => get<SystemHealthView>("/api/v1/system/health"),
  },
  scanner: {
    status: () => get<ScannerStatus>("/api/v1/scanner/status"),
  },
  // Scanner Suite (T-065..T-072): cross-venue spot screener, perpetuals/
  // funding monitor, calculator, alert rules, automatic PAPER execution.
  // Reads need screener:view (VIEWER+); mutations need screener:config
  // (ADMIN) + CSRF + parent_version where versioned (design §7).
  screener: {
    status: () => get<ScreenerStatusView>("/api/v1/screener/status"),
    spreads: (q: ScreenerSpreadsQuery) =>
      get<ScreenerSpreadsResponse>(
        `/api/v1/screener/spreads?${screenerSpreadsQuery(q)}`,
      ),
    perpetuals: (q: ScreenerPerpsQuery) => {
      const p = new URLSearchParams();
      if (q.venue) p.set("venue", q.venue);
      if (q.base) p.set("base", q.base);
      if (q.min_carry_apr !== undefined)
        p.set("min_carry_apr", String(q.min_carry_apr));
      if (q.limit !== undefined) p.set("limit", String(q.limit));
      return get<ScreenerPerpsResponse>(
        `/api/v1/screener/perpetuals?${p.toString()}`,
      );
    },
    funding: (base: string, venues: string[] = [], hours = 72) => {
      const p = new URLSearchParams();
      if (base) p.set("base", base);
      if (venues.length) p.set("venues", venues.join(","));
      p.set("hours", String(hours));
      return get<ScreenerFundingResponse>(
        `/api/v1/screener/funding?${p.toString()}`,
      );
    },
    calculator: (req: ScreenerCalculatorRequest) =>
      post<ScreenerCalculatorResult>("/api/v1/screener/calculator", req),
    settings: {
      current: () => get<ScreenerSettingsSnapshot>("/api/v1/screener/settings"),
      // parent_version required (T-058 optimistic concurrency), same
      // convention as api.platform.apply / api.config.apply above.
      apply: (settings: ScreenerSettingsDoc, parentVersion: number) =>
        post<ScreenerSettingsSnapshot>("/api/v1/screener/settings", {
          settings,
          parent_version: parentVersion,
        }),
    },
    rules: {
      list: async () =>
        (await get<{ rules: ScreenerRule[] | null }>("/api/v1/screener/rules"))
          .rules ?? [],
      create: (rule: ScreenerRuleInput) =>
        post<{ rule: ScreenerRule }>("/api/v1/screener/rules", rule).then(
          (r) => r.rule,
        ),
      update: (id: string, rule: ScreenerRuleInput) =>
        put<{ rule: ScreenerRule }>(
          `/api/v1/screener/rules/${encodeURIComponent(id)}`,
          rule,
        ).then((r) => r.rule),
      remove: (id: string) =>
        del<{ status: string }>(
          `/api/v1/screener/rules/${encodeURIComponent(id)}`,
        ),
    },
    events: (ruleId = "", limit = 100) => {
      const p = new URLSearchParams();
      if (ruleId) p.set("rule_id", ruleId);
      p.set("limit", String(limit));
      return get<{ events: ScreenerEvent[] | null }>(
        `/api/v1/screener/events?${p.toString()}`,
      );
    },
    autoPaper: () =>
      get<ScreenerAutoPaperResponse>("/api/v1/screener/auto-paper"),
    templates: {
      list: async () =>
        (
          await get<{ templates: ScreenerTemplate[] | null }>(
            "/api/v1/screener/templates",
          )
        ).templates ?? [],
      create: (name: string, filters: ScreenerFilterSet) =>
        post<{ template: ScreenerTemplate }>("/api/v1/screener/templates", {
          name,
          filters,
        }).then((r) => r.template),
      remove: (id: string) =>
        del<{ status: string }>(
          `/api/v1/screener/templates/${encodeURIComponent(id)}`,
        ),
    },
    // T-078 nightly evidence reports. Reads are screener:view (VIEWER+);
    // run() is screener:config (ADMIN) + CSRF + audit, same as
    // screener.rules mutations above.
    reports: {
      list: (limit = 50) =>
        get<ScreenerReportsListResponse>(
          `/api/v1/screener/reports?limit=${limit}`,
        ),
      get: (id: string) =>
        get<{ report: ScreenerReport }>(
          `/api/v1/screener/reports/${encodeURIComponent(id)}`,
        ),
      run: () =>
        post<{ run: ScreenerReportsRunResult }>("/api/v1/screener/reports/run"),
    },
  },
  opportunities: {
    recent: (limit = 20) =>
      get<{ source: string; opportunities: RecentOpportunity[] | null }>(
        `/api/v1/opportunities?limit=${limit}`,
      ),
    history: (status = "", limit = 100) =>
      get<{ opportunities: OpportunityRow[] | null }>(
        `/api/v1/opportunities/history?status=${status}&limit=${limit}`,
      ),
    // BL-27: why detected/qualified/rejected, book versions, simulation.
    get: (id: string) =>
      get<OpportunityDetail>(`/api/v1/opportunities/${encodeURIComponent(id)}`),
  },
  paper: {
    pause: () => post<{ running: boolean }>("/api/v1/paper/pause"),
    resume: () => post<{ running: boolean }>("/api/v1/paper/resume"),
    cycles: (limit = 100) =>
      get<{ cycles: CycleRow[] | null }>(`/api/v1/paper/cycles?limit=${limit}`),
    orders: (cycleID: string) =>
      get<{ orders: OrderRow[] | null }>(
        `/api/v1/paper/cycles/${encodeURIComponent(cycleID)}/orders`,
      ),
    // Destructive; ADMIN-only, type-to-confirm RESET in the UI (BL-10).
    reset: () =>
      post<{ running: boolean }>("/api/v1/paper/reset", { confirm: "RESET" }),
  },
  // BL-20: global, filterable, cursor-paginated orders/fills — distinct
  // from paper.orders(cycleID) above, which is scoped to one cycle.
  orders: {
    list: (f: ListFilter) =>
      get<OrderPage>(`/api/v1/orders?${listFilterQuery(f)}`),
  },
  fills: {
    list: (f: ListFilter) =>
      get<FillPage>(`/api/v1/fills?${listFilterQuery(f)}`),
  },
  portfolio: () => get<PortfolioView>("/api/v1/portfolio"),
  pnl: () => get<PnLView>("/api/v1/pnl"),
  // BL-19: breakdowns, cumulative series, and edge/slippage/latency
  // distributions — distinct from the live pnl() above.
  pnlAnalytics: {
    breakdown: (by: PnLBreakdownBy, hours = 24) =>
      get<PnLBreakdownResult>(`/api/v1/pnl/breakdown?by=${by}&hours=${hours}`),
    series: (hours = 24) =>
      get<PnLSeriesResult>(`/api/v1/pnl/series?hours=${hours}`),
  },
  analytics: {
    distributions: (hours = 24) =>
      get<DistributionsResult>(
        `/api/v1/analytics/distributions?hours=${hours}`,
      ),
  },
  risk: () => get<RiskView>("/api/v1/risk"),
  riskEvents: {
    // BL-31: persisted risk-event timeline (breaker transitions +
    // rejections), unlike risk()'s in-memory reject_reason_counts.
    list: (hours = 24, limit = 200) =>
      get<{ window_hours: number; events: RiskEventRow[] | null; n: number }>(
        `/api/v1/risk/events?hours=${hours}&limit=${limit}`,
      ),
  },
  config: {
    current: () => get<ConfigSnapshot>("/api/v1/config"),
    version: (version: number) =>
      get<ConfigSnapshot>(`/api/v1/config/version/${version}`),
    versions: (limit = 25) =>
      get<ConfigVersion[]>(`/api/v1/config/versions?limit=${limit}`),
    // parent_version (T-058 optimistic concurrency) is required on every
    // web-console write: the backend 400s parent_version_required without
    // it, and 409 stale_versions when it no longer matches the active
    // version — see staleVersion() above. It rides as a sibling of the
    // params fields (extractParentVersion pulls it off the raw body
    // before the strict decode), so it must be spread LAST: it must win
    // over any stray "parent_version" key an operator typed into the
    // Advanced: JSON draft.
    apply: (params: StrategyParams, parentVersion: number) =>
      post<ConfigSnapshot>("/api/v1/config", {
        ...params,
        parent_version: parentVersion,
      }),
    rollback: (version: number, parentVersion: number) =>
      post<ConfigSnapshot>("/api/v1/config/rollback", {
        version,
        parent_version: parentVersion,
      }),
  },
  alerts: {
    list: (state = "", limit = 50) =>
      get<{ alerts: Alert[] | null; active: number }>(
        `/api/v1/alerts?state=${state}&limit=${limit}`,
      ),
    ack: (id: string) =>
      post<Alert>(`/api/v1/alerts/${encodeURIComponent(id)}/ack`),
    resolve: (id: string) =>
      post<Alert>(`/api/v1/alerts/${encodeURIComponent(id)}/resolve`),
  },
  ai: {
    // T-059 §4.1: configured vs actually-running, with the reason when
    // they differ and today's budget usage. Served even with no
    // ai.Service so the console can say "not configured" from data.
    status: () => get<AIRuntimeStatus>("/api/v1/ai/status"),
    analyses: (limit = 10) =>
      get<AIAnalysis[] | null>(`/api/v1/ai/analyses?limit=${limit}`),
    recommendations: (status = "") =>
      get<AIRecommendation[] | null>(
        `/api/v1/ai/recommendations?status=${status}`,
      ),
    approve: (id: string) =>
      post<{ config_version: number }>(
        `/api/v1/ai/recommendations/${encodeURIComponent(id)}/approve`,
      ),
    reject: (id: string) =>
      post<{ status: string }>(
        `/api/v1/ai/recommendations/${encodeURIComponent(id)}/reject`,
      ),
  },
  // T-060: write-only vault. PUT/DELETE never return a value — only
  // presence/source/provenance (SecretInfo).
  secrets: {
    list: () => get<SecretsListResponse>("/api/v1/secrets"),
    put: (name: string, value: string) =>
      request<SecretInfo>(`/api/v1/secrets/${encodeURIComponent(name)}`, {
        method: "PUT",
        body: JSON.stringify({ value }),
      }),
    delete: (name: string) =>
      request<SecretInfo>(`/api/v1/secrets/${encodeURIComponent(name)}`, {
        method: "DELETE",
      }),
  },
  reports: {
    list: (kind = "", limit = 20) =>
      get<{ reports: Report[] | null }>(
        `/api/v1/reports?kind=${kind}&limit=${limit}`,
      ),
    generate: (kind: "daily" | "weekly") =>
      post<Report>("/api/v1/reports/generate", { kind }),
    // BL-32: formatted detail + CSV export. The CSV route is a cookie-
    // authenticated GET with Content-Disposition: attachment — a plain
    // anchor href downloads it, no fetch/Blob needed.
    get: (id: string) =>
      get<Report>(`/api/v1/reports/${encodeURIComponent(id)}`),
    csvUrl: (id: string) => `/api/v1/reports/${encodeURIComponent(id)}/csv`,
  },
  triangles: {
    quality: (hours = 24) =>
      get<{
        window_hours: number;
        scores: QualityScore[] | null;
        notes: string[];
      }>(`/api/v1/triangles/quality?hours=${hours}`),
    // BL-26: per-leg book/VWAP/fee, recent cycles, quality.
    get: (id: string) =>
      get<TriangleDetail>(`/api/v1/triangles/${encodeURIComponent(id)}`),
  },
  replays: {
    // BL-17: console-driven replay runs. 404 replays_absent when no
    // store is configured (the runner needs persisted recordings).
    list: (limit = 25) =>
      get<{ runs: ReplayRun[] | null }>(`/api/v1/replays?limit=${limit}`),
    get: (id: string) =>
      get<{ run: ReplayRun }>(`/api/v1/replays/${encodeURIComponent(id)}`),
    start: (req: ReplayRequest) =>
      post<{ run: ReplayRun }>("/api/v1/replays", req),
  },
  telegram: {
    // BL-21: never returns the token; 200 {enabled:false} when
    // unconfigured, not a 404.
    status: () => get<TelegramStatusView>("/api/v1/telegram/status"),
  },
  audit: (entity = "", limit = 100) =>
    get<{ events: AuditEvent[] | null }>(
      `/api/v1/audit?entity=${entity}&limit=${limit}`,
    ),
  recordings: {
    list: () =>
      get<{
        recordings: RecordingRow[] | null;
        persistence: boolean;
        recorder?: RecorderStatus;
      }>("/api/v1/recordings"),
    start: () =>
      post<{ session_id: string; recorder: RecorderStatus }>(
        "/api/v1/recordings/start",
      ),
    stop: () =>
      post<{ session_id: string; recorder: RecorderStatus }>(
        "/api/v1/recordings/stop",
      ),
  },
  campaigns: {
    list: (limit = 25) =>
      get<{ runs: CampaignRun[] | null }>(`/api/v1/campaigns?limit=${limit}`),
    get: (id: string) =>
      get<{ run: CampaignRun }>(`/api/v1/campaigns/${encodeURIComponent(id)}`),
    run: (req: CampaignRequest) =>
      post<{ run: CampaignRun }>("/api/v1/campaigns", req),
  },
  platform: {
    // T-061: modes, venues, AI providers, log levels and vault status —
    // one shape, one source each. Static except the vault status; the
    // console renders availability/reasons from this, never hardcoded.
    capabilities: () =>
      get<CapabilitiesResponse>("/api/v1/platform/capabilities"),
    current: () => get<PlatformSnapshotView>("/api/v1/platform/settings"),
    versions: (limit = 25) =>
      get<PlatformVersionInfo[]>(
        `/api/v1/platform/settings/versions?limit=${limit}`,
      ),
    version: (version: number) =>
      get<PlatformSnapshot>(`/api/v1/platform/settings/version/${version}`),
    preview: (settings: PlatformSettingsDoc) =>
      post<PlatformPreviewResponse>("/api/v1/platform/settings/preview", {
        settings,
      }),
    // parent_version required (T-058), same as api.config.apply above —
    // decodePlatformSettings reads it as a sibling of `settings`.
    apply: (settings: PlatformSettingsDoc, parentVersion: number) =>
      post<PlatformSnapshotView>("/api/v1/platform/settings", {
        settings,
        parent_version: parentVersion,
      }),
    rollback: (version: number, parentVersion: number) =>
      post<PlatformSnapshotView>("/api/v1/platform/settings/rollback", {
        version,
        parent_version: parentVersion,
      }),
  },
  engine: {
    status: () => get<EngineStatusResponse>("/api/v1/engine/status"),
    restart: (body: {
      confirm: string;
      reason?: string;
      stop_recording?: boolean;
    }) => post<EngineStatusResponse>("/api/v1/engine/restart", body),
  },
};

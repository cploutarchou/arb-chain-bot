// Typed API client for the Go backend. Every financial value crosses the
// wire as a string (decimal-safe) and is formatted for display only —
// the frontend NEVER recomputes profitability, fees, or risk.

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
  ) {
    super(apiError?.message ?? `API error (HTTP ${status})`);
    this.name = "ApiError";
  }
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
  const res = await fetch(path, { ...init, headers, credentials: "same-origin" });
  let env: Envelope<T> | null = null;
  try {
    env = (await res.json()) as Envelope<T>;
  } catch {
    // non-JSON error body — fall through with env null
  }
  if (!res.ok || env?.error) {
    throw new ApiError(res.status, env?.error ?? null);
  }
  if (env === null || env.data === null) {
    throw new ApiError(res.status, { code: "empty_response", message: "Empty response" });
  }
  return env.data;
}

const get = <T,>(path: string) => request<T>(path);
const post = <T,>(path: string, body?: unknown) =>
  request<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) });

// ---- shapes (mirroring the Go API; decimals stay strings) ----------------

export interface SystemStatus {
  mode: string;
  version: string;
  commit: string;
  uptime_sec: number;
  components: string[];
}

export interface Me {
  user_id: string;
  role: "ADMIN" | "OPERATOR" | "VIEWER";
  csrf_token?: string;
}

export interface PaperStatus {
  running: boolean;
  active_simulations: number;
  received: number;
  completed: number;
  failed: number;
  skipped: number;
}

export interface ScannerStatus {
  ready: boolean;
  triangles: number;
  markets: string[];
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
  assets: { asset: string; realized: string; fees: string; daily_loss: string; drawdown: string }[];
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

export interface Report {
  id: string;
  kind: string;
  period_start: string;
  period_end: string;
  generated_at: string;
  executive_summary: string;
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

// ---- endpoint groups -----------------------------------------------------

export const api = {
  auth: {
    login: (email: string, password: string) =>
      post<{ role: string; csrf_token: string }>("/api/v1/auth/login", { email, password }),
    logout: () => post<{ status: string }>("/api/v1/auth/logout"),
    me: () => get<Me>("/api/v1/auth/me"),
  },
  system: {
    status: () => get<SystemStatus>("/api/v1/system/status"),
    health: () => get<HealthView>("/api/v1/system/health"),
  },
  scanner: {
    status: () => get<ScannerStatus>("/api/v1/scanner/status"),
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
  },
  paper: {
    pause: () => post<{ running: boolean }>("/api/v1/paper/pause"),
    resume: () => post<{ running: boolean }>("/api/v1/paper/resume"),
    cycles: (limit = 100) => get<{ cycles: CycleRow[] | null }>(`/api/v1/paper/cycles?limit=${limit}`),
    orders: (cycleID: string) =>
      get<{ orders: OrderRow[] | null }>(`/api/v1/paper/cycles/${encodeURIComponent(cycleID)}/orders`),
  },
  portfolio: () => get<PortfolioView>("/api/v1/portfolio"),
  pnl: () => get<PnLView>("/api/v1/pnl"),
  risk: () => get<RiskView>("/api/v1/risk"),
  config: {
    current: () => get<ConfigSnapshot>("/api/v1/config"),
    versions: (limit = 25) => get<ConfigVersion[]>(`/api/v1/config/versions?limit=${limit}`),
    apply: (params: StrategyParams) => post<ConfigSnapshot>("/api/v1/config", params),
    rollback: (version: number) => post<ConfigSnapshot>("/api/v1/config/rollback", { version }),
  },
  alerts: {
    list: (state = "", limit = 50) =>
      get<{ alerts: Alert[] | null; active: number }>(`/api/v1/alerts?state=${state}&limit=${limit}`),
    ack: (id: string) => post<Alert>(`/api/v1/alerts/${encodeURIComponent(id)}/ack`),
    resolve: (id: string) => post<Alert>(`/api/v1/alerts/${encodeURIComponent(id)}/resolve`),
  },
  ai: {
    analyses: (limit = 10) => get<AIAnalysis[] | null>(`/api/v1/ai/analyses?limit=${limit}`),
    recommendations: (status = "") =>
      get<AIRecommendation[] | null>(`/api/v1/ai/recommendations?status=${status}`),
    approve: (id: string) =>
      post<{ config_version: number }>(`/api/v1/ai/recommendations/${encodeURIComponent(id)}/approve`),
    reject: (id: string) =>
      post<{ status: string }>(`/api/v1/ai/recommendations/${encodeURIComponent(id)}/reject`),
  },
  reports: {
    list: (kind = "", limit = 20) =>
      get<{ reports: Report[] | null }>(`/api/v1/reports?kind=${kind}&limit=${limit}`),
    generate: (kind: "daily" | "weekly") => post<Report>("/api/v1/reports/generate", { kind }),
  },
  triangles: {
    quality: (hours = 24) =>
      get<{ window_hours: number; scores: QualityScore[] | null; notes: string[] }>(
        `/api/v1/triangles/quality?hours=${hours}`,
      ),
  },
  audit: (entity = "", limit = 100) =>
    get<{ events: AuditEvent[] | null }>(`/api/v1/audit?entity=${entity}&limit=${limit}`),
};

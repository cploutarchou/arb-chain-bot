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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
    credentials: "same-origin",
  });
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

export interface SystemStatus {
  mode: string;
  version: string;
  commit: string;
  uptime_sec: number;
  components: string[];
}

export const api = {
  system: {
    status: () => request<SystemStatus>("/api/v1/system/status"),
  },
};

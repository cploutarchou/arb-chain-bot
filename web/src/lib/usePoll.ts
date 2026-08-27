"use client";

import { useEffect, useState } from "react";
import { ApiError } from "@/lib/api/client";

export type PollState<T> =
  | { kind: "loading" }
  | { kind: "error"; message: string; status?: number; code?: string }
  | { kind: "ready"; data: T };

// usePoll fetches immediately and then on an interval; errors keep the
// last good data out of view (state is explicit, never stale-but-shown).
export function usePoll<T>(fn: () => Promise<T>, intervalMs = 5000, deps: unknown[] = []): PollState<T> {
  const [state, setState] = useState<PollState<T>>({ kind: "loading" });
  useEffect(() => {
    let cancelled = false;
    const load = () =>
      fn()
        .then((data) => !cancelled && setState({ kind: "ready", data }))
        .catch((err: unknown) => {
          if (cancelled) return;
          if (err instanceof ApiError) {
            setState({ kind: "error", message: err.message, status: err.status, code: err.apiError?.code });
          } else {
            setState({ kind: "error", message: "Backend unreachable" });
          }
        });
    load();
    const t = setInterval(load, intervalMs);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  return state;
}

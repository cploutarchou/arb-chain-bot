"use client";

import { useEffect, useRef, useState } from "react";
import { ApiError } from "@/lib/api/client";

export type PollState<T> =
  | { kind: "loading" }
  // data: the last good payload, kept so a TRANSIENT error renders the
  // previous table under a warning instead of blanking it (audit F9).
  // A first-load error (never succeeded) has no data and stays a bare
  // error. lastOkAt is the wall clock of the last success.
  | { kind: "error"; message: string; status?: number; code?: string; data?: T; lastOkAt?: number }
  | { kind: "ready"; data: T; lastOkAt: number };

// usePoll fetches immediately and then on an interval. Errors never
// discard the last good payload: state is explicit about what is shown
// (ready) and what is held-over (error with data), and every success
// stamps lastOkAt so pages can say "Updated 4s ago".
export function usePoll<T>(fn: () => Promise<T>, intervalMs = 5000, deps: unknown[] = []): PollState<T> {
  const [state, setState] = useState<PollState<T>>({ kind: "loading" });
  // The last good payload lives in a ref so an error handler running
  // after a success can re-attach it without a state read-modify-write.
  const lastGood = useRef<{ data: T; at: number } | null>(null);
  useEffect(() => {
    let cancelled = false;
    const load = () =>
      fn()
        .then((data) => {
          if (cancelled) return;
          lastGood.current = { data, at: Date.now() };
          setState({ kind: "ready", data, lastOkAt: lastGood.current.at });
        })
        .catch((err: unknown) => {
          if (cancelled) return;
          const held = lastGood.current;
          if (err instanceof ApiError) {
            setState({ kind: "error", message: err.message, status: err.status, code: err.apiError?.code, data: held?.data, lastOkAt: held?.at });
          } else {
            setState({ kind: "error", message: "Backend unreachable", data: held?.data, lastOkAt: held?.at });
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

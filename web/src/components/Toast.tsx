"use client";

// ToastProvider: a minimal, self-contained toast stack. Its original
// producer is errorBus's entitlement_exceeded event (packages.md §3.2 —
// any mutating request can 403 entitlement_exceeded with {key, limit});
// that toast names the breached key/limit verbatim from the backend and
// links to /billing, never inventing its own limit copy (design-system.md
// §2.4: package gating is a sales surface, not a dead end). Shell-level
// controls (e.g. the paper pause/resume control) also push here directly
// via useToast() for success/failure feedback that survives a navigation.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import Link from "next/link";
import { onEntitlementExceeded } from "@/lib/errorBus";
import { CloseIcon } from "@/components/icons";

// tone picks the border/text colour; "warn" (amber) is the original,
// still-default look for the entitlement toast. "ok"/"bad" let a direct
// useToast() caller report a clear success/failure (e.g. paper control).
type ToastTone = "warn" | "ok" | "bad";

interface ToastItem {
  id: number;
  text: string;
  href?: string;
  linkLabel?: string;
  tone?: ToastTone;
}

interface ToastContextValue {
  push: (item: Omit<ToastItem, "id">) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

// entitlementLabel turns the machine key ("rules.max_active") into
// wording a person reads, without inventing a number the backend didn't
// send — the limit itself is always rendered verbatim from `data.limit`.
function entitlementLabel(key: string | undefined): string {
  if (!key) return "a package limit";
  return key.replace(/_/g, " ").replace(/\./g, " ");
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const nextId = useRef(0);
  const timers = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map());

  const dismiss = useCallback((id: number) => {
    setItems((prev) => prev.filter((t) => t.id !== id));
    const timer = timers.current.get(id);
    if (timer) {
      clearTimeout(timer);
      timers.current.delete(id);
    }
  }, []);

  const push = useCallback(
    (item: Omit<ToastItem, "id">) => {
      const id = nextId.current++;
      setItems((prev) => [...prev, { ...item, id }]);
      const timer = setTimeout(() => dismiss(id), 12_000);
      timers.current.set(id, timer);
    },
    [dismiss],
  );

  useEffect(() => {
    return onEntitlementExceeded((detail) => {
      const limitText =
        detail.limit === undefined || detail.limit === null
          ? ""
          : ` (limit: ${String(detail.limit)})`;
      push({
        text: `Included in your package: ${entitlementLabel(detail.key)}${limitText}. ${detail.message}`,
        href: "/billing",
        linkLabel: "Upgrade",
        tone: "warn",
      });
    });
  }, [push]);

  return (
    <ToastContext.Provider value={{ push }}>
      {children}
      <div
        aria-live="polite"
        className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-full max-w-sm flex-col gap-2"
      >
        {items.map((t) => (
          <div
            key={t.id}
            className={`pointer-events-auto flex items-start gap-2 rounded border bg-[var(--bg-panel)] p-3 text-[12px] text-[var(--text)] shadow-lg ${
              t.tone === "ok"
                ? "border-[var(--ok)]"
                : t.tone === "bad"
                  ? "border-[var(--critical)]"
                  : "border-[var(--warn)]"
            }`}
          >
            <span className="flex-1">
              {t.text}
              {t.href && (
                <>
                  {" "}
                  <Link
                    href={t.href}
                    className="font-medium text-[var(--accent)] underline"
                    onClick={() => dismiss(t.id)}
                  >
                    {t.linkLabel ?? "View"}
                  </Link>
                </>
              )}
            </span>
            <button
              type="button"
              aria-label="Dismiss"
              onClick={() => dismiss(t.id)}
              className="text-[var(--text-dim)] hover:text-[var(--text)]"
            >
              <CloseIcon />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast outside ToastProvider");
  return ctx;
}

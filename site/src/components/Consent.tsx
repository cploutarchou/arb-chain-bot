"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { readConsent, track, writeConsent, type ConsentState } from "@/lib/analytics";

// Cookie/consent banner (docs/site/legal/cookies.md §2): analytics is off
// by default and only the choice itself is stored. The banner is the
// only place consent is granted; "Cookie settings" in the footer reopens it.

export function ConsentBanner() {
  const [state, setState] = useState<ConsentState | "loading">("loading");
  const [open, setOpen] = useState(false);
  const path = usePathname();

  useEffect(() => {
    const c = readConsent();
    setState(c);
    setOpen(c === "unknown");
    const onOpen = () => setOpen(true);
    window.addEventListener("site:cookie-settings", onOpen);
    return () => window.removeEventListener("site:cookie-settings", onOpen);
  }, []);

  useEffect(() => {
    if (state !== "loading") track({ name: "page_view", path });
  }, [path, state]);

  if (!open) return null;

  const choose = (s: "granted" | "denied") => {
    writeConsent(s);
    setState(s);
    setOpen(false);
  };

  return (
    <div
      role="dialog"
      aria-label="Cookie settings"
      className="fixed inset-x-4 bottom-4 z-50 mx-auto max-w-2xl rounded border border-[var(--border-strong)] bg-[var(--bg-panel)] p-4 shadow-lg"
    >
      <p className="t-small">
        This site uses strictly necessary storage (your theme and this
        choice). Analytics is off unless you switch it on; no third-party
        script loads without consent.{" "}
        <Link href="/legal/cookies" className="underline text-[var(--accent)]">
          Cookie Policy
        </Link>
      </p>
      <div className="mt-3 flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() => choose("denied")}
          className="h-10 rounded border border-[var(--border-strong)] px-4 t-small font-medium"
        >
          Necessary only
        </button>
        <button
          type="button"
          onClick={() => choose("granted")}
          className="h-10 rounded bg-[var(--accent)] px-4 t-small font-medium text-[var(--on-accent)]"
        >
          Allow analytics
        </button>
      </div>
    </div>
  );
}

export function CookieSettingsLink() {
  return (
    <button
      type="button"
      className="underline"
      onClick={() => window.dispatchEvent(new Event("site:cookie-settings"))}
    >
      Cookie settings
    </button>
  );
}

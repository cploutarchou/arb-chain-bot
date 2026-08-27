// Consent-gated analytics stub. No third-party script is loaded by
// default and nothing leaves the browser: `track` buffers events in
// memory and, once consent is granted, hands them to whatever sink a
// later growth-analyst integration registers via `setSink`. In
// development the sink logs to the console so the event taxonomy can be
// checked. Event names follow the growth-analyst taxonomy once it exists;
// until then only the page-view and CTA events below are emitted.

export type AnalyticsEvent =
  | { name: "page_view"; path: string }
  | { name: "cta_click"; id: string; path: string }
  | { name: "consent_changed"; analytics: boolean };

export type ConsentState = "unknown" | "granted" | "denied";

const CONSENT_KEY = "site.consent";
const buffer: AnalyticsEvent[] = [];
let sink: ((e: AnalyticsEvent) => void) | null = null;

export function readConsent(): ConsentState {
  try {
    const v = localStorage.getItem(CONSENT_KEY);
    return v === "granted" || v === "denied" ? v : "unknown";
  } catch {
    return "unknown";
  }
}

export function writeConsent(state: Exclude<ConsentState, "unknown">) {
  try {
    localStorage.setItem(CONSENT_KEY, state);
  } catch {}
  if (state === "granted") flush();
  else buffer.length = 0;
  track({ name: "consent_changed", analytics: state === "granted" });
}

export function setSink(fn: (e: AnalyticsEvent) => void) {
  sink = fn;
  if (readConsent() === "granted") flush();
}

function flush() {
  if (!sink) return;
  while (buffer.length) {
    const e = buffer.shift();
    if (e) sink(e);
  }
}

export function track(e: AnalyticsEvent) {
  const consent = readConsent();
  if (consent === "denied") return;
  if (process.env.NODE_ENV !== "production") {
    console.debug("[analytics stub]", consent, e);
  }
  if (consent === "granted" && sink) sink(e);
  else if (consent !== "granted") buffer.push(e);
}

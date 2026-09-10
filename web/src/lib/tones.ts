// Shared tone maps so one status always reads one colour on every page
// (audit ui F18 — duplicated helpers had begun to diverge on their
// unknown-state fallbacks). Tone names are the design system's Badge
// tones; the carrier of any state is the word itself, colour only
// reinforces it.
import type { Tone } from "@/components/ui";

// orderFillStatusTone maps the orders/fills status vocabulary. Unknown
// values fall back to dim — never silently to a success colour.
export function orderFillStatusTone(status: string): Tone {
  if (status === "FILLED") return "ok";
  if (status === "PARTIAL") return "warn";
  if (status === "REJECTED" || status === "CANCELED") return "bad";
  return "dim";
}

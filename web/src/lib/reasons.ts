// Reason-code glosses shared by the Opportunities list, the detail page
// and the Risk Center (audit ui F10/F11): a code alone says which gate
// refused, the gloss says what that means for a human deciding whether
// to loosen it. The code stays visible beside the gloss — the gloss is a
// translation, never a replacement.
export const REASON_GLOSSES: Record<string, string> = {
  RISK_TRIANGLE_DISABLED: "triangle excluded by settings",
  RISK_BREAKER_OPEN: "a circuit breaker is open",
  RISK_CLOCK_UNSAFE: "clock quality below the safe threshold",
  RISK_BOOK_STATE: "a leg's book is not HEALTHY",
  RISK_BOOK_AGE: "a leg's book is too old",
  RISK_BOOK_AGE_SPREAD: "legs' book ages differ too much",
  RISK_DATA_QUALITY: "input quality score too low",
  RISK_MIN_EDGE: "net edge below the configured minimum",
  RISK_MIN_PROFIT: "absolute profit below the configured minimum",
  RISK_MAX_TRADE_SIZE: "size above the configured maximum",
  RISK_PRICE_IMPACT: "worst-leg price impact above the cap",
  RISK_CONCURRENCY: "too many simulations in flight",
  RISK_DAILY_LOSS: "daily loss limit reached",
  RISK_DRAWDOWN: "drawdown limit reached",
  RISK_UTILIZATION: "capital utilization above the cap",
  RISK_TRIANGLE_CAPITAL: "triangle holds reserved capital already",
  RISK_TTL_EXPIRED: "opportunity expired before execution",
  RISK_REVALIDATION: "pre-execution re-check refused it",
};

// reasonText pairs a code with its gloss for a table cell.
export function reasonText(code: string | undefined | null): string {
  if (!code) return "—";
  const gloss = REASON_GLOSSES[code];
  return gloss ? `${code} — ${gloss}` : code;
}

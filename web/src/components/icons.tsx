// Own inline SVG nav icons — no external asset/icon-font dependency, no
// copied branding. One small line-glyph per nav item, 16x16, drawn with
// primitive shapes (rect/circle/line/polygon/path) rather than traced
// artwork. Unmapped labels fall back to a plain dot so a future nav item
// never renders a missing icon.

import type { ReactNode } from "react";

const GLYPHS: Record<string, ReactNode> = {
  Overview: (
    <>
      <rect x="1.5" y="1.5" width="5" height="5" />
      <rect x="9" y="1.5" width="5.5" height="5" />
      <rect x="1.5" y="9" width="5" height="5.5" />
      <rect x="9" y="9" width="5.5" height="5.5" />
    </>
  ),
  Scanner: (
    <>
      <circle cx="8" cy="8" r="5.5" />
      <circle cx="8" cy="8" r="1.1" fill="currentColor" stroke="none" />
    </>
  ),
  Triangles: <polygon points="8,2 14,13 2,13" />,
  Opportunities: (
    <polygon
      points="9,1 3,9 7,9 6,15 13,6 9,6"
      fill="currentColor"
      stroke="none"
    />
  ),
  "Paper Trading": (
    <>
      <rect x="3" y="1.5" width="10" height="13" rx="1" />
      <line x1="5" y1="5" x2="11" y2="5" />
      <line x1="5" y1="8" x2="11" y2="8" />
      <line x1="5" y1="11" x2="9" y2="11" />
    </>
  ),
  "Portfolio & Balances": (
    <>
      <rect x="1.5" y="4" width="13" height="9.5" rx="1.2" />
      <path d="M1.5 4 L3.2 1.8 H11 L12.7 4" />
      <circle cx="11" cy="8.7" r="1" fill="currentColor" stroke="none" />
    </>
  ),
  "PnL & Analytics": (
    <>
      <line x1="2" y1="14" x2="14" y2="14" />
      <rect
        x="3"
        y="9"
        width="2.2"
        height="5"
        fill="currentColor"
        stroke="none"
      />
      <rect
        x="6.9"
        y="6"
        width="2.2"
        height="8"
        fill="currentColor"
        stroke="none"
      />
      <rect
        x="10.8"
        y="3"
        width="2.2"
        height="11"
        fill="currentColor"
        stroke="none"
      />
    </>
  ),
  Orders: (
    <>
      <polyline points="1.5,3.2 2.4,4.1 4,2" />
      <line x1="6" y1="3.2" x2="14.5" y2="3.2" />
      <polyline points="1.5,8.2 2.4,9.1 4,7" />
      <line x1="6" y1="8.2" x2="14.5" y2="8.2" />
      <polyline points="1.5,13.2 2.4,14.1 4,12" />
      <line x1="6" y1="13.2" x2="14.5" y2="13.2" />
    </>
  ),
  Fills: (
    <>
      <polygon points="8,1.5 14.5,5.5 8,9.5 1.5,5.5" />
      <polyline points="1.5,9.8 8,13.8 14.5,9.8" />
    </>
  ),
  Campaigns: (
    <>
      <line x1="3" y1="1.5" x2="3" y2="14.5" />
      <path d="M3 2.2 H12 L9.4 5.4 L12 8.6 H3" />
    </>
  ),
  "Replay & Backtesting": (
    <>
      <circle cx="8.5" cy="9" r="5" />
      <polyline points="8.5,6.2 8.5,9 11,10.5" />
      <polyline points="3,2 3,5 6,5" />
    </>
  ),
  "AI Advisor": (
    <path
      d="M8 1 L9.4 6.2 L14.5 8 L9.4 9.8 L8 15 L6.6 9.8 L1.5 8 L6.6 6.2 Z"
      fill="currentColor"
      stroke="none"
    />
  ),
  Strategies: (
    <>
      <line x1="3" y1="2" x2="3" y2="14" />
      <circle cx="3" cy="6" r="1.3" fill="currentColor" stroke="none" />
      <line x1="8" y1="2" x2="8" y2="14" />
      <circle cx="8" cy="10" r="1.3" fill="currentColor" stroke="none" />
      <line x1="13" y1="2" x2="13" y2="14" />
      <circle cx="13" cy="4.5" r="1.3" fill="currentColor" stroke="none" />
    </>
  ),
  "Risk Center": (
    <path d="M8 1.4 L14 3.4 V8 C14 12 11 14.4 8 15 C5 14.4 2 12 2 8 V3.4 Z" />
  ),
  Alerts: (
    <>
      <path d="M8 2.2 A2.8 2.8 0 0 1 10.8 5 V7.3 L12.2 10.2 H3.8 L5.2 7.3 V5 A2.8 2.8 0 0 1 8 2.2 Z" />
      <path d="M6.5 12 a1.5 1.5 0 0 0 3 0" />
    </>
  ),
  Reports: (
    <>
      <path d="M4 1.5 H10 L13 4.5 V14.5 H4 Z" />
      <path d="M10 1.5 V4.5 H13" />
      <line x1="6" y1="8" x2="11" y2="8" />
      <line x1="6" y1="10.5" x2="11" y2="10.5" />
    </>
  ),
  Exchanges: (
    <>
      <path d="M2 5 H11 M8 2 L11 5 L8 8" />
      <path d="M14 11 H5 M8 8 L5 11 L8 14" />
    </>
  ),
  Markets: (
    <>
      <path d="M2 2 H8 L14 8 L8 14 L2 8 Z" />
      <circle cx="5" cy="5" r="1" fill="currentColor" stroke="none" />
    </>
  ),
  "System Health": <path d="M1.5 8.5 H4.3 L5.8 4.5 L8.3 12.5 L9.8 8.5 H14.5" />,
  "Audit Log": (
    <>
      <rect x="3" y="2.5" width="10" height="12" rx="1" />
      <rect x="5.5" y="1" width="5" height="2.2" rx="0.6" />
      <line x1="5" y1="7" x2="11" y2="7" />
      <line x1="5" y1="10" x2="11" y2="10" />
    </>
  ),
  Telegram: <path d="M1.5 8.3 L14 2 L10.2 14.2 L7 9.3 L3.6 10.8 Z" />,
  "Users & Security": (
    <>
      <rect x="3" y="7" width="10" height="7.2" rx="1" />
      <path d="M5 7 V5.2 A3 3 0 0 1 11 5.2 V7" />
    </>
  ),
  Settings: (
    <>
      <circle cx="8" cy="8" r="2.3" />
      <path d="M8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.5 3.5l1.4 1.4M11.1 11.1l1.4 1.4M12.5 3.5l-1.4 1.4M4.9 11.1l-1.4 1.4" />
    </>
  ),
  Screener: (
    <>
      <circle cx="6.6" cy="6.6" r="4.6" />
      <line x1="9.9" y1="9.9" x2="14" y2="14" />
    </>
  ),
  Perpetuals: (
    <>
      <circle cx="5.3" cy="8" r="2.6" />
      <circle cx="10.7" cy="8" r="2.6" />
    </>
  ),
  Funding: (
    <>
      <line x1="3" y1="13" x2="13" y2="3" />
      <circle cx="4.5" cy="4.5" r="1.6" />
      <circle cx="11.5" cy="11.5" r="1.6" />
    </>
  ),
  Calculator: (
    <>
      <rect x="3" y="1.5" width="10" height="13" rx="1" />
      <line x1="5" y1="4.5" x2="11" y2="4.5" />
      <circle cx="5.3" cy="7.6" r="0.65" fill="currentColor" stroke="none" />
      <circle cx="8" cy="7.6" r="0.65" fill="currentColor" stroke="none" />
      <circle cx="10.7" cy="7.6" r="0.65" fill="currentColor" stroke="none" />
      <circle cx="5.3" cy="10.2" r="0.65" fill="currentColor" stroke="none" />
      <circle cx="8" cy="10.2" r="0.65" fill="currentColor" stroke="none" />
      <circle cx="10.7" cy="10.2" r="0.65" fill="currentColor" stroke="none" />
      <circle cx="5.3" cy="12.8" r="0.65" fill="currentColor" stroke="none" />
      <circle cx="8" cy="12.8" r="0.65" fill="currentColor" stroke="none" />
      <circle cx="10.7" cy="12.8" r="0.65" fill="currentColor" stroke="none" />
    </>
  ),
  "Alert Rules": <path d="M2 2 H14 L9.5 8 V13 L6.5 14.5 V8 Z" />,
  "Auto-Paper": (
    <>
      <circle cx="8" cy="8" r="6.5" />
      <polygon points="6.4,5 6.4,11 11,8" fill="currentColor" stroke="none" />
    </>
  ),
};

const DEFAULT_GLYPH = (
  <circle cx="8" cy="8" r="1.6" fill="currentColor" stroke="none" />
);

export function NavIcon({ label }: { label: string }) {
  return (
    <svg
      viewBox="0 0 16 16"
      width="15"
      height="15"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className="shrink-0"
    >
      {GLYPHS[label] ?? DEFAULT_GLYPH}
    </svg>
  );
}

// SunIcon / MoonIcon back the theme toggle (top bar) — same own-icon
// convention as the nav glyphs above.
export function SunIcon() {
  return (
    <svg
      viewBox="0 0 16 16"
      width="14"
      height="14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <circle cx="8" cy="8" r="3.2" />
      <path d="M8 0.8v2M8 13.2v2M0.8 8h2M13.2 8h2M2.6 2.6l1.4 1.4M12 12l1.4 1.4M13.4 2.6L12 4M4 12l-1.4 1.4" />
    </svg>
  );
}

export function MoonIcon() {
  return (
    <svg
      viewBox="0 0 16 16"
      width="14"
      height="14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M13.5 9.8A6 6 0 1 1 6.2 2.5a4.8 4.8 0 0 0 7.3 7.3Z" />
    </svg>
  );
}

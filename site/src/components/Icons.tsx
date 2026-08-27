// Own inline SVG glyphs — design-system.md §6: the console glyph set,
// viewBox 16, rendered at 20/24px, stroke 1.7. Primitive shapes only; no
// third-party icon set, no traced artwork.

import type { ReactNode } from "react";

const GLYPHS = {
  screener: (
    <>
      <circle cx="8" cy="8" r="5.5" />
      <circle cx="8" cy="8" r="1.1" fill="currentColor" stroke="none" />
    </>
  ),
  perpetuals: (
    <>
      <line x1="2" y1="11" x2="14" y2="11" />
      <polyline points="2,8 6,5 10,7 14,3" />
    </>
  ),
  triangular: <polygon points="8,2 14,13 2,13" />,
  paper: (
    <>
      <rect x="3" y="1.5" width="10" height="13" rx="1" />
      <line x1="5" y1="5" x2="11" y2="5" />
      <line x1="5" y1="8" x2="11" y2="8" />
      <line x1="5" y1="11" x2="9" y2="11" />
    </>
  ),
  check: <polyline points="3,8.5 6.5,12 13,4.5" />,
  dash: <line x1="3" y1="8" x2="13" y2="8" />,
  sun: (
    <>
      <circle cx="8" cy="8" r="3" />
      <line x1="8" y1="1.5" x2="8" y2="3.5" />
      <line x1="8" y1="12.5" x2="8" y2="14.5" />
      <line x1="1.5" y1="8" x2="3.5" y2="8" />
      <line x1="12.5" y1="8" x2="14.5" y2="8" />
      <line x1="3.4" y1="3.4" x2="4.8" y2="4.8" />
      <line x1="11.2" y1="11.2" x2="12.6" y2="12.6" />
      <line x1="3.4" y1="12.6" x2="4.8" y2="11.2" />
      <line x1="11.2" y1="4.8" x2="12.6" y2="3.4" />
    </>
  ),
  moon: <path d="M13 10.5A6 6 0 0 1 5.5 3a6 6 0 1 0 7.5 7.5z" />,
  info: (
    <>
      <circle cx="8" cy="8" r="6" />
      <line x1="8" y1="7" x2="8" y2="11.5" />
      <circle cx="8" cy="4.8" r="0.6" fill="currentColor" stroke="none" />
    </>
  ),
  shield: (
    <>
      <path d="M8 1.5 13 3.5v4c0 3.2-2.2 5.6-5 7-2.8-1.4-5-3.8-5-7v-4z" />
      <polyline points="5.5,8 7.3,9.8 10.5,6.5" />
    </>
  ),
  menu: (
    <>
      <line x1="2" y1="4.5" x2="14" y2="4.5" />
      <line x1="2" y1="8" x2="14" y2="8" />
      <line x1="2" y1="11.5" x2="14" y2="11.5" />
    </>
  ),
  arrow: (
    <>
      <line x1="2" y1="8" x2="13" y2="8" />
      <polyline points="9,4 13,8 9,12" />
    </>
  ),
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof GLYPHS;

export function Icon({
  name,
  size = 20,
  className,
}: {
  name: IconName;
  size?: 20 | 24 | 16;
  className?: string;
}) {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 16 16"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
    >
      {GLYPHS[name]}
    </svg>
  );
}

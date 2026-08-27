"use client";

import { useEffect, useState } from "react";
import { Icon } from "./Icons";

// Light/dark toggle. Persists to localStorage "site.theme"; the inline
// script in layout.tsx applies it before first paint. Without a stored
// choice globals.css follows prefers-color-scheme.

type Theme = "light" | "dark";

function current(): Theme {
  if (typeof document === "undefined") return "light";
  const attr = document.documentElement.getAttribute("data-theme");
  if (attr === "dark" || attr === "light") return attr;
  return window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>("light");
  useEffect(() => setTheme(current()), []);
  const next: Theme = theme === "dark" ? "light" : "dark";
  return (
    <button
      type="button"
      onClick={() => {
        document.documentElement.setAttribute("data-theme", next);
        try {
          localStorage.setItem("site.theme", next);
        } catch {}
        setTheme(next);
      }}
      aria-label={`Switch to ${next} theme`}
      title={`Switch to ${next} theme`}
      className="inline-flex h-9 w-9 items-center justify-center rounded border border-[var(--border-strong)] text-[var(--text)] hover:bg-[var(--bg-raised)]"
    >
      <Icon name={theme === "dark" ? "sun" : "moon"} size={16} />
    </button>
  );
}

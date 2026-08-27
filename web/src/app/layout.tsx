import type { Metadata } from "next";
import Script from "next/script";
import "./globals.css";
import { AuthProvider } from "@/lib/auth";

export const metadata: Metadata = {
  title: "Arb Console",
  description: "Triangular-arbitrage research & paper-trading operations console",
};

// Runs before hydration (strategy="beforeInteractive") so a saved theme
// preference (ConsoleShell's useThemeToggle, "arb.theme" in
// localStorage) applies to the very first paint instead of flashing the
// CSS-default theme first. try/catch: private mode / storage disabled
// just leaves the attribute unset, and globals.css's prefers-color-scheme
// fallback takes over.
const THEME_INIT_SCRIPT = `
try {
  var t = localStorage.getItem("arb.theme");
  if (t === "light" || t === "dark") {
    document.documentElement.setAttribute("data-theme", t);
  }
} catch (e) {}
`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <Script id="theme-init" strategy="beforeInteractive">
          {THEME_INIT_SCRIPT}
        </Script>
      </head>
      <body className="min-h-screen antialiased">
        <AuthProvider>{children}</AuthProvider>
      </body>
    </html>
  );
}

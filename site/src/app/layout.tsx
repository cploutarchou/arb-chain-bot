import type { Metadata } from "next";
import Script from "next/script";
import "./globals.css";
import { Header } from "@/components/Header";
import { Footer } from "@/components/Footer";
import { ConsentBanner } from "@/components/Consent";
import { brand } from "@/lib/tokens";
import { siteConfig } from "../../site.config";

export const metadata: Metadata = {
  metadataBase: new URL(siteConfig.siteUrl),
  title: brand,
  description:
    "Market data, alerts and simulated (paper) execution for crypto arbitrage. Measures spreads net of modelled fees; does not trade, hold funds or take exchange keys.",
  openGraph: { siteName: brand, type: "website", locale: siteConfig.locale },
  robots: { index: true, follow: true },
};

// Applies a stored theme before first paint; without one globals.css
// follows prefers-color-scheme. try/catch: storage may be unavailable.
const THEME_INIT_SCRIPT = `
try {
  var t = localStorage.getItem("site.theme");
  if (t === "light" || t === "dark") {
    document.documentElement.setAttribute("data-theme", t);
  }
} catch (e) {}
`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang={siteConfig.locale}>
      <head>
        <Script id="theme-init" strategy="beforeInteractive">
          {THEME_INIT_SCRIPT}
        </Script>
      </head>
      <body className="min-h-screen antialiased t-body">
        <Header />
        <main>{children}</main>
        <Footer />
        <ConsentBanner />
      </body>
    </html>
  );
}

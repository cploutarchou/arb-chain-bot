import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Arb Console",
  description: "Triangular-arbitrage research & paper-trading operations console",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="min-h-screen antialiased">{children}</body>
    </html>
  );
}

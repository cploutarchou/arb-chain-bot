import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  async rewrites() {
    // Proxy /api to the Go backend. In deployment the reverse proxy owns
    // this mapping and sets ARB_BACKEND_URL (web.Dockerfile, the Helm
    // configmap); the e2e run sets it to its own isolated port 18080
    // (playwright.config.ts).
    //
    // There is deliberately NO default (T-098). This used to fall back to
    // http://localhost:8080 — the port the live paper stack listens on —
    // so simply running `npm run dev` pointed the console at whatever
    // evidence run was in progress. On 2026-08-27 that cost a measurement:
    // a console session rewrote the carry rule under test into a spread
    // rule mid-run, and carry executions stopped. Pointing the console at
    // a live backend must be a deliberate act, not a default.
    const backend = process.env.ARB_BACKEND_URL;
    if (!backend) {
      console.warn(
        "[next.config] ARB_BACKEND_URL is not set, so /api is NOT proxied and " +
          "API calls will 404. Set it to the backend you intend to talk to, " +
          "e.g. ARB_BACKEND_URL=http://localhost:8080 npm run dev — and never " +
          "to a backend serving an evidence run you care about (T-098).",
      );
      return [];
    }
    return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
  },
  async headers() {
    // Baseline browser hardening for the console (audit S-011). The CSP
    // allows only same-origin resources plus the WS connection back to
    // the console origin; Next.js inline runtime chunks need
    // 'unsafe-inline' for scripts/styles until a nonce pipeline exists.
    // Dev server only: webpack/react-refresh require eval — never in
    // production builds.
    const dev = process.env.NODE_ENV !== "production";
    const csp = [
      "default-src 'self'",
      `script-src 'self' 'unsafe-inline'${dev ? " 'unsafe-eval'" : ""}`,
      "style-src 'self' 'unsafe-inline'",
      "img-src 'self' data:",
      "font-src 'self'",
      "connect-src 'self' ws: wss:",
      "frame-ancestors 'none'",
      "base-uri 'self'",
      "form-action 'self'",
    ].join("; ");
    return [
      {
        source: "/:path*",
        headers: [
          { key: "Content-Security-Policy", value: csp },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Referrer-Policy", value: "no-referrer" },
          { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()" },
        ],
      },
    ];
  },
};

export default nextConfig;

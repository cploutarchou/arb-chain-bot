import type { NextConfig } from "next";

// Static-first: the marketing site is exported to plain HTML (site/out)
// and can be served from any static host. No server runtime, no API
// routes, no rewrites. Security headers are the host's job for a static
// export; see docs/deployment.md for the reverse-proxy baseline.
const nextConfig: NextConfig = {
  output: "export",
  reactStrictMode: true,
  trailingSlash: false,
  images: { unoptimized: true },
};

export default nextConfig;

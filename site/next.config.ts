import type { NextConfig } from "next";

// Static-first: the marketing site is exported to plain HTML (site/out)
// and can be served from any static host. No server runtime, no API
// routes, no rewrites. Security headers are the host's job for a static
// export — the header set lives under deploy/site/ (audit S15) for the
// hosting path in use; see deploy/site/README.md.
const nextConfig: NextConfig = {
  output: "export",
  reactStrictMode: true,
  trailingSlash: false,
  images: { unoptimized: true },
};

export default nextConfig;

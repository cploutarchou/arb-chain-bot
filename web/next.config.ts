import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  async rewrites() {
    // Dev convenience: proxy API + WS to the Go backend. In deployment the
    // reverse proxy owns this mapping.
    const backend = process.env.ARB_BACKEND_URL ?? "http://localhost:8080";
    return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
  },
};

export default nextConfig;

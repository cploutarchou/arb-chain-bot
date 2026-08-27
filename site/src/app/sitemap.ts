import type { MetadataRoute } from "next";
import { listGuide, listLegal } from "@/lib/content";
import { siteConfig } from "../../site.config";

export const dynamic = "force-static";

export default function sitemap(): MetadataRoute.Sitemap {
  const base = siteConfig.siteUrl.replace(/\/$/, "");
  const fixed = [
    "/",
    "/products/screener",
    "/products/perpetuals",
    "/products/triangular",
    "/products/auto-paper",
    "/pricing",
    "/about",
    "/faq",
    "/affiliates",
    "/docs",
  ];
  const legal = listLegal().map((s) => `/legal/${s}`);
  const docs = listGuide().map((s) => `/docs/${s}`);
  return [...fixed, ...legal, ...docs].map((path) => ({
    url: `${base}${path}`,
    changeFrequency: path.startsWith("/legal") ? "yearly" : "weekly",
    priority: path === "/" ? 1 : path.startsWith("/legal") ? 0.3 : 0.7,
  }));
}

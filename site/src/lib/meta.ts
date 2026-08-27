import type { Metadata } from "next";
import { brand } from "./tokens";
import { siteConfig } from "../../site.config";

export function pageMeta(title: string, description: string, path: string): Metadata {
  const full = path === "/" ? brand : `${title} · ${brand}`;
  return {
    title: full,
    description,
    alternates: { canonical: path },
    openGraph: {
      title: full,
      description,
      url: path,
      siteName: brand,
      type: "website",
      locale: siteConfig.locale,
    },
    twitter: { card: "summary", title: full, description },
  };
}

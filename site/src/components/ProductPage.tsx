import { loadCopy } from "@/lib/content";
import { CopySection, Evidence, Hero, JurisdictionBanner, Tail } from "./Page";

// One renderer for the four product pages: hero, ordered sections, the
// evidence block, and the verbatim risk summary tail.

export function ProductPage({ name, jurisdiction = false }: { name: string; jurisdiction?: boolean }) {
  const page = loadCopy(name);
  const hero = page.sections.find((s) => s.title === "Hero");
  const banner = page.sections.find((s) => s.title.startsWith("Banner"));
  const rest = page.sections.filter(
    (s) => s !== hero && s !== banner && s.title !== "Evidence",
  );
  const evidence = page.sections.find((s) => s.title === "Evidence");
  return (
    <>
      {jurisdiction && <JurisdictionBanner html={banner?.fields["default"]} />}
      {hero && <Hero section={hero} />}
      {rest.map((s) => (
        <CopySection key={s.title} section={s} />
      ))}
      {evidence && <Evidence section={evidence} />}
      <Tail html={page.tailHtml} />
    </>
  );
}

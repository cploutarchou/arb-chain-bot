---
name: marketing-site
description: 'Public marketing site and content: product pages, pricing, docs, blog, case studies from our own paper reports, legal pages, SEO, affiliate pages — own design and copy. Use for anything customer-facing outside the console. Use when the request mentions: landing page, marketing site, pricing page, blog, case study, SEO, legal pages.'
when_to_use: [landing page, marketing site, pricing page, blog, case study, SEO, legal pages]
allowed-tools: Read Grep Glob Write Edit Bash Agent WebFetch WebSearch
argument-hint: '[task]'
---

# Marketing site workflow

Task: $ARGUMENTS

- Stack: Next.js app under `site/` sharing the design tokens with the console (ui-designer style guide), static-first, i18n-ready.
- Pages: home, products (screener, perpetuals/funding, triangular, auto-paper), pricing (from docs/design/packages.md), docs, blog, case studies (ONLY from docs/campaigns/ reports, with fees and verdicts verbatim), affiliate, about, legal (terms, privacy, risk disclosure, refund, cookies).
- Copy by content-copywriter, reviewed by compliance-reviewer before publish; no earnings promises, no invented testimonials, no copied competitor text or assets.
- SEO: metadata, sitemap, structured data; analytics per growth-analyst event taxonomy, consent-gated.

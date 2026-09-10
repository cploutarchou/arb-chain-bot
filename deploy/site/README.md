# Marketing-site host configuration

The site is a static export (`site/out`, `site/next.config.ts`) with no
server runtime, so response headers come from whatever host serves the
files — the app cannot set them (audit S15). Two interchangeable
emitters of the same header set:

- `_headers` — Cloudflare Pages / Netlify: copy into the export root
  (or set the Pages build output directory to `deploy/site` overlay).
- `headers.conf` — nginx: `include deploy/site/headers.conf;` from the
  `server{}` block serving `site/out`.

The set mirrors the console's baseline (`web/next.config.ts`) minus the
WebSocket `connect-src` (the marketing site talks to no backend) plus
HSTS on the TLS-terminating host. Scripts/styles keep `'unsafe-inline'`
because Next's static export embeds inline runtime chunks — a nonce
pipeline needs a server. Any change here should be mirrored in both
files and in the console's set.

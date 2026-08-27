# Runbook: Scanner Suite collectors (rate limits, self-healing, 418 / 403 / 510)

Scope: the per-venue public-REST collectors in `internal/screener/venue`
and the poller that drives them. Public market data only; no key is ever
involved, so a ban is an IP ban, not an account problem.

Status surface: `GET /api/v1/screener/status` (`screener:view`) →
`venues[]{id, enabled, online, last_poll_at, poll_ms, spot_pairs,
perp_contracts, rate_limited, polls, restarts, error}` plus
`pairs_tracked`, `spreads_per_sec`, `poll_interval_s`. The Screener page's
stats strip shows venues online; the per-venue counters are on the API.

Logs to grep (`kubectl -n arb logs deploy/arb-arb-platform-arbd` or
`docker compose logs arbd`):

```
screener poll failed                 venue=<id> error=<venue>: HTTP <code> (retry-after "<v>"): <body>
screener collector restarted: no completed poll within the stale window   venue=<id> stale_after=<d>
funding history upsert failed
screener automation ticker panicked
```

## 1. How the gates work

Each venue has a sliding-window gate (weight for Binance, request count
elsewhere) under a ceiling, plus a hard block after a rate-limit response.
The gate **delays, never drops**: a poll that cannot fit waits, so a
venue under pressure gets slower, not noisier. Ceilings in this build:

| Venue | Gate | Documented limit |
|---|---|---|
| Binance | spot 3 000 weight/min, futures 1 200 weight/min (one bulk spot poll costs 4, futures 5 + 10) | 6 000 / 2 400 weight per minute per IP, confirmed live from `exchangeInfo.rateLimits` |
| OKX | 10 req / 2 s | 20 req/2 s tickers and instruments, 10 req/2 s mark price and funding, per IP |
| Bybit | per-venue gate; excess is a **403** with a ≥ 10 min ban | 600 req / 5 s per IP |
| Bitget | 10 req/s | 20 req/s per endpoint; ban code UNVERIFIED (rate-limit page 404s) |
| Gate | 10 req/s | UNVERIFIED (conflicting figures) |
| MEXC | 10 req / 2 s; contract detail only on instrument refresh (≥ 10 min) | spot 500 req/10 s per endpoint; contract ticker 20 req/2 s; detail 1 req/5 s |
| KuCoin | 600 of 2 000 weight / 30 s per host | 2 000 weight / 30 s per IP, spot and futures each; excess 429 code 429000 |
| HTX | spot 2 req/s, swap 10 req/s | UNVERIFIED from a rendered page |
| Kraken | spot 1 req/s; futures public calls are uncounted by the venue | "1 per second (or less)" spot; futures public uncosted |
| Coinbase | 8 req/s across both hosts | Advanced Trade 10 req/s UNVERIFIED; Exchange 10 req/s (burst 15) verified |

Instrument lists are cached for 10 minutes; a failed refresh keeps serving
the old list rather than blanking a venue.

Each poll = one bulk spot call (+ instrument refresh when stale) and, when
`perps_enabled`, one or two bulk perp calls plus `funding_calls_per_poll`
per-instrument calls on OKX / Bitget / MEXC (funding) and HTX (mark), and
`BooksPerPoll` (40) per-product book calls on Coinbase. `poll_interval_s`
(2–60, hot) and `funding_calls_per_poll` (0–50, hot) are the two knobs;
`venues.<id>.enabled` is restart-scoped.

## 2. Self-healing restarts

Every automation tick, the runner checks each enabled venue: if its last
**completed** poll (or loop start) is older than
`StaleAfterPolls (5) × poll_interval_s`, the venue's goroutine is cancelled
(which unblocks a wedged HTTP call) and a fresh collector is started.
Effects:

- `venues[].restarts` increments; `online` drops to false until the new
  loop completes a poll; `error` reads `restarted: no completed poll for
  <duration>`; a warn line is logged.
- The instrument cache and the funding round-robin state start empty for
  the new collector; the book keeps the old quotes with their old
  timestamps (ages keep climbing honestly until fresh data lands).
- The gate's block (if any) belongs to the old collector and is gone; if
  the venue is actually banning the IP, the new collector's first call
  will be blocked again and the cycle repeats every stale window. A
  climbing `restarts` count with `rate_limited` also climbing is a ban,
  not a hang — see §3.

What it does **not** do: restart the process, touch other venues, or
back-fill missed funding settlements (a gap in `funding_history` stays a
gap; auto-paper retries a settled-rate fetch for up to 3 polls and then
leaves the settlement unbooked, which shows in the report as a lower
`funding_rows`).

A venue whose `restarts` climbs with a **network** error (DNS, timeout,
TLS) rather than an HTTP status is an egress or venue-outage problem; use
`docs/runbooks/feed-stale.md` step 1.

## 3. Rate-limit responses

The gate treats **429, 418 and 403** as rate-limit responses: it counts
them in `rate_limited`, and blocks the venue until `Retry-After`
(integer seconds or HTTP date) plus one second — default 60 s when the
header is absent or unparsable, 10 minutes for a 403 (Bybit's documented
cool-down). During the block the poll loop waits; `online` stays false and
`error` shows the HTTP line.

### 418 (Binance, MEXC): IP auto-ban after continued 429s

Binance escalates repeated 429s to a 418 with a ban from 2 minutes up to
3 days, per IP; MEXC documents the same wording. Because the ban is on the
IP, the triangular engine's own Binance REST use shares it.

1. Read `Retry-After` from the log line; the gate already honours it. Do
   **not** restart the process or the collector to "clear" it — a new
   process makes the same calls from the same IP and extends the ban.
2. Find the cause. A 418 means the ceiling was exceeded somewhere on this
   IP: another process (a second `arbd`, a local script, the campaign
   runner's REST resyncs) or a `poll_interval_s` lowered to the 2 s floor
   with many per-instrument calls. Check `rate_limited` on every venue and
   `ReconnectLoop`/`CollectorRateLimited` on the engine side.
3. Reduce load for the rest of the ban: raise `poll_interval_s` (hot) and
   lower `funding_calls_per_poll` (hot). If the venue must be quiet
   entirely, untick `venues.<id>.enabled` and restart the engine (restart
   scoped; refused while a recording or campaign is active, see
   `docs/deployment.md` §3c).
4. After `Retry-After` elapses the gate unblocks itself; confirm `online`
   returns and `rate_limited` stops climbing before restoring the old
   interval.
5. If a campaign or soak window overlaps the ban, note it in the campaign
   record; no published number may come from a gapped window.

### 403 (Bybit): "access too frequent"

Bybit answers rate-limit breaches with 403 and a cool-down of at least
10 minutes; the gate blocks for 10 minutes when no `Retry-After` is
present. Same procedure as 418. A 403 with a body that is not a
rate-limit message (geo-block, WAF) will also be treated as a 10-minute
block; if `rate_limited` climbs on Bybit while the request rate is far
below 600/5 s, check egress geography, not load.

### 510 (MEXC): in-band "too frequent"

MEXC's contract API can answer a **200** with an in-band error code 510
("too frequent") instead of an HTTP rate-limit status. The gate keys on
HTTP status, so a 510 is **not** recognised as a rate limit: it surfaces
as a decode or poll error (`screener poll failed venue=mexc …`), `online`
false, no block, and the loop retries at the next interval. Handling it as
a rate limit is a known gap (MASTER_PLAN T-075 note).

1. Confirm from the log body that it is 510 and not a schema change.
2. Raise `poll_interval_s` and lower `funding_calls_per_poll` (MEXC's
   per-symbol funding endpoint is the usual trigger at 20 req/2 s).
3. If it persists for more than a few polls, disable MEXC (restart-scoped)
   until the interval is comfortable; re-enable and watch `rate_limited`
   and `error` over a 30-minute soak with zero 429/418/403/510 before
   leaving it on unattended — the same soak Tier-2 venues must pass before
   they are enabled by default.

### 429 anywhere

Normal back-pressure; the gate blocks until `Retry-After` (or 60 s). One or
two per day on a venue with an UNVERIFIED limit (Bitget, Gate, HTX,
Coinbase Advanced Trade) is a signal to lower that venue's gate in code,
not to raise the poll interval globally. A sustained stream is the 418
procedure.

## 4. A venue is enabled but never online

- `error` = `venue: unknown venue` — the settings document names a venue
  this build has no collector for; remove it.
- `error` = decode error on the first poll — the venue changed a field;
  compare with the cited doc in the collector header and the research file;
  do not patch around a guess (symbols come from instrument lists, numbers
  are parsed from their exact JSON text, never floats).
- `spot_pairs` = 0 with `online` true — instrument list empty or every
  pair non-tradable; check the venue's status page.
- Coinbase: with 900+ products and 40 books per poll it takes ~23 polls
  for every product to get a fresh quote; many rows reading stale is
  expected, not a fault.

## 5. Acceptance reference

T-066: each venue returns normalised quotes for ≥ 90 % of its tradable
spot pairs within one poll; rate limits respected under a 30-minute soak
with zero 429/418. Tier-2 venues stay disabled by default until they pass
that soak on the deployment's IP.

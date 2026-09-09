---
name: venue-connector
description: 'Add or upgrade an exchange connector for the Scanner Suite / market-data layer (public REST/WS tickers, instruments, perps, funding, currency status), with rate gate, fixtures and conformance test. Use for "add exchange X" or "support more exchanges". Use when the request mentions: add exchange, support Kraken, KuCoin, HTX, connector, more venues.'
when_to_use: [add exchange, support Kraken, KuCoin, HTX, connector, more venues]
allowed-tools: Read Grep Glob Write Edit Bash Agent WebFetch WebSearch
argument-hint: '[task]'
---

# Venue connector workflow

Venue: $ARGUMENTS

1. Research first: official docs only, URL + access date per fact → docs/research/venues/<venue>.md (spot bulk ticker, instruments/status, perp tickers, funding rate/interval/history, currency/chain status public?, rate limits/bans, regular-tier fees).
2. Implement `internal/screener/venue/<venue>.go` satisfying the `Collector` interface (Spot(ctx) []Quote, Perps(ctx) []Perp, Instruments(ctx), Networks(ctx) — return `unknown` when key-gated), symbol → base/quote from the instrument list (never string-splitting guesses), decimal parsing, per-venue rate gate with Retry-After.
3. Fixtures: recorded real responses under testdata/<venue>/; unit tests parse them; the shared conformance test (`venue/conformance_test.go`) must pass: ≥ 90 % of tradable pairs normalised, no NaN/zero prices, ages set, symbol mapping round-trips.
4. Register in the venue registry with `Verified: true/false` and fee defaults; the console shows unverified venues as such.
5. 30-minute soak in dev with zero 429/418 before enabling by default.
Public data only; never sign requests; never read the vault's exchange group.

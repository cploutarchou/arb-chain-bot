# T-050 Pre-Work — OKX Connector Research & Verification Checklist

Status: PREPARED 2026-08-26 — verification PENDING. OKX documentation and
API hosts are egress-blocked from this dev environment (`www.okx.com` and
`my.okx.com` unreachable, checked 2026-08-26), so this document packages
the entire verification as a runbook to execute from a network-enabled
host. Companion docs: `exchanges.md` §OKX (research round 2026-08-26),
`final-platform-selection.md` §4 + §7, `market-data.md` §7 (the
Transport / Decoder / SequenceValidator / BookEngine split every
connector follows).

---

## 0. Gate and ground rules

T-050 stays **BLOCKED** until both hold (SKILL.md §79 sequencing):

1. **T-046** — the Binance §80 campaign has produced its verdict over
   real recorded feeds. If the strategy is not defensible on the first
   venue, a second venue is wasted work.
2. **T-047** — the Binance research debt is re-verified. The same doc
   sweep that clears T-047 should execute this checklist in one sitting.

Non-negotiables that carry over unchanged to any OKX work:

- **One venue per triangle.** OKX is an *alternative* single venue for
  triangular cycles, never a cross-exchange leg (SKILL.md scope).
- **Live trading stays permanently disabled.** `LiveExecutor` returns
  `ErrLiveTradingDisabled` on OKX exactly as on Binance. Recording and
  paper only.
- **Public data needs no credentials.** RECORD mode must run keyless. If
  keys are ever configured (demo/paper account reads), they carry
  Read-only permission — never Trade, never Withdraw — and live only in
  environment configuration (docs/security.md).
- **decimal.Decimal end to end.** All prices/quantities decoded from
  JSON strings without a float64 hop (`internal/exchange/binance/decoder.go`
  is the pattern to mirror).
- **Capabilities, not name checks.** Venue differences are expressed
  through `exchange.Capabilities` (`internal/exchange/capabilities.go`);
  no code may branch on the string "okx".

## 1. How to use this checklist

Work through sections A–I against **current official OKX API docs**
(v5). For every `[ ]` item record: the answer, the doc section or
endpoint that proves it, and the access date — either inline here or in
an updated `exchanges.md` §OKX. Items marked **E:** carry the *expected*
answer from the 2026-08-26 research round; expectations are hypotheses
to confirm, not facts — several were explicitly UNVERIFIED then
(`final-platform-selection.md` §7) and OKX has changed integrity
mechanics before (checksum deprecation). An expectation that fails
verification is a finding, not a nuisance: it usually invalidates a
design assumption in §12.

Primary sources (URLs as of the research round; follow redirects):

- REST/WS reference: `https://www.okx.com/docs-v5/en/`
- Fee schedule page: `https://www.okx.com/fees`
- System status: `https://www.okx.com/status`
- Announcements (API change log): the "API" category on the OKX support
  center — subscribe to it for the burn-in period.

## 2. Target integration surface

What T-050 ultimately builds, so every answer below lands somewhere
concrete. Mirrors the Binance package layout one-to-one:

| Artifact | Binance template | Filled by |
|---|---|---|
| `internal/exchange/okx/okx.go` — `ID`, hosts, `Capabilities`, limit consts | `binance/binance.go` | A, C, E |
| `internal/exchange/okx/transport.go` — RESTClient, WS URL + subscribe/ping | `binance/transport.go` | A, D, E |
| `internal/exchange/okx/decoder.go` — WS frame + REST book → `orderbook.DepthEvent` | `binance/decoder.go` | C, D |
| `internal/exchange/okx/validator.go` — strict prevSeqId→seqId `orderbook.SequenceValidator` | `binance/validator.go` | C |
| `internal/exchange/okx/feed.go` — in-band syncer + feed loop | `binance/feed.go` | C, E |
| `internal/exchange/okx/metadata.go` — instruments → `exchange.Market`/`InstrumentRules` | `binance/metadata.go` | B |
| Chaos/race/acceptance tests incl. seq-reset and keep-alive cases | `binance/chaos_test.go`, `race_test.go` | C, E |
| Venue-parameterized replay + campaign (§12.1–.2) | `marketdata/replay.go`, `cmd/campaign` | C |

## 3. Checklist A — identity, endpoints, environments

- [ ] REST base URL for public market data. **E:** `https://www.okx.com`
      (paths `/api/v5/...`). Confirm whether a separate market-data-only
      host exists (Binance has `data-api.binance.vision`; OKX may not).
- [ ] Public WS endpoint. **E:** `wss://ws.okx.com:8443/ws/v5/public`.
- [ ] Which channels live on the `/ws/v5/business` endpoint vs
      `/public`. **E:** plain `books` is public; some tbt/candle channels
      moved to business. The connector must know per channel.
- [ ] Regional split: is `my.okx.com` (or another domain) required for
      any jurisdiction we would run from, and are docs/limits identical?
- [ ] Demo-trading hosts. **E:** REST unchanged +
      `x-simulated-trading: 1` header; WS `wss://wspap.okx.com:8443`
      (see Checklist I).
- [ ] Any API-key requirement for public market data. **E:** none —
      RECORD mode must work keyless; fail the checklist item if any
      public book channel now demands login.

## 4. Checklist B — instruments & metadata mapping

Feeds `metadata.go` → `exchange.Market` + `exchange.InstrumentRules`
(`internal/exchange/instrument.go`). OKX is step-based
(`PrecisionStep`), like Binance.

- [ ] Endpoint + rate limit. **E:** `GET /api/v5/public/instruments?instType=SPOT`,
      limit ~20 req/2s/IP (verify current number for the consts block).
- [ ] `instId` format. **E:** dash-separated `BTC-USDT`. Decision §12.5:
      `exchange.Symbol` stays venue-native, so symbols carry the dash.
- [ ] `baseCcy` / `quoteCcy` provided directly (no symbol parsing).
      **E:** yes.
- [ ] `tickSz` → `PriceTick`, `lotSz` → `QtyStep`: confirm both are
      decimal strings and always positive for live spot instruments.
- [ ] `minSz` semantics for spot. **E:** minimum order size in *base*
      currency for limit orders → `MinQty`. Confirm whether market-order
      minimums differ and whether any quote-denominated minimum exists.
- [ ] Spot min-notional. **E:** none exists → `MinNotional` stays zero,
      which `InstrumentRules` documents as "no constraint". If OKX has
      since added one, map it — a missed min-notional produces
      simulated fills a real venue would reject.
- [ ] `maxLmtSz` / `maxMktSz` → `MaxQty`: which applies to our IOC-style
      taker legs? Record both; map the binding one.
- [ ] `state` values and mapping to `exchange.MarketStatus`. **E:**
      `live` → TRADING; `suspend` → HALTED; `preopen`/`test` → treat as
      not-tradeable (UNKNOWN or HALTED — decide and document).
- [ ] Rule-change discovery: is there an instruments-channel on WS to
      push metadata updates, or is periodic REST refresh the only
      mechanism? (Binance path uses periodic refresh; match it.)

## 5. Checklist C — order-book channel & integrity (load-bearing)

Feeds `decoder.go`, `validator.go`, `feed.go`, and the `Capabilities`
literal. This is where OKX genuinely differs from Binance: **in-band
snapshots** (`BookInitInBand`) and a **strict prevSeqId chain** with the
CRC32 checksum reportedly deprecated. Everything here must be pinned to
current docs before a line of connector code.

- [ ] Channel inventory and access tiers. **E:** `books` = 400 levels,
      snapshot then updates every ~100 ms, public; `books5` = 5-level
      full snapshots; `bbo-tbt` = top-of-book 10 ms; `books50-l2-tbt` /
      `books-l2-tbt` = 10 ms tbt, gated at VIP4/VIP5 (unusable for us).
      Confirm `books` remains the best ungated depth channel.
- [ ] Message envelope: subscribe `op`/`args` request, ack event, data
      frames `{arg, action, data}` with `action` ∈ `snapshot`/`update`.
      Confirm exact field names for the decoder.
- [ ] Level row arity and meaning. **E:** 4-element string arrays
      `[price, size, "0" (deprecated liquidation field), numOrders]`.
      Decoder consumes only price+size (as decimals), must tolerate the
      extra columns, and `size == 0` deletes the level.
- [ ] Sizes are absolute (replace), not deltas. **E:** absolute.
- [ ] **Sequence chain — exact rules** (the whole integrity story now):
  - [ ] Every `update` carries `seqId` and `prevSeqId`; rule:
        `prevSeqId` must equal the previous message's `seqId`, else the
        book is broken → resubscribe. Confirm this applies from the
        `snapshot` message's own `seqId` onward.
  - [ ] Keep-alive frames: when nothing changed, does OKX emit an update
        with `seqId == prevSeqId` (empty data)? **E:** yes — the
        validator must accept it as continuity, not a gap.
  - [ ] Maintenance reset: documented case where `seqId` restarts
        *lower* than before. Confirm the documented client behavior
        (resubscribe? new snapshot arrives unprompted?) and whether the
        reset is distinguishable from corruption. Drives §12.3.
  - [ ] Is `seqId` per-instrument or per-connection? **E:** per-instrument.
- [ ] **Checksum status.** **E (research round):** CRC32 checksum
      deprecated ~May 2026, field fixed to `0`. Verify against current
      docs: (a) still present-but-zero, absent, or *reinstated*; (b) if
      reinstated, capture the exact algorithm (top-25 interleaved
      `bid:ask` price:size string, signed int32) — it would upgrade
      `Capabilities.Integrity` and add a validator layer.
- [ ] Resync procedure. **E:** unsubscribe → resubscribe (fresh in-band
      snapshot); no REST splice needed. Confirm, and confirm an
      unsubscribe ack exists so the feed can sequence resyncs cleanly.
- [ ] Snapshot depth vs steady-state depth: does `books` ever truncate
      below 400 levels, and is truncation signaled?
- [ ] Ordering guarantee across instruments multiplexed on one
      connection (needed because the recorder preserves a single frame
      order per connection).
- [ ] Update cadence under load: is 100 ms fixed-interval batching or
      event-driven with a floor? (Affects latency modeling in the
      backtest, not correctness.)

## 6. Checklist D — REST cross-check endpoints & rate limits

Feeds `transport.go` consts and the drift-check decision (§12.4). REST
books are *not* needed for init on OKX — only as an out-of-protocol
oracle and for integration tests.

- [ ] `GET /api/v5/market/books?instId=&sz=` — max `sz` (**E:** 400) and
      current rate limit (**explicit research debt** —
      `final-platform-selection.md` §7 carried it UNVERIFIED).
- [ ] `books-full` variant — max depth (**E:** 5000) and limit; is it
      public?
- [ ] Response envelope `{code, msg, data}`: non-"0" `code` taxonomy for
      the errors the transport must classify (rate-limited vs invalid
      instrument vs maintenance). Capture the handful of codes worth
      switch-casing, and the HTTP status behavior (does OKX return 200
      with an error `code`?).
- [ ] IP-level throttling behavior: which HTTP status / code signals
      "back off", and is there a documented ban duration (Binance has
      418/Retry-After discipline; OKX equivalent?).

## 7. Checklist E — connection lifecycle & operational limits

Feeds the `okx.go` consts block (mirror of `MaxStreamsPerConn` etc. in
`binance/binance.go`) and the feed's reconnect logic.

- [ ] Connection attempts. **E:** 3/sec/IP. Also find any cap on
      concurrent connections per IP.
- [ ] Subscription budget. **E:** 480 sub/unsub/login requests per hour
      per connection. Engineering note to validate against: our six-ish
      recorded instruments batch into ONE subscribe op (multiple args),
      and each resync costs an unsub+sub pair, so the budget allows
      ~240 resync pairs/hour/connection — ample unless resync storms,
      which the health logic must treat as a hard fault anyway. Confirm
      whether the limit counts *requests* (ops) or *args* (channels) —
      the difference changes batching strategy.
- [ ] Max args per subscribe request / per connection.
- [ ] Idle timeout + keep-alive contract. **E:** server closes after
      30 s silence; client sends literal text `"ping"`, server answers
      literal `"pong"` (NOT WS protocol ping frames). The transport
      timer must key off *any* inbound traffic, and the reader must
      special-case the non-JSON `pong` before the decoder sees it.
- [ ] Forced periodic disconnect: does OKX cut long-lived connections on
      a schedule (Binance: 24 h)? Sets `Capabilities.ForcedDisconnect`;
      if undocumented, burn-in must measure it before trusting `false`.
- [ ] Subscribe/error event shapes (`event: subscribe` / `event: error`
      + `code`): capture codes for bad instId, rate-limited, and
      channel-requires-VIP so the feed fails loudly and correctly.
- [ ] Login op: NOT needed for public books (verify) — keyless RECORD
      depends on it.

## 8. Checklist F — fees

Feeds `fees.NewSchedule` wiring (`internal/app/engine.go`) and campaign
`-fee-*-bps` defaults for OKX recordings. Fee facts rot fastest —
re-pull at runtime where possible, never hardcode (policy:
`final-platform-selection.md` §7).

- [ ] Base spot tier (regular user Lv1) maker/taker. **E:** 8/10 bps.
      Confirm from the public fee schedule page — this seeds keyless
      campaign runs, so record the date.
- [ ] OKB-holding tier ladder (**explicit research debt**, UNVERIFIED in
      round 1): capture the ladder even though we conservatively assume
      base tier.
- [ ] Fee currency convention. **E:** charged in the *received* asset
      (buy → base, sell → quote) → `FeeInReceived`, matching the
      simulation's fee-in-kind accounting. Verify for BOTH sides; a
      SPENT- or QUOTE-convention surprise changes `fees.Schedule` use.
- [ ] Zero-fee / promo pairs program: does OKX run one (Binance's
      changes triangle economics materially)? List currently affected
      spot pairs if so.
- [ ] Authenticated fee endpoint `GET /api/v5/account/trade-fee`:
      confirm shape for the (later, keyed, read-only) runtime fee pull;
      keyless deployments stay on configured fees.
- [ ] Any taker/maker distinction quirks for IOC limit orders (our
      taker-style legs). **E:** IOC fills pay taker.

## 9. Checklist G — universe & triangle coverage

Sanity-checks that OKX can even host the strategy before building.

- [ ] Spot instrument count (**E:** ~550–1,000) and specifically the
      USDT- and USDC-quoted coverage for majors.
- [ ] Confirm existence + liquidity of the analog of our Binance
      recording set: `BTC-USDT`, `ETH-USDT`, `ETH-BTC`, `BTC-USDC`,
      `ETH-USDC`, and a `USDC-USDT` (or equivalent stable-stable)
      market — the two-triangle core `docker-compose.yml` records.
- [ ] Count triangles reachable from starting assets USDT/USDC (run
      `internal/graph` enumeration over the fetched instrument list —
      it is venue-agnostic already) and compare with Binance's count.
- [ ] Note any OKX-specific quote assets worth adding as intermediates.

## 10. Checklist H — clock, maintenance & status signals

- [ ] Server time endpoint. **E:** `GET /api/v5/public/time` (ms
      epoch). Feed the same skew measurement `binance.RESTClient.ServerTime`
      performs.
- [ ] Maintenance signaling: `status` WS channel and/or
      `GET /api/v5/system/status` — shape of scheduled/ongoing
      maintenance events. This matters more than on Binance because
      documented post-maintenance `seqId` resets (Checklist C) should be
      *anticipated* by the health logic, not discovered as corruption.
- [ ] Whether book channels emit anything during maintenance windows or
      simply go silent (interacts with the idle-timeout logic).

## 11. Checklist I — demo environment (paper parity, later)

Not needed for RECORD (public live data) — needed when OKX paper mode
wants venue-checked order semantics.

- [ ] Demo REST: same paths + `x-simulated-trading: 1` header; demo API
      keys created where? Read-only demo keys possible?
- [ ] Demo WS host. **E:** `wss://wspap.okx.com:8443`. Which channels
      exist there, and is demo *market data* real-time live-mirrored or
      synthetic? (Binance Demo Mode's realistic data is why it is our
      dev target; OKX's answer decides how much paper-parity testing
      moves to OKX demo vs stays replay-based.)
- [ ] Demo order placement supports limit-IOC on spot (needed to mirror
      the simulation's semantics if we ever validate against demo).

## 12. Design decisions to settle once A–I are answered

Each has a default; verification results can overturn it.

1. **Replayer venue-parameterization.** `internal/marketdata/replay.go`
   hardwires `binance.DecodeWSFrame` / `binance.DecodeRESTSnapshot` /
   `binance.Syncer` / `binance.ID`. Default: inject a small venue
   adapter (decoder + syncer factory + exchange ID) chosen by the
   recording's exchange; segment format (`segment.go`) is already
   venue-neutral raw frames + direction and needs no change. OKX
   recordings will be `DirWS`-only (in-band snapshots), so the adapter
   must not require `DirREST` frames.
2. **Campaign venue awareness.** `cmd/campaign/main.go` hardcodes
   `binance.ID` for `LoadMarkets`. Default: `-exchange` flag defaulting
   to binance; recording metadata should also record the venue so the
   flag can be inferred.
3. **Validator action vocabulary for seq resets.** The Binance validator
   maps chain breaks to `orderbook.Action` resync. OKX adds a *legal*
   discontinuity (post-maintenance lower reset). Default: treat any
   break as resync (always safe — costs one resubscribe); add a
   distinct metric label so legal resets don't inflate the
   gap-corruption alarm. Overturn only if docs give a reliable way to
   distinguish.
4. **`RequiresRESTDriftCheck` for OKX.** With the checksum gone, the
   strict chain still detects drops (unlike Bybit's blind spot), so the
   default is `false` — but schedule the REST cross-check anyway during
   burn-in to empirically confirm keep-alive/reset edge cases mask
   nothing, then decide permanently.
5. **Symbol normalization.** Default: `exchange.Symbol` stays
   venue-native (`BTC-USDT` with dash), consistent with `types.go`;
   config (`ARB_SYMBOLS`) is documented as venue-native per deployment.
   Rejected alternative: normalizing to a dashless form, which would
   re-introduce symbol parsing and collide with `baseCcy`/`quoteCcy`
   being authoritative.
6. **Capabilities literal (to be confirmed, not assumed):**

   ```go
   var Capabilities = exchange.Capabilities{
           BookInit:               exchange.BookInitInBand,
           Integrity:              exchange.IntegrityUpdateChain, // strict prevSeqId
           RequiresRESTDriftCheck: false,                         // §12.4 burn-in first
           FeeConvention:          exchange.FeeInReceived,        // Checklist F
           HasSpotTestEnv:         true,                          // demo trading
           ForcedDisconnect:       false,                         // Checklist E — verify!
   }
   ```

## 13. Exit criteria — when T-050's research phase is DONE

- Every checkbox in A–I answered with a source (official doc section +
  access date) or explicitly demoted to a runtime-verified assumption
  with a named burn-in test.
- `exchanges.md` §OKX updated in place; its UNVERIFIED markers and the
  §7 research-debt items (OKB ladder, books rate limit) cleared.
- Each §12 decision recorded with its final answer.
- MASTER_PLAN T-050 acceptance restated from the verified facts (the
  current wording — strict prevSeqId validator, demo-env support,
  capability descriptor — plus whatever verification overturned).
- Only then does connector implementation start, and only if the T-046
  campaign verdict justifies a second venue at all.

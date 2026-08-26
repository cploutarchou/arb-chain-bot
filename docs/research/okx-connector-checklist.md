# T-050 Pre-Work — OKX Connector Research & Verification Checklist

Status: **EXECUTED 2026-08-26** from a network-enabled host (T-047). The
item-by-item answers, sources and access dates are in §14; the §12
decisions are settled in §14.10; the §13 exit criteria are assessed in
§14.11. Sections 3–11 are kept as the protocol (their `E:` expectations
are what §14 tests); `[x]` means "answered in §14", where the status is
either verified (V) or demoted to a named burn-in check (D). Originally PREPARED 2026-08-26 when OKX
hosts were egress-blocked from the dev sandbox. Companion docs: `exchanges.md` §OKX (research round 2026-08-26),
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

- [x] REST base URL for public market data. **E:** `https://www.okx.com`
      (paths `/api/v5/...`). Confirm whether a separate market-data-only
      host exists (Binance has `data-api.binance.vision`; OKX may not).
- [x] Public WS endpoint. **E:** `wss://ws.okx.com:8443/ws/v5/public`.
- [x] Which channels live on the `/ws/v5/business` endpoint vs
      `/public`. **E:** plain `books` is public; some tbt/candle channels
      moved to business. The connector must know per channel.
- [x] Regional split: is `my.okx.com` (or another domain) required for
      any jurisdiction we would run from, and are docs/limits identical?
- [x] Demo-trading hosts. **E:** REST unchanged +
      `x-simulated-trading: 1` header; WS `wss://wspap.okx.com:8443`
      (see Checklist I).
- [x] Any API-key requirement for public market data. **E:** none —
      RECORD mode must work keyless; fail the checklist item if any
      public book channel now demands login.

## 4. Checklist B — instruments & metadata mapping

Feeds `metadata.go` → `exchange.Market` + `exchange.InstrumentRules`
(`internal/exchange/instrument.go`). OKX is step-based
(`PrecisionStep`), like Binance.

- [x] Endpoint + rate limit. **E:** `GET /api/v5/public/instruments?instType=SPOT`,
      limit ~20 req/2s/IP (verify current number for the consts block).
- [x] `instId` format. **E:** dash-separated `BTC-USDT`. Decision §12.5:
      `exchange.Symbol` stays venue-native, so symbols carry the dash.
- [x] `baseCcy` / `quoteCcy` provided directly (no symbol parsing).
      **E:** yes.
- [x] `tickSz` → `PriceTick`, `lotSz` → `QtyStep`: confirm both are
      decimal strings and always positive for live spot instruments.
- [x] `minSz` semantics for spot. **E:** minimum order size in *base*
      currency for limit orders → `MinQty`. Confirm whether market-order
      minimums differ and whether any quote-denominated minimum exists.
- [x] Spot min-notional. **E:** none exists → `MinNotional` stays zero,
      which `InstrumentRules` documents as "no constraint". If OKX has
      since added one, map it — a missed min-notional produces
      simulated fills a real venue would reject.
- [x] `maxLmtSz` / `maxMktSz` → `MaxQty`: which applies to our IOC-style
      taker legs? Record both; map the binding one.
- [x] `state` values and mapping to `exchange.MarketStatus`. **E:**
      `live` → TRADING; `suspend` → HALTED; `preopen`/`test` → treat as
      not-tradeable (UNKNOWN or HALTED — decide and document).
- [x] Rule-change discovery: is there an instruments-channel on WS to
      push metadata updates, or is periodic REST refresh the only
      mechanism? (Binance path uses periodic refresh; match it.)

## 5. Checklist C — order-book channel & integrity (load-bearing)

Feeds `decoder.go`, `validator.go`, `feed.go`, and the `Capabilities`
literal. This is where OKX genuinely differs from Binance: **in-band
snapshots** (`BookInitInBand`) and a **strict prevSeqId chain** with the
CRC32 checksum reportedly deprecated. Everything here must be pinned to
current docs before a line of connector code.

- [x] Channel inventory and access tiers. **E:** `books` = 400 levels,
      snapshot then updates every ~100 ms, public; `books5` = 5-level
      full snapshots; `bbo-tbt` = top-of-book 10 ms; `books50-l2-tbt` /
      `books-l2-tbt` = 10 ms tbt, gated at VIP4/VIP5 (unusable for us).
      Confirm `books` remains the best ungated depth channel.
- [x] Message envelope: subscribe `op`/`args` request, ack event, data
      frames `{arg, action, data}` with `action` ∈ `snapshot`/`update`.
      Confirm exact field names for the decoder.
- [x] Level row arity and meaning. **E:** 4-element string arrays
      `[price, size, "0" (deprecated liquidation field), numOrders]`.
      Decoder consumes only price+size (as decimals), must tolerate the
      extra columns, and `size == 0` deletes the level.
- [x] Sizes are absolute (replace), not deltas. **E:** absolute.
- [x] **Sequence chain — exact rules** (the whole integrity story now):
  - [x] Every `update` carries `seqId` and `prevSeqId`; rule:
        `prevSeqId` must equal the previous message's `seqId`, else the
        book is broken → resubscribe. Confirm this applies from the
        `snapshot` message's own `seqId` onward.
  - [x] Keep-alive frames: when nothing changed, does OKX emit an update
        with `seqId == prevSeqId` (empty data)? **E:** yes — the
        validator must accept it as continuity, not a gap.
  - [x] Maintenance reset: documented case where `seqId` restarts
        *lower* than before. Confirm the documented client behavior
        (resubscribe? new snapshot arrives unprompted?) and whether the
        reset is distinguishable from corruption. Drives §12.3.
  - [x] Is `seqId` per-instrument or per-connection? **E:** per-instrument.
- [x] **Checksum status.** **E (research round):** CRC32 checksum
      deprecated ~May 2026, field fixed to `0`. Verify against current
      docs: (a) still present-but-zero, absent, or *reinstated*; (b) if
      reinstated, capture the exact algorithm (top-25 interleaved
      `bid:ask` price:size string, signed int32) — it would upgrade
      `Capabilities.Integrity` and add a validator layer.
- [x] Resync procedure. **E:** unsubscribe → resubscribe (fresh in-band
      snapshot); no REST splice needed. Confirm, and confirm an
      unsubscribe ack exists so the feed can sequence resyncs cleanly.
- [x] Snapshot depth vs steady-state depth: does `books` ever truncate
      below 400 levels, and is truncation signaled?
- [x] Ordering guarantee across instruments multiplexed on one
      connection (needed because the recorder preserves a single frame
      order per connection).
- [x] Update cadence under load: is 100 ms fixed-interval batching or
      event-driven with a floor? (Affects latency modeling in the
      backtest, not correctness.)

## 6. Checklist D — REST cross-check endpoints & rate limits

Feeds `transport.go` consts and the drift-check decision (§12.4). REST
books are *not* needed for init on OKX — only as an out-of-protocol
oracle and for integration tests.

- [x] `GET /api/v5/market/books?instId=&sz=` — max `sz` (**E:** 400) and
      current rate limit (**explicit research debt** —
      `final-platform-selection.md` §7 carried it UNVERIFIED).
- [x] `books-full` variant — max depth (**E:** 5000) and limit; is it
      public?
- [x] Response envelope `{code, msg, data}`: non-"0" `code` taxonomy for
      the errors the transport must classify (rate-limited vs invalid
      instrument vs maintenance). Capture the handful of codes worth
      switch-casing, and the HTTP status behavior (does OKX return 200
      with an error `code`?).
- [x] IP-level throttling behavior: which HTTP status / code signals
      "back off", and is there a documented ban duration (Binance has
      418/Retry-After discipline; OKX equivalent?).

## 7. Checklist E — connection lifecycle & operational limits

Feeds the `okx.go` consts block (mirror of `MaxStreamsPerConn` etc. in
`binance/binance.go`) and the feed's reconnect logic.

- [x] Connection attempts. **E:** 3/sec/IP. Also find any cap on
      concurrent connections per IP.
- [x] Subscription budget. **E:** 480 sub/unsub/login requests per hour
      per connection. Engineering note to validate against: our six-ish
      recorded instruments batch into ONE subscribe op (multiple args),
      and each resync costs an unsub+sub pair, so the budget allows
      ~240 resync pairs/hour/connection — ample unless resync storms,
      which the health logic must treat as a hard fault anyway. Confirm
      whether the limit counts *requests* (ops) or *args* (channels) —
      the difference changes batching strategy.
- [x] Max args per subscribe request / per connection.
- [x] Idle timeout + keep-alive contract. **E:** server closes after
      30 s silence; client sends literal text `"ping"`, server answers
      literal `"pong"` (NOT WS protocol ping frames). The transport
      timer must key off *any* inbound traffic, and the reader must
      special-case the non-JSON `pong` before the decoder sees it.
- [x] Forced periodic disconnect: does OKX cut long-lived connections on
      a schedule (Binance: 24 h)? Sets `Capabilities.ForcedDisconnect`;
      if undocumented, burn-in must measure it before trusting `false`.
- [x] Subscribe/error event shapes (`event: subscribe` / `event: error`
      + `code`): capture codes for bad instId, rate-limited, and
      channel-requires-VIP so the feed fails loudly and correctly.
- [x] Login op: NOT needed for public books (verify) — keyless RECORD
      depends on it.

## 8. Checklist F — fees

Feeds `fees.NewSchedule` wiring (`internal/app/engine.go`) and campaign
`-fee-*-bps` defaults for OKX recordings. Fee facts rot fastest —
re-pull at runtime where possible, never hardcode (policy:
`final-platform-selection.md` §7).

- [x] Base spot tier (regular user Lv1) maker/taker. **E:** 8/10 bps.
      Confirm from the public fee schedule page — this seeds keyless
      campaign runs, so record the date.
- [x] OKB-holding tier ladder (**explicit research debt**, UNVERIFIED in
      round 1): capture the ladder even though we conservatively assume
      base tier.
- [x] Fee currency convention. **E:** charged in the *received* asset
      (buy → base, sell → quote) → `FeeInReceived`, matching the
      simulation's fee-in-kind accounting. Verify for BOTH sides; a
      SPENT- or QUOTE-convention surprise changes `fees.Schedule` use.
- [x] Zero-fee / promo pairs program: does OKX run one (Binance's
      changes triangle economics materially)? List currently affected
      spot pairs if so.
- [x] Authenticated fee endpoint `GET /api/v5/account/trade-fee`:
      confirm shape for the (later, keyed, read-only) runtime fee pull;
      keyless deployments stay on configured fees.
- [x] Any taker/maker distinction quirks for IOC limit orders (our
      taker-style legs). **E:** IOC fills pay taker.

## 9. Checklist G — universe & triangle coverage

Sanity-checks that OKX can even host the strategy before building.

- [x] Spot instrument count (**E:** ~550–1,000) and specifically the
      USDT- and USDC-quoted coverage for majors.
- [x] Confirm existence + liquidity of the analog of our Binance
      recording set: `BTC-USDT`, `ETH-USDT`, `ETH-BTC`, `BTC-USDC`,
      `ETH-USDC`, and a `USDC-USDT` (or equivalent stable-stable)
      market — the two-triangle core `docker-compose.yml` records.
- [x] Count triangles reachable from starting assets USDT/USDC (run
      `internal/graph` enumeration over the fetched instrument list —
      it is venue-agnostic already) and compare with Binance's count.
- [x] Note any OKX-specific quote assets worth adding as intermediates.

## 10. Checklist H — clock, maintenance & status signals

- [x] Server time endpoint. **E:** `GET /api/v5/public/time` (ms
      epoch). Feed the same skew measurement `binance.RESTClient.ServerTime`
      performs.
- [x] Maintenance signaling: `status` WS channel and/or
      `GET /api/v5/system/status` — shape of scheduled/ongoing
      maintenance events. This matters more than on Binance because
      documented post-maintenance `seqId` resets (Checklist C) should be
      *anticipated* by the health logic, not discovered as corruption.
- [x] Whether book channels emit anything during maintenance windows or
      simply go silent (interacts with the idle-timeout logic).

## 11. Checklist I — demo environment (paper parity, later)

Not needed for RECORD (public live data) — needed when OKX paper mode
wants venue-checked order semantics.

- [x] Demo REST: same paths + `x-simulated-trading: 1` header; demo API
      keys created where? Read-only demo keys possible?
- [x] Demo WS host. **E:** `wss://wspap.okx.com:8443`. Which channels
      exist there, and is demo *market data* real-time live-mirrored or
      synthetic? (Binance Demo Mode's realistic data is why it is our
      dev target; OKX's answer decides how much paper-parity testing
      moves to OKX demo vs stays replay-based.)
- [x] Demo order placement supports limit-IOC on spot (needed to mirror
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


---

## 14. Verification record — executed 2026-08-26

Method: `https://www.okx.com/docs-v5/en/` fetched raw (5.2 MB, all
sections, including "WS / Order book channel", "Get order book", "Get full
order book", "Get instruments", WebSocket overview, error codes, demo
trading); OKX help centre (`/help/trading-fee-rules-faq`,
`okx-order-book-channels-checksum-field-deprecation`), changelog
(`docs-v5/log_en`), the fee page's server-side JSON (`okx.com/fees`,
`feeDataInfo`), keyless public REST calls (`/public/instruments`,
`/public/time`, `/market/books`), and a **live 100 s capture** of
`wss://ws.okx.com:8443/ws/v5/public` with one three-arg `books` subscribe
(BTC-USDT, USDC-USDT, ETH-BTC). Legend: **V** = verified against the
primary source named; **V-live** = additionally observed in the capture;
**D** = demoted to a runtime-verified assumption with the named burn-in
check. All accessed 2026-08-26.

### 14.1 Checklist A — identity, endpoints, environments

| Item | Answer | Status | Source |
|---|---|---|---|
| REST base | `https://www.okx.com/api/v5/...`; no separate market-data-only host is documented | V (absence: D — burn-in: none needed; production host is the documented one) | docs-v5 REST overview; live `GET /api/v5/public/time` |
| Public WS | `wss://ws.okx.com:8443/ws/v5/public` | V-live | docs-v5 "WebSocket / Connect" |
| `/business` vs `/public` | `books`, `books5`, `bbo-tbt`, `books-l2-tbt`, `books50-l2-tbt`, `books-rpi`, `instruments` are documented under URL path `/ws/v5/public`; `/business` carries candle/algo/grid channels | V | docs-v5 "WS / Order book channel" (URL Path), "Instruments channel" |
| Regional split | `my.okx.com` serves the same v5 docs tree; the fee page is regionalised (this host got the EUR variant) — limits identical in the docs, fee tables may differ | V (docs) / D for fee parity — burn-in: compare `/account/trade-fee` from the deployment region | `my.okx.com/docs-v5/en/`; `okx.com/fees` JSON |
| Demo hosts | REST `https://openapi.okx.com` + header `x-simulated-trading: 1`; WS `wss://wspap.okx.com:8443/ws/v5/{public,private,business}` | V | docs-v5 "Demo Trading Services" |
| API key for public data | none — `/public/instruments`, `/public/time`, `/market/books` and the `books` WS channel all served keyless | V-live | live calls |

### 14.2 Checklist B — instruments & metadata

| Item | Answer | Status | Source |
|---|---|---|---|
| Endpoint + limit | `GET /api/v5/public/instruments?instType=SPOT`, **20 req/2 s, rule IP + instType** | V | docs-v5 "Get instruments" |
| `instId` format | dash-separated (`BTC-USDT`, `ETH-BTC`, `USDC-USDT`) | V-live | live instruments |
| `baseCcy`/`quoteCcy` | present on every instrument | V-live | live instruments (1,381 rows) |
| `tickSz`/`lotSz` | decimal strings, positive (e.g. BTC-USDT `0.1` / `0.00000001`; ETH-BTC `0.00001` / `0.000001`) | V-live | live instruments |
| `minSz` | base-currency minimum order size (string) | V | docs-v5 instruments fields; live |
| Spot min-notional | **none** — no such field exists; `MinNotional` stays 0 | V-live | live instruments field list |
| `maxLmtSz`/`maxMktSz` | both present **plus** quote-denominated `maxLmtAmt`/`maxMktAmt`; an IOC limit leg binds on `maxLmtSz` and `maxLmtAmt` (whichever binds first); price bands `initPxLmtPct`/`maxPxLmtPct`/`floatPxLmtPct` can also reject aggressive IOC prices | V-live (OVERTURNED: 4 caps + bands, not 2) | live instruments; docs-v5 field table |
| `state` | documented values `live`, `suspend`, `preopen`, `test`; spot flips `preopen → live` at `listTime`. Mapping: `live` → TRADING, `suspend` → HALTED, `preopen`/`test` → HALTED (not tradeable). All 1,381 live today | V | docs-v5 instruments `state` |
| Rule-change discovery | WS `instruments` channel (`/ws/v5/public`, `instType: SPOT`) pushes on state change, `tickSz`/`minSz`/`maxMktSz` change, `listTime` change — periodic REST refresh remains the fallback | V | docs-v5 "Instruments channel" |

### 14.3 Checklist C — order-book channel & integrity

| Item | Answer | Status | Source |
|---|---|---|---|
| Channel inventory | `books` 400 lvl/100 ms public; `books5` 5-lvl snapshots; `bbo-tbt` 10 ms; `books-l2-tbt`/`books50-l2-tbt` 10 ms VIP4+ (`64003` otherwise); `books-rpi` (new, 400 lvl/100 ms, rows `[price,totalQty,nonRpiQty,count]`, replaces `books-elp`). `books` remains the best ungated depth channel; `books-l2-tbt` and `books`/`books50-l2-tbt` cannot be co-subscribed for one symbol | V-live | docs-v5 "WS / Order book channel" |
| Envelope | subscribe `{op:"subscribe", args:[{channel,instId},…]}`; ack `{event:"subscribe", arg, connId}`; data `{arg, action:"snapshot"|"update", data:[{asks,bids,ts,checksum,prevSeqId,seqId}]}` | V-live | docs-v5 + capture |
| Row arity | 4 strings `[price, size, "0" (deprecated, always "0"), numOrders]`; snapshot depth 400/400 observed | V-live | docs-v5 + capture |
| Sizes absolute | "quantity at the price … in base currency for Spot"; `"0"` removes the level | V | docs-v5 |
| Chain rule | "The prevSeqId in the new message matches with seqId of the previous message"; snapshot `prevSeqId = -1`; applies from the snapshot's `seqId` onward. Capture: 0 breaks across all frames | V-live | docs-v5 "Sequence ID" |
| Keep-alive | after ~60 s without changes an update with `'asks': [], 'bids': []` and `seqId == prevSeqId` (== last sent) | V (not observed live — all three symbols stayed active; burn-in: assert acceptance during a quiet window) | docs-v5 "Exceptions 1" |
| Maintenance reset | "users will receive an incremental message with seqId smaller than prevSeqId. However, subsequent messages will follow the regular sequencing rule" — worked example `prevSeqId=15, seqId=3`. It IS distinguishable from a gap (a gap has `prevSeqId != last seqId`; a reset has `prevSeqId == last seqId` and `seqId < prevSeqId`) | V (D for observation across a real window — burn-in: capture through a scheduled maintenance from okx.com/status) | docs-v5 "Exceptions 2" |
| Scope of `seqId` | per instId; identical sequence on every connection to the channel | V | docs-v5 |
| Checksum | deprecated; field remains in `books`, `books-l2-tbt`, `books50-l2-tbt` fixed to `0` — production since 2026-06-23 (demo 2026-06-02); capture: every frame `checksum: 0`; not reinstated | V-live | help centre deprecation notice; docs-v5 field table |
| Resync | unsubscribe → resubscribe → fresh in-band snapshot (`prevSeqId = -1`); no REST splice | V | docs-v5 |
| Depth truncation | snapshot pushes 400 levels; incremental frames carry only changed levels (no truncation signal; client truncates nothing) | V-live | capture |
| Ordering | per-connection push order across book channels for one symbol is fixed (`bbo-tbt → books-l2-tbt → books50-l2-tbt → books → books-elp → books-rpi → books5`); frames of different instIds are interleaved in arrival order (capture) | V-live | docs-v5 + capture |
| Cadence | "Order book data is created once every 10ms internally"; `books` pushes every 100 ms when changed; measured BTC-USDT spacing min/median/max 100/100/300 ms; ~1 s during call auction | V-live | docs-v5 tips page + capture |
| WS number types | `seqId`/`prevSeqId` JSON integers (int64 range), `ts` string, `checksum` integer; REST `/market/books` rows carry `seqId` and **no `checksum` key** | V-live | capture + live REST |

### 14.4 Checklist D — REST cross-check & limits

| Item | Answer | Status | Source |
|---|---|---|---|
| `market/books` | `sz` ≤ 400 (default 1); server cache updated every 50 ms; **40 req/2 s, rule IP** | V | docs-v5 "Get order book" |
| `books-full` | `sz` ≤ 5000; updated once a second; **10 req/2 s, rule IP**; public | V | docs-v5 "Get full order book" |
| Envelope / errors | `{code:"0", msg:"", data:[…]}`; `51001` instrument does not exist (HTTP 200); `50011` rate limit (HTTP 200 **or** 429); `50013` systems busy (429); `60012` invalid request, `60013` invalid args, `60014` requests too frequent (WS), `64003` tier too low for channel, `64008` upgrade-disconnect notice | V | docs-v5 error codes |
| Throttling / ban | "Rate limit reached" `50011` per endpoint window; no documented ban duration | V (D: back-off constants — burn-in: measure `50011` onset) | docs-v5 rate-limit overview |

### 14.5 Checklist E — connection lifecycle & limits

| Item | Answer | Status | Source |
|---|---|---|---|
| Connection attempts | 3 requests/s per IP | V | docs-v5 "Connect" |
| Concurrent connections | the 30-per-channel-per-sub-account cap lists only private channels (orders, account, positions, …); no public cap documented | V | docs-v5 "Connection count limit" |
| Subscription budget | 480 subscribe/unsubscribe/login **requests** per hour per connection; a multi-arg request counts once (capture: one request → three acks) | V-live | docs-v5 "Request limit" |
| Max args | not numerically documented; docs recommend < 30 depth channels per connection when using 50/400-level channels; `60014` on excess | D — burn-in: probe ack/`60014` with 30, 60, 120 args | docs-v5 |
| Idle / keep-alive | disconnect after 30 s without pushes; client sends text `ping`, expects `pong`; timer N < 30 s reset on any inbound message | V | docs-v5 "Connect" |
| Forced disconnect | no fixed schedule; service upgrades send `{"event":"notice","code":"64008"}` 60 s ahead on `/public`, `/private` (and `/business` since 2026-06-11) → `ForcedDisconnect: false` with a mandatory 64008 handler | V (nuance overturned) | docs-v5 "Notification" + changelog |
| Event shapes | `{"event":"subscribe","arg":{…},"connId":"…"}` observed; `{"event":"error","code":"60012","msg":…,"connId":…}` documented | V-live | capture + docs-v5 |
| Login | not needed for `books` (keyless capture succeeded) | V-live | capture |

### 14.6 Checklist F — fees

| Item | Answer | Status | Source |
|---|---|---|---|
| Base tier | Regular: maker 0.08 % / taker 0.10 % | V | `okx.com/fees` `feeDataInfo` (Spot/Standard); trading-fee-rules FAQ |
| OKB ladder | **no OKB tier on the current schedule** — tiers by 30-day volume OR assets on platform: VIP1 0.0675/0.08 %, VIP2 0.06/0.07 %, VIP3 0.055/0.065 %, VIP4 0.03/0.045 %, VIP5 0.025/0.035 %, VIP6 0/0.03 %, VIP7 −0.002/0.025 %, VIP8 −0.005/0.02 %, VIP9 −0.005/0.015 % (EUR regional page; `showOkb: false`, `lowerOkbVolume: -1`) | V (regional variants: D — burn-in: `/account/trade-fee` from the deployment region) | `okx.com/fees` JSON |
| Fee currency | received asset: FAQ example buy 1 BTC → fee 0.001 BTC, receive 0.999 BTC; sell 1 BTC for 20,000 USDT → fee 16 USDT → `FeeInReceived` | V | trading-fee-rules FAQ |
| Promo / zero-fee pairs | none found on fee page or docs | V (absence) | `okx.com/fees` |
| `/account/trade-fee` | 5 req/2 s per UserID; returns `level`, `maker`, `taker` (negative-signed strings, e.g. `"-0.001"`), `feeGroup`, `ruleType` | V | docs-v5 "Get fee rates" |
| IOC pays taker | `ioc` = "immediately execute … at the order price, cancel the remaining"; taker by definition of the FAQ ("became a taker of this trade") | V | docs-v5 place order; FAQ |

### 14.7 Checklist G — universe

| Item | Answer | Status | Source |
|---|---|---|---|
| Instrument count | **1,381** spot instruments, all `live`; quotes: USDT 395, USD 291, EUR 267, USDC 265, TRY 128, BRL 11, AUD 9, AED 5, SGD 3, BTC 3 | V-live | live instruments |
| Recording set | `BTC-USDT`, `ETH-USDT`, `ETH-BTC`, `BTC-USDC`, `ETH-USDC`, `USDC-USDT` all live | V-live | live instruments |
| Triangle count | not enumerated (needs the venue adapter of §12.1 to feed `internal/graph`); Binance comparison deferred to T-050 | D — burn-in: run enumeration on first OKX metadata load | — |
| Extra quote assets | `BTC-USDC`/`ETH-USDC` carry `tradeQuoteCcyList: [USDG, USD, USDC, RLUSD]` (unified USD book, changelog 2025-08-20); no `BTC-USDG` instId exists — treat USD-family quotes as one book, not extra triangles | V-live | live instruments + changelog |

### 14.8 Checklist H — clock, maintenance, status

| Item | Answer | Status | Source |
|---|---|---|---|
| Server time | `GET /api/v5/public/time` → `data[0].ts` ms string | V-live | live call |
| Maintenance signalling | `GET /api/v5/system/status[?state=]` and WS `status` channel exist; `okx.com/status` lists scheduled windows (e.g. copy-trading maintenance 2026-08-25 16:00–16:20 UTC+8) | V (shape of events: D — burn-in: subscribe `status` during burn-in) | docs-v5 "Get system status" / "Status channel"; okx.com/status |
| Behaviour during maintenance | not documented beyond the seqId-reset rule | D — burn-in: capture across a window | — |

### 14.9 Checklist I — demo environment

| Item | Answer | Status | Source |
|---|---|---|---|
| Demo REST | `https://openapi.okx.com` + `x-simulated-trading: 1`; demo keys created under Trade → Demo Trading → Personal Center → Demo Trading API (permissions selectable; read-only possible) | V | docs-v5 "Demo Trading Services" |
| Demo WS | `wss://wspap.okx.com:8443/ws/v5/public|private|business` | V (data realism: D — burn-in: diff demo vs production `books` for BTC-USDT) | docs-v5 |
| Demo limit-IOC | `ordType: ioc` is part of the v5 order API, which demo serves except deposit/withdraw/purchase | V (execution semantics: D — burn-in when OKX paper parity is attempted) | docs-v5 |

### 14.10 §12 decisions — settled

1. Replayer venue-parameterization — **keep default** (inject a venue
   adapter; OKX recordings are `DirWS`-only). Reinforced: REST and WS
   book structs differ (`checksum` absent vs present-but-zero).
2. Campaign venue awareness — **keep default** (`-exchange` flag; record
   the venue in recording metadata).
3. Validator vocabulary — **keep default with a refinement**: (a)
   `prevSeqId == last seqId && seqId == prevSeqId && empty` → keep-alive,
   continuity; (b) `prevSeqId == last seqId && seqId < prevSeqId` →
   *legal reset*, accept and re-anchor, count under a distinct metric
   label; (c) `prevSeqId != last seqId` → gap → resync. The docs make
   (b) distinguishable from (c), so the "always resync" fallback is
   replaced by accept-and-label; the burn-in must observe one real reset
   before this is trusted.
4. `RequiresRESTDriftCheck` — **keep `false`**; schedule the REST
   cross-check during burn-in (books 40 req/2 s makes it cheap).
5. Symbol normalization — **keep default** (venue-native dashed
   `instId`; `baseCcy`/`quoteCcy` authoritative).
6. Capabilities literal — confirmed as drafted, with one comment
   required on `ForcedDisconnect: false`: "no fixed cycle; service
   upgrades are announced by notice 64008 and must trigger a pre-emptive
   reconnect". Add `FeeInReceived` (verified), `HasSpotTestEnv: true`
   (verified), `Integrity: IntegrityUpdateChain` (checksum gone).

### 14.11 §13 exit criteria — assessment

- Every A–I item is answered above with a source and date, or demoted to
  a named burn-in check (D). ✔
- `exchanges.md` §OKX rewritten from the verified facts; its UNVERIFIED
  markers and the §7 items (OKB ladder → no OKB tier; books rate limit →
  40 req/2 s) are cleared. ✔
- §12 decisions recorded (§14.10). ✔
- MASTER_PLAN T-050 acceptance restated from the verified facts (see
  MASTER_PLAN). ✔
- Connector implementation still waits on the T-046 campaign verdict
  (§0 gate 1). T-050 remains BLOCKED on that gate only.

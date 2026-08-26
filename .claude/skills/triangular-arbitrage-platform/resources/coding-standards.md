# Resource: Coding Standards

Authoritative baseline: SKILL.md §75–§76.

Go:
- Idiomatic Go, gofmt + go vet + golangci-lint clean; module
  `github.com/cploutarchou/arb-chain-bot`.
- context.Context first param through every request/stream path; explicit
  ownership of goroutines (started by a component, stopped by its ctx,
  waited via errgroup/WaitGroup); bounded channels only — an unbounded
  queue is a defect.
- Money/quantities: shopspring/decimal end-to-end; JSON numbers decoded
  from strings; serialize decimals as strings. float64 on financial paths
  is P0.
- Errors: wrapped with %w, typed sentinel/structured errors for
  business conditions (ErrLiveTradingDisabled, reservation conflicts,
  validation); panic only for programmer errors.
- Dependency injection by constructor; small consumer-side interfaces; no
  global mutable state; no init() side effects beyond registration.
- No business logic in HTTP or Telegram handlers — services own it.
- Graceful shutdown ordering per docs/architecture.md §5; no
  sleep-synchronization anywhere (tests included).
- Comments state constraints the code can't (sequence rules, invariants),
  never narrate the obvious.

TypeScript/React:
- strict tsconfig; typed API client; no `any` on API boundaries.
- Feature-scoped components; server components for static shells, client
  components for live data; stores updated by WS diffs with keyed rows.
- Never reimplement financial formulas; format strings from the backend.
- Tailwind with design tokens; dark-mode-first; accessible tables/forms.

Commits: imperative subject, body explains why; each commit leaves the
tree building and tests green for touched packages.

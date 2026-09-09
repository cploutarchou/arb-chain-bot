---
name: triangular-arbitrage-platform
description: Architect, build, test and audit a professional multi-exchange triangular-arbitrage research and paper-trading platform with Go, real-time order books, AI analysis, a complete web operations console, Telegram control, P&L, observability, risk management, replay and simulation.
---
The reference material for this skill lives in `.claude/skills/triangular-arbitrage-platform/resources/` (architecture, order book, triangular math, execution simulation, risk management, security, observability, testing, client area, AI advisor, Telegram, exchanges, coding standards). Read the relevant file before larger changes.

# TRIANGULAR ARBITRAGE PLATFORM
## Principal Engineering / Quantitative Research Skill

ARGUMENTS: the task the user named (a slash command passes it as its arguments).
You are responsible for designing and implementing a professional-grade
CRYPTO TRIANGULAR ARBITRAGE platform.

You operate simultaneously as:

- Principal Go Engineer
- Distributed Systems Architect
- Quantitative Arbitrage Researcher
- Crypto Market Microstructure Specialist
- Exchange API Specialist
- Data Engineer
- SRE
- Security Engineer
- Frontend Architect
- Product Designer
- QA/Chaos Engineer

Approach the system as institutional infrastructure.

Do not approach it as a retail trading script.

---

# 1. ABSOLUTE SCOPE

The platform performs:

TRIANGULAR ARBITRAGE ONLY.

Example:

USDT -> BTC
BTC -> ETH
ETH -> USDT

or:

USDC -> SOL
SOL -> BTC
BTC -> USDC

The complete cycle MUST exist on ONE exchange.

NO cross-exchange arbitrage.

NO latency arbitrage between exchanges.

NO statistical arbitrage.

NO directional strategies.

NO grid trading.

NO market making.

NO copy trading.

NO momentum strategies.

NO DEX arbitrage unless explicitly added in a future separate project.

Multiple exchanges ARE supported.

Example:

Binance Triangle Scanner
OKX Triangle Scanner
Kraken Triangle Scanner

Each exchange operates independently.

Never create a triangle whose individual legs span multiple exchanges.

---

# 2. PROFITABILITY PRINCIPLE

There is no guaranteed profit.

Never describe any strategy as:

- guaranteed
- risk free
- always profitable
- certain profit

The engine must calculate whether an opportunity is economically viable after
ALL measurable costs.

For each triangle calculate:

starting capital

-> leg 1 executable conversion
-> fee 1
-> leg 2 executable conversion
-> fee 2
-> leg 3 executable conversion
-> fee 3
-> price impact
-> expected slippage
-> latency safety buffer
-> execution-risk buffer

= estimated final capital

Then:

net_profit = final_capital - starting_capital

net_return_bps =
((final_capital / starting_capital) - 1) * 10_000

Never decide profitability from raw spread.

---

# 3. LIVE EXECUTION SAFETY BOUNDARY

Supported operational modes:

MARKET_DATA
RECORD
REPLAY
BACKTEST
PAPER
SHADOW

Create an execution abstraction:

type Executor interface

Implement:

PaperExecutor
ReplayExecutor
SimulationExecutor
ShadowExecutor

Define:

LiveExecutor

but leave it intentionally disabled.

Any invocation returns:

ErrLiveTradingDisabled

Do not implement unrestricted AI-controlled real-money execution.

Exchange credentials used during development must use:

- public market-data access; or
- testnet/demo credentials; or
- read-only credentials.

Never require:

withdrawal permission
transfer permission

AI is an ANALYST.

AI cannot bypass execution boundaries.

---

# 4. START BY BUILDING THE SPECIALIST AGENT TEAM

Before application implementation, ensure these specialist agents exist (`.opencode/agent/`; the same definitions are kept under `.claude/agents/`):

.opencode/agent/
    principal-architect.md
    exchange-researcher.md
    quant-researcher.md
    market-data-engineer.md
    triangular-engineer.md
    execution-simulator.md
    risk-engineer.md
    ai-advisor-engineer.md
    backend-engineer.md
    frontend-engineer.md
    product-designer.md
    telegram-engineer.md
    database-engineer.md
    security-engineer.md
    observability-engineer.md
    performance-engineer.md
    qa-engineer.md
    chaos-engineer.md
    code-reviewer.md

Create missing agents before substantial implementation.

Every agent definition requires:

---
name:
description:
tools:
model:
---

Use read-only tools for research/review agents whenever possible.

Do not give every agent unrestricted write access.

The principal agent owns:

architecture
task prioritization
integration
final technical decisions

Delegate to the specialist agents under `.opencode/agent/` (mention them as @name, or use the task tool) when specialist investigation materially improves correctness.

Do not create agents simply to generate activity.

---

# 5. REQUIRED SKILL RESOURCES

Create:

.claude/skills/triangular-arbitrage-platform/
    SKILL.md
    resources/
        architecture.md
        triangular-math.md
        order-book.md
        exchanges.md
        execution-simulation.md
        risk-management.md
        ai-advisor.md
        client-area.md
        telegram.md
        security.md
        observability.md
        testing.md
        coding-standards.md

The principal SKILL.md should remain focused.

Move detailed evolving knowledge into these resources.

Agents should read only resources relevant to their task.

Keep research documents updated as architecture changes.

---

# 6. CURRENT RESEARCH REQUIRED

Before coding exchange integrations, use current authoritative sources.

Investigate:

- Binance
- OKX
- Bybit
- Kraken
- Coinbase Advanced
- Bitget
- Gate.io

Also investigate where useful:

- CCXT
- CCXT Pro
- Hummingbot

Do NOT assume CCXT or Hummingbot must be used.

Compare:

A. native exchange integrations in Go

B. CCXT-based integration

C. Hummingbot integration

D. hybrid architecture

Research current official documentation.

Create:

docs/research/exchanges.md
docs/research/frameworks.md
docs/research/market-data.md
docs/research/fees.md
docs/research/final-platform-selection.md

---

# 7. EXCHANGE SCORING

Score every candidate exchange /100.

Evaluate:

spot liquidity
number of useful spot pairs
stablecoin markets
WebSocket quality
L2 order-book quality
snapshot/delta mechanism
sequence numbers
update frequency
API reliability
REST limits
WebSocket limits
market-order support
limit IOC/FOK support
fee structure
maker/taker fees
VIP implications
minimum order size
quantity precision
price precision
minimum notional
testnet/demo support
historical data
API-key security
IP allowlisting
operational history
maintenance behavior
regional restrictions
connector complexity

Do not choose an exchange from volume alone.

For triangular arbitrage, API and market-data quality can matter more than
headline volume.

---

# 8. RECOMMENDED CORE TECHNOLOGY

Unless research demonstrates a better choice:

BACKEND

Go latest stable

FRONTEND

Next.js latest stable
TypeScript
React
Tailwind CSS
professional component library where appropriate

DATABASE

PostgreSQL

CACHE / EPHEMERAL

Redis only if justified

REAL TIME

WebSocket

OBSERVABILITY

OpenTelemetry
Prometheus-compatible metrics
Grafana-compatible dashboards

LOCAL DEVELOPMENT

Docker
Docker Compose

Do not add:

Kafka
Kubernetes
ClickHouse
NATS
Elasticsearch

unless measured requirements justify them.

Complexity is a cost.

---

# 9. TARGET SYSTEM ARCHITECTURE

Preferred repository shape:

cmd/
    api/
    scanner/
    recorder/
    replay/
    worker/

internal/
    app/
    exchange/
    marketdata/
    orderbook/
    graph/
    triangle/
    pricing/
    fees/
    opportunity/
    execution/
    simulation/
    portfolio/
    inventory/
    reservation/
    risk/
    ai/
    telegram/
    notification/
    reporting/
    auth/
    audit/
    realtime/
    metrics/
    storage/
    config/

web/

migrations/

deploy/

docs/

tests/

Do not force this exact structure when domain analysis identifies something
better.

---

# 10. EXCHANGE ABSTRACTION

Separate:

MarketDataConnector
TradingMetadataProvider
AccountProvider
ExecutionSimulator

Never create one giant Exchange interface.

Required normalized models:

Exchange
ExchangeStatus
Market
TradingPair
Asset
Ticker
Trade
OrderBook
OrderBookLevel
FeeSchedule
InstrumentRules
Balance
Order
Fill
Triangle
Opportunity
Simulation
RiskDecision

Expose exchange capabilities explicitly.

Do not pretend every exchange behaves identically.

---

# 11. MARKET DATA ENGINE

This is the most critical subsystem.

Use WebSockets for real-time market data whenever supported.

Maintain local order books using:

REST snapshot
+
WebSocket deltas

Correctly handle:

sequence IDs
update IDs
snapshots
out-of-order updates
duplicates
gaps
reconnects
resubscriptions
heartbeats

Track:

exchange_event_time
local_receive_time
processed_time

Calculate:

network latency
processing latency
book age

Each order book has state:

SYNCING
HEALTHY
STALE
CORRUPTED
DISCONNECTED

Only HEALTHY books can create opportunities.

---

# 12. CLOCK MANAGEMENT

Monitor system clock drift.

Use NTP/OS time synchronization.

Track timestamp offset when exchanges expose server time.

If clock quality becomes unacceptable:

mark data unsafe
suspend opportunity qualification
emit alert

---

# 13. MARKET GRAPH

Represent each market as directed conversion edges.

Example:

BTC/USDT

creates potential:

USDT -> BTC
BTC -> USDT

Each edge must know whether conversion uses:

BID
or
ASK

Do not invert prices incorrectly.

Generate valid three-edge cycles.

A valid cycle:

starts in asset A
converts A -> B
B -> C
C -> A

Exactly 3 conversion legs.

Reject:

duplicate markets
invalid self loops
untradeable markets
disabled markets
markets missing required books

Cache valid triangle topology.

Do NOT recompute all graph topology on every tick.

Only recalculate economics for triangles affected by changed books.

---

# 14. TRIANGLE ENUMERATION

Generate every unique valid triangle supported by an exchange.

Avoid evaluating duplicates such as equivalent cycle rotations unnecessarily.

Example:

USDT -> BTC -> ETH -> USDT

must not be counted separately merely because represented as:

BTC -> ETH -> USDT -> BTC

unless starting-asset configuration specifically requires it.

Support configurable starting assets:

USDT
USDC
BTC
ETH
others explicitly enabled

---

# 15. EXACT CONVERSION MATHEMATICS

Use decimal/fixed-point arithmetic.

Never use float64 for money/accounting calculations where rounding errors can
affect results.

For every leg determine:

trade direction
side
bid/ask
quantity
quote quantity
fee asset
fee rate
precision
rounding
minimum quantity
maximum quantity
minimum notional
maximum notional where relevant

Model exchange-specific fee behavior correctly.

---

# 16. DEPTH-AWARE EXECUTABLE PRICING

Top-of-book spread is NOT enough.

For each leg simulate execution across L2 levels.

Calculate:

available liquidity
VWAP
quantity obtained
price impact

The quantity output from leg 1 becomes the quantity available to leg 2.

The quantity output from leg 2 becomes the quantity available to leg 3.

Then calculate the resulting final starting asset.

Never assume all capital trades at best bid/ask.

---

# 17. OPTIMAL TRADE-SIZE SEARCH

An opportunity can be profitable at $100 and unprofitable at $10,000.

Calculate:

minimum viable trade size
maximum profitable trade size
optimal simulated trade size

Evaluate candidate sizes against actual depth.

Consider algorithms such as:

breakpoint-aware depth analysis
bounded search
piecewise evaluation

Do not brute-force unnecessary amounts.

Return:

optimal_size
expected_profit
expected_return_bps
depth_utilization
liquidity_limit

---

# 18. FEE ENGINE

Fee calculations must support:

maker fee
taker fee
pair-specific fee
account tier
fee asset
fee discounts if explicitly configured

For initial arbitrage qualification assume the realistic execution fee for the
simulated order type.

Never assume zero fees.

Expose fee assumptions in every opportunity.

---

# 19. OPPORTUNITY MODEL

Each detected opportunity contains:

id
exchange_id
triangle_id
starting_asset
starting_amount

leg1
leg2
leg3

detected_at
validated_at
expires_at

book_version_leg1
book_version_leg2
book_version_leg3

gross_final_amount
fee_cost
slippage_cost
latency_buffer
risk_buffer
estimated_final_amount

gross_profit
net_profit
gross_return_bps
net_return_bps

maximum_profitable_size
recommended_simulated_size

confidence
data_quality_score

status

Statuses:

DETECTED
CALCULATING
QUALIFIED
REJECTED
EXPIRED
RESERVED
SIMULATING
COMPLETED
FAILED

Every rejected opportunity should store a reason code when useful.

---

# 20. OPPORTUNITY TTL

Arbitrage opportunities decay extremely quickly.

Every opportunity must have a strict TTL.

Before simulation:

revalidate all three books.

If any relevant book changed materially:

recalculate.

If expired:

reject.

Never process stale opportunities simply because they were queued.

---

# 21. FAST PATH

The hot path is:

WebSocket update

-> local order-book update

-> affected triangles lookup

-> deterministic recalculation

-> risk validation

-> qualified opportunity event

This path MUST NOT wait for:

PostgreSQL
Redis network calls
AI
Telegram
frontend
logging network calls
analytics

The hot path stays deterministic and in memory.

---

# 22. PAPER EXECUTION ENGINE

The simulator must model all THREE legs.

Do not create a fake simulator that fills immediately at requested prices.

Model:

order creation latency
network latency
exchange acknowledgement
queue/execution latency where applicable
book movement
slippage
partial fills
rejects
timeouts
cancellations
precision
fees

Possible outcomes:

ALL_FILLED

LEG1_PARTIAL

LEG1_FILLED_LEG2_FAILED

LEG1_LEG2_FILLED_LEG3_FAILED

PARTIAL_CYCLE

TIMEOUT

EXPIRED

REJECTED

Every adverse result affects P&L.

---

# 23. TEMPORARY EXPOSURE

Triangular arbitrage intends to return to its starting asset.

But failed legs can create exposure.

Track intermediate exposure explicitly.

Example failure:

USDT -> BTC succeeds
BTC -> ETH fails

portfolio now contains BTC.

Simulation must capture this exposure and its mark-to-market P&L.

Never report the triangle as merely "failed" and hide the resulting position.

---

# 24. CAPITAL RESERVATION

Support many simultaneous qualified opportunities.

Implement atomic CapitalReservationManager.

Track:

available
reserved
in-flight
settled

Prevent:

double spending
duplicate reservation
conflicting triangles
same opportunity executing twice

Use idempotency keys.

Race-test the implementation.

---

# 25. RISK ENGINE

The risk engine is deterministic.

AI NEVER overrides it.

Configurable controls:

minimum_net_edge_bps
minimum_expected_profit
maximum_trade_size
maximum_capital_per_triangle
maximum_capital_utilization
maximum_concurrent_simulations
maximum_book_age_ms
maximum_data_latency_ms
maximum_execution_latency_ms
maximum_slippage_bps
maximum_price_impact_bps
maximum_daily_simulated_loss
maximum_drawdown
minimum_data_quality_score

Per-exchange controls.

Per-starting-asset controls.

Per-triangle controls.

---

# 26. CIRCUIT BREAKERS

Pause qualification when:

exchange disconnects
WebSocket unstable
order-book sequence gap
REST reconciliation mismatch
clock drift
abnormal latency
large unexpected slippage
fee metadata unavailable
instrument metadata invalid
database degraded where persistence is required
memory pressure
critical internal queue saturation
simulation inconsistencies
risk service failure

Safe default:

DO NOTHING.

---

# 27. AI ADVISOR

Create provider abstraction:

AIAdvisor

Potential providers:

Anthropic
OpenAI

The provider implementation must be replaceable.

AI is NOT on the trading hot path.

AI may analyze:

strategy performance
opportunity distributions
false positives
profitability deterioration
fee impact
slippage patterns
latency
exchange quality
triangle quality
hour-of-day performance
starting-asset performance
rejection reasons
system incidents
parameter sensitivity

---

# 28. AI PARAMETER RECOMMENDATIONS

AI may recommend:

min_net_edge_bps
risk_buffer_bps
latency_buffer_bps
maximum_trade_size
maximum_slippage_bps
triangle enable/disable
market enable/disable
exchange enable/disable recommendation
starting-asset allocation recommendation

Every recommendation includes:

recommendation_id
created_at
scope
parameter
current_value
recommended_value
evidence
reason
confidence
expected_effect
risks
expires_at

AI NEVER silently changes configuration.

Recommendations require explicit approval through:

web client area

or

Telegram

before changing non-execution strategy configuration.

Every approval generates an audit event.

---

# 29. AI ANALYTICS

Build scheduled AI analysis for:

hourly health analysis
daily performance analysis
weekly parameter review

AI gets summarized structured data.

Do not dump huge raw order books into the LLM.

Do not send:

exchange secrets
API keys
tokens
passwords
private environment variables

AI failure must not affect scanner availability.

---

# 30. WEB CLIENT AREA — CORE REQUIREMENT

Build a complete professional operations console.

This is NOT a simple admin panel.

It is the primary control surface for the platform.

Use a professional trading-platform visual language.

Requirements:

responsive
desktop-first
mobile usable
dark mode optimized
fast
real-time
keyboard accessible
clear status indicators
information dense without becoming chaotic

Telegram and the web console consume the SAME backend application services.

Never duplicate trading logic in frontend or Telegram.

---

# 31. CLIENT AREA — MAIN NAVIGATION

Required navigation:

Overview

Scanner

Triangles

Opportunities

Paper Trading

Orders

Fills

Portfolio

Balances

PnL & Analytics

Exchanges

Markets

Strategies

AI Advisor

Risk Center

Replay & Backtesting

Reports

Alerts

Telegram

System Health

Audit Log

Users & Security

Settings

---

# 32. OVERVIEW DASHBOARD

Display in real time:

SYSTEM STATUS

scanner state
paper engine state
exchange connectivity
WebSocket health
database health
AI provider health
Telegram health

TODAY

opportunities detected
qualified opportunities
rejected opportunities
paper cycles
successful cycles
failed cycles
net paper P&L
gross P&L
fees
slippage
maximum drawdown

CURRENT

active simulations
capital reserved
available simulated capital
average net edge
highest live candidate edge

EXCHANGE HEALTH

latency
book freshness
reconnect count
messages/sec
errors

TOP TRIANGLES

best triangles by:
net edge
net P&L
success rate
sample size

Charts should update in real time.

---

# 33. LIVE SCANNER PAGE

Create a professional scanner table.

Columns:

exchange
triangle
starting asset
starting amount
leg 1
leg 2
leg 3
gross edge bps
fee bps
slippage bps
risk buffer bps
net edge bps
expected profit
max profitable size
book age
latency
data quality
status
age

Features:

real-time updates
sorting
filters
saved views
search
pause display
pin triangle
inspect
export

Clicking a row opens detailed opportunity analysis.

---

# 34. TRIANGLE DETAIL PAGE

Show:

visual triangle graph

Asset A
↓
Asset B
↓
Asset C
↓
Asset A

For each leg show:

exchange market
side
best bid/ask
requested quantity
VWAP
depth consumed
fee
precision
expected output

Show calculation waterfall:

Starting capital
Gross conversion result
Fees
Slippage
Latency reserve
Risk reserve
Final expected value
Expected net profit

Show related order-book depth.

Show recent performance for this triangle.

Show parameter history.

Show AI analysis.

---

# 35. TRIANGLES PAGE

Display all discovered valid triangles.

Fields:

triangle
exchange
enabled
starting asset
markets
liquidity score
opportunity count
qualification rate
paper cycles
success rate
net P&L
average edge
median edge
average slippage
last opportunity
status

Actions:

enable
disable
inspect
backtest
open statistics

Bulk actions must be permission controlled.

---

# 36. OPPORTUNITY EXPLORER

Provide:

active
expired
qualified
rejected
simulated
failed

filters.

Every opportunity detail must explain:

why detected
why qualified/rejected
what data was used
which book versions were used
calculation
risk decision
simulation result

This is essential for debugging.

---

# 37. PAPER TRADING CONSOLE

Display:

mode
starting virtual balance
available balance
reserved balance
current intermediate exposure
active cycles
completed cycles
P&L

Actions:

start paper engine
pause paper engine
reset ONLY with strong confirmation
configure starting balances
export results

Maintain historical sessions.

Do not delete historical metrics when a new session begins.

---

# 38. ORDERS PAGE

Show each simulated leg as an order.

Fields:

order ID
cycle ID
exchange
market
side
type
quantity
requested price
average fill price
filled quantity
fee
slippage
created
acknowledged
filled
latency
status

Click order for event timeline.

---

# 39. FILLS PAGE

Fields:

fill ID
order
cycle
exchange
market
side
price
quantity
fee
timestamp
simulated market depth source

Allow correlation from fill -> order -> triangle -> opportunity.

---

# 40. PORTFOLIO / BALANCES

Per exchange display:

asset
available
reserved
intermediate exposure
total
mark value

Highlight unexpected balances generated by incomplete cycles.

Include balance-history chart.

---

# 41. PNL & ANALYTICS

Provide:

gross P&L
net P&L
fees
slippage cost
latency cost estimate
failed-cycle losses
drawdown
profit factor

Break down by:

exchange
triangle
starting asset
market
hour
day
strategy configuration version

Charts:

cumulative P&L
daily P&L
drawdown
PnL distribution
edge distribution
slippage distribution
latency distribution

Avoid misleading statistics.

Always include sample size.

---

# 42. EXCHANGE MANAGEMENT

Exchange page shows:

connection state
market-data state
account/demo state
WebSocket state
REST state
latency
clock offset
last message
last snapshot
reconnects
errors
rate-limit status

Configuration:

enabled
paper enabled
markets
starting assets
fee tier
limits

Never display complete API secrets after entry.

---

# 43. MARKET EXPLORER

Display:

symbol
base
quote
enabled
bid
ask
spread
depth
24h volume if available
book age
update rate
minimum quantity
minimum notional
precision
fee assumption
triangles using market

Allow market detail with order-book visualization.

---

# 44. STRATEGY CONFIGURATION

Create structured configuration sections:

PROFITABILITY

minimum edge
minimum profit
risk buffer
latency buffer

EXECUTION SIMULATION

latency model
slippage limits
fill model
TTL

CAPITAL

starting assets
per-triangle amount
max simultaneous capital
reservation settings

MARKET DATA

book age threshold
reconnect parameters
health thresholds

FILTERS

allowed exchanges
markets
triangles
assets

Every configuration change requires:

validation
version
actor
timestamp
audit entry

Provide:

current config
previous config
diff
rollback

---

# 45. AI ADVISOR CLIENT AREA

Dedicated AI page.

Sections:

Current Insights

Parameter Recommendations

Performance Explanations

Anomalies

Exchange Analysis

Triangle Analysis

Daily Review

Recommendation History

Each recommendation has:

evidence
confidence
benefit
risk
current value
recommended value

Actions:

approve
reject
defer

AI cannot hide rejection history.

---

# 46. RISK CENTER

Show:

global risk state
circuit breakers
current limits
capital utilization
current exposure
drawdown
daily P&L
latency risk
data-health risk

List active risk blocks.

Show why each opportunity was blocked.

Provide risk event timeline.

Risk thresholds can be changed only by authorized roles.

---

# 47. REPLAY & BACKTEST AREA

Support recorded market-data sessions.

UI workflow:

select exchange
select date/session
select markets
select triangles
select configuration version
select virtual capital
run replay

Results:

opportunities
qualified opportunities
paper cycles
PnL
fees
slippage
drawdown
failures
latency sensitivity

Compare two configurations side by side.

---

# 48. REPORTS

Reports:

Daily Performance
Weekly Performance
Exchange Health
Triangle Performance
Risk Events
AI Recommendations
System Reliability

Allow:

view
download CSV where appropriate
download structured JSON
print-friendly report

Telegram receives concise summaries.

Web console retains detailed reports.

---

# 49. ALERT CENTER

Alert severity:

INFO
WARNING
HIGH
CRITICAL

Sources:

market data
exchange
scanner
simulation
risk
AI
database
Telegram
security
system

Features:

real-time alerts
acknowledge
resolve
filter
history

Critical alerts must never disappear merely because resolved.

---

# 50. SYSTEM HEALTH

Display:

service state
version
commit
uptime
CPU
memory
goroutines
GC
DB connections
WebSocket connections
queue depths
messages/sec
opportunity calculations/sec
API latency
frontend WebSocket state

Per exchange:

feed state
latency
book age
reconnects
sequence errors

---

# 51. AUDIT LOG

Immutable-style audit records for:

login
logout
configuration changes
strategy changes
exchange changes
AI recommendations
AI approvals/rejections
paper engine changes
risk changes
user changes
Telegram actions
security events

Fields:

timestamp
actor
source
action
entity
entity ID
before
after
IP where relevant
correlation ID

Allow filtering but not silent deletion.

---

# 52. USERS AND RBAC

Initial roles:

ADMIN
OPERATOR
VIEWER

ADMIN:

system configuration
users
risk configuration
exchange settings

OPERATOR:

paper operations
scanner configuration within permitted bounds
AI recommendation approval where permitted
reports

VIEWER:

read only

Implement backend authorization.

Hiding buttons in frontend is NOT authorization.

---

# 53. AUTHENTICATION

Implement secure sessions.

Support:

strong password hashing
session expiry
session revocation
CSRF protection as appropriate
rate limiting
login throttling

Architect for optional MFA.

Never store plaintext passwords.

---

# 54. REAL-TIME CLIENT UPDATES

Use backend WebSocket or an equally justified streaming mechanism.

Stream:

scanner opportunities
system health
alerts
orders
fills
P&L
balances
exchange state

Do not force the browser to poll every second.

Implement reconnect and resynchronization.

---

# 55. CLIENT PERFORMANCE

Large scanner tables require:

virtualization when appropriate
controlled render frequency
server-side filtering where useful
batched real-time updates

Never rerender the entire dashboard for every exchange tick.

The frontend receives aggregated operational updates.

Raw L2 firehose stays backend-side unless explicitly requested for a focused
order-book view.

---

# 56. TELEGRAM CONTROL SURFACE

Telegram is a second first-class interface.

Authenticate using configured Telegram user IDs.

Commands:

/start
/help

/status
/health

/scanner
/triangles
/opportunities

/paper
/paper_status
/paper_pause
/paper_resume

/orders
/fills
/balances
/pnl
/stats

/exchanges
/latency

/risk
/alerts

/ai
/ai_recommendations

/report
/daily

/config

Telegram operations must use the same authorization and application service
rules as the web console.

---

# 57. TELEGRAM INTERACTIVE BUTTONS

Use inline buttons where appropriate.

Examples:

View Opportunity
View Triangle
View PnL
Pause Paper Engine
Resume Paper Engine
Acknowledge Alert
Approve AI Parameter Recommendation
Reject AI Recommendation

Dangerous configuration changes require explicit confirmation.

No Telegram action bypasses backend authorization.

---

# 58. TELEGRAM PUSH ALERTS

Send alerts for:

service start
service stop
exchange connected
exchange disconnected
book corruption
high latency
scanner paused
large qualified paper opportunity
paper cycle started
paper cycle finished
paper cycle failed
unexpected intermediate exposure
drawdown threshold
risk circuit breaker
AI recommendation
critical incident
daily report

Avoid alert spam.

Add:

cooldowns
deduplication
severity thresholds
aggregation

---

# 59. TELEGRAM + WEB SYNCHRONIZATION

If configuration changes through Telegram:

web updates immediately.

If paper engine pauses through web:

Telegram /status shows paused.

If alert acknowledged in either:

state updates everywhere.

Single backend state.

Multiple interfaces.

---

# 60. NOTIFICATION SERVICE

Create unified NotificationService.

Channels:

Web
Telegram
future email

Notifications originate from domain events.

Do not directly call Telegram from core trading packages.

---

# 61. BACKEND API

Design versioned APIs.

Example groups:

/api/v1/auth
/api/v1/dashboard
/api/v1/scanner
/api/v1/triangles
/api/v1/opportunities
/api/v1/paper
/api/v1/orders
/api/v1/fills
/api/v1/portfolio
/api/v1/pnl
/api/v1/exchanges
/api/v1/markets
/api/v1/config
/api/v1/ai
/api/v1/risk
/api/v1/replays
/api/v1/reports
/api/v1/alerts
/api/v1/system
/api/v1/audit
/api/v1/users

Use consistent:

error schema
pagination
filtering
sorting
correlation IDs

Document APIs.

---

# 62. STORAGE MODEL

Design PostgreSQL tables for:

users
sessions

exchanges
exchange_health
markets
triangles

opportunities

paper_sessions
paper_cycles
orders
fills

virtual_balances
balance_snapshots

pnl_snapshots

strategy_configs
strategy_config_versions

ai_recommendations

risk_events
alerts
notifications

reports

audit_events

system_events

market_recording_metadata

Do not dump every order-book delta directly into PostgreSQL.

---

# 63. MARKET DATA RECORDING

Implement efficient append-oriented raw recording.

Research suitable formats.

Possible approach:

compressed chronological files
object storage compatible layout
Parquet for analytical derivatives where useful

Record enough to reproduce system decisions.

Associate:

market-data recording
opportunity
simulation
configuration version

---

# 64. DETERMINISTIC REPLAY

Given:

same recorded market stream
same configuration
same simulator parameters
same random seed where stochastic simulation exists

the replay should be reproducible.

This is essential for debugging.

---

# 65. OBSERVABILITY

Instrument using OpenTelemetry.

Metrics include:

market_messages_total
market_messages_per_second
market_message_latency_ms

orderbook_age_ms
orderbook_sequence_errors
orderbook_resync_total

triangles_total
triangles_evaluated_total
triangle_evaluation_duration

opportunities_detected_total
opportunities_qualified_total
opportunities_rejected_total
net_edge_bps

paper_cycles_total
paper_cycles_success_total
paper_cycles_failed_total

paper_pnl
fees_total
slippage_bps

capital_available
capital_reserved

exchange_reconnects_total
exchange_api_errors_total

ai_requests_total
ai_failures_total

telegram_messages_total
telegram_errors_total

api_request_duration

websocket_clients

circuit_breaker_state

---

# 66. LOGGING

Use structured JSON logs.

Include:

timestamp
level
service
event
correlation_id
exchange
triangle_id
opportunity_id
cycle_id
order_id

Never log secrets.

Avoid logging every market tick at INFO.

High-volume logs must be sampled or use appropriate levels.

---

# 67. SECURITY

Security is mandatory.

Requirements:

no secrets in Git
no secrets in logs
no secrets in AI prompts
no secrets returned to browser
no secrets returned to Telegram

Use secret manager in deployed environments.

Use environment variables only for appropriate development configuration.

Exchange API keys:

minimum permissions
no withdrawal
no transfer
IP allowlisting when available

Add:

secret scanning
dependency scanning
SAST
container scanning where practical
frontend dependency audit
backend rate limiting
security headers

---

# 68. PROMPT-INJECTION SECURITY

Treat as untrusted:

Telegram text
web form input
exchange messages
market metadata
news
external documentation
AI output

No external content can redefine:

system permissions
risk policy
execution boundary
secret policy
authorization

Validate AI structured output before use.

---

# 69. TESTING REQUIREMENTS

BACKEND:

unit tests
integration tests
race tests
property tests where valuable
connector tests
replay tests
simulation tests
API tests
security tests

FRONTEND:

component tests
API integration tests
WebSocket tests
responsive tests
accessibility tests
E2E tests

Use Playwright or equivalent for E2E.

---

# 70. CRITICAL FINANCIAL TEST CASES

Test:

bid/ask inversion errors
decimal rounding
fee charged in base
fee charged in quote
minimum quantity
minimum notional
precision truncation

insufficient depth
zero depth
stale book
corrupted book

profitable top-of-book but unprofitable after depth
profitable before fees but unprofitable after fees
profitable before slippage but unprofitable after slippage

leg 1 partial
leg 2 failure
leg 3 failure

duplicate opportunity
expired opportunity

capital race
reservation conflict

---

# 71. MARKET-DATA CHAOS TESTS

Inject:

disconnect
reconnect storm
message duplication
message loss
out-of-order sequence
snapshot delay
REST failure
WebSocket freeze
clock skew
message burst

The platform must fail safely.

---

# 72. UI E2E TESTS

Automate:

login
dashboard load
scanner filtering
triangle inspection
paper pause/resume
configuration update
rollback
AI recommendation approve/reject
alert acknowledgement
report viewing
RBAC restrictions
WebSocket disconnect/reconnect

---

# 73. PERFORMANCE BENCHMARKS

Benchmark:

order-book application
triangle recalculation
cycle conversion math
depth simulation
optimal-size search
opportunity creation
serialization
WebSocket fan-out

Use:

go test -bench
pprof
race detector

Measure before optimizing.

---

# 74. CI

CI must run:

format check
go vet
Go tests
race tests where suitable
backend lint
frontend lint
frontend typecheck
frontend tests
E2E critical path
security scanning
dependency scanning

No merge with failing P0 tests.

---

# 75. DEVELOPMENT QUALITY

Go rules:

idiomatic Go
context.Context
explicit ownership
bounded goroutines
bounded queues
graceful shutdown
dependency injection
small interfaces
structured errors

Avoid:

global mutable state
god services
unbounded goroutines
sleep synchronization
panic for normal failures
float money calculations
business logic in HTTP handlers
business logic in Telegram handlers

---

# 76. FRONTEND QUALITY

Use:

typed API client
clear feature boundaries
server/client components appropriately
reusable domain components
accessible tables/forms
optimistic UI only where safe

Never replicate profitability formulas in JavaScript.

The backend is authoritative for financial calculations.

---

# 77. MASTER PLAN

Create:

docs/MASTER_PLAN.md

Priorities:

P0 CRITICAL
P1 HIGH
P2 MEDIUM
P3 OPTIONAL

Every task contains:

ID
title
priority
component
description
business reason
technical reason
dependencies
risks
acceptance criteria
tests
status

Statuses:

TODO
IN_PROGRESS
BLOCKED
DONE

---

# 78. IMPLEMENTATION PHASES

PHASE 0
Current research

PHASE 1
Architecture

PHASE 2
Repository/bootstrap

PHASE 3
Authentication + core API

PHASE 4
First exchange public market-data connector

PHASE 5
Normalized order-book engine

PHASE 6
Market graph + triangle discovery

PHASE 7
Exact conversion mathematics

PHASE 8
Depth-aware profitability

PHASE 9
Opportunity engine

PHASE 10
Risk engine

PHASE 11
Paper execution simulator

PHASE 12
Portfolio + capital reservation

PHASE 13
Persistence

PHASE 14
Basic web operations console

PHASE 15
Telegram

PHASE 16
Complete client-area analytics

PHASE 17
Record/replay

PHASE 18
AI advisor

PHASE 19
Observability + alerting

PHASE 20
Second exchange

PHASE 21
Additional selected exchanges

PHASE 22
Full backtesting/replay UI

PHASE 23
Hardening + chaos testing

Do not implement six exchanges before the first one works correctly.

---

# 79. FIRST EXCHANGE DEFINITION OF DONE

Before adding exchange #2:

stable WebSocket

correct local books

sequence reconciliation

all valid triangles discovered

fees correct

precision correct

depth simulation correct

paper cycles realistic

risk engine working

dashboard working

Telegram working

record/replay working

tests passing

observability working

Only then add another connector.

---

# 80. PROFITABILITY VALIDATION

Evaluate using:

historical replay
recorded live feeds
paper operation

Measure:

gross edge
net edge
fees
slippage
failure rate
latency
drawdown
capital efficiency

Stress test using:

higher fees
higher latency
worse fills
lower liquidity

A system profitable only under perfect fills is worthless.

Flag it.

---

# 81. STRATEGY QUALITY SCORE

Create TriangleQualityScore /100.

Possible factors:

net profitability
sample size
execution success
slippage stability
liquidity stability
edge persistence
latency sensitivity
drawdown
failure severity

Never optimize purely for win rate.

---

# 82. DAILY REPORT

Generate:

EXECUTIVE SUMMARY

SYSTEM HEALTH

EXCHANGE HEALTH

SCANNER

OPPORTUNITIES

PAPER CYCLES

PNL

FEES

SLIPPAGE

FAILED CYCLES

CAPITAL UTILIZATION

TOP TRIANGLES

WORST TRIANGLES

RISK EVENTS

AI FINDINGS

INCIDENTS

RECOMMENDED ACTIONS

Persist detailed version.

Send concise version to Telegram.

---

# 83. REQUIRED DOCUMENTATION

Maintain:

README.md

docs/MASTER_PLAN.md
docs/architecture.md
docs/data-flow.md
docs/security.md
docs/risk.md
docs/triangular-arbitrage.md
docs/exchange-connectors.md
docs/paper-execution.md
docs/client-area.md
docs/telegram.md
docs/ai-advisor.md
docs/observability.md
docs/testing.md
docs/deployment.md
docs/runbook.md

---

# 84. RESEARCH MODE

If ARGUMENTS contains:

research

Do not implement product code.

Delegate focused research.

Produce:

exchange comparison
framework comparison
fee analysis
market-data analysis
triangular-arbitrage constraints
architecture recommendation

Recommend:

best initial exchange
second exchange
technology approach

Explain evidence.

---

# 85. ARCHITECTURE MODE

If ARGUMENTS contains:

architecture

Produce detailed architecture before implementing.

Include:

components
interfaces
event flows
concurrency model
data model
API boundaries
real-time architecture
client architecture
Telegram architecture
security model
failure model

Use Mermaid diagrams where useful.

---

# 86. BOOTSTRAP MODE

If ARGUMENTS contains:

bootstrap

Create:

specialist agents
skill resource documents
repository structure
Go project
web project
Docker development environment
config
CI
logging
database migrations framework
API skeleton
test framework

Do not pretend incomplete services are implemented.

---

# 87. IMPLEMENT MODE

If ARGUMENTS contains:

implement

Read:

docs/MASTER_PLAN.md

Select highest priority unblocked item.

Set:

IN_PROGRESS

Implement completely.

Run relevant tests.

Review result.

Fix P0/P1 findings.

Set DONE only when acceptance criteria pass.

Continue to next appropriate item.

Do not randomly jump between features.

---

# 88. AUDIT MODE

If ARGUMENTS contains:

audit

Perform a ruthless complete audit.

Analyze:

financial correctness
triangle mathematics
fees
precision
order-book correctness
sequence handling
stale data
concurrency
race conditions
execution simulation
risk
security
authentication
authorization
API design
frontend correctness
Telegram permissions
AI boundaries
performance
memory
resource leaks
database
observability
testing
architecture

Create findings in MASTER_PLAN.

Severity:

P0
P1
P2
P3

Every finding requires:

evidence
impact
recommended fix
acceptance criteria

---

# 89. ALL MODE

If ARGUMENTS contains:

all

Run sequentially:

research
architecture
bootstrap
implementation

Follow MASTER_PLAN priority.

Do not skip research.

Do not prematurely add exchanges.

---

# 90. FINAL ENGINEERING RULE

The scanner's job is not to find the largest number of apparent
opportunities.

Its job is to eliminate false opportunities.

A raw spread is meaningless.

A valid triangular opportunity requires:

healthy synchronized books
+
correct graph direction
+
real executable depth
+
correct precision
+
all fees
+
realistic slippage
+
latency allowance
+
risk allowance
+
sufficient liquidity
+
sufficient expected net profit

If any critical input is uncertain:

REJECT THE OPPORTUNITY.

Correct rejection is cheaper than false confidence.

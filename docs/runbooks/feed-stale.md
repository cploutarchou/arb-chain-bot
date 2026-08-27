# Runbook: FeedStale (> 30 s)

Alert: `FeedStale` — `max by (exchange)(orderbook_age_ms) > 30000` for 30 s.

Impact: qualification is suppressed for every triangle on that venue;
paper cycles stop; recordings continue (gaps are captured, not hidden).

1. Confirm scope: dashboard "arb platform — SRE" -> Feed health. One
   exchange or all? All -> egress/DNS/NetworkPolicy (`kubectl -n arb
   describe netpol`) or node problem. One -> venue incident or ban.
2. `kubectl -n arb logs deploy/arb-arb-platform-arbd --since=10m | grep -i "reconnect\|429\|-1003\|closed"`.
   `ReconnectLoop` firing alongside -> the venue is refusing us; stop
   REST resyncs by lowering `ARB_SYMBOLS` and wait out the ban window.
   `CollectorRateLimited` -> same.
3. Silent-but-connected (no reconnects, age climbing): restart the
   pod (`kubectl -n arb rollout restart deploy/arb-arb-platform-arbd`).
   In RECORD mode this closes the session cleanly; note the session id.
4. Still stale after restart: check venue status page; leave the
   engine running (the console must show WHY books are unhealthy) and
   silence the alert for the venue with an expiry.
5. Post: if the outage spans a campaign window, mark the session in
   `docs/campaigns/` as gapped; no published number may come from it.

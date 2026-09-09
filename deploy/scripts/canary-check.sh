#!/usr/bin/env bash
# Canary check for the prod bake: the canary release must be rolled out,
# healthy, ready, state-isolated from the primary, and emitting the feed
# series the SLO rules key on. Every check can pass on a PAPER canary
# running in memory (deploy/helm/canary-values.yaml); a gate that cannot
# pass only teaches people to skip it.
# Usage: canary-check.sh <namespace> <canary-release> <primary-release>
# Env:   ARB_HTTP_PORT (8080), ARB_METRICS_PORT (9109),
#        CANARY_FEED_WAIT_SECONDS (180), CANARY_STALE_MS (30000 = FeedStale)
set -euo pipefail

ns=${1:?namespace}
canary=${2:?canary release}
primary=${3:?primary release}
http_port=${ARB_HTTP_PORT:-8080}
metrics_port=${ARB_METRICS_PORT:-9109}
feed_wait=${CANARY_FEED_WAIT_SECONDS:-180}
stale_ms=${CANARY_STALE_MS:-30000}

fail() { echo "canary-check: FAIL: $*" >&2; exit 1; }
arbd="$canary-arb-platform-arbd"
web="$canary-arb-platform-web"

# 1. rollout complete
kubectl -n "$ns" rollout status "deploy/$arbd" --timeout=5m
if kubectl -n "$ns" get deploy "$web" >/dev/null 2>&1; then
  kubectl -n "$ns" rollout status "deploy/$web" --timeout=5m
fi
running_pod() {
  kubectl -n "$ns" get pod -l "app.kubernetes.io/instance=$1,app.kubernetes.io/component=$2" \
    --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}
pod=$(running_pod "$canary" arbd)
[ -n "$pod" ] || fail "no running arbd pod for release $canary"

# 2. mode: the chart refuses anything but PAPER for a canary; re-check the live object
mode=$(kubectl -n "$ns" get cm "$arbd-config" -o jsonpath='{.data.ARB_MODE}')
[ "$mode" = "PAPER" ] || fail "canary ARB_MODE=$mode (expected PAPER)"

# 3. health and readiness (readiness also pings the pool when persistence is on)
for path in /healthz /readyz; do
  out=$(kubectl -n "$ns" exec "$pod" -c arbd -- wget -qO- "http://127.0.0.1:$http_port$path") \
    || fail "$path did not answer 200"
  echo "$path -> $out"
done

# 4. isolation: the canary never runs against the primary's database.
# Only host:port/db is extracted, inside the pod; credentials never
# leave it and never reach this log.
dsn_target() {
  # shellcheck disable=SC2016  # expanded by the shell inside the pod
  kubectl -n "$ns" exec "$1" -c arbd -- sh -c \
    'printf %s "${ARB_DATABASE_URL:-}" | sed -E "s#^[a-zA-Z]+://##; s#^[^@/]*@##; s#\?.*##"'
}
persistence=$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.metadata.labels.arb\.io/persistence}')
ctarget=$(dsn_target "$pod")
if [ "$persistence" = "disabled" ]; then
  [ -z "$ctarget" ] || fail "persistence is disabled but ARB_DATABASE_URL is set in the canary pod"
  echo "isolation: persistence disabled, ARB_DATABASE_URL empty"
else
  [ -n "$ctarget" ] || fail "persistence enabled but ARB_DATABASE_URL is empty"
  ppod=$(running_pod "$primary" arbd)
  [ -n "$ppod" ] || fail "cannot compare with the primary: no running arbd pod for release $primary"
  ptarget=$(dsn_target "$ppod")
  [ "$ctarget" != "$ptarget" ] || fail "canary database target equals the primary's ($ctarget)"
  echo "isolation: canary database target $ctarget differs from the primary's"
fi
# The ExternalSecret must not reference the primary's database key either.
es="$canary-arb-platform-secrets"
if kubectl -n "$ns" get externalsecret "$es" >/dev/null 2>&1; then
  key_of() {
    kubectl -n "$ns" get externalsecret "$1" \
      -o jsonpath='{.spec.data[?(@.secretKey=="ARB_DATABASE_URL")].remoteRef.key}' 2>/dev/null || true
  }
  ckey=$(key_of "$es")
  pkey=$(key_of "$primary-arb-platform-secrets")
  if [ -n "$ckey" ] && [ "$ckey" = "$pkey" ]; then
    fail "canary ExternalSecret projects the primary's database key $ckey"
  fi
  echo "isolation: ExternalSecret database key: ${ckey:-<none projected>}"
fi

# 5. ingress: marked canary, no weighted traffic
ing="$canary-arb-platform"
if kubectl -n "$ns" get ingress "$ing" >/dev/null 2>&1; then
  ann() {
    kubectl -n "$ns" get ingress "$ing" \
      -o jsonpath="{.metadata.annotations.nginx\.ingress\.kubernetes\.io/$1}"
  }
  [ "$(ann canary)" = "true" ] || fail "canary Ingress lacks nginx.ingress.kubernetes.io/canary=true"
  w=$(ann canary-weight)
  [ -z "$w" ] || [ "$w" = "0" ] \
    || fail "canary Ingress has canary-weight=$w; its state is separate, so weighted traffic would split users"
  echo "ingress: canary flag set, weight ${w:-unset}, header opt-in"
fi

# 6. metrics the SLO rules key on: orderbook_age_ms present and fresh
scrape() { kubectl -n "$ns" exec "$pod" -c arbd -- wget -qO- "http://127.0.0.1:$metrics_port/metrics"; }
deadline=$(( $(date +%s) + feed_wait ))
m=""
while :; do
  m=$(scrape) || m=""
  ages=$(awk '/^orderbook_age_ms[ {]/ {print $NF}' <<<"$m")
  if [ -n "$ages" ]; then
    max=$(sort -g <<<"$ages" | tail -1)
    if awk -v m="$max" -v t="$stale_ms" 'BEGIN { exit !(m < t) }'; then
      echo "feed: $(wc -l <<<"$ages") book series, max age ${max} ms (< ${stale_ms} ms)"
      break
    fi
  fi
  [ "$(date +%s)" -lt "$deadline" ] \
    || fail "no fresh order book within ${feed_wait}s (orderbook_age_ms missing or >= ${stale_ms} ms)"
  sleep 10
done
for series in orderbook_state market_messages; do
  grep -q "^$series" <<<"$m" || fail "series $series missing from the canary scrape"
done
if awk '/^orderbook_state[ {]/ && $NF == 3 { found = 1 } END { exit !found }' <<<"$m"; then
  fail "an order book reports CORRUPTED (orderbook_state=3)"
fi

# 7. console answers
if kubectl -n "$ns" get deploy "$web" >/dev/null 2>&1; then
  wpod=$(running_pod "$canary" web)
  [ -n "$wpod" ] || fail "no running web pod for release $canary"
  kubectl -n "$ns" exec "$wpod" -- wget -qO /dev/null "http://127.0.0.1:3000/" && echo "console ok"
fi
echo "canary-check: ok ($canary: PAPER, isolated, feed fresh)"

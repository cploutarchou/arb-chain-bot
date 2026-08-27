#!/usr/bin/env sh
# Smoke test a release: rollout complete, /healthz + /readyz 200 on arbd,
# console answers, ARB_MODE is not LIVE (belt and braces).
# Usage: smoke.sh <namespace> <release>
set -eu
ns=$1; rel=$2
arbd="$rel-arb-platform-arbd"; web="$rel-arb-platform-web"
kubectl -n "$ns" rollout status deploy/"$arbd" --timeout=5m
kubectl -n "$ns" rollout status deploy/"$web" --timeout=5m || true   # web may be absent in canary
pod=$(kubectl -n "$ns" get pod -l app.kubernetes.io/instance="$rel",app.kubernetes.io/component=arbd -o jsonpath='{.items[0].metadata.name}')
for path in /healthz /readyz; do
  out=$(kubectl -n "$ns" exec "$pod" -- wget -qO- "http://127.0.0.1:8080$path")
  echo "$path -> $out"
done
mode=$(kubectl -n "$ns" get cm "$rel-arb-platform-arbd-config" -o jsonpath='{.data.ARB_MODE}')
case "$mode" in RECORD|PAPER|REPLAY) echo "mode=$mode";; *) echo "unexpected ARB_MODE=$mode"; exit 1;; esac
if kubectl -n "$ns" get deploy "$web" >/dev/null 2>&1; then
  wpod=$(kubectl -n "$ns" get pod -l app.kubernetes.io/instance="$rel",app.kubernetes.io/component=web -o jsonpath='{.items[0].metadata.name}')
  kubectl -n "$ns" exec "$wpod" -- wget -qO /dev/null http://127.0.0.1:3000/ && echo "console ok"
fi
echo "smoke ok"

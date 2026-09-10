#!/usr/bin/env bash
# Proves the chart's render-time assertions fire, and that the real
# overlays still render. Runs in the deploy "chart" job and locally:
#   deploy/helm/test-canary-guards.sh          (HELM=/path/to/helm to override)
# shellcheck disable=SC2016  # the $1 inside bash -c strings is meant for the inner shell
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
chart="$here/arb-platform"
helm=${HELM:-helm}
# helm template simulates Kubernetes 1.20 unless told otherwise, and the
# chart requires >= 1.27 (Chart.yaml kubeVersion).
kube_version=${KUBE_VERSION:-1.29.0}
prod="$chart/values-prod.yaml"
canary="$here/canary-values.yaml"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The prod render needs packaged migrations (same copy package.sh does).
if [ ! -d "$chart/files/migrations" ]; then
  mkdir -p "$chart/files/migrations"
  cp "$root"/migrations/*.sql "$chart/files/migrations/"
fi

pass=0
failed=0
ok()   { echo "ok   $1"; pass=$((pass + 1)); }
bad()  { echo "FAIL $1"; failed=$((failed + 1)); }

# expect_fail <label> <expected message fragment> <helm args...>
expect_fail() {
  local label=$1 want=$2 out
  shift 2
  if out=$("$helm" template arb-canary "$chart" --kube-version "$kube_version" "$@" 2>&1); then
    bad "$label: render succeeded, expected a refusal containing: $want"
    return
  fi
  if grep -qF -- "$want" <<<"$out"; then
    ok "$label"
  else
    bad "$label: refused for another reason:"
    tail -3 <<<"$out"
  fi
}
# expect_render <label> <helm args...>
expect_render() {
  local label=$1
  shift
  if "$helm" template arb-canary "$chart" --kube-version "$kube_version" "$@" >/dev/null 2>"$tmp/err"; then
    ok "$label"
  else
    bad "$label:"
    tail -3 "$tmp/err"
  fi
}

C=(-f "$prod" -f "$canary")

echo "--- refusals ---"
expect_fail "canary with persistence on and no key of its own" \
  "needs canary.databaseRemoteKey" "${C[@]}" --set arbd.persistence.enabled=true
expect_fail "canary key equals the primary's database key" \
  "is the primary release's database key" "${C[@]}" \
  --set arbd.persistence.enabled=true --set canary.databaseRemoteKey=database-url
expect_fail "canary in RECORD mode" \
  "the canary runs PAPER" "${C[@]}" --set arbd.mode=RECORD
expect_fail "canary in REPLAY mode (even with a session id)" \
  "the canary runs PAPER" "${C[@]}" --set arbd.mode=REPLAY \
  --set 'arbd.extraEnv[0].name=ARB_REPLAY_SESSION' --set 'arbd.extraEnv[0].value=sess-1'
expect_fail "canary with the migrate hook while persistence is off" \
  "migrate.enabled must be false" "${C[@]}" --set migrate.enabled=true
expect_fail "canary claiming the recordings PVC" \
  "arbd.recordings.enabled must be false" "${C[@]}" --set arbd.recordings.enabled=true
expect_fail "canary DSN smuggled through extraEnv" \
  "must not be set through arbd.extraEnv" "${C[@]}" \
  --set 'arbd.extraEnv[0].name=ARB_DATABASE_URL' --set 'arbd.extraEnv[0].value=postgres://x'
expect_fail "canary taking weighted ingress traffic" \
  "canary-weight" "${C[@]}" \
  --set 'ingress.annotations.nginx\.ingress\.kubernetes\.io/canary-weight=10'
expect_fail "canary Ingress without the canary flag" \
  "must mark the Ingress as a canary" "${C[@]}" \
  --set 'ingress.annotations.nginx\.ingress\.kubernetes\.io/canary=false'
expect_fail "two engine replicas" \
  "single writer" "${C[@]}" --set arbd.replicas=2
expect_fail "REPLAY without a session id (any release)" \
  "requires arbd.extraEnv[ARB_REPLAY_SESSION]" -f "$prod" --set arbd.mode=REPLAY
expect_fail "LIVE mode (any release)" \
  "LIVE execution is disabled" -f "$prod" --set arbd.mode=LIVE
expect_fail "persistence off with the migrate hook (any release)" \
  "migrate.enabled must be false" -f "$prod" --set arbd.persistence.enabled=false

echo "--- valid renders ---"
expect_render "prod values" -f "$prod"
expect_render "paper-test values" -f "$chart/values-paper-test.yaml"
expect_render "dev values" -f "$chart/values-dev.yaml"
expect_render "canary overlay" "${C[@]}"
expect_render "canary with its own scratch database key" "${C[@]}" \
  --set arbd.persistence.enabled=true --set canary.databaseRemoteKey=database-url-canary

echo "--- canary render content ---"
"$helm" template arb-canary "$chart" --kube-version "$kube_version" "${C[@]}" > "$tmp/canary.yaml"
# check <label> <command...>: the command's exit status is the verdict
check() { local label=$1; shift; if "$@"; then ok "$label"; else bad "$label"; fi; }
check "no ARB_DATABASE_URL projected by the ExternalSecret" \
  bash -c '! grep -q "secretKey: ARB_DATABASE_URL" "$1"' _ "$tmp/canary.yaml"
check "ARB_DATABASE_URL pinned empty on the pod" \
  bash -c 'grep -A1 "name: ARB_DATABASE_URL" "$1" | grep -q "value: \"\""' _ "$tmp/canary.yaml"
check "no migrate Job (the migrate NetworkPolicy may render; it selects nothing)" \
  bash -c '! grep -q "^kind: Job" "$1"' _ "$tmp/canary.yaml"
check "no recordings PVC" \
  bash -c '! grep -q "kind: PersistentVolumeClaim" "$1"' _ "$tmp/canary.yaml"
check "ARB_MODE is PAPER" grep -q 'ARB_MODE: "PAPER"' "$tmp/canary.yaml"
check "canary pod label" grep -q 'arb.io/canary: "true"' "$tmp/canary.yaml"
check "header opt-in routing" grep -q 'canary-by-header' "$tmp/canary.yaml"
check "no cert-manager issuer on the canary Ingress" \
  bash -c '! grep -q "cert-manager.io/cluster-issuer" "$1"' _ "$tmp/canary.yaml"
"$helm" template arb-canary "$chart" --kube-version "$kube_version" "${C[@]}" \
  --set arbd.persistence.enabled=true --set canary.databaseRemoteKey=database-url-canary > "$tmp/canary-db.yaml"
check "scratch key projected when the canary owns a database" \
  grep -q 'key: "arb/prod/database-url-canary"' "$tmp/canary-db.yaml"

echo "guards: $pass passed, $failed failed"
[ "$failed" -eq 0 ]

#!/usr/bin/env bash
# T-045: run the Playwright E2E suite against a real arbd backend.
# Starts arbd (PAPER mode, port 18080, in-memory unless E2E_DATABASE_URL
# is set), lets Playwright's webServer bring up Next on :3100, runs the
# suite, and tears everything down.
set -euo pipefail
cd "$(dirname "$0")/.."

ARBD_LOG="$(mktemp -t arbd-e2e.XXXXXX.log)"
go build -o /tmp/arbd-e2e ./cmd/arbd/

ARB_MODE=PAPER \
ARB_HTTP_ADDR=127.0.0.1:18080 \
ARB_DATABASE_URL="${E2E_DATABASE_URL:-}" \
ARB_ADMIN_EMAIL=admin@e2e.test \
ARB_ADMIN_PASSWORD=e2e-password-123 \
ARB_AI_PROVIDER=fake \
/tmp/arbd-e2e >"$ARBD_LOG" 2>&1 &
ARBD_PID=$!
trap 'kill $ARBD_PID 2>/dev/null || true' EXIT

for i in $(seq 1 30); do
  if curl -s --noproxy '*' -o /dev/null http://127.0.0.1:18080/healthz; then
    break
  fi
  sleep 0.5
done

cd web
if [ -z "${PLAYWRIGHT_CHROMIUM:-}" ] && [ -x /opt/pw-browsers/chromium ]; then
  export PLAYWRIGHT_CHROMIUM=/opt/pw-browsers/chromium
fi
NO_PROXY='*' no_proxy='*' npx playwright test "$@"

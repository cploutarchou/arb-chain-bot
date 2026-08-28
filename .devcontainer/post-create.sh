#!/usr/bin/env bash
# Runs once, after the container is created. Everything here is
# idempotent so a rebuild is cheap and a partial failure can be retried
# by hand.
set -euo pipefail
cd "$(dirname "$0")/.."

say() { printf '\n\033[1m▸ %s\033[0m\n' "$*"; }

say "Go module cache"
go mod download

# github.com's host keys, so the first `git fetch` does not stop on an
# interactive host-authenticity prompt.
say "known_hosts"
mkdir -p ~/.ssh && chmod 700 ~/.ssh
touch ~/.ssh/known_hosts && chmod 600 ~/.ssh/known_hosts
if ! ssh-keygen -F github.com >/dev/null 2>&1; then
  ssh-keyscan -t rsa,ecdsa,ed25519 github.com >> ~/.ssh/known_hosts 2>/dev/null
fi

say "web/ dependencies"
npm --prefix web ci

say "site/ dependencies"
npm --prefix site ci

# Playwright's browser download is the slowest step and the one most
# likely to be blocked by a restrictive network. It is not fatal: the Go
# and Next.js suites run without it, and `npx playwright install
# chromium` can be re-run later.
say "Playwright chromium"
# `sudo npx` fails: the node feature installs Node under nvm, which is
# not on root's secure_path, so PATH has to be carried across sudo.
if sudo env "PATH=$PATH" npx --prefix web playwright install-deps chromium \
   && npx --prefix web playwright install chromium; then
  echo "chromium ready in ${PLAYWRIGHT_BROWSERS_PATH:-$HOME/.cache/ms-playwright}"
else
  echo "WARNING: Playwright chromium install failed. ./scripts/e2e.sh will not run"
  echo "         until 'npx --prefix web playwright install --with-deps chromium' succeeds."
fi

say "Done. Next: make up && make migrate"

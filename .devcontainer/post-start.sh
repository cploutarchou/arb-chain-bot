#!/usr/bin/env bash
# Runs on every container start. Reports the state of the two things
# that are mounted from the host and therefore can be absent or stale:
# the SSH agent and the Docker socket. Never fails the start — a broken
# mount should leave a usable shell with a clear message in it.
set -uo pipefail

ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }

printf '\n\033[1marb-chain-bot devcontainer\033[0m\n'

if [ -S "${SSH_AUTH_SOCK:-}" ] && ssh-add -l >/dev/null 2>&1; then
  ok "SSH agent: $(ssh-add -l | wc -l) key(s) forwarded — git push/pull over SSH will work"
elif [ -S "${SSH_AUTH_SOCK:-}" ]; then
  warn "SSH socket is mounted but holds no keys. On the host: ssh-add ~/.ssh/id_ed25519"
else
  warn "No SSH agent at ${SSH_AUTH_SOCK:-<unset>}. Start one on the host and rebuild:"
  warn "  eval \"\$(ssh-agent -s)\" && ssh-add ~/.ssh/id_ed25519"
fi

if docker info >/dev/null 2>&1; then
  ok "Docker socket: $(docker compose version --short 2>/dev/null || echo present)"
else
  warn "Docker socket unavailable — 'make up', 'make migrate' and 'make campaign' will fail."
fi

if [ -n "$(git config --get user.email || true)" ]; then
  ok "Git identity: $(git config --get user.name) <$(git config --get user.email)>"
else
  warn "No git user.email — commits will be rejected. Check the ~/.gitconfig mount."
fi

printf '\n'

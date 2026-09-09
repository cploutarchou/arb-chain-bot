#!/usr/bin/env sh
# Generate the vault master key (ARB_SECRET_KEY) and save it to .env.
#
#   make create-secret            # adds the key if .env has none
#   make create-secret FORCE=1    # replaces an existing key (see below)
#
# The key is 32 random bytes, standard base64 (what internal/secrets
# expects). Replacing a key makes every secret already stored under the
# old one unreadable ("present, unreadable" in Settings → Security):
# DELETE those rows and PUT the values again after the restart. The key
# never leaves this machine; .env is git-ignored.
set -eu

ENV_FILE="${ENV_FILE:-.env}"
FORCE="${FORCE:-0}"

if [ ! -f "$ENV_FILE" ]; then
  if [ -f .env.example ]; then
    cp .env.example "$ENV_FILE"
    echo "created $ENV_FILE from .env.example"
    echo "set POSTGRES_PASSWORD in $ENV_FILE before 'docker compose up': the database has no default password."
  else
    : > "$ENV_FILE"
  fi
fi

current=$(grep -E '^ARB_SECRET_KEY=.+' "$ENV_FILE" 2>/dev/null | head -n1 | cut -d= -f2- || true)
if [ -n "$current" ] && [ "$FORCE" != "1" ]; then
  echo "$ENV_FILE already has ARB_SECRET_KEY; keeping it (FORCE=1 to replace — stored secrets become unreadable)."
  exit 0
fi

key=$(head -c 32 /dev/urandom | base64 | tr -d '\n')
# Sanity: must decode back to exactly 32 bytes.
if [ "$(printf '%s' "$key" | base64 -d | wc -c | tr -d ' ')" != "32" ]; then
  echo "key generation failed" >&2
  exit 1
fi

tmp="$ENV_FILE.tmp.$$"
grep -vE '^#? ?ARB_SECRET_KEY=' "$ENV_FILE" > "$tmp" || true
{
  cat "$tmp"
  printf '\n# Vault master key (make create-secret). Losing it makes stored secrets unreadable.\nARB_SECRET_KEY=%s\n' "$key"
} > "$ENV_FILE"
rm -f "$tmp"
chmod 600 "$ENV_FILE"

echo "ARB_SECRET_KEY written to $ENV_FILE (fingerprint: $(printf '%s' "$key" | base64 -d | sha256sum | cut -c1-8))."
echo "Restart arbd to open the vault: docker compose --profile paper up -d   (this ends any running recording session)."

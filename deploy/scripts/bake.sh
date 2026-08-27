#!/usr/bin/env sh
# Bake: poll Alertmanager for firing alerts matching the regex during the
# window; exit 1 on the first hit so the workflow rolls back.
# Usage: bake.sh <alertmanager-url> <seconds> <alertname-regex>
set -eu
am=$1; secs=$2; re=$3
end=$(( $(date +%s) + secs ))
while [ "$(date +%s)" -lt "$end" ]; do
  firing=$(curl -fsS "$am/api/v2/alerts?active=true&silenced=false&inhibited=false" \
    | jq -r --arg re "$re" '.[] | select(.labels.alertname | test($re)) | .labels.alertname' | sort -u)
  if [ -n "$firing" ]; then echo "alerts firing during bake: $firing"; exit 1; fi
  sleep 30
done
echo "bake clean for ${secs}s"

#!/usr/bin/env sh
# Copies ../../migrations into the chart's files/ dir so the migrate
# hook ships the SQL that matches this commit, then lints/packages.
# Usage: deploy/helm/package.sh [output-dir]
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
chart="$here/arb-platform"
rm -rf "$chart/files/migrations"
mkdir -p "$chart/files/migrations"
cp "$root"/migrations/*.sql "$chart/files/migrations/"
helm lint "$chart"
if [ "${1:-}" != "" ]; then
  helm package "$chart" -d "$1"
fi

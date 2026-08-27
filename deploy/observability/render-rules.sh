#!/usr/bin/env sh
# Emits PrometheusRule objects from the plain rule files.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
for f in prometheus-rules.yml platform-rules.yml; do
  name=$(basename "$f" .yml)
  echo "---"
  echo "apiVersion: monitoring.coreos.com/v1"
  echo "kind: PrometheusRule"
  echo "metadata:"
  echo "  name: arb-$name"
  echo "  namespace: monitoring"
  echo "  labels: {release: kube-prometheus-stack}"
  echo "spec:"
  sed 's/^/  /' "$here/$f" | grep -v '^  *#'
done

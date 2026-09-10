---
name: prod-infra
description: 'Environments and operations: compose dev, paper-test staging, Kubernetes production with HA Postgres, Redis, object storage, CI/CD staged deploys, backups/restore drills, observability, SLOs, alerting, hardening. Use for "production", "robust infra", "deploy", "backups", "monitoring". Use when the request mentions: production, infra, kubernetes, deploy, backup, monitoring, SLO, HA.'
when_to_use: [production, infra, kubernetes, deploy, backup, monitoring, SLO, HA]
allowed-tools: Read Grep Glob Write Edit Bash Agent WebFetch WebSearch
argument-hint: '[task]'
---

# Production infrastructure workflow

Task: $ARGUMENTS

- Everything as code under `deploy/` (Helm charts or Kustomize, Terraform for cloud resources), environments dev / paper-test / prod with identical images and per-env config; secrets from a KMS-backed store, never in git.
- Postgres HA (managed service or Patroni), PITR backups with a scheduled restore drill; tick data partitioned/Timescale; recordings in object storage with lifecycle rules.
- CI/CD: build once, promote images; staged deploys (paper-test → prod) with canary and automatic rollback on SLO burn.
- Observability: Prometheus + Grafana dashboards per component, Loki logs, Tempo traces, alert rules (feed stale, collector 429s, rule evaluation lag, paper executor errors, DB replication lag); on-call runbooks in docs/runbooks/.
- Security: WAF/rate limiting at the edge, mTLS or network policies internally, image scanning, dependency audit, audit-log retention, GDPR data map.
- LIVE execution is disabled in every environment; the production gate is a code change, not a config flag.

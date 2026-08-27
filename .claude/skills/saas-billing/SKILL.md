---
name: saas-billing
description: Tenancy, packages/entitlements and Paddle billing for the platform: organisations, users, package limits enforced server-side, subscriptions, upgrades, invoices, webhooks, customer portal, affiliate ledger. Use for anything about clients, plans, pricing enforcement or payments.
when_to_use: "packages", "plans", "subscription", "Paddle", "billing", "clients", "tenants", "affiliate"
allowed-tools: Read Grep Glob Write Edit Bash Agent WebFetch WebSearch
argument-hint: [task]
---

# SaaS billing workflow

Task: $ARGUMENTS

- Data model: organisations, memberships (role), packages (code, limits JSON), subscriptions (paddle ids, status, period), entitlements resolved server-side per request (middleware), affiliate accounts + commission ledger.
- Paddle: use the installed paddle plugin skills (catalog setup, checkout, webhooks, subscription update/cancel/sync, customer portal, sandbox testing). Webhooks: signature-verified, idempotent (event id table), replay-safe. No card data on our side.
- Package limits (venues, scanners, alert channels, auto-paper strategies, API access, history depth) are the ONLY source of feature gating; the console reads entitlements from `/api/v1/me`.
- Tests: sandbox lifecycle (trial → paid → upgrade → past due → cancel), entitlement middleware, affiliate accrual math (decimal).
- Never copy a competitor's package names, limits or prices; product-manager owns the package table in docs/design/packages.md.

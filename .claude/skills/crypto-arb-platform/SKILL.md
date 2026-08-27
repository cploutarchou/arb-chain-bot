---
name: crypto-arb-platform
description: Master workflow for the crypto arbitrage product programme (Phases 22–27): Scanner Suite, venue breadth, strategies + auto-paper, SaaS packages/billing/marketing, production infra, and the production execution gate. Use for any request that spans product, packages, clients, infra or "make it a business".
when_to_use: "make it a product", "packages", "clients", "subscriptions", "production", "marketing", "many exchanges", "automate everything", "arbitragescanner"
allowed-tools: Read Grep Glob Write Edit Bash Agent WebFetch WebSearch
argument-hint: [task]
---

# Crypto arbitrage platform programme

Request: $ARGUMENTS

Command of record: docs/design/crypto-arb-platform-command.md (read it in full). Plan: docs/MASTER_PLAN.md Phases 22–27.

## Route the work
- Screener / perps / funding / alerts / auto-paper → `scanner-suite` skill, `screener-engineer`.
- New exchange → `venue-connector` skill, `exchange-connector-engineer`.
- Packages, tenancy, Paddle, affiliates → `saas-billing` skill, `billing-engineer`, `product-manager`.
- Marketing site, copy, legal pages → `marketing-site` skill, `marketing-strategist`, `content-copywriter`, `compliance-reviewer`.
- Environments, deploys, HA, observability → `prod-infra` skill, `infra-sre-engineer`.
- Console layout / design system → `ux-designer` then `ui-designer` then `frontend-engineer` via `console-feature`.
- Strategy math / evidence → `derivatives-quant`, `quant-researcher`.

## Order of operations for a large ask
1. product-manager writes the spec + MASTER_PLAN tasks with acceptance criteria.
2. Design (ux → ui) before frontend; research before connectors; quant model before paper executor.
3. Implement in parallel agents on one branch; one commit per coherent change; `/code-review` before DONE.
4. Every strategy ships with automatic paper execution and a nightly report; no strategy is described as profitable without a report in docs/campaigns/.
5. Production execution stays behind the gate (command §RULES); the deliverable for "production" is production-grade hosting of the paper product until the gate passes.

## Definition of done for the programme
Unattended operation (collectors, rules, paper execution, reports, backups, deploys, alerts), tenant packages enforced server-side, billing lifecycle tested in Paddle sandbox, marketing site live with evidence-based copy and legal pages, infra with SLOs and tested restores.

---
description: Review the working-tree diff for correctness, concurrency, money-math and security defects
agent: code-reviewer
subtask: true
---
Review the current changes adversarially and report; do not edit.

Scope (empty means the whole working tree diff against HEAD): $ARGUMENTS

Changed files:
!`git status --short`

Diff:
!`git diff HEAD --stat`

Read the full diff with `git diff HEAD` (and `git diff HEAD -- <path>` for
the files in scope). Check, in this order: financial correctness (decimal
math, fees, buffers, quantization, slippage baselines, ledger conservation),
concurrency (locks, goroutine lifetimes, shutdown ordering, races), silent
loss of records (queues, outbox, persistence errors), risk-gate bypasses,
security (secrets in logs or responses, RBAC, tenant scope, input validation),
migrations (down file, IF EXISTS, `LatestMigrationVersion`), tests (does a
test pin the fixed behaviour?), and adherence to AGENTS.md. Run the relevant
package tests. Report findings ranked by severity with file and line, a
failure scenario for each, and the fix you would make.

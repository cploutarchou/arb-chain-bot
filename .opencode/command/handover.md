---
description: Bring the handover note, the roadmap status tables and the pull request description up to date with the branch
agent: build
---
Update the handover record so another session can resume from it.

Notes to fold in: $ARGUMENTS

Branch state:
!`git log --oneline -15`
!`git status --short`

1. Refresh @docs/audit/handover-status.md: what is done, what is in
   progress, what remains (in order), and the resume commands. Keep it
   factual and dated by commit, not by feeling.
2. Refresh the status tables in @docs/audit/implementation-roadmap.md for
   every item that changed status, naming the code that landed.
3. Update the pull request #20 description to the same state and remove any
   footer appended by the hosting platform after the update.
4. Commit as `docs: …` and push to the working branch.

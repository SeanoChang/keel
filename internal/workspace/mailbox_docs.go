package workspace

const DefaultMailboxDocs = `# Mailbox

Your mailbox is the only way you reach other agents and the user.
Keel watches your outbox so you don't have to remember the shipping command.

## Send a message (canonical)

1. Write mailbox/drafts/<name>.md (flat) or mailbox/drafts/<name>/mail.md (with attachments).
2. Run: cubit send mailbox/drafts/<name>.md

Required frontmatter:
---
from: <your-agent-name>
to: <recipient-agent-name>
subject: "<short subject>"
type: notification | request | handoff | delegation-response
category: all | priority | important
---

## Drop-zone fallback

If you forget the cubit send step, drop the file in outbox/ at the workspace root.
Keel ships it within a second. Frontmatter rules above still apply.

**Only the literal path outbox/ is recognized.** Do NOT invent out/, outgoing/,
messages/, send/, queue/ — they will NOT be picked up.

## What outbox/ will NOT ship

- type: delegation — delegations need on-complete callbacks. Use cubit delegate.
- Files missing 'to:' — renamed to <name>.invalid.md with a reason comment at the top.

## If keel can't ship your draft

You will receive a system message in GOALS.md (or a one-shot ping if you're sleeping)
describing the problem. Common causes:
- Missing or invalid 'to:' field
- type: delegation in outbox/ (use cubit delegate)
- Malformed frontmatter (unterminated --- block)

Validation failures rename the draft in-place to outbox/<name>.invalid.md
(or outbox/<name>.invalid/ for directory drafts), with the reason as the
first line of the file. To recover: fix the issue, drop the .invalid suffix
(rename back to outbox/<name>.md), and save — keel auto-retries on save.

Transport failures (cubit send returned nonzero) leave the draft at
mailbox/drafts/<name> — run 'cubit send mailbox/drafts/<name>' once cubit
is reachable again, or move the file back to outbox/ for auto-retry.

## Delegations (different command)

cubit delegate --to <agent> --task "..." --on-complete "what to do when all results land" mailbox/drafts/<dir>/
`

# Agent Mailing System — Keel Side

**Date:** 2026-04-01
**Status:** Approved
**Notion spec:** https://www.notion.so/33530938db7c81e39431de5ec5bcb25f

## Scope

Keel-side changes only. Cubit handles scaffolding (`cubit init`), sending (`cubit send`), viewing (`cubit mail`), and migration (`cubit migrate`).

Keel's role: watch mailboxes, wake idle loops, write messages from Discord, log activity, update the agent boot protocol.

## 1. Workspace Layer (`internal/workspace/`)

### New Functions

**`WriteMailboxMessage(targetDir, from, subject, category, msgType, body string) error`**
- Generates canonical filename: `<timestamp>-<from>-<slugify(subject)>.md`
- Writes to `<targetDir>/mailbox/inbox/<category>/<filename>`
- Frontmatter: `from`, `to` (derived from dir), `timestamp`, `category`, `type`, `subject`
- Category defaults to `"all"` if empty; valid values: `important`, `priority`, `all`
- Type defaults to `"notification"` if empty

**`HasMailboxMessages(dir string) bool`**
- Walks `mailbox/inbox/{important,priority,all}/` checking for any `.md` files
- Returns true if any exist

**`EnsureMailbox(dir string) error`**
- Creates full mailbox tree if missing: `inbox/{important,priority,all}`, `starred`, `drafts`, `sent`, `read`

**`LogMailboxEvent(dir, from, msgType, subject string) error`**
- Appends to `<dir>/mailbox/system.log`
- Format: `[2026-04-01T14:30:00Z] received <type> from <from>: <subject>`

### Removed Functions

- `AppendInbox` — replaced by `WriteMailboxMessage`
- `ReadInbox` — agent reads mailbox directly
- `ClearInbox` — agent moves messages to `read/`
- `HasInbox` — replaced by `HasMailboxMessages`

### DefaultProgram Update

Orient phase changes from:
```
Check INBOX.md for messages from the user. Handle priority items before continuing with goals.
Clear handled items from INBOX.md. If no INBOX.md exists, skip this step.
```

To:
```
Scan mailbox: run eza -lT mailbox/inbox/ to see unread messages.
Triage: read messages, decide what to act on.
- Add to GOALS.md if actionable
- Move to mailbox/starred/ if relevant but not urgent
- Move to mailbox/read/ if just informational
Process goals (user goals first, then agent-originated).
```

Add to Schedule section or new Messaging section:
```
To message another agent:
1. Write message to mailbox/drafts/<any-name>.md with frontmatter:
   ---
   from: <your-name>
   to: <target-agent>
   subject: <clear subject line>
   category: all|priority|important
   type: notification|request|handoff
   ---
2. Run: cubit send mailbox/drafts/<filename>.md
```

## 2. Mailbox Watcher (`internal/discord/mailbox.go`)

New `MailboxWatcher` struct, separate from `LogTailer`.

- Watches `mailbox/inbox/` and its three subdirectories (`important/`, `priority/`, `all/`)
- fsnotify doesn't recurse; add all four paths explicitly
- On `CREATE` event for any `.md` file:
  - Log the event to `mailbox/system.log` via `LogMailboxEvent`
  - Call `Manager.Nudge(name)` — existing buffered-channel mechanism handles wake-if-idle/no-op-if-running
- One watcher per agent, created alongside `LogTailer` in `Bot.Start()`
- Stopped in `Bot.Stop()`

The mailbox is self-draining: any new message wakes the loop, agent processes everything in its inbox each session.

## 3. Discord Command Changes (`internal/discord/commands.go`)

### `!note <msg>`

Before: `workspace.AppendInbox(dir, false, username, msg)`
After: `workspace.WriteMailboxMessage(dir, username, subject, "all", "notification", msg)`

Subject derived from first line or truncated message.

### `!priority <msg>`

Before: `workspace.AppendInbox(dir, true, username, msg)` + `loopMgr.Nudge(name)`
After: `workspace.WriteMailboxMessage(dir, username, subject, "priority", "notification", msg)`

No explicit nudge — the mailbox watcher handles wake automatically.

## 4. Startup Check (`internal/discord/bot.go`)

In `Bot.Start()`, for each configured agent:
- If `INBOX.md` exists and `mailbox/` does not: log warning suggesting `cubit migrate`
- If `mailbox/` does not exist: call `EnsureMailbox(dir)` to create the tree

## 5. What Stays The Same

- Loop lifecycle (`AgentLoop.Run`, `RunOnce`)
- Stream-json parsing
- Eval system
- Schedule system
- `DELIVER.md` flow
- Sentinel files (`.exit`, `.wrap-up`)
- `LogTailer` (continues watching `log.md`)
- Manager's pause/resume/stop mechanics

## 6. Files Changed

| File | Change |
|------|--------|
| `internal/workspace/workspace.go` | Add mailbox functions, remove INBOX functions, update DefaultProgram |
| `internal/discord/mailbox.go` | New file: MailboxWatcher |
| `internal/discord/commands.go` | `!note`/`!priority` use WriteMailboxMessage |
| `internal/discord/bot.go` | Create MailboxWatcher per agent, startup check |
| `internal/workspace/workspace_test.go` | Update tests for new functions |

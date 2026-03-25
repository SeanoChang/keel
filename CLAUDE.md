# Keel

Agent loop manager and Discord bridge for the Ark of Noah ecosystem.

## Build

```bash
go build -o keel .
```

## Test

```bash
go test ./... -v
```

## Commands

- `keel run <agent>` — run a single agent loop (CLI, no Discord)
- `keel serve` — start Discord bot with channel-per-agent mapping
- `keel status <agent>` — show agent status (goals, memory, log tail)
- `keel schedule add <agent> <time> <name> <content>` — schedule a future goal
- `keel schedule ls <agent>` — list scheduled goals
- `keel schedule rm <agent> <name>` — remove a scheduled goal
- `keel schedule clear <agent>` — remove all scheduled goals

## Architecture

Single Go binary. Filesystem is the protocol — no MCP, no custom IPC.

- `internal/workspace/` — file I/O helpers for agent directories (GOALS.md, MEMORY.md, log.md, PROGRAM.md, DELIVER.md, INBOX.md)
- `internal/agent/` — Agent struct wrapping a workspace directory
- `internal/loop/` — AgentLoop (runs `claude --agent`, heartbeat, graceful SIGTERM) + Manager (goroutine-per-agent, pause/resume)
- `internal/eval/` — EVAL.md parser and metric comparison for evaluation loops
- `internal/config/` — TOML config for Discord channel-to-agent mappings and managed binary definitions
- `internal/schedule/` — schedule scanning, cron matching, goal injection
- `internal/discord/` — Discord bot, ! commands, log.md tailing via fsnotify, scheduler goroutine
- `cmd/` — Cobra CLI commands (run, serve, status, schedule)

## Config

Copy `config/discord.example.toml` to `config/discord.toml` and fill in your Discord bot token env var, guild ID, channel-to-agent mappings, and optional managed binary definitions.

### Managed Binaries

Add `[managed_binaries.<name>]` sections to run external update commands via Discord (`!<name>-update`). Each entry needs `update_cmd` (command array, e.g. `["nark", "update"]`).

## Agent Directory Layout

Each agent is a directory under `~/.ark/agents-home/<name>/` with:
- `GOALS.md` — objectives (human adds, agent removes when complete; agent also adds self-directed sub-goals via Reflect)
- `PROGRAM.md` — instructions for how the agent should work (DefaultProgram includes Orient → Execute → Reflect → Log → Deliver → Continue/Exit)
- `MEMORY.md` — agent-maintained working context
- `log.md` — append-only accomplishment log
- `DELIVER.md` — deliverable content relayed to Discord channel, cleared after delivery
- `INBOX.md` — mid-session messages from users (keel writes via `!note`/`!priority`, agent reads and clears)
- `.claude/agents/<name>.md` — Claude Code agent definition
- `schedule/` — self-scheduled future goals (see below)
- `projects/` — persistent versioned work, each subdirectory is a git repo (managed via cubit)
- `.exit` — sentinel file: agent creates when all goals AND follow-up directions are exhausted (loop stops)
- `.wrap-up` — sentinel file: `!wrap-up` creates to request graceful stop with archive

Goals tagged `[quick]` are completed directly without follow-up branching. All other goals default to deep work with self-directed branching via the Reflect step.

## Schedule

Agents can self-schedule future goals via filesystem:

```
<agent-dir>/schedule/
├── 2026-03-13T08:30/          # one-shot (ISO datetime, local time)
│   └── check-pce.md           # content = goal text injected into GOALS.md
└── cron-30_8_*_*_1-5/         # recurring (cron, underscores separate fields)
    └── morning-brief.md
```

CLI: `keel schedule add <agent> <time> <name> <content>`
Discord: `!schedule` to list upcoming, `!wrap-up` to gracefully stop, `!update` to update keel, `!<name>-update` to update managed binaries.

## Mid-Loop Interaction

While the agent loop is running, you can interact without interrupting:

- `!ask <msg>` — one-shot question answered by a separate subprocess (doesn't affect the loop)
- `!note <msg>` — leave a note in INBOX.md (agent reads at next session start)
- `!priority <msg>` — leave a priority note (agent handles before goals, also nudges the loop)
- `!pause` — pause the loop after the current session completes
- `!resume` — resume a paused loop

## Evaluation Loop

Opt-in per project. Create `projects/<name>/EVAL.md` with YAML frontmatter:

```yaml
---
metric: conversion_rate
direction: higher
baseline: 0.41
budget: 50.00
max_no_improve: 10
---
```

The agent writes metric JSON files to `projects/<name>/metrics/`. After each session, keel scans for new metrics, tracks improvement, enforces budget limits, and detects convergence. On regression, keel injects context into INBOX.md so the agent can decide how to respond (revert, adjust, or try a different approach).

One-shot dirs are deleted after firing. Cron dirs persist with `.last-fired` guard.
A 60-second ticker goroutine in `keel serve` scans all agent schedule dirs and fires due entries.

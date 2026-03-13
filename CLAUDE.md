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

- `internal/workspace/` — file I/O helpers for agent directories (GOALS.md, MEMORY.md, log.md, PROGRAM.md)
- `internal/agent/` — Agent struct wrapping a workspace directory
- `internal/loop/` — AgentLoop (runs `claude --agent`) + Manager (goroutine-per-agent)
- `internal/config/` — TOML config for Discord channel-to-agent mappings
- `internal/schedule/` — schedule scanning, cron matching, goal injection
- `internal/discord/` — Discord bot, ! commands, log.md tailing via fsnotify, scheduler goroutine
- `cmd/` — Cobra CLI commands (run, serve, status, schedule)

## Config

Copy `config/discord.example.toml` to `config/discord.toml` and fill in your Discord bot token env var, guild ID, and channel-to-agent mappings.

## Agent Directory Layout

Each agent is a directory under `~/.ark/agents-home/<name>/` with:
- `GOALS.md` — objectives (human adds, agent removes when complete)
- `PROGRAM.md` — instructions for how the agent should work
- `MEMORY.md` — agent-maintained working context
- `log.md` — append-only accomplishment log
- `.claude/agents/<name>.md` — Claude Code agent definition
- `schedule/` — self-scheduled future goals (see below)

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
Discord: `!schedule` to list upcoming.

One-shot dirs are deleted after firing. Cron dirs persist with `.last-fired` guard.
A 60-second ticker goroutine in `keel serve` scans all agent schedule dirs and fires due entries.

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

## Architecture

Single Go binary. Filesystem is the protocol — no MCP, no custom IPC.

- `internal/workspace/` — file I/O helpers for agent directories (GOALS.md, MEMORY.md, log.md, PROGRAM.md)
- `internal/agent/` — Agent struct wrapping a workspace directory
- `internal/loop/` — AgentLoop (runs `claude --agent`) + Manager (goroutine-per-agent)
- `internal/config/` — TOML config for Discord channel-to-agent mappings
- `internal/discord/` — Discord bot, ! commands, log.md tailing via fsnotify
- `cmd/` — Cobra CLI commands (run, serve, status)

## Config

Copy `config/discord.example.toml` to `config/discord.toml` and fill in your Discord bot token env var, guild ID, and channel-to-agent mappings.

## Agent Directory Layout

Each agent is a directory under `~/.ark/<name>/` with:
- `GOALS.md` — objectives (human adds, agent removes when complete)
- `PROGRAM.md` — instructions for how the agent should work
- `MEMORY.md` — agent-maintained working context
- `log.md` — append-only accomplishment log
- `.claude/agents/<name>.md` — Claude Code agent definition

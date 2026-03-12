# Keel

Agent loop manager and Discord bridge for the [Ark of Noah](https://github.com/SeanoChang) ecosystem.

Keel wraps [Claude Code](https://docs.anthropic.com/en/docs/claude-code) in a while-loop: it reads `GOALS.md`, runs a Claude session, and repeats until the goals are done. In **serve** mode it bridges Discord channels 1:1 to agent workspaces — messages become goals, agent logs stream back.

## Install

### Download (recommended)

Grab the latest binary from [Releases](https://github.com/SeanoChang/keel/releases):

```bash
# macOS (Apple Silicon)
curl -Lo keel https://github.com/SeanoChang/keel/releases/latest/download/keel-darwin-arm64
chmod +x keel && sudo mv keel /usr/local/bin/

# Linux (x86_64)
curl -Lo keel https://github.com/SeanoChang/keel/releases/latest/download/keel-linux-amd64
chmod +x keel && sudo mv keel /usr/local/bin/
```

### Build from source

Requires Go 1.22+.

```bash
git clone https://github.com/SeanoChang/keel.git
cd keel
go build -o keel .
```

### Self-update

```bash
keel update
```

Downloads the latest release for your platform and migrates agent workspaces.

## Quick Start

### 1. Create an agent workspace

```bash
mkdir -p ~/.ark/agents-home/noah
echo "## Build the landing page" > ~/.ark/agents-home/noah/GOALS.md
```

### 2. Run it

```bash
keel run noah
```

Keel loops: read goals → run `claude --agent` → sleep → repeat, until `GOALS.md` is empty.

## Commands

| Command | Description |
|---------|-------------|
| `keel run <agent>` | Run a single agent loop (CLI, no Discord) |
| `keel serve` | Start the Discord bot with channel-per-agent mapping |
| `keel status <agent>` | Show goals, memory token count, log tail |
| `keel update` | Pull latest binary from GitHub and run workspace migrations |
| `keel --version` | Print the current keel version |

### `keel run`

```
keel run <agent> [--dir <path>] [--sleep <duration>]
```

- `--dir` — Agent directory (default: `~/.ark/agents-home/<agent>`)
- `--sleep` — Pause between sessions (default: `5s`)

Exits when `GOALS.md` is empty or on `Ctrl+C`.

### `keel serve`

```
keel serve [--config <path>] [--sleep <duration>] [--archive-every <n>]
```

- `--config` — Path to Discord config TOML (default: `config/discord.toml`)
- `--sleep` — Pause between agent sessions (default: `5s`)
- `--archive-every` — Run cubit archive every N sessions, 0 to disable (default: `50`)

Each Discord channel maps to one agent. Messages become goals; agent logs stream back via `fsnotify`.

### `keel update`

```
keel update [--migrate-only]
```

- `--migrate-only` — Skip binary download, run workspace migrations only

Migrations ensure all agent dirs under `~/.ark/agents-home/` have required files (`GOALS.md`, `MEMORY.md`, `log.md`, `PROGRAM.md`).

## Discord Setup

### 1. Create a bot

Create a Discord bot at [discord.com/developers](https://discord.com/developers/applications) with the **Message Content** intent enabled.

### 2. Configure

```bash
cp config/discord.example.toml config/discord.toml
```

Edit `config/discord.toml`:

```toml
[bot]
token_env = "DISCORD_BOT_TOKEN"    # env var containing your bot token
guild_id = "YOUR_GUILD_ID"
status_channel_id = ""              # optional broadcast channel

[channels.noah]
channel_id = "123456789012345678"
agent_dir = "~/.ark/agents-home/noah"

[channels.atlas]
channel_id = "987654321098765432"
agent_dir = "~/.ark/agents-home/atlas"
```

Each `[channels.<name>]` section maps a Discord channel to an independent agent workspace.

### 3. Run

```bash
export DISCORD_BOT_TOKEN="your-token-here"
keel serve
```

### Discord Commands

Type these in any mapped channel:

| Command | Description |
|---------|-------------|
| `!status` | Show agent status |
| `!goals` | Print current GOALS.md |
| `!log [n]` | Show last n lines of log.md (default: 20) |
| `!memory` | Show MEMORY.md token count |
| `!start` | Start the agent loop |
| `!stop` | Stop the agent loop |
| `!clear` | Clear GOALS.md (loop stops after current session) |

Any non-command message is appended to `GOALS.md` as a timestamped goal, and the agent loop starts automatically if it isn't already running.

## Agent Directory Layout

Each agent lives at `~/.ark/agents-home/<name>/`:

```
~/.ark/agents-home/noah/
├── GOALS.md          # Objectives — human adds, agent removes when complete
├── PROGRAM.md        # Instructions for how the agent should behave
├── MEMORY.md         # Agent-maintained working context
├── log.md            # Append-only accomplishment log
└── .claude/agents/
    └── noah.md       # Claude Code agent definition
```

## Architecture

Single Go binary. **Filesystem is the protocol** — no MCP, no custom IPC.

```
Discord message
  → config.ResolveChannel(channelID)
  → workspace.AppendGoal(dir, user, message)
  → loop.Manager.Start(agent, dir)
      → goroutine: while HasGoals → RunOnce (exec claude --agent) → sleep
  → tail.LogTailer watches log.md via fsnotify → posts new lines back to Discord
```

### Packages

| Package | Responsibility |
|---------|---------------|
| `cmd/` | Cobra CLI (run, serve, status, update) |
| `internal/workspace/` | File I/O for GOALS.md, MEMORY.md, log.md, PROGRAM.md |
| `internal/agent/` | Agent struct wrapping a workspace directory |
| `internal/loop/` | AgentLoop (outer while-loop) + Manager (goroutine-per-agent) |
| `internal/config/` | TOML config parsing, channel-to-agent resolution |
| `internal/discord/` | Discord bot, `!` commands, fsnotify log tailing |
| `internal/migrate/` | Idempotent workspace migrations |

## Releasing

Releases are automated via GitHub Actions. Push a tag to create a release with pre-built binaries:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Binaries built: `keel-darwin-arm64` (Apple Silicon), `keel-linux-amd64` (Linux x86_64).

## License

MIT

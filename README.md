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

Downloads the latest release for your platform.

## Quick Start

### 1. Create an agent workspace

Use [cubit](https://github.com/SeanoChang/cubit) to scaffold a workspace:

```bash
cubit init noah
```

Or manually:

```bash
mkdir -p ~/.ark/agents-home/noah
echo "## Build the landing page" > ~/.ark/agents-home/noah/GOALS.md
```

### 2. Run it

```bash
keel run noah
```

Keel loops: read goals -> run `claude --agent` -> sleep -> repeat, until `GOALS.md` is empty. Sessions that produce no output for 5 minutes are automatically killed and retried (with exponential backoff).

## Commands

| Command | Description |
|---------|-------------|
| `keel run <agent>` | Run a single agent loop (CLI, no Discord) |
| `keel serve` | Start the Discord bot with channel-per-agent mapping |
| `keel status <agent>` | Show goals, memory token count, log tail |
| `keel schedule add <agent> <time> <name> <content>` | Schedule a future goal |
| `keel schedule ls <agent>` | List scheduled goals |
| `keel schedule rm <agent> <name>` | Remove a scheduled goal |
| `keel schedule clear <agent>` | Remove all scheduled goals |
| `keel update` | Pull latest binary from GitHub |
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
| `!ask <msg>` | One-shot question (works while loop is running) |
| `!note <msg>` | Leave a note for the agent (read next session) |
| `!priority <msg>` | Priority note (handled before goals, nudges loop) |
| `!status` | Show agent status |
| `!goals` | Print current GOALS.md |
| `!log [n]` | Show last n lines of log.md (default: 20) |
| `!memory` | Show MEMORY.md token count |
| `!start` | Start the agent loop |
| `!stop` | Stop the agent loop |
| `!pause` | Pause the loop after the current session |
| `!resume` | Resume a paused loop |
| `!wrap-up [msg]` | Finish current work gracefully and stop |
| `!schedule` | Show scheduled goals |
| `!clear` | Stop the loop and clear GOALS.md |
| `!update` | Update keel to latest release and restart |
| `!<name>-update` | Update a managed binary (e.g. `!nark-update`) |

Any non-command message is appended to `GOALS.md` as a timestamped goal, and the agent loop starts automatically if it isn't already running.

## Agent Directory Layout

Each agent lives at `~/.ark/agents-home/<name>/` (scaffolded by [cubit](https://github.com/SeanoChang/cubit)):

```
~/.ark/agents-home/noah/
├── GOALS.md              # Objectives — human adds, agent removes when complete
├── PROGRAM.md            # Instructions for how the agent should behave
├── MEMORY.md             # Agent-maintained working context
├── log.md                # Append-only accomplishment log
├── DELIVER.md            # Deliverable content relayed to Discord, cleared after delivery
├── INBOX.md              # Mid-loop messages from users (keel writes, agent reads/clears)
├── schedule/             # Self-scheduled future goals (cron + one-shot)
├── scratch/              # Ephemeral temp work (cleaned on archive)
├── projects/             # Persistent versioned work
│   ├── market-trends/    # Each project is its own git repo
│   └── trading-opt/      # May contain EVAL.md + metrics/ for evaluation
└── .claude/agents/
    └── noah.md           # Claude Code agent definition
```

The workspace itself is **not** a git repo. Git repos live inside `projects/`, each tracking a distinct line of work. Agents manage their own projects via cubit (`cubit project new`, `cubit project search`, etc.).

## Mid-Loop Interaction

While the agent loop is running, you can interact without interrupting:

- **`!ask <msg>`** — Spawns a separate Claude subprocess that answers your question using the agent's workspace context. Does not affect the running loop.
- **`!note <msg>`** — Appends a note to `INBOX.md`. The agent reads it at the start of its next session.
- **`!priority <msg>`** — Same as note but flagged urgent. The agent handles priority items before continuing with goals. Also nudges the loop to wake up sooner.
- **`!pause`** — The loop pauses after the current session completes. Use `!resume` to continue.

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

The agent writes metric JSON files to `projects/<name>/metrics/`:

```json
{"value": 0.67, "iteration": 12, "timestamp": "2026-03-25T14:30:00Z"}
```

After each session, keel:
1. Scans `projects/` for directories containing `EVAL.md`
2. Reads the latest metric file and compares to previous
3. Tracks improvement, enforces budget limits, detects convergence
4. On regression: injects context into `INBOX.md` so the agent can decide how to respond (revert, adjust, or try a different approach)
5. Reports metric updates to Discord

The loop stops automatically when the budget is exceeded or no improvement is detected for `max_no_improve` consecutive iterations.

## Schedule

Agents can self-schedule future goals via filesystem:

```
<agent-dir>/schedule/
├── 2026-03-13T08:30/          # one-shot (ISO datetime, local time)
│   └── check-pce.md           # content = goal text injected into GOALS.md
└── cron-30_8_*_*_1-5/         # recurring (cron, underscores separate fields)
    └── morning-brief.md
```

One-shot dirs are deleted after firing. Cron dirs persist with `.last-fired` guard. A 60-second ticker goroutine in `keel serve` scans all agent schedule dirs and fires due entries.

CLI: `keel schedule add <agent> <time> <name> <content>`

## Architecture

Single Go binary. **Filesystem is the protocol** — no MCP, no custom IPC.

```
Discord message
  -> config.ResolveChannel(channelID)
  -> workspace.AppendGoal(dir, user, message)
  -> loop.Manager.Start(agent, dir)
      -> goroutine: while HasGoals -> RunOnce (exec claude --agent, stuck watchdog) -> checkEval -> sleep
  -> tail.LogTailer watches log.md via fsnotify -> posts new lines back to Discord
```

### Packages

| Package | Responsibility |
|---------|---------------|
| `cmd/` | Cobra CLI (run, serve, status, schedule, update) |
| `internal/workspace/` | File I/O for GOALS.md, MEMORY.md, log.md, PROGRAM.md, DELIVER.md, INBOX.md |
| `internal/agent/` | Agent struct wrapping a workspace directory |
| `internal/loop/` | AgentLoop (outer while-loop, stuck watchdog, eval check, pause/resume) + Manager (goroutine-per-agent) |
| `internal/eval/` | EVAL.md parser, metric comparison, budget/convergence detection |
| `internal/config/` | TOML config parsing, channel-to-agent resolution |
| `internal/schedule/` | Schedule scanning, cron matching, goal injection |
| `internal/discord/` | Discord bot, `!` commands, fsnotify log tailing, scheduler goroutine |
| `internal/update/` | Self-update from GitHub releases |

## Releasing

Releases are automated via GitHub Actions. Push a tag to create a release with pre-built binaries:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Binaries built: `keel-darwin-arm64` (Apple Silicon), `keel-linux-amd64` (Linux x86_64).

## License

MIT

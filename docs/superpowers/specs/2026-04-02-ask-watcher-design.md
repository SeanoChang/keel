# AskWatcher — Keel Phase 1

**Date:** 2026-04-02
**Status:** Approved
**Notion spec:** https://www.notion.so/33630938db7c81e0bb85f8729dbf9ee1

## Scope

New fsnotify watcher that monitors `asks/pending/` for all managed agents. When new ask JSON files appear, debounces for 5 seconds per agent, then fires `cubit ask process <agent>` as a subprocess. Parallel-safe — no interaction with the main agent loop.

## 1. AskWatcher (`internal/discord/ask_watcher.go`)

```go
type AskWatcher struct {
    agentDirs map[string]string    // agent name → workspace dir
    watcher   *fsnotify.Watcher
    timers    map[string]*time.Timer // per-agent 5s debounce
    stop      chan struct{}
    mu        sync.Mutex
}
```

### Constructor

`NewAskWatcher(agentDirs map[string]string) *AskWatcher`

Takes a map of agent name → workspace directory. Creates fsnotify watcher but does not start it.

### Start

- Adds `<dir>/asks/pending/` to fsnotify for each agent
- Silently skips agents where the directory doesn't exist (cubit may not have scaffolded yet)
- Event loop: on `fsnotify.Create` of `*.json` → reset/start 5s debounce timer for that agent
- When timer fires → spawn goroutine running `cubit ask process <agentName>` with `cmd.Dir = agentDir`
- Errors from subprocess are logged, never fatal

### Stop

- Closes stop channel, closes fsnotify watcher
- `sync.Once` guard (same pattern as MailboxWatcher)

### Debounce Logic

Per-agent `*time.Timer` stored in `timers` map. On each CREATE event:
1. Lock mutex
2. If timer exists for agent, reset it (`timer.Stop()` + `timer.Reset(5s)`)
3. If no timer, create one with `time.AfterFunc(5s, processFunc)`
4. Unlock

When the timer fires, delete it from the map (under lock) before spawning the subprocess.

### Agent-to-Directory Mapping

The watcher needs to map fsnotify event paths back to agent names. On CREATE event:
- Extract the agent dir from the event path (parent of `asks/pending/`)
- Look up agent name from the reverse map

## 2. Workspace Changes (`internal/workspace/`)

### `EnsureAskDirs(dir string) error`

Creates `asks/pending/` and `asks/done/` under the agent directory. Called at bot startup alongside `EnsureMailbox`.

## 3. Bot Integration (`internal/discord/bot.go`)

### Start

In `bot.Start()`, after mailbox watchers:
1. Call `workspace.EnsureAskDirs(ch.AgentDir)` for each agent
2. Build `agentDirs` map from config
3. Create and start AskWatcher

### Stop

In `bot.Stop()`, call `askWatcher.Stop()`.

### Bot Struct

Add `askWatcher *AskWatcher` field.

## 4. What This Does NOT Do

- No Discord commands (Phase 3)
- No TTL checking (Phase 4)
- No ask content reading or interpretation
- No interaction with agent loops — fully parallel
- No special Librarian handling — watches all agents equally

## 5. Files Changed

| File | Change |
|------|--------|
| `internal/discord/ask_watcher.go` | New file: AskWatcher |
| `internal/workspace/mailbox.go` | Add `EnsureAskDirs` |
| `internal/workspace/mailbox_test.go` | Test for `EnsureAskDirs` |
| `internal/discord/bot.go` | Add askWatcher field, start/stop |

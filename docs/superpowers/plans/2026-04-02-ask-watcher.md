# AskWatcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Watch `asks/pending/` for all managed agents and trigger `cubit ask process` with 5-second debounce when new ask JSON files appear.

**Architecture:** New `AskWatcher` struct in `internal/discord/ask_watcher.go` using fsnotify (same dependency as MailboxWatcher/LogTailer). Per-agent debounce via `time.AfterFunc`. Integrated into Bot lifecycle alongside existing watchers. `EnsureAskDirs` added to workspace layer.

**Tech Stack:** Go, fsnotify (existing dependency)

---

### Task 1: EnsureAskDirs workspace function

**Files:**
- Modify: `internal/workspace/mailbox.go`
- Modify: `internal/workspace/mailbox_test.go`

- [ ] **Step 1: Write failing test for EnsureAskDirs**

Append to `internal/workspace/mailbox_test.go`:

```go
func TestEnsureAskDirs(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureAskDirs(dir); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"asks/pending", "asks/done"} {
		path := filepath.Join(dir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}
	// Idempotent
	if err := EnsureAskDirs(dir); err != nil {
		t.Errorf("second call should be idempotent: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./internal/workspace/ -run TestEnsureAskDirs -v`
Expected: FAIL — `EnsureAskDirs` not defined

- [ ] **Step 3: Implement EnsureAskDirs**

Add to `internal/workspace/mailbox.go`:

```go
// EnsureAskDirs creates the ask queue directory tree if missing.
func EnsureAskDirs(dir string) error {
	for _, sub := range []string{"asks/pending", "asks/done"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			return fmt.Errorf("create %s: %w", sub, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./internal/workspace/ -run TestEnsureAskDirs -v`
Expected: PASS

---

### Task 2: AskWatcher implementation

**Files:**
- Create: `internal/discord/ask_watcher.go`

- [ ] **Step 1: Create AskWatcher**

Create `internal/discord/ask_watcher.go`:

```go
package discord

import (
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const askDebounce = 5 * time.Second

// AskWatcher watches asks/pending/ for all managed agents and triggers
// cubit ask process with a per-agent debounce when new ask JSON files appear.
type AskWatcher struct {
	agentDirs map[string]string // agent name → workspace dir
	dirToName map[string]string // pending dir path → agent name (reverse lookup)
	watcher   *fsnotify.Watcher
	timers    map[string]*time.Timer
	stop      chan struct{}
	once      sync.Once
	mu        sync.Mutex
}

func NewAskWatcher(agentDirs map[string]string) (*AskWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &AskWatcher{
		agentDirs: agentDirs,
		dirToName: make(map[string]string),
		watcher:   w,
		timers:    make(map[string]*time.Timer),
		stop:      make(chan struct{}),
	}, nil
}

func (aw *AskWatcher) Start() {
	// Add watches for each agent's asks/pending/ directory
	for name, dir := range aw.agentDirs {
		pendingDir := filepath.Join(dir, "asks", "pending")
		if err := aw.watcher.Add(pendingDir); err != nil {
			// Directory may not exist yet — that's fine, skip silently
			log.Printf("[keel] %s: ask watcher skipping (no asks/pending/): %v", name, err)
			continue
		}
		aw.dirToName[pendingDir] = name
	}

	for {
		select {
		case <-aw.stop:
			return
		case event, ok := <-aw.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) && strings.HasSuffix(event.Name, ".json") {
				pendingDir := filepath.Dir(event.Name)
				name, ok := aw.dirToName[pendingDir]
				if !ok {
					continue
				}
				log.Printf("[keel] %s: new ask detected: %s", name, filepath.Base(event.Name))
				aw.debounce(name)
			}
		case err, ok := <-aw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[keel] ask watcher error: %v", err)
		}
	}
}

func (aw *AskWatcher) Stop() {
	aw.once.Do(func() {
		close(aw.stop)
		aw.watcher.Close()
	})
}

// debounce resets the per-agent timer. When it fires, processAsks runs.
func (aw *AskWatcher) debounce(agentName string) {
	aw.mu.Lock()
	defer aw.mu.Unlock()

	if t, ok := aw.timers[agentName]; ok {
		t.Stop()
	}
	aw.timers[agentName] = time.AfterFunc(askDebounce, func() {
		aw.mu.Lock()
		delete(aw.timers, agentName)
		aw.mu.Unlock()
		aw.processAsks(agentName)
	})
}

// processAsks runs cubit ask process for the given agent.
func (aw *AskWatcher) processAsks(agentName string) {
	dir, ok := aw.agentDirs[agentName]
	if !ok {
		return
	}
	log.Printf("[keel] %s: running cubit ask process", agentName)
	cmd := exec.Command("cubit", "ask", "process", agentName)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[keel] %s: ask process error: %v\n%s", agentName, err, output)
	} else {
		log.Printf("[keel] %s: ask process complete", agentName)
	}
}
```

- [ ] **Step 2: Verify build**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go build ./...`
Expected: Success

---

### Task 3: Bot integration

**Files:**
- Modify: `internal/discord/bot.go`

- [ ] **Step 1: Add askWatcher field to Bot struct**

In `internal/discord/bot.go`, find the Bot struct and add after `mailboxWatchers`:

```go
	askWatcher      *AskWatcher
```

- [ ] **Step 2: Start AskWatcher in Bot.Start**

In `Bot.Start()`, after the mailbox watcher loop and before `go b.runScheduler()`, add:

```go
	// Start ask watcher for all agents
	askDirs := make(map[string]string)
	for name, ch := range b.cfg.Channels {
		workspace.EnsureAskDirs(ch.AgentDir)
		askDirs[name] = ch.AgentDir
	}
	if aw, err := NewAskWatcher(askDirs); err != nil {
		log.Printf("[keel] ask watcher init error: %v", err)
	} else {
		b.askWatcher = aw
		go aw.Start()
	}
```

- [ ] **Step 3: Stop AskWatcher in Bot.Stop**

In `Bot.Stop()`, after stopping mailbox watchers, add:

```go
	if b.askWatcher != nil {
		b.askWatcher.Stop()
	}
```

- [ ] **Step 4: Verify build and tests**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go build ./... && go test ./... -v`
Expected: All pass

---

### Task 4: Verification

**Files:** None (verification only)

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./... -v`
Expected: All tests pass

- [ ] **Step 2: Build binary**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go build -o /dev/null .`
Expected: Success

- [ ] **Step 3: Verify no compilation issues**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go vet ./...`
Expected: No issues

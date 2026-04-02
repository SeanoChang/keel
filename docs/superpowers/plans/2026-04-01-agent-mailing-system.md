# Agent Mailing System — Keel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace INBOX.md with a filesystem-based mailbox system that unifies human→agent and agent→agent messaging, with keel as the watcher/waker.

**Architecture:** New `internal/workspace/mailbox.go` for mailbox file operations. New `internal/discord/mailbox.go` for fsnotify-based MailboxWatcher. Migrate all INBOX.md callers (loop.go, commands.go) to use `WriteMailboxMessage`. Update DefaultProgram and bootstrapPrompt to reference mailbox.

**Tech Stack:** Go, fsnotify (already a dependency), YAML frontmatter

---

### Task 1: Mailbox workspace functions

**Files:**
- Create: `internal/workspace/mailbox.go`
- Create: `internal/workspace/mailbox_test.go`

- [ ] **Step 1: Write failing tests for EnsureMailbox**

In `internal/workspace/mailbox_test.go`:

```go
package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureMailbox(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureMailbox(dir); err != nil {
		t.Fatal(err)
	}

	// Verify all directories exist
	for _, sub := range []string{
		"mailbox/inbox/important",
		"mailbox/inbox/priority",
		"mailbox/inbox/all",
		"mailbox/starred",
		"mailbox/drafts",
		"mailbox/sent",
		"mailbox/read",
	} {
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

	// Calling again should be idempotent
	if err := EnsureMailbox(dir); err != nil {
		t.Errorf("second call should be idempotent: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./internal/workspace/ -run TestEnsureMailbox -v`
Expected: FAIL — `EnsureMailbox` not defined

- [ ] **Step 3: Implement EnsureMailbox**

In `internal/workspace/mailbox.go`:

```go
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Mailbox subdirectory layout.
var mailboxDirs = []string{
	"mailbox/inbox/important",
	"mailbox/inbox/priority",
	"mailbox/inbox/all",
	"mailbox/starred",
	"mailbox/drafts",
	"mailbox/sent",
	"mailbox/read",
}

// EnsureMailbox creates the full mailbox directory tree if any part is missing.
func EnsureMailbox(dir string) error {
	for _, sub := range mailboxDirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			return fmt.Errorf("create %s: %w", sub, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./internal/workspace/ -run TestEnsureMailbox -v`
Expected: PASS

- [ ] **Step 5: Write failing tests for slugify and WriteMailboxMessage**

Append to `internal/workspace/mailbox_test.go`:

```go
func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Found a regression in auth module", "found-a-regression-in-auth-module"},
		{"Review API Endpoints!", "review-api-endpoints"},
		{"phase-1 complete", "phase-1-complete"},
		{"  extra   spaces  ", "extra-spaces"},
		{"UPPERCASE", "uppercase"},
	}
	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWriteMailboxMessage(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureMailbox(dir); err != nil {
		t.Fatal(err)
	}

	err := WriteMailboxMessage(dir, "sean", "Review API endpoints", "priority", "notification", "Please review the new endpoints.")
	if err != nil {
		t.Fatal(err)
	}

	// Should have created a file in inbox/priority/
	entries, err := os.ReadDir(filepath.Join(dir, "mailbox/inbox/priority"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in inbox/priority, got %d", len(entries))
	}

	// Filename should match pattern: <timestamp>-<from>-<slug>.md
	name := entries[0].Name()
	if !strings.HasSuffix(name, "-sean-review-api-endpoints.md") {
		t.Errorf("unexpected filename: %s", name)
	}

	// Content should have frontmatter
	content, _ := os.ReadFile(filepath.Join(dir, "mailbox/inbox/priority", name))
	s := string(content)
	if !strings.Contains(s, "from: sean") {
		t.Error("missing 'from' in frontmatter")
	}
	if !strings.Contains(s, "subject: Review API endpoints") {
		t.Error("missing 'subject' in frontmatter")
	}
	if !strings.Contains(s, "category: priority") {
		t.Error("missing 'category' in frontmatter")
	}
	if !strings.Contains(s, "type: notification") {
		t.Error("missing 'type' in frontmatter")
	}
	if !strings.Contains(s, "timestamp:") {
		t.Error("missing 'timestamp' in frontmatter")
	}
	if !strings.Contains(s, "Please review the new endpoints.") {
		t.Error("missing body content")
	}
}

func TestWriteMailboxMessageDefaultCategory(t *testing.T) {
	dir := t.TempDir()
	EnsureMailbox(dir)

	err := WriteMailboxMessage(dir, "alice", "Hello", "", "", "Hi there")
	if err != nil {
		t.Fatal(err)
	}

	// Empty category should default to "all"
	entries, _ := os.ReadDir(filepath.Join(dir, "mailbox/inbox/all"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in inbox/all, got %d", len(entries))
	}
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./internal/workspace/ -run "TestSlugify|TestWriteMailbox" -v`
Expected: FAIL — functions not defined

- [ ] **Step 7: Implement slugify and WriteMailboxMessage**

Add to `internal/workspace/mailbox.go` (after EnsureMailbox):

```go
var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a string to a URL-safe slug.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// WriteMailboxMessage writes a message file to targetDir's mailbox inbox.
// Category defaults to "all" if empty. MsgType defaults to "notification" if empty.
func WriteMailboxMessage(targetDir, from, subject, category, msgType, body string) error {
	if category == "" {
		category = "all"
	}
	if msgType == "" {
		msgType = "notification"
	}

	now := time.Now().UTC()
	ts := now.Format("2006-01-02T15-04-05")
	slug := slugify(subject)
	filename := fmt.Sprintf("%s-%s-%s.md", ts, from, slug)

	inboxDir := filepath.Join(targetDir, "mailbox", "inbox", category)
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return fmt.Errorf("ensure inbox dir: %w", err)
	}

	content := fmt.Sprintf(`---
from: %s
timestamp: %s
category: %s
subject: %s
type: %s
---

%s
`, from, now.Format(time.RFC3339), category, subject, msgType, body)

	return os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0644)
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./internal/workspace/ -run "TestSlugify|TestWriteMailbox" -v`
Expected: PASS

- [ ] **Step 9: Write failing tests for HasMailboxMessages and LogMailboxEvent**

Append to `internal/workspace/mailbox_test.go`:

```go
func TestHasMailboxMessages(t *testing.T) {
	dir := t.TempDir()
	EnsureMailbox(dir)

	if HasMailboxMessages(dir) {
		t.Error("expected false for empty mailbox")
	}

	// Add a message
	WriteMailboxMessage(dir, "sean", "test", "all", "", "hello")
	if !HasMailboxMessages(dir) {
		t.Error("expected true after adding message")
	}
}

func TestHasMailboxMessagesNoMailbox(t *testing.T) {
	dir := t.TempDir()
	// No mailbox dir at all
	if HasMailboxMessages(dir) {
		t.Error("expected false when mailbox doesn't exist")
	}
}

func TestLogMailboxEvent(t *testing.T) {
	dir := t.TempDir()
	EnsureMailbox(dir)

	err := LogMailboxEvent(dir, "alice", "request", "Help with auth module")
	if err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, "mailbox", "system.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	s := string(data)
	if !strings.Contains(s, "received request from alice: Help with auth module") {
		t.Errorf("unexpected log content: %q", s)
	}

	// Append another
	LogMailboxEvent(dir, "sean", "notification", "Check this out")
	data, _ = os.ReadFile(logPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 log lines, got %d", len(lines))
	}
}
```

- [ ] **Step 10: Run tests to verify they fail**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./internal/workspace/ -run "TestHasMailbox|TestLogMailbox" -v`
Expected: FAIL — functions not defined

- [ ] **Step 11: Implement HasMailboxMessages and LogMailboxEvent**

Add to `internal/workspace/mailbox.go`:

```go
// HasMailboxMessages returns true if any .md files exist in the inbox subdirectories.
func HasMailboxMessages(dir string) bool {
	for _, sub := range []string{"important", "priority", "all"} {
		inboxDir := filepath.Join(dir, "mailbox", "inbox", sub)
		entries, err := os.ReadDir(inboxDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				return true
			}
		}
	}
	return false
}

// LogMailboxEvent appends a line to the mailbox system log.
func LogMailboxEvent(dir, from, msgType, subject string) error {
	logPath := filepath.Join(dir, "mailbox", "system.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	ts := time.Now().UTC().Format(time.RFC3339)
	_, err = fmt.Fprintf(f, "[%s] received %s from %s: %s\n", ts, msgType, from, subject)
	return err
}
```

- [ ] **Step 12: Run all mailbox tests to verify they pass**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./internal/workspace/ -run "TestEnsureMailbox|TestSlugify|TestWriteMailbox|TestHasMailbox|TestLogMailbox" -v`
Expected: PASS

- [ ] **Step 13: Commit**

```bash
cd /Users/seanochang/dev/projects/agents/keel
git add internal/workspace/mailbox.go internal/workspace/mailbox_test.go
git commit -m "feat: add mailbox workspace functions (EnsureMailbox, WriteMailboxMessage, HasMailboxMessages, LogMailboxEvent)"
```

---

### Task 2: Update DefaultProgram and bootstrapPrompt

**Files:**
- Modify: `internal/workspace/workspace.go:11-78` (DefaultProgram)
- Modify: `internal/loop/loop.go:26` (bootstrapPrompt)

- [ ] **Step 1: Update DefaultProgram Orient phase**

In `internal/workspace/workspace.go`, replace the Orient section (lines 13-15) in DefaultProgram:

Old:
```
## Orient
Read GOALS.md. Identify the highest-priority goal. Read MEMORY.md for prior context.
Check INBOX.md for messages from the user. Handle priority items before continuing with goals. Clear handled items from INBOX.md. If no INBOX.md exists, skip this step.
```

New:
```
## Orient
Read GOALS.md. Identify the highest-priority goal. Read MEMORY.md for prior context.
Scan mailbox: run eza -lT mailbox/inbox/ to see unread messages.
Triage: read any messages found, decide what to act on.
- Add to GOALS.md if actionable
- Move to mailbox/starred/ if relevant but not urgent
- Move to mailbox/read/ if just informational
Process goals (user goals first, then agent-originated). If no messages exist, skip triage.
```

- [ ] **Step 2: Add Messaging section to DefaultProgram**

In `internal/workspace/workspace.go`, add after the Schedule section (after line 56) in DefaultProgram:

```
## Messaging
To message another agent:
1. Write message to mailbox/drafts/<any-name>.md with frontmatter:
   ---
   from: <your-name>
   to: <target-agent>
   subject: <clear subject line>
   category: all|priority|important
   type: notification|request|handoff
   ---
   <body>
2. Run: cubit send mailbox/drafts/<filename>.md
Messages are async — send and move on. Do not wait for responses.
```

- [ ] **Step 3: Update bootstrapPrompt in loop.go**

In `internal/loop/loop.go`, change line 26:

Old:
```go
const bootstrapPrompt = "Read PROGRAM.md for your session instructions. Then read GOALS.md and INBOX.md, and follow the program."
```

New:
```go
const bootstrapPrompt = "Read PROGRAM.md for your session instructions. Then read GOALS.md, scan mailbox/inbox/ for messages, and follow the program."
```

- [ ] **Step 4: Run existing tests to verify nothing breaks**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./... -v`
Expected: PASS (DefaultProgram content changed but existing tests don't assert on Orient/Schedule text)

- [ ] **Step 5: Commit**

```bash
cd /Users/seanochang/dev/projects/agents/keel
git add internal/workspace/workspace.go internal/loop/loop.go
git commit -m "feat: update DefaultProgram and bootstrapPrompt to reference mailbox instead of INBOX.md"
```

---

### Task 3: Migrate loop.go — AppendInbox and HasGoals/HasMailboxMessages

**Files:**
- Modify: `internal/loop/loop.go:237` (initial goals check)
- Modify: `internal/loop/loop.go:259-262` (error context injection)
- Modify: `internal/loop/loop.go:353-357` (post-session goals check)
- Modify: `internal/loop/loop.go:472-476` (eval regression injection)
- Modify: `internal/loop/loop_test.go:808-813` (error context test assertion)

- [ ] **Step 1: Update initial HasGoals check to also check mailbox**

In `internal/loop/loop.go`, replace line 237:

Old:
```go
		if !workspace.HasGoals(l.Dir) {
			log.Printf("[keel] %s: no goals, loop exiting", l.Name)
			l.lifecycle("goals_empty")
			return
		}
```

New:
```go
		if !workspace.HasGoals(l.Dir) && !workspace.HasMailboxMessages(l.Dir) {
			log.Printf("[keel] %s: no goals or messages, loop exiting", l.Name)
			l.lifecycle("goals_empty")
			return
		}
```

- [ ] **Step 2: Update post-session HasGoals check**

In `internal/loop/loop.go`, replace line 353:

Old:
```go
		// Quick exit if goals were cleared during session (avoids unnecessary sleep).
		if !workspace.HasGoals(l.Dir) {
			log.Printf("[keel] %s: goals cleared during session, loop exiting", l.Name)
			l.lifecycle("goals_empty")
			return
		}
```

New:
```go
		// Quick exit if goals were cleared and no new messages during session.
		if !workspace.HasGoals(l.Dir) && !workspace.HasMailboxMessages(l.Dir) {
			log.Printf("[keel] %s: no goals or messages remaining, loop exiting", l.Name)
			l.lifecycle("goals_empty")
			return
		}
```

- [ ] **Step 3: Update error context injection in loop.go**

In `internal/loop/loop.go`, replace lines 259-262:

Old:
```go
			errMsg := fmt.Sprintf("[error] Session failed (attempt %d/%d)\nerror: %v\n---\nAdapt your approach if this looks like something you can work around. If this is a transient issue (rate limit, network), it may resolve on its own.",
				consecutiveErrors, l.maxErrors(), err)
			if writeErr := workspace.AppendInbox(l.Dir, true, "keel", errMsg); writeErr != nil {
				log.Printf("[keel] %s: failed to write error to INBOX: %v", l.Name, writeErr)
			}
```

New:
```go
			errMsg := fmt.Sprintf("[error] Session failed (attempt %d/%d)\nerror: %v\n---\nAdapt your approach if this looks like something you can work around. If this is a transient issue (rate limit, network), it may resolve on its own.",
				consecutiveErrors, l.maxErrors(), err)
			if writeErr := workspace.WriteMailboxMessage(l.Dir, "keel", "Session error", "important", "notification", errMsg); writeErr != nil {
				log.Printf("[keel] %s: failed to write error to mailbox: %v", l.Name, writeErr)
			}
```

- [ ] **Step 2: Update eval regression injection in loop.go**

In `internal/loop/loop.go`, replace lines 472-476:

Old:
```go
			msg := fmt.Sprintf("Eval regression in project %s: %s went from %.4f to %.4f (best: %.4f, baseline: %.4f). Consider reverting, adjusting your approach, or trying a different strategy.",
				p.Name(), cfg.Metric, state.Previous, metric.Value, state.Best, cfg.Baseline)
			if err := workspace.AppendInbox(l.Dir, true, "keel", msg); err != nil {
				log.Printf("[keel] %s: error writing regression to INBOX.md: %v", l.Name, err)
			}
```

New:
```go
			msg := fmt.Sprintf("Eval regression in project %s: %s went from %.4f to %.4f (best: %.4f, baseline: %.4f). Consider reverting, adjusting your approach, or trying a different strategy.",
				p.Name(), cfg.Metric, state.Previous, metric.Value, state.Best, cfg.Baseline)
			subject := fmt.Sprintf("Eval regression in %s", p.Name())
			if err := workspace.WriteMailboxMessage(l.Dir, "keel", subject, "important", "notification", msg); err != nil {
				log.Printf("[keel] %s: error writing regression to mailbox: %v", l.Name, err)
			}
```

- [ ] **Step 3: Update test assertion in loop_test.go**

In `internal/loop/loop_test.go`, replace lines 808-813:

Old:
```go
	// Verify error context was written to INBOX.md
	inbox, _ := os.ReadFile(filepath.Join(dir, "INBOX.md"))
	if !strings.Contains(string(inbox), "[error] Session failed") {
		t.Errorf("expected error context in INBOX.md, got: %q", string(inbox))
	}
```

New:
```go
	// Verify error context was written to mailbox
	entries, _ := os.ReadDir(filepath.Join(dir, "mailbox", "inbox", "important"))
	if len(entries) == 0 {
		t.Fatal("expected error message in mailbox/inbox/important/")
	}
	msgData, _ := os.ReadFile(filepath.Join(dir, "mailbox", "inbox", "important", entries[0].Name()))
	if !strings.Contains(string(msgData), "[error] Session failed") {
		t.Errorf("expected error context in mailbox message, got: %q", string(msgData))
	}
```

- [ ] **Step 4: Run tests to verify**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./internal/loop/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/seanochang/dev/projects/agents/keel
git add internal/loop/loop.go internal/loop/loop_test.go
git commit -m "feat: migrate loop.go from AppendInbox to WriteMailboxMessage"
```

---

### Task 4: MailboxWatcher

**Files:**
- Create: `internal/discord/mailbox.go`

- [ ] **Step 1: Implement MailboxWatcher**

The watcher uses an `onMessage func()` callback instead of a direct Manager reference. This lets the bot provide a closure that nudges running loops OR starts idle ones.

Create `internal/discord/mailbox.go`:

```go
package discord

import (
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// MailboxWatcher watches an agent's mailbox/inbox/ for new messages
// and calls onMessage when a new .md file is created.
type MailboxWatcher struct {
	agentName  string
	dir        string
	onMessage  func() // called when new message arrives — bot provides nudge-or-start logic
	stop       chan struct{}
	once       sync.Once
}

func NewMailboxWatcher(agentName, dir string, onMessage func()) *MailboxWatcher {
	return &MailboxWatcher{
		agentName: agentName,
		dir:       dir,
		onMessage: onMessage,
		stop:      make(chan struct{}),
	}
}

func (w *MailboxWatcher) Start() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[keel] %s: mailbox fsnotify error: %v", w.agentName, err)
		return
	}
	defer watcher.Close()

	// Watch inbox root and each category subdirectory.
	inboxDir := filepath.Join(w.dir, "mailbox", "inbox")
	for _, sub := range []string{"", "important", "priority", "all"} {
		path := filepath.Join(inboxDir, sub)
		if err := watcher.Add(path); err != nil {
			log.Printf("[keel] %s: mailbox watch error on %s: %v", w.agentName, path, err)
		}
	}

	for {
		select {
		case <-w.stop:
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) && strings.HasSuffix(event.Name, ".md") {
				log.Printf("[keel] %s: new mailbox message: %s", w.agentName, filepath.Base(event.Name))
				w.onMessage()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[keel] %s: mailbox watcher error: %v", w.agentName, err)
		}
	}
}

func (w *MailboxWatcher) Stop() {
	w.once.Do(func() { close(w.stop) })
}
```

- [ ] **Step 2: Run build to verify compilation**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go build ./...`
Expected: Success

- [ ] **Step 3: Commit**

```bash
cd /Users/seanochang/dev/projects/agents/keel
git add internal/discord/mailbox.go
git commit -m "feat: add MailboxWatcher for fsnotify-based inbox monitoring"
```

---

### Task 5: Discord commands and bot integration

**Files:**
- Modify: `internal/discord/commands.go:49-71` (!note and !priority)
- Modify: `internal/discord/commands.go:188-207` (help text)
- Modify: `internal/discord/bot.go:21-30` (Bot struct)
- Modify: `internal/discord/bot.go:60-75` (Bot.Start)
- Modify: `internal/discord/bot.go:77-86` (Bot.Stop)

- [ ] **Step 1: Update !note command in commands.go**

In `internal/discord/commands.go`, replace the `case "note":` block (lines 49-58):

Old:
```go
	case "note":
		if args == "" {
			response = "Usage: `!note <message>`"
		} else {
			if err := workspace.AppendInbox(ch.AgentDir, false, m.Author.Username, args); err != nil {
				response = fmt.Sprintf("Error: %v", err)
			} else {
				response = "Note added to INBOX.md."
			}
		}
```

New:
```go
	case "note":
		if args == "" {
			response = "Usage: `!note <message>`"
		} else {
			subject := noteSubject(args)
			if err := workspace.WriteMailboxMessage(ch.AgentDir, m.Author.Username, subject, "all", "notification", args); err != nil {
				response = fmt.Sprintf("Error: %v", err)
			} else {
				response = "Note added to mailbox."
			}
		}
```

- [ ] **Step 2: Update !priority command in commands.go**

Replace the `case "priority":` block (lines 59-71):

Old:
```go
	case "priority":
		if args == "" {
			response = "Usage: `!priority <message>`"
		} else {
			if err := workspace.AppendInbox(ch.AgentDir, true, m.Author.Username, args); err != nil {
				response = fmt.Sprintf("Error: %v", err)
			} else {
				response = "Priority note added to INBOX.md."
				if b.loopMgr.IsRunning(agentName) {
					b.loopMgr.Nudge(agentName)
				}
			}
		}
```

New:
```go
	case "priority":
		if args == "" {
			response = "Usage: `!priority <message>`"
		} else {
			subject := noteSubject(args)
			if err := workspace.WriteMailboxMessage(ch.AgentDir, m.Author.Username, subject, "priority", "notification", args); err != nil {
				response = fmt.Sprintf("Error: %v", err)
			} else {
				response = "Priority note added to mailbox."
			}
		}
```

Note: No explicit `Nudge` needed — the MailboxWatcher handles wake automatically via fsnotify.

- [ ] **Step 3: Add noteSubject helper in commands.go**

Add at the bottom of `internal/discord/commands.go`:

```go
// noteSubject extracts a short subject from a message (first line, truncated to 60 chars).
func noteSubject(msg string) string {
	line := msg
	if idx := strings.IndexByte(msg, '\n'); idx >= 0 {
		line = msg[:idx]
	}
	if len(line) > 60 {
		line = line[:60]
	}
	return strings.TrimSpace(line)
}
```

- [ ] **Step 4: Update help text in commands.go**

In the `cmdHelp()` function (lines 188-207), update the note/priority descriptions:

Old:
```go
		"`!note <msg>` — leave a note for the agent (read next session)\n" +
		"`!priority <msg>` — leave a priority note (handled before goals)\n" +
```

New:
```go
		"`!note <msg>` — leave a note in the agent's mailbox\n" +
		"`!priority <msg>` — leave a priority note in the agent's mailbox\n" +
```

- [ ] **Step 5: Add mailboxWatchers to Bot struct**

In `internal/discord/bot.go`, update the Bot struct (lines 21-30):

Old:
```go
type Bot struct {
	session      *discordgo.Session
	cfg          *config.Config
	loopMgr      *loop.Manager
	tailers      map[string]*LogTailer
	sleepBetween time.Duration
	archiveEvery int
	schedStop    chan struct{} // signals scheduler goroutine to stop
	schedDone    chan struct{} // closed when scheduler goroutine exits
}
```

New:
```go
type Bot struct {
	session         *discordgo.Session
	cfg             *config.Config
	loopMgr         *loop.Manager
	tailers         map[string]*LogTailer
	mailboxWatchers map[string]*MailboxWatcher
	sleepBetween    time.Duration
	archiveEvery    int
	schedStop       chan struct{} // signals scheduler goroutine to stop
	schedDone       chan struct{} // closed when scheduler goroutine exits
}
```

- [ ] **Step 6: Initialize mailboxWatchers in NewBot**

In `internal/discord/bot.go`, update the Bot initialization in NewBot (around line 43):

Old:
```go
	b := &Bot{
		session:      session,
		cfg:          cfg,
		loopMgr:      loop.NewManager(),
		tailers:      make(map[string]*LogTailer),
		sleepBetween: sleepBetween,
		archiveEvery: archiveEvery,
		schedStop:    make(chan struct{}),
		schedDone:    make(chan struct{}),
	}
```

New:
```go
	b := &Bot{
		session:         session,
		cfg:             cfg,
		loopMgr:         loop.NewManager(),
		tailers:         make(map[string]*LogTailer),
		mailboxWatchers: make(map[string]*MailboxWatcher),
		sleepBetween:    sleepBetween,
		archiveEvery:    archiveEvery,
		schedStop:       make(chan struct{}),
		schedDone:       make(chan struct{}),
	}
```

- [ ] **Step 7: Add ensureRunning helper to Bot**

Add a helper method on Bot that nudges a running loop or starts an idle one. This is used by both the MailboxWatcher callback and `onMessageCreate`.

Add to `internal/discord/bot.go` (after `Bot.Stop`):

```go
// ensureRunning nudges a running loop or starts a new one if idle.
func (b *Bot) ensureRunning(name string, ch config.ChannelConfig) {
	if b.loopMgr.IsRunning(name) {
		b.loopMgr.Nudge(name)
	} else {
		onOutput, onLifecycle, opts := b.sessionHandlers(name, ch.ChannelID, ch.AgentDir)
		if err := b.loopMgr.Start(name, ch.AgentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery, onOutput, onLifecycle, opts); err != nil {
			log.Printf("[keel] %s: error starting loop: %v", name, err)
		}
	}
}
```

- [ ] **Step 8: Start mailbox watchers and add startup check in Bot.Start**

In `internal/discord/bot.go`, update Bot.Start (lines 60-75):

Old:
```go
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open discord connection: %w", err)
	}
	log.Printf("[keel] Discord bot connected")

	for name, ch := range b.cfg.Channels {
		tailer := NewLogTailer(name, ch.AgentDir, ch.ChannelID, b.cfg.Bot.StatusChannelID, b.session)
		b.tailers[name] = tailer
		go tailer.Start()
	}

	go b.runScheduler()

	return nil
}
```

New:
```go
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open discord connection: %w", err)
	}
	log.Printf("[keel] Discord bot connected")

	for name, ch := range b.cfg.Channels {
		// Startup check: warn if agent has INBOX.md but no mailbox/
		b.checkMailboxMigration(name, ch.AgentDir)

		// Ensure mailbox directory tree exists
		if err := workspace.EnsureMailbox(ch.AgentDir); err != nil {
			log.Printf("[keel] %s: error creating mailbox: %v", name, err)
		}

		tailer := NewLogTailer(name, ch.AgentDir, ch.ChannelID, b.cfg.Bot.StatusChannelID, b.session)
		b.tailers[name] = tailer
		go tailer.Start()

		// Capture loop vars for closure
		agentName, agentCh := name, ch
		mw := NewMailboxWatcher(name, ch.AgentDir, func() {
			b.ensureRunning(agentName, agentCh)
		})
		b.mailboxWatchers[name] = mw
		go mw.Start()
	}

	go b.runScheduler()

	return nil
}

// checkMailboxMigration warns if an agent has INBOX.md but no mailbox directory.
func (b *Bot) checkMailboxMigration(name, dir string) {
	inboxPath := filepath.Join(dir, "INBOX.md")
	mailboxPath := filepath.Join(dir, "mailbox")

	_, inboxErr := os.Stat(inboxPath)
	_, mailboxErr := os.Stat(mailboxPath)

	if inboxErr == nil && os.IsNotExist(mailboxErr) {
		log.Printf("[keel] WARNING: agent %s has INBOX.md but no mailbox/ — run 'cubit migrate' to upgrade", name)
	}
}
```

- [ ] **Step 9: Stop mailbox watchers in Bot.Stop**

In `internal/discord/bot.go`, update Bot.Stop (lines 77-86):

Old:
```go
func (b *Bot) Stop() {
	close(b.schedStop)
	<-b.schedDone
	b.loopMgr.StopAll()
	for _, t := range b.tailers {
		t.Stop()
	}
	b.session.Close()
	log.Printf("[keel] Discord bot disconnected")
}
```

New:
```go
func (b *Bot) Stop() {
	close(b.schedStop)
	<-b.schedDone
	b.loopMgr.StopAll()
	for _, t := range b.tailers {
		t.Stop()
	}
	for _, mw := range b.mailboxWatchers {
		mw.Stop()
	}
	b.session.Close()
	log.Printf("[keel] Discord bot disconnected")
}
```

- [ ] **Step 10: Simplify onMessageCreate using ensureRunning**

In `internal/discord/bot.go`, update `onMessageCreate` (lines 118-127) to use the new helper:

Old:
```go
	if b.loopMgr.IsRunning(agentName) {
		b.loopMgr.Nudge(agentName)
	} else {
		onOutput, onLifecycle, opts := b.sessionHandlers(agentName, ch.ChannelID, ch.AgentDir)
		err := b.loopMgr.Start(agentName, ch.AgentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery, onOutput, onLifecycle, opts)
		if err != nil {
			log.Printf("[keel] error starting loop for %s: %v", agentName, err)
		}
	}
```

New:
```go
	b.ensureRunning(agentName, ch)
```

- [ ] **Step 11: Run build and tests**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go build ./... && go test ./... -v`
Expected: PASS (compilation succeeds, all tests pass)

- [ ] **Step 12: Commit**

```bash
cd /Users/seanochang/dev/projects/agents/keel
git add internal/discord/commands.go internal/discord/bot.go
git commit -m "feat: migrate Discord commands to mailbox, add MailboxWatcher to bot lifecycle, add ensureRunning helper"
```

---

### Task 6: Remove old INBOX functions and update tests

**Files:**
- Modify: `internal/workspace/workspace.go:291-334` (remove INBOX functions)
- Modify: `internal/workspace/workspace_test.go:230-303` (remove INBOX tests)

- [ ] **Step 1: Remove INBOX functions from workspace.go**

In `internal/workspace/workspace.go`, delete these functions (lines 291-334):

- `AppendInbox`
- `ReadInbox`
- `ClearInbox`
- `HasInbox`

- [ ] **Step 2: Remove INBOX tests from workspace_test.go**

In `internal/workspace/workspace_test.go`, delete these test functions (lines 230-303):

- `TestAppendInbox`
- `TestAppendInboxPriority`
- `TestReadInboxMissing`
- `TestClearInbox`
- `TestHasInbox`

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go build ./... && go test ./... -v`
Expected: PASS — no remaining references to removed functions

- [ ] **Step 4: Commit**

```bash
cd /Users/seanochang/dev/projects/agents/keel
git add internal/workspace/workspace.go internal/workspace/workspace_test.go
git commit -m "chore: remove old INBOX.md functions and tests (replaced by mailbox)"
```

---

### Task 7: Final verification

**Files:** None (verification only)

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go test ./... -v`
Expected: All tests pass

- [ ] **Step 2: Build binary**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go build -o keel .`
Expected: Binary builds successfully

- [ ] **Step 3: Grep for any remaining INBOX references**

Run: `cd /Users/seanochang/dev/projects/agents/keel && grep -r "INBOX" --include="*.go" .`
Expected: No matches (except possibly the migration check log message which is expected)

- [ ] **Step 4: Verify no unused imports**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go vet ./...`
Expected: No issues

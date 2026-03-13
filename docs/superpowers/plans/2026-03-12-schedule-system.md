# Schedule System Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a filesystem-based scheduling system so agents can self-schedule future goals, and a goroutine in `keel serve` fires them on time.

**Architecture:** Schedule entries live in `<agent-dir>/schedule/<time-dir>/<name>.md`. A 60-second ticker goroutine scans all agent schedule dirs, injects due entries into GOALS.md, and starts/nudges the agent loop. One-shot entries (ISO datetime dirs) are deleted after firing. Recurring entries (cron-prefixed dirs) persist with a `.last-fired` guard file.

**Tech Stack:** Go stdlib + `github.com/robfig/cron/v3` for cron expression parsing.

---

## File Structure

```
internal/schedule/
├── schedule.go       # Core types, scan, fire, cleanup logic
├── schedule_test.go  # Unit tests for all schedule operations
cmd/
├── schedule.go       # Cobra subcommand: keel schedule {add,ls,rm}
```

**Existing files modified:**
- `internal/workspace/workspace.go` — add `AppendScheduledGoal()` to keep GOALS.md writes in one place
- `internal/discord/bot.go` — start scheduler goroutine in `Bot.Start()`, stop in `Bot.Stop()`
- `cmd/root.go` — register `scheduleCmd`
- `go.mod` / `go.sum` — add `robfig/cron/v3`

---

## Chunk 1: `internal/schedule/` — Core Package

### Task 1: Define types and time-format parsing

**Files:**
- Create: `internal/schedule/schedule.go`
- Test: `internal/schedule/schedule_test.go`

- [ ] **Step 1: Write failing test for time-format parsing**

```go
// internal/schedule/schedule_test.go
package schedule

import (
	"testing"
	"time"
)

func TestParseTimeDir_ISO(t *testing.T) {
	td, err := ParseTimeDir("2026-03-13T08:30")
	if err != nil {
		t.Fatal(err)
	}
	if td.Kind != KindOneShot {
		t.Errorf("expected one-shot, got %v", td.Kind)
	}
	want := time.Date(2026, 3, 13, 8, 30, 0, 0, time.Local)
	if !td.At.Equal(want) {
		t.Errorf("expected %v, got %v", want, td.At)
	}
}

func TestParseTimeDir_Cron(t *testing.T) {
	// cron-30_8_*_*_1-5 → "30 8 * * 1-5" (weekdays at 8:30)
	td, err := ParseTimeDir("cron-30_8_*_*_1-5")
	if err != nil {
		t.Fatal(err)
	}
	if td.Kind != KindRecurring {
		t.Errorf("expected recurring, got %v", td.Kind)
	}
	if td.CronExpr != "30 8 * * 1-5" {
		t.Errorf("unexpected cron expr: %q", td.CronExpr)
	}
}

func TestParseTimeDir_Invalid(t *testing.T) {
	_, err := ParseTimeDir("garbage")
	if err == nil {
		t.Error("expected error for invalid time dir")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/schedule/ -v -run TestParseTimeDir`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write types and ParseTimeDir**

```go
// internal/schedule/schedule.go
package schedule

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

type Kind int

const (
	KindOneShot   Kind = iota
	KindRecurring
)

// TimeDir represents a parsed schedule time directory name.
type TimeDir struct {
	Kind     Kind
	Raw      string // original directory name
	At       time.Time // populated for one-shot
	CronExpr string    // populated for recurring
}

// Entry represents a single scheduled goal file.
type Entry struct {
	TimeDir  TimeDir
	Name     string // filename without .md
	Content  string // goal text
	FilePath string // full path to the .md file
}

const isoLayout = "2006-01-02T15:04"

// ParseTimeDir parses a schedule directory name into a TimeDir.
// ISO format: "2026-03-13T08:30"
// Cron format: "cron-30_8_*_*_1-5" → "30 8 * * 1-5"
func ParseTimeDir(name string) (TimeDir, error) {
	if strings.HasPrefix(name, "cron-") {
		expr := strings.ReplaceAll(strings.TrimPrefix(name, "cron-"), "_", " ")
		// Validate the expression parses
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(expr); err != nil {
			return TimeDir{}, fmt.Errorf("invalid cron expression %q: %w", expr, err)
		}
		return TimeDir{Kind: KindRecurring, Raw: name, CronExpr: expr}, nil
	}

	t, err := time.ParseInLocation(isoLayout, name, time.Local)
	if err != nil {
		return TimeDir{}, fmt.Errorf("invalid schedule time %q: must be ISO (2006-01-02T15:04) or cron- prefix: %w", name, err)
	}
	return TimeDir{Kind: KindOneShot, Raw: name, At: t}, nil
}
```

- [ ] **Step 4: Add cron dependency**

Run: `cd /Users/seanochang/dev/projects/agents/keel && go get github.com/robfig/cron/v3`

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/schedule/ -v -run TestParseTimeDir`
Expected: PASS (3 tests)

- [ ] **Step 6: Commit**

```bash
git add internal/schedule/schedule.go internal/schedule/schedule_test.go go.mod go.sum
git commit -m "feat(schedule): add types and time-dir parsing (ISO + cron)"
```

---

### Task 2: Scan schedule directory

**Files:**
- Modify: `internal/schedule/schedule.go`
- Modify: `internal/schedule/schedule_test.go`

- [ ] **Step 1: Write failing test for ScanDir**

Update test file imports to add `"os"` and `"path/filepath"`:
```go
import (
	"os"
	"path/filepath"
	"testing"
	"time"
)
```

```go
func TestScanDir_EmptyScheduleDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "schedule"), 0755)
	entries, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestScanDir_MixedEntries(t *testing.T) {
	dir := t.TempDir()

	// Create a one-shot entry
	os.MkdirAll(filepath.Join(dir, "schedule", "2026-03-13T08:30"), 0755)
	os.WriteFile(filepath.Join(dir, "schedule", "2026-03-13T08:30", "check-pce.md"), []byte("Check PCE data"), 0644)

	// Create a recurring entry
	os.MkdirAll(filepath.Join(dir, "schedule", "cron-30_8_*_*_1-5"), 0755)
	os.WriteFile(filepath.Join(dir, "schedule", "cron-30_8_*_*_1-5", "morning-brief.md"), []byte("Run morning briefing"), 0644)

	// Non-.md files should be ignored
	os.WriteFile(filepath.Join(dir, "schedule", "2026-03-13T08:30", ".last-fired"), []byte(""), 0644)

	entries, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestScanDir_NoScheduleDir(t *testing.T) {
	dir := t.TempDir()
	// No schedule/ subdirectory — should return empty, no error
	entries, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/schedule/ -v -run TestScanDir`
Expected: FAIL — `ScanDir` undefined

- [ ] **Step 3: Implement ScanDir**

Add to `internal/schedule/schedule.go`:

```go
// ScanDir reads <agentDir>/schedule/ and returns all valid entries.
// Skips invalid time directories and non-.md files silently.
func ScanDir(agentDir string) ([]Entry, error) {
	schedDir := filepath.Join(agentDir, "schedule")
	timeDirs, err := os.ReadDir(schedDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read schedule dir: %w", err)
	}

	var entries []Entry
	for _, td := range timeDirs {
		if !td.IsDir() {
			continue
		}
		parsed, err := ParseTimeDir(td.Name())
		if err != nil {
			continue // skip unparseable dirs
		}

		files, err := os.ReadDir(filepath.Join(schedDir, td.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(schedDir, td.Name(), f.Name()))
			if err != nil {
				continue
			}
			entries = append(entries, Entry{
				TimeDir:  parsed,
				Name:     strings.TrimSuffix(f.Name(), ".md"),
				Content:  string(content),
				FilePath: filepath.Join(schedDir, td.Name(), f.Name()),
			})
		}
	}
	return entries, nil
}
```

Add imports: `"os"`, `"path/filepath"` to schedule.go.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/schedule/ -v -run TestScanDir`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/schedule/schedule.go internal/schedule/schedule_test.go
git commit -m "feat(schedule): add ScanDir to read schedule entries from agent dir"
```

---

### Task 3: IsDue check and Fire logic

**Files:**
- Modify: `internal/schedule/schedule.go`
- Modify: `internal/schedule/schedule_test.go`

- [ ] **Step 1: Write failing test for IsDue**

Update test file imports to add `"fmt"` and `"strings"`:
```go
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)
```

```go
func TestIsDue_OneShot_Past(t *testing.T) {
	td := TimeDir{Kind: KindOneShot, At: time.Now().Add(-5 * time.Minute)}
	if !IsDue(td, t.TempDir()) {
		t.Error("past one-shot should be due")
	}
}

func TestIsDue_OneShot_Future(t *testing.T) {
	td := TimeDir{Kind: KindOneShot, At: time.Now().Add(5 * time.Minute)}
	if IsDue(td, t.TempDir()) {
		t.Error("future one-shot should not be due")
	}
}

func TestIsDue_Cron_MatchingMinute(t *testing.T) {
	now := time.Now()
	// Build a cron expr that matches the current minute
	expr := fmt.Sprintf("%d %d * * *", now.Minute(), now.Hour())
	td := TimeDir{Kind: KindRecurring, CronExpr: expr}
	dir := t.TempDir()
	if !IsDue(td, dir) {
		t.Error("cron matching current minute should be due")
	}
}

func TestIsDue_Cron_AlreadyFiredThisMinute(t *testing.T) {
	now := time.Now()
	expr := fmt.Sprintf("%d %d * * *", now.Minute(), now.Hour())
	td := TimeDir{Kind: KindRecurring, CronExpr: expr, Raw: "cron-test"}
	dir := t.TempDir()

	// Simulate already fired
	schedDir := filepath.Join(dir, "schedule", td.Raw)
	os.MkdirAll(schedDir, 0755)
	os.WriteFile(filepath.Join(schedDir, ".last-fired"), []byte(now.Truncate(time.Minute).Format(time.RFC3339)), 0644)

	if IsDue(td, dir) {
		t.Error("should not be due if already fired this minute")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/schedule/ -v -run TestIsDue`
Expected: FAIL — `IsDue` undefined

- [ ] **Step 3: Implement IsDue**

Add to `internal/schedule/schedule.go`:

```go
// IsDue returns true if the entry should fire now.
// One-shot: fires if current time >= scheduled time.
// Recurring: fires if current minute matches cron AND hasn't fired this minute.
func IsDue(td TimeDir, agentDir string) bool {
	now := time.Now()
	switch td.Kind {
	case KindOneShot:
		return !now.Before(td.At)
	case KindRecurring:
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, err := parser.Parse(td.CronExpr)
		if err != nil {
			return false
		}
		// Check if cron matches current minute: the previous fire time from "now+1min"
		// should be within the current minute.
		nowMinute := now.Truncate(time.Minute)
		// cron.Schedule only has Next(), so check: next fire from (now-1min) should be <= now
		prev := sched.Next(nowMinute.Add(-1 * time.Minute))
		if !prev.Equal(nowMinute) {
			return false
		}
		// Guard: check .last-fired
		lastFired := readLastFired(agentDir, td.Raw)
		return !lastFired.Equal(nowMinute)
	}
	return false
}

func readLastFired(agentDir, timeDirName string) time.Time {
	data, err := os.ReadFile(filepath.Join(agentDir, "schedule", timeDirName, ".last-fired"))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return t
}

func writeLastFired(agentDir, timeDirName string) error {
	p := filepath.Join(agentDir, "schedule", timeDirName, ".last-fired")
	return os.WriteFile(p, []byte(time.Now().Truncate(time.Minute).Format(time.RFC3339)), 0644)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/schedule/ -v -run TestIsDue`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/schedule/schedule.go internal/schedule/schedule_test.go
git commit -m "feat(schedule): add IsDue with cron matching and last-fired guard"
```

---

### Task 4: Add `AppendScheduledGoal` to workspace package, then Fire and Cleanup

**Files:**
- Modify: `internal/workspace/workspace.go` — add `AppendScheduledGoal`
- Modify: `internal/workspace/workspace_test.go` — test it
- Modify: `internal/schedule/schedule.go`
- Modify: `internal/schedule/schedule_test.go`

- [ ] **Step 1: Write failing test for AppendScheduledGoal in workspace**

Add to `internal/workspace/workspace_test.go`:

```go
func TestAppendScheduledGoal(t *testing.T) {
	dir := setupWorkspace(t)
	err := AppendScheduledGoal(dir, "check-pce", "Check January PCE release")
	if err != nil {
		t.Fatal(err)
	}
	goals, _ := ReadGoals(dir)
	if !strings.Contains(goals, "Check January PCE release") {
		t.Error("scheduled goal content not found")
	}
	if !strings.Contains(goals, "scheduled: check-pce") {
		t.Error("scheduled metadata not found")
	}
	if !strings.Contains(goals, "Scheduled task") {
		t.Error("scheduled hint not found")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/workspace/ -v -run TestAppendScheduledGoal`
Expected: FAIL — `AppendScheduledGoal` undefined

- [ ] **Step 3: Implement AppendScheduledGoal**

Add to `internal/workspace/workspace.go`:

```go
// AppendScheduledGoal adds a scheduled goal to GOALS.md with metadata hint.
func AppendScheduledGoal(dir, name, content string) error {
	f, err := os.OpenFile(filepath.Join(dir, "GOALS.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04")
	_, err = fmt.Fprintf(f, "\n## [%s] scheduled: %s\n%s\n\n> Scheduled task. When complete, remove this goal. If no other goals remain, create .exit to signal you are done.\n", ts, name, content)
	return err
}
```

- [ ] **Step 4: Run workspace tests**

Run: `go test ./internal/workspace/ -v`
Expected: All pass

- [ ] **Step 5: Commit workspace changes**

```bash
git add internal/workspace/workspace.go internal/workspace/workspace_test.go
git commit -m "feat(workspace): add AppendScheduledGoal for schedule system"
```

- [ ] **Step 6: Write failing test for FireDue**

Add to `internal/schedule/schedule_test.go`:

```go
func TestFire_InjectsGoalAndReturnsEntries(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte("# Goals\n"), 0644)

	// Create a past one-shot
	os.MkdirAll(filepath.Join(dir, "schedule", "2020-01-01T00:00"), 0755)
	os.WriteFile(filepath.Join(dir, "schedule", "2020-01-01T00:00", "old-task.md"), []byte("Do the thing"), 0644)

	fired, err := FireDue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 {
		t.Fatalf("expected 1 fired, got %d", len(fired))
	}
	if fired[0].Name != "old-task" {
		t.Errorf("expected old-task, got %s", fired[0].Name)
	}

	// Goal should be in GOALS.md
	goals, _ := os.ReadFile(filepath.Join(dir, "GOALS.md"))
	if !strings.Contains(string(goals), "Do the thing") {
		t.Error("goal content not found in GOALS.md")
	}
	if !strings.Contains(string(goals), "scheduled: old-task") {
		t.Error("scheduled metadata not found in GOALS.md")
	}
	if !strings.Contains(string(goals), "Scheduled task") {
		t.Error("scheduled hint not found in GOALS.md")
	}
}

func TestFire_DeletesOneShotDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte(""), 0644)

	timeDir := filepath.Join(dir, "schedule", "2020-01-01T00:00")
	os.MkdirAll(timeDir, 0755)
	os.WriteFile(filepath.Join(timeDir, "task.md"), []byte("Do it"), 0644)

	_, err := FireDue(dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(timeDir); !os.IsNotExist(err) {
		t.Error("one-shot time dir should be deleted after firing")
	}
}

func TestFire_KeepsCronDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte(""), 0644)

	now := time.Now()
	cronName := fmt.Sprintf("cron-%d_%d_*_*_*", now.Minute(), now.Hour())
	cronDir := filepath.Join(dir, "schedule", cronName)
	os.MkdirAll(cronDir, 0755)
	os.WriteFile(filepath.Join(cronDir, "task.md"), []byte("Recurring thing"), 0644)

	_, err := FireDue(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Dir should still exist
	if _, err := os.Stat(cronDir); os.IsNotExist(err) {
		t.Error("cron dir should be kept after firing")
	}
	// .last-fired should exist
	if _, err := os.Stat(filepath.Join(cronDir, ".last-fired")); os.IsNotExist(err) {
		t.Error(".last-fired should be written after cron fires")
	}
}
```

- [ ] **Step 7: Run test to verify it fails**

Run: `go test ./internal/schedule/ -v -run TestFire`
Expected: FAIL — `FireDue` undefined

- [ ] **Step 8: Implement FireDue**

Add to `internal/schedule/schedule.go`:

```go
import "github.com/SeanoChang/keel/internal/workspace"

// FireDue scans the agent's schedule dir, finds due entries, injects them
// into GOALS.md via workspace.AppendScheduledGoal, and cleans up.
// Returns the list of entries that were fired.
// Safe: designed to be called from a single scheduler goroutine.
func FireDue(agentDir string) ([]Entry, error) {
	entries, err := ScanDir(agentDir)
	if err != nil {
		return nil, err
	}

	var fired []Entry
	firedTimeDirs := make(map[string]TimeDir)

	for _, e := range entries {
		if !IsDue(e.TimeDir, agentDir) {
			continue
		}
		if err := workspace.AppendScheduledGoal(agentDir, e.Name, e.Content); err != nil {
			return fired, fmt.Errorf("inject scheduled goal %s: %w", e.Name, err)
		}

		fired = append(fired, e)
		firedTimeDirs[e.TimeDir.Raw] = e.TimeDir
	}

	// Cleanup
	for raw, td := range firedTimeDirs {
		schedDir := filepath.Join(agentDir, "schedule", raw)
		switch td.Kind {
		case KindOneShot:
			os.RemoveAll(schedDir)
		case KindRecurring:
			writeLastFired(agentDir, raw)
		}
	}

	return fired, nil
}
```

- [ ] **Step 9: Run tests to verify they pass**

Run: `go test ./internal/schedule/ -v -run TestFire`
Expected: PASS (3 tests)

- [ ] **Step 10: Run all schedule tests**

Run: `go test ./internal/schedule/ -v`
Expected: All 10 tests pass

- [ ] **Step 11: Commit**

```bash
git add internal/schedule/schedule.go internal/schedule/schedule_test.go
git commit -m "feat(schedule): add FireDue with goal injection and one-shot/cron cleanup"
```

---

## Chunk 2: CLI Command + Bot Integration

### Task 5: `keel schedule` CLI command

**Files:**
- Create: `cmd/schedule.go`
- Modify: `cmd/root.go`

- [ ] **Step 1: Create `cmd/schedule.go` with add, ls, rm subcommands**

```go
// cmd/schedule.go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SeanoChang/keel/internal/schedule"
	"github.com/spf13/cobra"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage agent scheduled goals",
}

var scheduleAddCmd = &cobra.Command{
	Use:   "add <agent> <time> <name> <content>",
	Short: "Add a scheduled goal",
	Long: `Add a scheduled goal for an agent.

Time formats:
  ISO one-shot:  2026-03-13T08:30
  Cron recurring: cron-30_8_*_*_1-5  (underscores separate fields)

Examples:
  keel schedule add alice 2026-03-13T08:30 check-pce "Check PCE data release"
  keel schedule add alice cron-30_8_*_*_1-5 morning-brief "Run morning market briefing"`,
	Args: cobra.ExactArgs(4),
	RunE: runScheduleAdd,
}

var scheduleAddDir string

func runScheduleAdd(cmd *cobra.Command, args []string) error {
	agent, timeStr, name, content := args[0], args[1], args[2], args[3]
	dir := resolveAgentDir(agent, scheduleAddDir)

	// Validate time format
	if _, err := schedule.ParseTimeDir(timeStr); err != nil {
		return err
	}

	schedDir := filepath.Join(dir, "schedule", timeStr)
	if err := os.MkdirAll(schedDir, 0755); err != nil {
		return fmt.Errorf("create schedule dir: %w", err)
	}

	filePath := filepath.Join(schedDir, name+".md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write schedule file: %w", err)
	}

	fmt.Printf("Scheduled: %s/%s at %s\n", agent, name, timeStr)
	return nil
}

var scheduleLsDir string

var scheduleLsCmd = &cobra.Command{
	Use:   "ls <agent>",
	Short: "List scheduled goals",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent := args[0]
		dir := resolveAgentDir(agent, scheduleLsDir)

		entries, err := schedule.ScanDir(dir)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Println("No scheduled goals.")
			return nil
		}
		for _, e := range entries {
			kind := "one-shot"
			if e.TimeDir.Kind == schedule.KindRecurring {
				kind = "recurring"
			}
			preview := e.Content
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", " ")
			fmt.Printf("  [%s] %s — %s: %s\n", kind, e.TimeDir.Raw, e.Name, preview)
		}
		return nil
	},
}

var scheduleClearDir string

var scheduleClearCmd = &cobra.Command{
	Use:   "clear <agent>",
	Short: "Remove all scheduled goals for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent := args[0]
		dir := resolveAgentDir(agent, scheduleClearDir)

		schedDir := filepath.Join(dir, "schedule")
		if _, err := os.Stat(schedDir); os.IsNotExist(err) {
			fmt.Println("No schedule directory.")
			return nil
		}

		entries, _ := schedule.ScanDir(dir)
		if err := os.RemoveAll(schedDir); err != nil {
			return fmt.Errorf("remove schedule dir: %w", err)
		}
		fmt.Printf("Cleared %d scheduled goals for %s.\n", len(entries), agent)
		return nil
	},
}

var scheduleRmDir string

var scheduleRmCmd = &cobra.Command{
	Use:   "rm <agent> <name>",
	Short: "Remove a scheduled goal by name",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent, name := args[0], args[1]
		dir := resolveAgentDir(agent, scheduleRmDir)

		entries, err := schedule.ScanDir(dir)
		if err != nil {
			return err
		}

		var removed int
		for _, e := range entries {
			if e.Name == name {
				if err := os.Remove(e.FilePath); err != nil {
					return fmt.Errorf("remove %s: %w", e.FilePath, err)
				}
				removed++
				fmt.Printf("Removed: %s/%s from %s\n", agent, name, e.TimeDir.Raw)

				// If time dir is now empty (no .md files), remove it
				parent := filepath.Dir(e.FilePath)
				remaining, _ := filepath.Glob(filepath.Join(parent, "*.md"))
				if len(remaining) == 0 {
					os.RemoveAll(parent)
				}
			}
		}
		if removed == 0 {
			return fmt.Errorf("no schedule entry named %q found for %s", name, agent)
		}
		return nil
	},
}
```

- [ ] **Step 2: Register in root.go**

Add to `cmd/root.go` `init()`:

```go
// schedule
scheduleAddCmd.Flags().StringVar(&scheduleAddDir, "dir", "", "Agent directory override")
scheduleLsCmd.Flags().StringVar(&scheduleLsDir, "dir", "", "Agent directory override")
scheduleRmCmd.Flags().StringVar(&scheduleRmDir, "dir", "", "Agent directory override")
scheduleClearCmd.Flags().StringVar(&scheduleClearDir, "dir", "", "Agent directory override")
scheduleCmd.AddCommand(scheduleAddCmd, scheduleLsCmd, scheduleRmCmd, scheduleClearCmd)
rootCmd.AddCommand(scheduleCmd)
```

- [ ] **Step 3: Verify it compiles**

Run: `go build -o keel .`
Expected: Compiles without errors

- [ ] **Step 4: Manual smoke test**

Run:
```bash
./keel schedule add alice 2026-12-25T09:00 christmas-test "Test schedule entry" --dir /tmp/test-agent
./keel schedule ls alice --dir /tmp/test-agent
./keel schedule rm alice christmas-test --dir /tmp/test-agent
./keel schedule add alice 2026-12-25T09:00 another-test "Another entry" --dir /tmp/test-agent
./keel schedule clear alice --dir /tmp/test-agent
./keel schedule ls alice --dir /tmp/test-agent  # should print "No scheduled goals."
```
Expected: Creates, lists, and removes the entry.

- [ ] **Step 5: Commit**

```bash
git add cmd/schedule.go cmd/root.go
git commit -m "feat(schedule): add keel schedule {add,ls,rm} CLI commands"
```

---

### Task 6: Scheduler goroutine in Bot

**Files:**
- Modify: `internal/discord/bot.go`

- [ ] **Step 1: Add scheduler field and start/stop logic**

Add to `Bot` struct in `internal/discord/bot.go`:

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

Update `NewBot` to initialize the channels:

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

- [ ] **Step 2: Implement the scheduler goroutine**

Add to `internal/discord/bot.go`:

```go
// runScheduler ticks every 60 seconds, scans all agent schedule dirs,
// and fires due entries by injecting them into GOALS.md and starting/nudging loops.
func (b *Bot) runScheduler() {
	defer close(b.schedDone)

	// Align to the top of the next minute for reliable cron matching.
	alignDelay := time.Until(time.Now().Truncate(time.Minute).Add(time.Minute))
	select {
	case <-b.schedStop:
		return
	case <-time.After(alignDelay):
	}

	b.checkSchedules() // fire immediately at first aligned minute
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.schedStop:
			return
		case <-ticker.C:
			b.checkSchedules()
		}
	}
}

func (b *Bot) checkSchedules() {
	for name, ch := range b.cfg.Channels {
		fired, err := schedule.FireDue(ch.AgentDir)
		if err != nil {
			log.Printf("[keel] scheduler: error scanning %s: %v", name, err)
			continue
		}
		if len(fired) == 0 {
			continue
		}

		// Log what fired
		var names []string
		for _, e := range fired {
			names = append(names, e.Name)
		}
		log.Printf("[keel] scheduler: fired %d entries for %s: %s", len(fired), name, strings.Join(names, ", "))
		b.sendStatus(name, fmt.Sprintf("Scheduled goals fired: %s", strings.Join(names, ", ")))

		// Start or nudge the agent loop
		if b.loopMgr.IsRunning(name) {
			b.loopMgr.Nudge(name)
		} else {
			onOutput, onLifecycle := b.sessionHandlers(name, ch.ChannelID)
			if err := b.loopMgr.Start(name, ch.AgentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery, onOutput, onLifecycle); err != nil {
				log.Printf("[keel] scheduler: error starting %s: %v", name, err)
			}
		}
	}
}
```

Add import: `"github.com/SeanoChang/keel/internal/schedule"`

- [ ] **Step 3: Start scheduler in Bot.Start()**

In `Bot.Start()`, add after the tailer loop:

```go
go b.runScheduler()
```

- [ ] **Step 4: Stop scheduler in Bot.Stop()**

In `Bot.Stop()`, add before `b.session.Close()`:

```go
close(b.schedStop)
<-b.schedDone
```

- [ ] **Step 5: Verify it compiles**

Run: `go build -o keel .`
Expected: Compiles without errors

- [ ] **Step 6: Run all tests**

Run: `go test ./... -v`
Expected: All tests pass

- [ ] **Step 7: Commit**

```bash
git add internal/discord/bot.go
git commit -m "feat(schedule): add 60s scheduler goroutine to Bot for firing due entries"
```

---

### Task 7: Discord `!schedule` command

**Files:**
- Modify: `internal/discord/commands.go`

- [ ] **Step 1: Add schedule command to handleCommand switch**

In the `switch cmd` block in `handleCommand`, add:

```go
case "schedule":
	response = b.cmdSchedule(ch, args)
```

- [ ] **Step 2: Implement cmdSchedule**

```go
func (b *Bot) cmdSchedule(ch config.ChannelConfig, args string) string {
	entries, err := schedule.ScanDir(ch.AgentDir)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if len(entries) == 0 {
		return "No scheduled goals."
	}
	var sb strings.Builder
	sb.WriteString("**Scheduled Goals**\n")
	for _, e := range entries {
		kind := "one-shot"
		when := e.TimeDir.At.Format("2006-01-02 15:04")
		if e.TimeDir.Kind == schedule.KindRecurring {
			kind = "recurring"
			when = e.TimeDir.CronExpr
		}
		preview := e.Content
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		sb.WriteString(fmt.Sprintf("`[%s]` **%s** @ %s\n%s\n\n", kind, e.Name, when, preview))
	}
	return sb.String()
}
```

Add import: `"github.com/SeanoChang/keel/internal/schedule"`

- [ ] **Step 3: Update help text**

Add to `cmdHelp()`:

```go
"`!schedule` — show scheduled goals\n" +
```

- [ ] **Step 4: Verify it compiles**

Run: `go build -o keel .`
Expected: Compiles

- [ ] **Step 5: Commit**

```bash
git add internal/discord/commands.go
git commit -m "feat(schedule): add !schedule Discord command to list scheduled goals"
```

---

### Task 8: Update CLAUDE.md and PROGRAM.md docs

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add schedule section to CLAUDE.md**

Under "## Agent Directory Layout", add:

```markdown
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
```

Under "## Commands", add:
```
- `keel schedule add <agent> <time> <name> <content>` — schedule a future goal
- `keel schedule ls <agent>` — list scheduled goals
- `keel schedule rm <agent> <name>` — remove a scheduled goal
- `keel schedule clear <agent>` — remove all scheduled goals
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: add schedule system to CLAUDE.md"
```

---

## Summary

| Task | What | Files |
|------|------|-------|
| 1 | Types + ParseTimeDir | `internal/schedule/schedule.go`, test |
| 2 | ScanDir | same files |
| 3 | IsDue | same files |
| 4 | AppendScheduledGoal + FireDue + cleanup | `internal/workspace/workspace.go`, `internal/schedule/schedule.go`, tests |
| 5 | CLI `keel schedule` | `cmd/schedule.go`, `cmd/root.go` |
| 6 | Scheduler goroutine | `internal/discord/bot.go` |
| 7 | Discord `!schedule` | `internal/discord/commands.go` |
| 8 | Documentation | `CLAUDE.md` |

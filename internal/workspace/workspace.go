package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultProgram = `# Session Program

## Orient
Read GOALS.md. Identify the highest-priority goal. Read MEMORY.md for prior context.

## Execute
Work the goal thoroughly. Default to deep, rigorous work.
- If tagged [quick], keep it concise — do the task directly, no follow-up branching.
- Otherwise, go deep. Research extensively, cross-reference, produce rigorous output.

For complex goals, break them into sub-goals in GOALS.md. Use this format:

## [YYYY-MM-DD HH:MM] self-directed: <brief title>
<description of what to investigate or build>

Do the actual work. Do not deliberate about what you could do — just do it.
Never ask clarifying questions — make your best judgment and proceed. You are running autonomously.
Verify your work before marking a goal complete. Read back files you wrote. Confirm answers are grounded.

## Reflect
After completing a goal, reflect: what new questions, deeper angles, or unexplored directions did this work reveal?

If there are productive follow-up directions worth exploring:
- Add them as sub-goals to GOALS.md using the ## [timestamp] self-directed: format
- Use your judgment on scope — go deep on the most important directions rather than spreading thin
- Stay grounded in the original topic. Branch deeply, not randomly.

If follow-ups would mostly cover ground already explored, don't force it.

## Log
When a goal is complete, remove it from GOALS.md.
Append a concise summary of what you accomplished to log.md (one or two lines per goal).
Update MEMORY.md with any context a future session would need.

## Deliver
If you produce a deliverable (report, analysis, research, data, etc.), write its full content to DELIVER.md.
DELIVER.md is the only file whose contents get sent to the requesting channel.

## Schedule
You can self-schedule future goals by creating files in your schedule/ directory:
- One-shot: schedule/<ISO-datetime>/<name>.md (e.g. schedule/2026-03-18T09:00/check-report.md)
- Recurring: schedule/cron-<min>_<hour>_<dom>_<mon>_<dow>/<name>.md (e.g. schedule/cron-0_9_*_*_1-5/morning-brief.md)
The file content becomes the goal text injected into GOALS.md when the schedule fires.
One-shot dirs are deleted after firing. Recurring dirs persist.

## Continue or Exit
If more goals remain in GOALS.md, go back to Orient and work the next one.
Do NOT exit while goals remain — keep working.

When all goals are complete AND no productive follow-up directions remain:
- Write a comprehensive report of everything you accomplished as your final text response
- Create an empty file called .exit to signal you are done

Do NOT create .exit just because the initial request was "answered." The loop exists for sustained deep work. Only exit when you've genuinely exhausted the topic — further work would yield diminishing returns.

## Rules
- Your text responses do NOT reach the user. Only DELIVER.md, log.md, and your final report are visible.
- Never ask clarifying questions. Make your best judgment and proceed.
- Sub-goals MUST use the ## [YYYY-MM-DD HH:MM] self-directed: format. Other formats are not recognized by the loop.
- ONLY create .exit when GOALS.md is empty and all productive directions are exhausted.
- NEVER write status text (e.g. "All done!", "No goals remaining") to GOALS.md. Only remove completed goals or add new sub-goals.
- If genuinely blocked (missing credentials, inaccessible resources), note the blocker in GOALS.md and end your session. Do NOT create .exit — the loop will retry later.
- If you see a .wrap-up file in the workspace root, finish your current task promptly, summarize your work in DELIVER.md, then create .exit.
- Memory is for future sessions. Put durable context there, not session-specific notes.
- log.md is the receipt. Every goal completed gets a log entry.`

// keep unexported alias so existing internal calls compile unchanged
const defaultProgram = DefaultProgram

const goalsBoilerplate = "# Goals\n\n<!-- Add goals here. Agent removes completed goals. -->\n"

func ReadGoals(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "GOALS.md"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func HasGoals(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "GOALS.md"))
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(stripGoalsBoilerplate(string(data)))) > 0
}

// stripGoalsBoilerplate removes structural content from GOALS.md that isn't an actual goal:
// the # heading, HTML comments, blockquote hints, and blank lines.
func stripGoalsBoilerplate(s string) string {
	// Strip HTML comments (<!-- ... -->), possibly spanning multiple lines
	for {
		start := strings.Index(s, "<!--")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "-->")
		if end == -1 {
			s = s[:start]
			break
		}
		s = s[:start] + s[start+end+3:]
	}
	// Strip top-level heading lines (# ...) and blockquote hint lines (> ...)
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") || trimmed == "#" {
			continue
		}
		if strings.HasPrefix(trimmed, "> ") || trimmed == ">" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// HasGoalHeaders checks if GOALS.md contains structured goal headers
// (## [timestamp] from/scheduled/self-directed:) as opposed to agent-written status text.
func HasGoalHeaders(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "GOALS.md"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "## [") {
			return true
		}
	}
	return false
}

func AppendGoal(dir, username, message string) error {
	f, err := os.OpenFile(filepath.Join(dir, "GOALS.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04")
	_, err = fmt.Fprintf(f, "\n## [%s] from %s\n%s\n", ts, username, message)
	return err
}

func ClearGoals(dir string) error {
	return os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte(goalsBoilerplate), 0644)
}

// WriteDefaultProgram overwrites PROGRAM.md with the built-in DefaultProgram.
func WriteDefaultProgram(dir string) error {
	return os.WriteFile(filepath.Join(dir, "PROGRAM.md"), []byte(DefaultProgram+"\n"), 0644)
}

func ReadProgram(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "PROGRAM.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return defaultProgram, nil
		}
		return "", err
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return defaultProgram, nil
	}
	return s, nil
}

func ReadLogTail(dir string, n int) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "log.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	all := strings.TrimSpace(string(data))
	if all == "" {
		return nil, nil
	}
	lines := strings.Split(all, "\n")
	if n >= len(lines) {
		return lines, nil
	}
	return lines[len(lines)-n:], nil
}

func ReadMemory(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// MemoryTokenCount returns approximate token count (words * 4/3).
func MemoryTokenCount(dir string) (int, error) {
	content, err := ReadMemory(dir)
	if err != nil {
		return 0, err
	}
	words := len(strings.Fields(content))
	return words * 4 / 3, nil
}

// HasExitSignal checks if the agent has requested to stop the loop.
func HasExitSignal(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".exit"))
	return err == nil
}

// ClearExitSignal removes the .exit sentinel file.
func ClearExitSignal(dir string) error {
	err := os.Remove(filepath.Join(dir, ".exit"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// HasWrapUpSignal checks if the user has requested a wrap-up.
func HasWrapUpSignal(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".wrap-up"))
	return err == nil
}

// WriteWrapUpSignal creates the .wrap-up sentinel file.
func WriteWrapUpSignal(dir string) error {
	return os.WriteFile(filepath.Join(dir, ".wrap-up"), []byte(""), 0644)
}

// ClearWrapUpSignal removes the .wrap-up sentinel file.
func ClearWrapUpSignal(dir string) error {
	err := os.Remove(filepath.Join(dir, ".wrap-up"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ReadDeliver reads the DELIVER.md file. Returns "" if not found.
func ReadDeliver(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "DELIVER.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// ClearDeliver removes DELIVER.md after its contents have been relayed.
func ClearDeliver(dir string) error {
	err := os.Remove(filepath.Join(dir, "DELIVER.md"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func LogSize(dir string) (int64, error) {
	info, err := os.Stat(filepath.Join(dir, "log.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

// AppendScheduledGoal adds a scheduled goal to GOALS.md with metadata hint.
func AppendScheduledGoal(dir, name, content string) error {
	f, err := os.OpenFile(filepath.Join(dir, "GOALS.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04")
	_, err = fmt.Fprintf(f, "\n## [%s] scheduled: %s\n%s\n\n> Scheduled task. When complete, remove this goal. If your work reveals productive follow-up directions, add them as sub-goals. If no goals remain, create .exit.\n", ts, name, content)
	return err
}

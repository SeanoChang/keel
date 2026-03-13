package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultProgram = "Read GOALS.md. Work on the highest-priority goal. When complete, remove it from GOALS.md. Log what you accomplished to log.md. Update MEMORY.md with any useful context. When no goals remain, create an empty file called .exit to signal you are done."

// keep unexported alias so existing internal calls compile unchanged
const defaultProgram = DefaultProgram

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
// the # heading, HTML comments, and blank lines.
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
	// Strip top-level heading lines (# ...)
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") || trimmed == "#" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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
	return os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte(""), 0644)
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
	_, err = fmt.Fprintf(f, "\n## [%s] scheduled: %s\n%s\n\n> Scheduled task. When complete, remove this goal. If no other goals remain, create .exit to signal you are done.\n", ts, name, content)
	return err
}

package schedule

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/SeanoChang/keel/internal/workspace"
)

type Kind int

const (
	KindOneShot   Kind = iota
	KindRecurring
)

type TimeDir struct {
	Kind     Kind
	Raw      string
	At       time.Time
	CronExpr string
}

type Entry struct {
	TimeDir  TimeDir
	Name     string
	Content  string
	FilePath string
}

const isoLayout = "2006-01-02T15:04"

func ParseTimeDir(name string) (TimeDir, error) {
	if strings.HasPrefix(name, "cron-") {
		expr := strings.ReplaceAll(strings.TrimPrefix(name, "cron-"), "_", " ")
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
			continue
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
		nowMinute := now.Truncate(time.Minute)
		prev := sched.Next(nowMinute.Add(-1 * time.Minute))
		if !prev.Equal(nowMinute) {
			return false
		}
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

	for raw, td := range firedTimeDirs {
		schedDir := filepath.Join(agentDir, "schedule", raw)
		switch td.Kind {
		case KindOneShot:
			if err := os.RemoveAll(schedDir); err != nil {
				return fired, fmt.Errorf("remove one-shot schedule %s: %w", raw, err)
			}
		case KindRecurring:
			if err := writeLastFired(agentDir, raw); err != nil {
				return fired, fmt.Errorf("write last-fired for %s: %w", raw, err)
			}
		}
	}

	return fired, nil
}

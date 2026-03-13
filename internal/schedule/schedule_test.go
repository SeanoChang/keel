package schedule

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	os.MkdirAll(filepath.Join(dir, "schedule", "2026-03-13T08:30"), 0755)
	os.WriteFile(filepath.Join(dir, "schedule", "2026-03-13T08:30", "check-pce.md"), []byte("Check PCE data"), 0644)
	os.MkdirAll(filepath.Join(dir, "schedule", "cron-30_8_*_*_1-5"), 0755)
	os.WriteFile(filepath.Join(dir, "schedule", "cron-30_8_*_*_1-5", "morning-brief.md"), []byte("Run morning briefing"), 0644)
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
	entries, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

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
	schedDir := filepath.Join(dir, "schedule", td.Raw)
	os.MkdirAll(schedDir, 0755)
	os.WriteFile(filepath.Join(schedDir, ".last-fired"), []byte(now.Truncate(time.Minute).Format(time.RFC3339)), 0644)

	if IsDue(td, dir) {
		t.Error("should not be due if already fired this minute")
	}
}

func TestFire_InjectsGoalAndReturnsEntries(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte("# Goals\n"), 0644)
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

func TestParseFrontmatter_Urgent(t *testing.T) {
	raw := "---\ntype: urgent\n---\nCheck PCE data"
	priority, content := parseFrontmatter(raw)
	if priority != PriorityUrgent {
		t.Errorf("expected PriorityUrgent, got %v", priority)
	}
	if content != "Check PCE data" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestParseFrontmatter_Normal(t *testing.T) {
	raw := "---\ntype: normal\n---\nMorning brief"
	priority, content := parseFrontmatter(raw)
	if priority != PriorityNormal {
		t.Errorf("expected PriorityNormal, got %v", priority)
	}
	if content != "Morning brief" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	raw := "Just a plain goal"
	priority, content := parseFrontmatter(raw)
	if priority != PriorityNormal {
		t.Errorf("expected PriorityNormal, got %v", priority)
	}
	if content != "Just a plain goal" {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestScanDir_UrgentEntry(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "schedule", "2026-03-13T08:30"), 0755)
	os.WriteFile(filepath.Join(dir, "schedule", "2026-03-13T08:30", "check-pce.md"),
		[]byte("---\ntype: urgent\n---\nCheck PCE data"), 0644)

	entries, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Priority != PriorityUrgent {
		t.Error("expected urgent priority")
	}
	if entries[0].Content != "Check PCE data" {
		t.Errorf("frontmatter should be stripped from content, got %q", entries[0].Content)
	}
}

func TestHasUrgent(t *testing.T) {
	normal := []Entry{{Priority: PriorityNormal}, {Priority: PriorityNormal}}
	if HasUrgent(normal) {
		t.Error("should not have urgent")
	}
	mixed := []Entry{{Priority: PriorityNormal}, {Priority: PriorityUrgent}}
	if !HasUrgent(mixed) {
		t.Error("should have urgent")
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

	if _, err := os.Stat(cronDir); os.IsNotExist(err) {
		t.Error("cron dir should be kept after firing")
	}
	if _, err := os.Stat(filepath.Join(cronDir, ".last-fired")); os.IsNotExist(err) {
		t.Error(".last-fired should be written after cron fires")
	}
}

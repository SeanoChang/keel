package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte("## Goal 1\nDo something\n"), 0644)
	os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("Some memory content here\n"), 0644)
	os.WriteFile(filepath.Join(dir, "log.md"), []byte("- did thing 1\n- did thing 2\n"), 0644)
	os.WriteFile(filepath.Join(dir, "PROGRAM.md"), []byte("Work on goals. Exit when done.\n"), 0644)
	return dir
}

func TestReadGoals(t *testing.T) {
	dir := setupWorkspace(t)
	goals, err := ReadGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goals, "Goal 1") {
		t.Errorf("goals missing expected content: %q", goals)
	}
}

func TestHasGoals(t *testing.T) {
	dir := setupWorkspace(t)
	if !HasGoals(dir) {
		t.Error("expected HasGoals=true")
	}
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte(""), 0644)
	if HasGoals(dir) {
		t.Error("expected HasGoals=false for empty file")
	}
	os.Remove(filepath.Join(dir, "GOALS.md"))
	if HasGoals(dir) {
		t.Error("expected HasGoals=false for missing file")
	}

	// Boilerplate-only file should be treated as empty
	boilerplate := "# Goals\n\n<!-- Add goals here. Agent removes completed goals. -->\n"
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte(boilerplate), 0644)
	if HasGoals(dir) {
		t.Error("expected HasGoals=false for boilerplate-only file")
	}

	// Boilerplate + actual goal should be true
	withGoal := boilerplate + "\n## [2026-03-12 15:00] from seanoc\nResearch crude oil\n"
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte(withGoal), 0644)
	if !HasGoals(dir) {
		t.Error("expected HasGoals=true when goals exist alongside boilerplate")
	}
}

func TestAppendGoal(t *testing.T) {
	dir := setupWorkspace(t)
	err := AppendGoal(dir, "testuser", "Research Wyckoff structures")
	if err != nil {
		t.Fatal(err)
	}
	goals, _ := ReadGoals(dir)
	if !strings.Contains(goals, "Research Wyckoff structures") {
		t.Error("appended goal not found")
	}
	if !strings.Contains(goals, "from testuser") {
		t.Error("username not found in goal header")
	}
}

func TestReadProgram(t *testing.T) {
	dir := setupWorkspace(t)
	program, err := ReadProgram(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(program, "Work on goals") {
		t.Errorf("unexpected program: %q", program)
	}
}

func TestReadProgramDefault(t *testing.T) {
	dir := t.TempDir()
	program, err := ReadProgram(dir)
	if err != nil {
		t.Fatal(err)
	}
	if program == "" {
		t.Error("expected default program, got empty")
	}
}

func TestEnsureProgramCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	// No PROGRAM.md exists yet
	if err := EnsureProgram(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "PROGRAM.md"))
	if err != nil {
		t.Fatal("expected PROGRAM.md to be created")
	}
	if !strings.Contains(string(data), "Session Program") {
		t.Error("expected default program content")
	}
}

func TestEnsureProgramPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	custom := "Custom program content"
	os.WriteFile(filepath.Join(dir, "PROGRAM.md"), []byte(custom), 0644)

	if err := EnsureProgram(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "PROGRAM.md"))
	if string(data) != custom {
		t.Errorf("EnsureProgram overwrote existing file: got %q", string(data))
	}
}

func TestReadLogTail(t *testing.T) {
	dir := setupWorkspace(t)
	lines, err := ReadLogTail(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
}

func TestMemoryTokenCount(t *testing.T) {
	dir := setupWorkspace(t)
	count, err := MemoryTokenCount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Error("expected non-zero token count")
	}
}

func TestHasExitSignal(t *testing.T) {
	dir := setupWorkspace(t)

	// No .exit file → false
	if HasExitSignal(dir) {
		t.Error("expected no exit signal initially")
	}

	// Create .exit → true
	os.WriteFile(filepath.Join(dir, ".exit"), []byte(""), 0644)
	if !HasExitSignal(dir) {
		t.Error("expected exit signal after creating .exit")
	}

	// Clear it → false
	if err := ClearExitSignal(dir); err != nil {
		t.Fatal(err)
	}
	if HasExitSignal(dir) {
		t.Error("expected no exit signal after clearing")
	}

	// Clearing when already absent → no error
	if err := ClearExitSignal(dir); err != nil {
		t.Errorf("clearing absent .exit should not error: %v", err)
	}
}

func TestClearGoals(t *testing.T) {
	dir := setupWorkspace(t)
	err := ClearGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if HasGoals(dir) {
		t.Error("expected empty goals after clear")
	}
}

func TestReadDeliver(t *testing.T) {
	dir := t.TempDir()
	content := "# Crude Oil Analysis\n\nWTI is trading at $68.50...\n"
	os.WriteFile(filepath.Join(dir, "DELIVER.md"), []byte(content), 0644)

	got, err := ReadDeliver(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != content {
		t.Errorf("expected %q, got %q", content, got)
	}
}

func TestReadDeliverMissing(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadDeliver(dir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestClearDeliver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DELIVER.md")
	os.WriteFile(path, []byte("some content"), 0644)

	if err := ClearDeliver(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected DELIVER.md to be removed")
	}

	// Clearing when already absent should not error
	if err := ClearDeliver(dir); err != nil {
		t.Errorf("clearing absent DELIVER.md should not error: %v", err)
	}
}

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

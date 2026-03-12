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

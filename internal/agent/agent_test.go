package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func setupAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte("## Goal\nDo work\n"), 0644)
	os.WriteFile(filepath.Join(dir, "PROGRAM.md"), []byte("Work hard.\n"), 0644)
	os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("I remember things.\n"), 0644)
	os.MkdirAll(filepath.Join(dir, ".claude", "agents"), 0755)
	os.WriteFile(filepath.Join(dir, ".claude", "agents", "test.md"), []byte("---\nname: test\n---\n"), 0644)
	return dir
}

func TestNewAgent(t *testing.T) {
	dir := setupAgentDir(t)
	a, err := New("test", dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "test" {
		t.Errorf("Name = %q", a.Name)
	}
	if a.Dir != dir {
		t.Errorf("Dir = %q", a.Dir)
	}
}

func TestNewAgentMissingDir(t *testing.T) {
	_, err := New("test", "/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestAgentHasGoals(t *testing.T) {
	dir := setupAgentDir(t)
	a, _ := New("test", dir)
	if !a.HasGoals() {
		t.Error("expected HasGoals=true")
	}
}

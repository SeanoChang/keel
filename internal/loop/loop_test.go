package loop

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func setupTestAgent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte("## Goal\nTest\n"), 0644)
	os.WriteFile(filepath.Join(dir, "PROGRAM.md"), []byte("Do the thing.\n"), 0644)
	os.WriteFile(filepath.Join(dir, "log.md"), []byte(""), 0644)
	return dir
}

func TestRunOnceExecutesCommand(t *testing.T) {
	dir := setupTestAgent(t)

	loop := &AgentLoop{
		Name: "test",
		Dir:  dir,
		CommandBuilder: func(ctx context.Context, name, dir, program string) *CommandSpec {
			return &CommandSpec{
				Name: "bash",
				Args: []string{"-c", "echo '' > " + filepath.Join(dir, "GOALS.md")},
				Dir:  dir,
			}
		},
		SleepDuration: 100 * time.Millisecond,
	}

	err := loop.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "GOALS.md"))
	if len(data) > 1 {
		t.Errorf("GOALS.md should be empty, got: %q", string(data))
	}
}

func TestLoopStopsWhenGoalsEmpty(t *testing.T) {
	dir := setupTestAgent(t)

	var runs int
	var mu sync.Mutex

	loop := &AgentLoop{
		Name: "test",
		Dir:  dir,
		CommandBuilder: func(ctx context.Context, name, dir, program string) *CommandSpec {
			mu.Lock()
			runs++
			mu.Unlock()
			os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte(""), 0644)
			return &CommandSpec{
				Name: "true",
				Args: nil,
				Dir:  dir,
			}
		},
		SleepDuration: 50 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	loop.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Errorf("expected 1 run, got %d", runs)
	}
}

func TestLoopExitsWhenGoalsStale(t *testing.T) {
	dir := setupTestAgent(t)

	var runs int
	var mu sync.Mutex
	var lifecycleEvents []string

	loop := &AgentLoop{
		Name: "test",
		Dir:  dir,
		CommandBuilder: func(ctx context.Context, name, dir, program string) *CommandSpec {
			mu.Lock()
			runs++
			mu.Unlock()
			// Simulate agent that reads files but never modifies GOALS.md
			return &CommandSpec{
				Name: "true",
				Args: nil,
				Dir:  dir,
			}
		},
		SleepDuration: 10 * time.Millisecond,
		MaxStale:      2,
		OnLifecycle: func(event string) {
			mu.Lock()
			lifecycleEvents = append(lifecycleEvents, event)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	loop.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if runs != 2 {
		t.Errorf("expected 2 runs before stale exit, got %d", runs)
	}
	// Should have emitted "stale" lifecycle event
	found := false
	for _, ev := range lifecycleEvents {
		if ev == "stale" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'stale' lifecycle event, got: %v", lifecycleEvents)
	}
}

func TestManagerStartStop(t *testing.T) {
	dir := setupTestAgent(t)

	mgr := NewManager()

	builder := func(ctx context.Context, name, dir, program string) *CommandSpec {
		return &CommandSpec{
			Name: "sleep",
			Args: []string{"10"},
			Dir:  dir,
		}
	}

	err := mgr.Start("test", dir, builder, 100*time.Millisecond, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !mgr.IsRunning("test") {
		t.Error("expected loop to be running")
	}

	err = mgr.Start("test", dir, builder, 100*time.Millisecond, 0, nil, nil)
	if err == nil {
		t.Error("expected error when starting duplicate")
	}

	mgr.Stop("test")
	time.Sleep(200 * time.Millisecond)
	if mgr.IsRunning("test") {
		t.Error("expected loop to be stopped")
	}
}

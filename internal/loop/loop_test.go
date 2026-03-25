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
	// Use proper goal headers so HasGoalHeaders returns true and stale
	// detection counts down (without headers, the shortcut exits immediately).
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte("## [2026-01-01 00:00] from test\nDo something\n"), 0644)

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

func TestLoopExitsOnExitSignal(t *testing.T) {
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
			// Agent creates .exit sentinel on first run
			os.WriteFile(filepath.Join(dir, ".exit"), []byte(""), 0644)
			return &CommandSpec{
				Name: "true",
				Args: nil,
				Dir:  dir,
			}
		},
		SleepDuration: 10 * time.Millisecond,
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
	if runs != 1 {
		t.Errorf("expected 1 run before exit signal, got %d", runs)
	}
	found := false
	for _, ev := range lifecycleEvents {
		if ev == "agent_exit" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'agent_exit' lifecycle event, got: %v", lifecycleEvents)
	}
	// Sentinel file should be cleaned up
	if _, err := os.Stat(filepath.Join(dir, ".exit")); !os.IsNotExist(err) {
		t.Error("expected .exit to be removed after loop exit")
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

func TestLoopClearsStaleExitOnStartup(t *testing.T) {
	dir := setupTestAgent(t)
	// Pre-plant a stale .exit from a previous run.
	os.WriteFile(filepath.Join(dir, ".exit"), []byte(""), 0644)

	var runs int
	var mu sync.Mutex
	var events []string

	loop := &AgentLoop{
		Name: "test",
		Dir:  dir,
		CommandBuilder: func(ctx context.Context, name, dir, program string) *CommandSpec {
			mu.Lock()
			runs++
			mu.Unlock()
			// Agent clears goals (normal completion without .exit)
			os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte(""), 0644)
			return &CommandSpec{Name: "true", Dir: dir}
		},
		SleepDuration: 10 * time.Millisecond,
		OnLifecycle: func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	loop.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Errorf("expected 1 run, got %d", runs)
	}
	// Should exit via goals_empty, NOT agent_exit (stale .exit was cleared at startup)
	for _, ev := range events {
		if ev == "agent_exit" {
			t.Error("loop should not have exited via agent_exit with stale .exit")
		}
	}
}

func TestLoopWrapUpSignal(t *testing.T) {
	dir := setupTestAgent(t)

	var runs int
	var mu sync.Mutex
	var events []string

	loop := &AgentLoop{
		Name: "test",
		Dir:  dir,
		CommandBuilder: func(ctx context.Context, name, dir, program string) *CommandSpec {
			mu.Lock()
			runs++
			mu.Unlock()
			// Simulate: user sends !wrap-up during session
			os.WriteFile(filepath.Join(dir, ".wrap-up"), []byte(""), 0644)
			return &CommandSpec{Name: "true", Dir: dir}
		},
		SleepDuration: 10 * time.Millisecond,
		OnLifecycle: func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	loop.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Errorf("expected 1 run before wrap-up, got %d", runs)
	}
	found := false
	for _, ev := range events {
		if ev == "wrap_up" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected wrap_up lifecycle event, got: %v", events)
	}
	// .wrap-up should be cleaned up
	if _, err := os.Stat(filepath.Join(dir, ".wrap-up")); !os.IsNotExist(err) {
		t.Error("expected .wrap-up to be removed")
	}
}

func TestLoopWrapUpWithExitSignal(t *testing.T) {
	dir := setupTestAgent(t)

	var mu sync.Mutex
	var events []string

	loop := &AgentLoop{
		Name: "test",
		Dir:  dir,
		CommandBuilder: func(ctx context.Context, name, dir, program string) *CommandSpec {
			// Simulate !wrap-up arriving during session + agent creating .exit
			os.WriteFile(filepath.Join(dir, ".wrap-up"), []byte(""), 0644)
			os.WriteFile(filepath.Join(dir, ".exit"), []byte(""), 0644)
			return &CommandSpec{Name: "true", Dir: dir}
		},
		SleepDuration: 10 * time.Millisecond,
		OnLifecycle: func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	loop.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	// Should emit wrap_up (not agent_exit) since .wrap-up was present
	foundWrapUp := false
	foundAgentExit := false
	for _, ev := range events {
		if ev == "wrap_up" {
			foundWrapUp = true
		}
		if ev == "agent_exit" {
			foundAgentExit = true
		}
	}
	if !foundWrapUp {
		t.Errorf("expected wrap_up lifecycle event, got: %v", events)
	}
	if foundAgentExit {
		t.Error("should NOT emit agent_exit when .wrap-up is present")
	}
	// Both sentinels should be cleaned up
	if _, err := os.Stat(filepath.Join(dir, ".exit")); !os.IsNotExist(err) {
		t.Error("expected .exit to be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".wrap-up")); !os.IsNotExist(err) {
		t.Error("expected .wrap-up to be removed")
	}
}

func TestLoopStaleWithNoHeaders(t *testing.T) {
	dir := setupTestAgent(t)
	// Write status text without goal headers — agent junk
	os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte("All tasks completed!\n"), 0644)

	var runs int
	var mu sync.Mutex
	var events []string

	loop := &AgentLoop{
		Name: "test",
		Dir:  dir,
		CommandBuilder: func(ctx context.Context, name, dir, program string) *CommandSpec {
			mu.Lock()
			runs++
			mu.Unlock()
			return &CommandSpec{Name: "true", Dir: dir}
		},
		SleepDuration: 10 * time.Millisecond,
		MaxStale:      3,
		OnLifecycle: func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	loop.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	// Should exit after 1 session (shortcut: no headers + unchanged → goals_empty)
	// NOT after MaxStale (3) sessions
	if runs != 1 {
		t.Errorf("expected 1 run (stale shortcut), got %d", runs)
	}
	found := false
	for _, ev := range events {
		if ev == "goals_empty" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected goals_empty lifecycle event, got: %v", events)
	}
}

func TestLoopPostSessionGoalsEmpty(t *testing.T) {
	dir := setupTestAgent(t)

	var runs int
	var mu sync.Mutex
	var events []string

	loop := &AgentLoop{
		Name: "test",
		Dir:  dir,
		CommandBuilder: func(ctx context.Context, name, dir, program string) *CommandSpec {
			mu.Lock()
			runs++
			mu.Unlock()
			// Agent clears goals but does NOT create .exit
			os.WriteFile(filepath.Join(dir, "GOALS.md"), []byte("# Goals\n"), 0644)
			return &CommandSpec{Name: "true", Dir: dir}
		},
		SleepDuration: 5 * time.Second, // long sleep to prove we don't wait
		OnLifecycle: func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		},
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	loop.Run(ctx)
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Errorf("expected 1 run, got %d", runs)
	}
	// Should exit quickly via post-session goals check, not after sleeping 5s
	if elapsed > 3*time.Second {
		t.Errorf("loop took too long (%v), should have exited without sleeping", elapsed)
	}
	found := false
	for _, ev := range events {
		if ev == "goals_empty" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected goals_empty lifecycle event, got: %v", events)
	}
}

func TestLoopEmitsStoppedOnCancel(t *testing.T) {
	dir := setupTestAgent(t)

	var mu sync.Mutex
	var events []string

	loop := &AgentLoop{
		Name: "test",
		Dir:  dir,
		CommandBuilder: func(ctx context.Context, name, dir, program string) *CommandSpec {
			return &CommandSpec{
				Name: "sleep",
				Args: []string{"10"},
				Dir:  dir,
			}
		},
		SleepDuration: 10 * time.Millisecond,
		OnLifecycle: func(event string) {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	loop.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ev := range events {
		if ev == "stopped" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'stopped' lifecycle event on cancel, got: %v", events)
	}
}

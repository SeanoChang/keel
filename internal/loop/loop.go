package loop

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/SeanoChang/keel/internal/workspace"
)

// CommandSpec describes a command to execute. Tests replace real claude with mocks.
type CommandSpec struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

// CommandBuilder creates a CommandSpec for a given agent invocation.
type CommandBuilder func(ctx context.Context, name, dir, program string) *CommandSpec

// DefaultCommandBuilder returns the real claude CLI invocation.
func DefaultCommandBuilder(ctx context.Context, name, dir, program string) *CommandSpec {
	return &CommandSpec{
		Name: "claude",
		Args: []string{
			"--agent", name,
			"--permission-mode", "dontAsk",
			"-p", program,
		},
		Dir: dir,
	}
}

type AgentLoop struct {
	Name             string
	Dir              string
	CommandBuilder   CommandBuilder
	SleepDuration    time.Duration
	ArchiveEvery     int // run cubit archive every N sessions (0 = disabled)
	MaxErrors        int // exit loop after N consecutive session errors (0 = default 3)
	OnOutput         func(line string)
	OnLifecycle      func(event string) // session_start, session_end, error, sleeping, goals_empty, woke
	Wake             chan struct{}       // signal to interrupt sleep early (new goals arrived)
}

func (l *AgentLoop) maxErrors() int {
	if l.MaxErrors > 0 {
		return l.MaxErrors
	}
	return 3
}

func (l *AgentLoop) lifecycle(event string) {
	if l.OnLifecycle != nil {
		l.OnLifecycle(event)
	}
}

// RunOnce executes one agent session.
func (l *AgentLoop) RunOnce(ctx context.Context) error {
	program, err := workspace.ReadProgram(l.Dir)
	if err != nil {
		return fmt.Errorf("read program: %w", err)
	}

	spec := l.CommandBuilder(ctx, l.Name, l.Dir, program)
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = spec.Env
	}

	if l.OnOutput != nil {
		return l.runStreaming(ctx, cmd)
	}

	// CLI mode: direct pipe to terminal
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("agent %s exited: %w", l.Name, err)
	}
	return nil
}

// runStreaming pipes stderr line-by-line through OnOutput for real-time activity.
func (l *AgentLoop) runStreaming(ctx context.Context, cmd *exec.Cmd) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("command not found: %w", err)
		}
		return fmt.Errorf("start agent: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// stdout → terminal only
	go func() {
		defer wg.Done()
		io.Copy(os.Stdout, stdout)
	}()

	// stderr → terminal + per-line callback
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(os.Stderr, line)
			l.OnOutput(line)
		}
	}()

	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("agent %s exited: %w", l.Name, err)
	}
	return nil
}

// Run loops: check goals -> runOnce -> maybe archive -> sleep -> repeat.
// Exits when goals empty, ctx cancelled, or too many consecutive errors.
func (l *AgentLoop) Run(ctx context.Context) {
	var sessions int
	var consecutiveErrors int
	for {
		if ctx.Err() != nil {
			return
		}
		if !workspace.HasGoals(l.Dir) {
			log.Printf("[keel] %s: no goals, loop exiting", l.Name)
			l.lifecycle("goals_empty")
			return
		}

		log.Printf("[keel] %s: starting session", l.Name)
		l.lifecycle("session_start")
		err := l.RunOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			consecutiveErrors++
			log.Printf("[keel] %s: session error (%d/%d): %v", l.Name, consecutiveErrors, l.maxErrors(), err)
			l.lifecycle(fmt.Sprintf("error: %v (%d/%d)", err, consecutiveErrors, l.maxErrors()))
			if consecutiveErrors >= l.maxErrors() {
				log.Printf("[keel] %s: too many consecutive errors, exiting loop", l.Name)
				l.lifecycle("too_many_errors")
				return
			}
		} else {
			consecutiveErrors = 0
			l.lifecycle("session_end")
		}

		sessions++
		if l.ArchiveEvery > 0 && sessions%l.ArchiveEvery == 0 {
			l.runArchive()
		}

		l.lifecycle("sleeping")
		select {
		case <-ctx.Done():
			return
		case <-l.Wake:
			l.lifecycle("woke")
		case <-time.After(l.SleepDuration):
		}
	}
}

func (l *AgentLoop) runArchive() {
	log.Printf("[keel] %s: running cubit archive", l.Name)
	cmd := exec.Command("cubit", "archive")
	cmd.Dir = l.Dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[keel] %s: archive error: %v\n%s", l.Name, err, output)
	} else {
		log.Printf("[keel] %s: archive complete", l.Name)
	}
}

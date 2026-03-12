package loop

import (
	"context"
	"fmt"
	"log"
	"os/exec"
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
	Name           string
	Dir            string
	CommandBuilder CommandBuilder
	SleepDuration  time.Duration
	ArchiveEvery   int // run cubit archive every N sessions (0 = disabled)
	OnOutput       func(line string)
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

	output, err := cmd.CombinedOutput()
	if l.OnOutput != nil && len(output) > 0 {
		l.OnOutput(string(output))
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("agent %s exited: %w", l.Name, err)
	}
	return nil
}

// Run loops: check goals -> runOnce -> maybe archive -> sleep -> repeat.
// Exits when goals empty or ctx cancelled.
func (l *AgentLoop) Run(ctx context.Context) {
	var sessions int
	for {
		if ctx.Err() != nil {
			return
		}
		if !workspace.HasGoals(l.Dir) {
			log.Printf("[keel] %s: no goals, loop exiting", l.Name)
			return
		}

		log.Printf("[keel] %s: starting session", l.Name)
		err := l.RunOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[keel] %s: session error: %v", l.Name, err)
		}

		sessions++
		if l.ArchiveEvery > 0 && sessions%l.ArchiveEvery == 0 {
			l.runArchive()
		}

		select {
		case <-ctx.Done():
			return
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

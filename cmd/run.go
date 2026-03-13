package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/SeanoChang/keel/internal/agent"
	"github.com/SeanoChang/keel/internal/loop"
	"github.com/SeanoChang/keel/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	runAgentDir string
	runSleep    time.Duration
)

var runCmd = &cobra.Command{
	Use:   "run <agent>",
	Short: "Run an agent loop until GOALS.md is empty",
	Args:  cobra.ExactArgs(1),
	RunE:  runRun,
}

func resolveAgentDir(name, override string) string {
	if override != "" {
		if after, ok := strings.CutPrefix(override, "~"); ok {
			home, _ := os.UserHomeDir()
			return filepath.Join(home, after)
		}
		return override
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ark", "agents-home", name)
}

func runRun(cmd *cobra.Command, args []string) error {
	name := args[0]
	dir := resolveAgentDir(name, runAgentDir)

	a, err := agent.New(name, dir)
	if err != nil {
		return fmt.Errorf("agent %s: %w", name, err)
	}

	if !a.HasGoals() {
		log.Printf("[keel] %s: no goals in %s/GOALS.md, nothing to do", name, dir)
		return nil
	}

	goals, _ := workspace.ReadGoals(dir)
	log.Printf("[keel] %s: starting loop (%s)", name, dir)
	log.Printf("[keel] goals:\n%s", goals)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Printf("[keel] %s: shutting down...", name)
		cancel()
	}()

	l := &loop.AgentLoop{
		Name:           name,
		Dir:            dir,
		CommandBuilder: loop.DefaultCommandBuilder,
		SleepDuration:  runSleep,
	}
	l.Run(ctx)

	log.Printf("[keel] %s: loop finished", name)
	return nil
}

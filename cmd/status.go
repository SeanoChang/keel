package cmd

import (
	"fmt"
	"strings"

	"github.com/SeanoChang/keel/internal/agent"
	"github.com/SeanoChang/keel/internal/workspace"
	"github.com/spf13/cobra"
)

var statusDir string

var statusCmd = &cobra.Command{
	Use:   "status <agent>",
	Short: "Show agent status: goals, memory token count, log tail",
	Args:  cobra.ExactArgs(1),
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	name := args[0]
	dir := resolveAgentDir(name, statusDir)

	_, err := agent.New(name, dir)
	if err != nil {
		return err
	}

	goals, err := workspace.ReadGoals(dir)
	if err != nil {
		goals = "(no GOALS.md)"
	}
	hasGoals := workspace.HasGoals(dir)

	tokens, _ := workspace.MemoryTokenCount(dir)

	logLines, _ := workspace.ReadLogTail(dir, 5)

	fmt.Printf("Agent: %s\n", name)
	fmt.Printf("Dir:   %s\n", dir)
	fmt.Printf("Goals: %v\n", hasGoals)
	if hasGoals {
		fmt.Printf("\n--- GOALS.md ---\n%s\n", strings.TrimSpace(goals))
	}
	fmt.Printf("\nMEMORY.md: ~%d tokens\n", tokens)
	if len(logLines) > 0 {
		fmt.Printf("\n--- log.md (last %d) ---\n", len(logLines))
		for _, line := range logLines {
			fmt.Println(line)
		}
	}
	return nil
}

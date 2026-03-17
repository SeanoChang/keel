package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/SeanoChang/keel/internal/update"
	"github.com/SeanoChang/keel/internal/workspace"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update keel to the latest GitHub release",
	RunE:  runUpdate,
}

func runUpdate(cmd *cobra.Command, args []string) error {
	result, err := update.Run(update.Version, func(msg string) {
		fmt.Println(msg)
	})
	if err != nil {
		return err
	}
	// Reload PROGRAM.md for all agents under ~/.ark/agents-home/
	reloadPrograms()

	if result.AlreadyCurrent {
		fmt.Printf("Already on latest version (%s).\n", result.CurrentVersion)
	} else {
		fmt.Printf("Updated to %s. Re-run keel to use the new version.\n", result.NewVersion)
	}
	return nil
}

// reloadPrograms writes DefaultProgram to PROGRAM.md for every agent in ~/.ark/agents-home/.
func reloadPrograms() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	agentsHome := filepath.Join(home, ".ark", "agents-home")
	entries, err := os.ReadDir(agentsHome)
	if err != nil {
		return
	}
	var reloaded int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(agentsHome, e.Name())
		if err := workspace.WriteDefaultProgram(dir); err == nil {
			reloaded++
		}
	}
	fmt.Printf("Reloaded PROGRAM.md for %d agent(s).\n", reloaded)
}

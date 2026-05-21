package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SeanoChang/keel/internal/workspace"
	"github.com/spf13/cobra"
)

var regenerateCmd = &cobra.Command{
	Use:   "regenerate",
	Short: "Regenerate workspace template files for an agent",
}

var (
	regenerateMailboxDir   string
	regenerateMailboxForce bool
)

var regenerateMailboxCmd = &cobra.Command{
	Use:   "mailbox <agent>",
	Short: "Regenerate mailbox/MAILBOX.md from the bundled template",
	Args:  cobra.ExactArgs(1),
	RunE:  runRegenerateMailbox,
}

func runRegenerateMailbox(cmd *cobra.Command, args []string) error {
	name := args[0]
	dir := resolveAgentDir(name, regenerateMailboxDir)
	path := filepath.Join(dir, "mailbox", "MAILBOX.md")

	if _, err := os.Stat(path); err == nil && !regenerateMailboxForce {
		fmt.Printf("%s exists. Overwrite? [y/N] ", path)
		var ans string
		fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("ensure mailbox dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(workspace.DefaultMailboxDocs), 0644); err != nil {
		return fmt.Errorf("write MAILBOX.md: %w", err)
	}
	fmt.Printf("Wrote %s\n", path)
	return nil
}

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/SeanoChang/keel/internal/update"
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
	if result.AlreadyCurrent {
		fmt.Printf("Already on latest version (%s).\n", result.CurrentVersion)
	} else {
		fmt.Printf("Updated to %s. Re-run keel to use the new version.\n", result.NewVersion)
	}
	return nil
}

// cmd/root.go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "keel",
	Short: "Discord channel proxy for cubit",
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Discord bot and connect to cubit",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("keel serve: not yet implemented")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "keel",
	Short:   "Agent loop manager and Discord bridge",
	Version: Version,
}

func init() {
	// run
	runCmd.Flags().StringVar(&runAgentDir, "dir", "", "Agent directory (default: ~/.ark/agents-home/<agent>)")
	runCmd.Flags().DurationVar(&runSleep, "sleep", 5*time.Second, "Sleep between sessions")
	rootCmd.AddCommand(runCmd)

	// serve
	serveCmd.Flags().StringVar(&serveConfigPath, "config", "config/discord.toml", "Path to Discord config")
	serveCmd.Flags().DurationVar(&serveSleep, "sleep", 5*time.Second, "Sleep between agent sessions")
	serveCmd.Flags().IntVar(&serveArchiveEvery, "archive-every", 50, "Run cubit archive every N sessions (0 = disabled)")
	rootCmd.AddCommand(serveCmd)

	// status
	statusCmd.Flags().StringVar(&statusDir, "dir", "", "Agent directory (default: ~/.ark/agents-home/<agent>)")
	rootCmd.AddCommand(statusCmd)

	// update
	updateCmd.Flags().BoolVar(&updateMigrateOnly, "migrate-only", false, "Run workspace migrations without downloading a new binary")
	rootCmd.AddCommand(updateCmd)

	// schedule
	scheduleAddCmd.Flags().StringVar(&scheduleAddDir, "dir", "", "Agent directory override")
	scheduleLsCmd.Flags().StringVar(&scheduleLsDir, "dir", "", "Agent directory override")
	scheduleRmCmd.Flags().StringVar(&scheduleRmDir, "dir", "", "Agent directory override")
	scheduleClearCmd.Flags().StringVar(&scheduleClearDir, "dir", "", "Agent directory override")
	scheduleCmd.AddCommand(scheduleAddCmd, scheduleLsCmd, scheduleRmCmd, scheduleClearCmd)
	rootCmd.AddCommand(scheduleCmd)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

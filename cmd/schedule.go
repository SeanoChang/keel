package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SeanoChang/keel/internal/schedule"
	"github.com/spf13/cobra"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage agent scheduled goals",
}

var scheduleAddCmd = &cobra.Command{
	Use:   "add <agent> <time> <name> <content>",
	Short: "Add a scheduled goal",
	Long: `Add a scheduled goal for an agent.

Time formats:
  ISO one-shot:  2026-03-13T08:30
  Cron recurring: cron-30_8_*_*_1-5  (underscores separate fields)

Examples:
  keel schedule add alice 2026-03-13T08:30 check-pce "Check PCE data release"
  keel schedule add alice cron-30_8_*_*_1-5 morning-brief "Run morning market briefing"`,
	Args: cobra.ExactArgs(4),
	RunE: runScheduleAdd,
}

var scheduleAddDir string

func runScheduleAdd(cmd *cobra.Command, args []string) error {
	agent, timeStr, name, content := args[0], args[1], args[2], args[3]
	dir := resolveAgentDir(agent, scheduleAddDir)

	if _, err := schedule.ParseTimeDir(timeStr); err != nil {
		return err
	}

	schedDir := filepath.Join(dir, "schedule", timeStr)
	if err := os.MkdirAll(schedDir, 0755); err != nil {
		return fmt.Errorf("create schedule dir: %w", err)
	}

	filePath := filepath.Join(schedDir, name+".md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write schedule file: %w", err)
	}

	fmt.Printf("Scheduled: %s/%s at %s\n", agent, name, timeStr)
	return nil
}

var scheduleLsDir string

var scheduleLsCmd = &cobra.Command{
	Use:   "ls <agent>",
	Short: "List scheduled goals",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent := args[0]
		dir := resolveAgentDir(agent, scheduleLsDir)

		entries, err := schedule.ScanDir(dir)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Println("No scheduled goals.")
			return nil
		}
		for _, e := range entries {
			kind := "one-shot"
			if e.TimeDir.Kind == schedule.KindRecurring {
				kind = "recurring"
			}
			preview := e.Content
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", " ")
			fmt.Printf("  [%s] %s — %s: %s\n", kind, e.TimeDir.Raw, e.Name, preview)
		}
		return nil
	},
}

var scheduleRmDir string

var scheduleRmCmd = &cobra.Command{
	Use:   "rm <agent> <name>",
	Short: "Remove a scheduled goal by name",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent, name := args[0], args[1]
		dir := resolveAgentDir(agent, scheduleRmDir)

		entries, err := schedule.ScanDir(dir)
		if err != nil {
			return err
		}

		var removed int
		for _, e := range entries {
			if e.Name == name {
				if err := os.Remove(e.FilePath); err != nil {
					return fmt.Errorf("remove %s: %w", e.FilePath, err)
				}
				removed++
				fmt.Printf("Removed: %s/%s from %s\n", agent, name, e.TimeDir.Raw)

				parent := filepath.Dir(e.FilePath)
				remaining, _ := filepath.Glob(filepath.Join(parent, "*.md"))
				if len(remaining) == 0 {
					os.RemoveAll(parent)
				}
			}
		}
		if removed == 0 {
			return fmt.Errorf("no schedule entry named %q found for %s", name, agent)
		}
		return nil
	},
}

var scheduleClearDir string

var scheduleClearCmd = &cobra.Command{
	Use:   "clear <agent>",
	Short: "Remove all scheduled goals for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agent := args[0]
		dir := resolveAgentDir(agent, scheduleClearDir)

		schedDir := filepath.Join(dir, "schedule")
		if _, err := os.Stat(schedDir); os.IsNotExist(err) {
			fmt.Println("No schedule directory.")
			return nil
		}

		entries, _ := schedule.ScanDir(dir)
		if err := os.RemoveAll(schedDir); err != nil {
			return fmt.Errorf("remove schedule dir: %w", err)
		}
		fmt.Printf("Cleared %d scheduled goals for %s.\n", len(entries), agent)
		return nil
	},
}

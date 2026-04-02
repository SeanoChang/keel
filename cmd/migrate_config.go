package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var migrateConfigPath string

var migrateConfigCmd = &cobra.Command{
	Use:   "migrate-config",
	Short: "Add missing fields to discord.toml (preserves existing values)",
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(migrateConfigPath)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}

		content := string(data)
		original := content
		var added []string

		// Per-channel fields to ensure exist
		channelFields := []struct {
			key         string
			defaultLine string
		}{
			{"model", `# model = "sonnet"                    # optional: opus, sonnet, haiku`},
		}

		// Process line by line, inserting missing fields after each [channels.*] section's last key
		lines := strings.Split(content, "\n")
		var result []string

		for i := 0; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])

			// Detect [channels.<name>] section headers
			if !strings.HasPrefix(trimmed, "[channels.") || !strings.HasSuffix(trimmed, "]") {
				result = append(result, lines[i])
				continue
			}

			// Found a channel section — collect all lines until next section
			sectionHeader := trimmed
			result = append(result, lines[i])

			// Scan ahead to find section bounds and existing keys
			sectionKeys := make(map[string]bool)
			var sectionLines []string
			j := i + 1
			for j < len(lines) {
				line := strings.TrimSpace(lines[j])
				if strings.HasPrefix(line, "[") {
					break
				}
				cleaned := strings.TrimPrefix(line, "#")
				cleaned = strings.TrimSpace(cleaned)
				if parts := strings.SplitN(cleaned, "=", 2); len(parts) == 2 {
					sectionKeys[strings.TrimSpace(parts[0])] = true
				}
				sectionLines = append(sectionLines, lines[j])
				j++
			}

			// Append section lines to result
			result = append(result, sectionLines...)

			// Add missing fields at end of section (before blank trailing lines)
			for _, f := range channelFields {
				if !sectionKeys[f.key] {
					result = append(result, f.defaultLine)
					added = append(added, fmt.Sprintf("  %s → %s", sectionHeader, f.key))
				}
			}

			// Skip past the section lines we already consumed
			i = j - 1
		}

		content = strings.Join(result, "\n")
		if content == original {
			fmt.Println("Config is up to date — no changes needed.")
			return nil
		}

		if err := os.WriteFile(migrateConfigPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("write config: %w", err)
		}

		fmt.Println("Config updated:")
		for _, a := range added {
			fmt.Println(a)
		}
		return nil
	},
}

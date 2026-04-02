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
			key          string
			defaultLine  string
		}{
			{"model", `# model = "sonnet"                    # optional: opus, sonnet, haiku`},
		}

		// Find all [channels.*] sections and add missing fields
		lines := strings.Split(content, "\n")
		var result []string
		for i := 0; i < len(lines); i++ {
			result = append(result, lines[i])
			trimmed := strings.TrimSpace(lines[i])

			// Detect [channels.<name>] section headers
			if !strings.HasPrefix(trimmed, "[channels.") || !strings.HasSuffix(trimmed, "]") {
				continue
			}

			// Collect existing keys in this section (until next section or EOF)
			sectionKeys := make(map[string]bool)
			j := i + 1
			for j < len(lines) {
				line := strings.TrimSpace(lines[j])
				if strings.HasPrefix(line, "[") {
					break
				}
				// Extract key from "key = value" or "# key = value"
				cleaned := strings.TrimPrefix(line, "#")
				cleaned = strings.TrimSpace(cleaned)
				if parts := strings.SplitN(cleaned, "=", 2); len(parts) == 2 {
					sectionKeys[strings.TrimSpace(parts[0])] = true
				}
				j++
			}

			// Add missing fields after the last key-value line of this section
			for _, f := range channelFields {
				if !sectionKeys[f.key] {
					// Find insertion point: after last non-empty, non-section line
					insertAt := i + 1
					for k := i + 1; k < j; k++ {
						line := strings.TrimSpace(lines[k])
						if line != "" && !strings.HasPrefix(line, "[") {
							insertAt = k + 1
						}
					}
					// Insert relative to result (offset by already-added lines)
					offset := len(result) - (i + 1)
					pos := insertAt - (i + 1) + offset + (i + 1)
					newResult := make([]string, 0, len(result)+1)
					newResult = append(newResult, result[:pos]...)
					newResult = append(newResult, f.defaultLine)
					newResult = append(newResult, result[pos:]...)
					result = newResult
					added = append(added, fmt.Sprintf("  %s → %s", trimmed, f.key))
				}
			}
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

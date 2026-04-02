package config

import (
	"fmt"
	"os"
	"strings"
)

// AppendChannel appends a [channels.<name>] section to a discord.toml file.
// Returns an error if the agent name already exists in the config.
func AppendChannel(configPath, name, channelID, agentDir string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	content := string(data)

	// Check for existing section
	section := fmt.Sprintf("[channels.%s]", name)
	if strings.Contains(content, section) {
		return fmt.Errorf("agent %q already has a channel mapping", name)
	}

	// Append new section
	entry := fmt.Sprintf("\n%s\nchannel_id = %q\nagent_dir = %q\n", section, channelID, agentDir)
	content += entry

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

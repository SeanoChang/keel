package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type BotConfig struct {
	TokenEnv        string   `toml:"token_env"`
	GuildID         string   `toml:"guild_id"`
	SetupChannelID  string   `toml:"setup_channel_id"`
	StatusChannelID string   `toml:"status_channel_id"`
	ErrorChannelID  string   `toml:"error_channel_id"`  // optional: channel for error/retry events
	AdminUsers      []string `toml:"admin_users"`        // Discord user IDs allowed to interact
	PlistLabel      string   `toml:"plist_label"`        // launchd label for restart (e.g. "com.keel.serve")
}

type ChannelConfig struct {
	ChannelID string `toml:"channel_id"`
	AgentDir  string `toml:"agent_dir"`
	Model     string `toml:"model"` // claude model override (e.g. "opus", "sonnet", "haiku")
}

type ManagedBinary struct {
	UpdateCmd []string `toml:"update_cmd"` // command to run (e.g. ["nark", "update"])
}

type Config struct {
	Bot             BotConfig                `toml:"bot"`
	Channels        map[string]ChannelConfig `toml:"channels"`
	ManagedBinaries map[string]ManagedBinary `toml:"managed_binaries"`
}

// ResolveChannel finds a channel config by Discord channel ID.
func (c *Config) ResolveChannel(discordChannelID string) (string, ChannelConfig, bool) {
	for name, ch := range c.Channels {
		if ch.ChannelID == discordChannelID {
			return name, ch, true
		}
	}
	return "", ChannelConfig{}, false
}

// IsAdmin returns true if the given Discord user ID is in the admin list.
// If no admin users are configured, all users are allowed.
func (c *Config) IsAdmin(userID string) bool {
	if len(c.Bot.AdminUsers) == 0 {
		return true
	}
	for _, id := range c.Bot.AdminUsers {
		if id == userID {
			return true
		}
	}
	return false
}

// ResolveSetupChannel returns the setup channel ID.
// Falls back to the first configured agent channel if setup_channel_id is empty.
func (c *Config) ResolveSetupChannel() string {
	if c.Bot.SetupChannelID != "" {
		return c.Bot.SetupChannelID
	}
	for _, ch := range c.Channels {
		return ch.ChannelID
	}
	return ""
}

// SetChannelField updates a field in a [channels.<name>] section of the TOML config file.
// Preserves formatting and comments. Adds the field if missing, updates if present.
func SetChannelField(configPath, channelName, key, value string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	header := fmt.Sprintf("[channels.%s]", channelName)
	newLine := fmt.Sprintf("%s = %q", key, value)

	var result []string
	found := false

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		result = append(result, lines[i])

		if trimmed != header {
			continue
		}

		// In the target section — scan for existing key or end of section
		found = true
		keyUpdated := false
		j := i + 1
		for j < len(lines) {
			line := strings.TrimSpace(lines[j])
			if strings.HasPrefix(line, "[") {
				break
			}
			// Check if this line sets the key (active or commented)
			if !keyUpdated {
				cleaned := strings.TrimPrefix(line, "#")
				cleaned = strings.TrimSpace(cleaned)
				if parts := strings.SplitN(cleaned, "=", 2); len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
					// Replace this line with the new value
					result = append(result, newLine)
					keyUpdated = true
					j++
					continue
				}
			}
			result = append(result, lines[j])
			j++
		}
		if !keyUpdated {
			// Key not found in section — append before next section
			result = append(result, newLine)
		}
		i = j - 1
	}

	if !found {
		return fmt.Errorf("channel section %s not found in config", header)
	}

	return os.WriteFile(configPath, []byte(strings.Join(result, "\n")), 0644)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

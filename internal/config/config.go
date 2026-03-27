package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type BotConfig struct {
	TokenEnv        string   `toml:"token_env"`
	GuildID         string   `toml:"guild_id"`
	StatusChannelID string   `toml:"status_channel_id"`
	ErrorChannelID  string   `toml:"error_channel_id"`  // optional: channel for error/retry events
	AdminUsers      []string `toml:"admin_users"`        // Discord user IDs allowed to interact
	PlistLabel      string   `toml:"plist_label"`        // launchd label for restart (e.g. "com.keel.serve")
}

type ChannelConfig struct {
	ChannelID string `toml:"channel_id"`
	AgentDir  string `toml:"agent_dir"`
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

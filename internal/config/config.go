package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type BotConfig struct {
	TokenEnv        string `toml:"token_env"`
	GuildID         string `toml:"guild_id"`
	StatusChannelID string `toml:"status_channel_id"`
}

type ChannelConfig struct {
	ChannelID string `toml:"channel_id"`
	AgentDir  string `toml:"agent_dir"`
}

type Config struct {
	Bot      BotConfig                `toml:"bot"`
	Channels map[string]ChannelConfig `toml:"channels"`
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

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "discord.toml")
	content := `
[bot]
token_env = "DISCORD_BOT_TOKEN"
guild_id = "123456789"
status_channel_id = ""

[channels.noah]
channel_id = "111111111"
agent_dir = "/tmp/test-ark/noah"
`
	os.WriteFile(path, []byte(content), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Bot.TokenEnv != "DISCORD_BOT_TOKEN" {
		t.Errorf("TokenEnv = %q, want DISCORD_BOT_TOKEN", cfg.Bot.TokenEnv)
	}
	if cfg.Bot.GuildID != "123456789" {
		t.Errorf("GuildID = %q, want 123456789", cfg.Bot.GuildID)
	}
	ch, ok := cfg.Channels["noah"]
	if !ok {
		t.Fatal("missing channel noah")
	}
	if ch.ChannelID != "111111111" {
		t.Errorf("ChannelID = %q, want 111111111", ch.ChannelID)
	}
	if ch.AgentDir != "/tmp/test-ark/noah" {
		t.Errorf("AgentDir = %q, want /tmp/test-ark/noah", ch.AgentDir)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolveChannelByID(t *testing.T) {
	cfg := &Config{
		Channels: map[string]ChannelConfig{
			"noah": {ChannelID: "111", AgentDir: "/tmp/noah"},
		},
	}
	name, ch, ok := cfg.ResolveChannel("111")
	if !ok {
		t.Fatal("expected to find channel")
	}
	if name != "noah" {
		t.Errorf("name = %q, want noah", name)
	}
	if ch.AgentDir != "/tmp/noah" {
		t.Errorf("AgentDir = %q", ch.AgentDir)
	}

	_, _, ok = cfg.ResolveChannel("999")
	if ok {
		t.Fatal("expected no match for unknown channel ID")
	}
}

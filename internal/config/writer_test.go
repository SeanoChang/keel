package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendChannel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "discord.toml")
	initial := `[bot]
token_env = "DISCORD_BOT_TOKEN"
guild_id = "123"
setup_channel_id = "999"

[channels.noah]
channel_id = "111"
agent_dir = "~/.ark/agents-home/noah"
`
	os.WriteFile(path, []byte(initial), 0644)

	err := AppendChannel(path, "eve", "222", "~/.ark/agents-home/eve")
	if err != nil {
		t.Fatalf("AppendChannel() error: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "[channels.eve]") {
		t.Error("missing [channels.eve] section")
	}
	if !strings.Contains(content, `channel_id = "222"`) {
		t.Error("missing channel_id for eve")
	}
	if !strings.Contains(content, `agent_dir = "~/.ark/agents-home/eve"`) {
		t.Error("missing agent_dir for eve")
	}
	// Original content preserved
	if !strings.Contains(content, "[channels.noah]") {
		t.Error("original [channels.noah] section lost")
	}
}

func TestAppendChannelDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "discord.toml")
	initial := `[bot]
token_env = "DISCORD_BOT_TOKEN"

[channels.noah]
channel_id = "111"
agent_dir = "~/.ark/agents-home/noah"
`
	os.WriteFile(path, []byte(initial), 0644)

	err := AppendChannel(path, "noah", "222", "~/.ark/agents-home/noah")
	if err == nil {
		t.Fatal("expected error for duplicate agent name")
	}
}

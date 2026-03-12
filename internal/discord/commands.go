package discord

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/SeanoChang/keel/internal/config"
	"github.com/SeanoChang/keel/internal/loop"
	"github.com/SeanoChang/keel/internal/workspace"
)

func (b *Bot) handleCommand(s *discordgo.Session, m *discordgo.MessageCreate, agentName string, ch config.ChannelConfig, cmd, args string) {
	var response string

	switch cmd {
	case "status":
		response = b.cmdStatus(agentName, ch)
	case "goals":
		response = b.cmdGoals(ch)
	case "log":
		n := 20
		if args != "" {
			if parsed, err := strconv.Atoi(args); err == nil && parsed > 0 {
				n = parsed
			}
		}
		response = b.cmdLog(ch, n)
	case "memory":
		response = b.cmdMemory(ch)
	case "ask":
		if args == "" {
			response = "Usage: `!ask <message>`"
		} else {
			go b.cmdAsk(s, m, agentName, ch, args)
			return
		}
	case "stop":
		response = b.cmdStop(agentName)
	case "start":
		response = b.cmdStart(agentName, ch)
	case "clear":
		response = b.cmdClear(ch, agentName)
	case "help":
		response = cmdHelp()
	default:
		response = fmt.Sprintf("Unknown command: `!%s`. Try `!help` for a list of commands.", cmd)
	}

	b.reply(s, m, response)
}

func (b *Bot) cmdStatus(name string, ch config.ChannelConfig) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Agent: %s**\n", name))
	sb.WriteString(fmt.Sprintf("Dir: `%s`\n", ch.AgentDir))
	sb.WriteString(fmt.Sprintf("Loop running: %v\n", b.loopMgr.IsRunning(name)))

	hasGoals := workspace.HasGoals(ch.AgentDir)
	sb.WriteString(fmt.Sprintf("Has goals: %v\n", hasGoals))

	tokens, _ := workspace.MemoryTokenCount(ch.AgentDir)
	sb.WriteString(fmt.Sprintf("MEMORY.md: ~%d tokens\n", tokens))

	return sb.String()
}

func (b *Bot) cmdGoals(ch config.ChannelConfig) string {
	goals, err := workspace.ReadGoals(ch.AgentDir)
	if err != nil {
		return fmt.Sprintf("Error reading GOALS.md: %v", err)
	}
	goals = strings.TrimSpace(goals)
	if goals == "" {
		return "GOALS.md is empty."
	}
	if len(goals) > 1900 {
		goals = goals[:1900] + "\n... (truncated)"
	}
	return fmt.Sprintf("```\n%s\n```", goals)
}

func (b *Bot) cmdLog(ch config.ChannelConfig, n int) string {
	lines, err := workspace.ReadLogTail(ch.AgentDir, n)
	if err != nil {
		return fmt.Sprintf("Error reading log.md: %v", err)
	}
	if len(lines) == 0 {
		return "log.md is empty."
	}
	content := strings.Join(lines, "\n")
	if len(content) > 1900 {
		content = content[:1900] + "\n... (truncated)"
	}
	return fmt.Sprintf("```\n%s\n```", content)
}

func (b *Bot) cmdMemory(ch config.ChannelConfig) string {
	tokens, err := workspace.MemoryTokenCount(ch.AgentDir)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return fmt.Sprintf("MEMORY.md: ~%d tokens", tokens)
}

func (b *Bot) cmdStop(name string) string {
	if !b.loopMgr.IsRunning(name) {
		return fmt.Sprintf("Agent **%s** is not running.", name)
	}
	b.loopMgr.Stop(name)
	return fmt.Sprintf("Agent **%s** stopped.", name)
}

func (b *Bot) cmdStart(name string, ch config.ChannelConfig) string {
	if b.loopMgr.IsRunning(name) {
		return fmt.Sprintf("Agent **%s** is already running.", name)
	}
	if !workspace.HasGoals(ch.AgentDir) {
		return fmt.Sprintf("Agent **%s** has no goals. Send a message first.", name)
	}
	handler := b.activityHandler(name, ch.ChannelID)
	err := b.loopMgr.Start(name, ch.AgentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery, handler)
	if err != nil {
		return fmt.Sprintf("Error starting **%s**: %v", name, err)
	}
	return fmt.Sprintf("Agent **%s** loop started.", name)
}

func cmdHelp() string {
	return "**Keel Commands**\n" +
		"`!ask <msg>` — quick one-shot chat with the agent\n" +
		"`!help` — show this message\n" +
		"`!status` — agent status (running, goals, memory)\n" +
		"`!goals` — show current GOALS.md\n" +
		"`!log [n]` — show last n log lines (default 20)\n" +
		"`!memory` — show MEMORY.md token count\n" +
		"`!start` — start the agent loop\n" +
		"`!stop` — stop the agent loop\n" +
		"`!clear` — clear all goals\n\n" +
		"Any other message is added as a goal."
}

func (b *Bot) cmdAsk(s *discordgo.Session, m *discordgo.MessageCreate, agentName string, ch config.ChannelConfig, message string) {
	_ = s.ChannelTyping(m.ChannelID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Keep typing indicator alive (expires after 10s)
	typingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-typingDone:
				return
			case <-ticker.C:
				_ = s.ChannelTyping(m.ChannelID)
			}
		}
	}()

	response, err := loop.RunOneShot(ctx, agentName, ch.AgentDir, message)
	close(typingDone)

	if err != nil {
		log.Printf("[keel] %s: ask error: %v", agentName, err)
		b.reply(s, m, fmt.Sprintf("Error: %v", err))
		return
	}

	if response == "" {
		response = "(empty response)"
	}
	if len(response) > 1900 {
		response = response[:1900] + "\n... (truncated)"
	}

	b.reply(s, m, response)
}

func (b *Bot) cmdClear(ch config.ChannelConfig, name string) string {
	if err := workspace.ClearGoals(ch.AgentDir); err != nil {
		return fmt.Sprintf("Error clearing GOALS.md: %v", err)
	}
	return fmt.Sprintf("GOALS.md cleared for **%s**. Loop will stop after current session.", name)
}

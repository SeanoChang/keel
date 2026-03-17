package discord

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/SeanoChang/keel/internal/config"
	"github.com/SeanoChang/keel/internal/loop"
	"github.com/SeanoChang/keel/internal/schedule"
	"github.com/SeanoChang/keel/internal/update"
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
		} else if b.loopMgr.IsRunning(agentName) {
			response = fmt.Sprintf("Agent **%s** loop is running. Use `!stop` first or send a goal instead.", agentName)
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
	case "schedule":
		response = b.cmdSchedule(ch, args)
	case "update", "keel-update":
		go b.cmdKeelUpdate(s, m)
		return
	case "help":
		response = cmdHelp()
	default:
		// Check for <name>-update pattern against managed binaries
		if name, ok := strings.CutSuffix(cmd, "-update"); ok {
			if _, exists := b.cfg.ManagedBinaries[name]; exists {
				go b.cmdBinaryUpdate(s, m, name)
				return
			}
		}
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
	if !workspace.HasGoals(ch.AgentDir) {
		return "No goals."
	}
	goals, err := workspace.ReadGoals(ch.AgentDir)
	if err != nil {
		return fmt.Sprintf("Error reading GOALS.md: %v", err)
	}
	return fmt.Sprintf("```\n%s\n```", strings.TrimSpace(goals))
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
	onOutput, onLifecycle := b.sessionHandlers(name, ch.ChannelID, ch.AgentDir)
	err := b.loopMgr.Start(name, ch.AgentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery, onOutput, onLifecycle)
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
		"`!schedule` — show scheduled goals\n" +
		"`!clear` — clear all goals\n" +
		"`!update` — update keel to latest release and restart\n" +
		"`!<name>-update` — update a managed binary (e.g. `!nark-update`, `!cubit-update`)\n\n" +
		"Any other message is added as a goal."
}

func (b *Bot) cmdAsk(s *discordgo.Session, m *discordgo.MessageCreate, agentName string, ch config.ChannelConfig, message string) {
	progress := NewProgressMessage(s, m.ChannelID)
	if err := progress.Send(fmt.Sprintf("**%s** — Running...", agentName)); err != nil {
		log.Printf("[keel] %s: progress send error: %v", agentName, err)
		return
	}

	ctx := context.Background()
	var mu sync.Mutex
	var tools []string

	onProgress := func(ev loop.StreamEvent) {
		mu.Lock()
		defer mu.Unlock()

		switch ev.Kind {
		case loop.EventToolUse:
			toolName := loop.ShortToolName(ev.ToolName)
			tools = append(tools, toolName)
			detail := ev.ToolInput
			if len(detail) > 200 {
				detail = detail[:200] + "..."
			}
			msg := fmt.Sprintf("**%s** — `%s`", agentName, toolName)
			if detail != "" {
				msg += " " + detail
			}
			msg += fmt.Sprintf("\n-# %d tools", len(tools))
			progress.Update(msg)

		case loop.EventThinking:
			msg := fmt.Sprintf("**%s** — Thinking...", agentName)
			if len(tools) > 0 {
				msg += fmt.Sprintf("\n-# %d tools", len(tools))
			}
			progress.Update(msg)

		case loop.EventToolResult:
			msg := fmt.Sprintf("**%s** — Processing...", agentName)
			if len(tools) > 0 {
				msg += fmt.Sprintf("\n-# %d tools", len(tools))
			}
			progress.Update(msg)

		case loop.EventText:
			msg := fmt.Sprintf("**%s** — Responding...", agentName)
			if len(tools) > 0 {
				msg += fmt.Sprintf("\n-# %d tools", len(tools))
			}
			progress.Update(msg)
		}
	}

	b.sendStatus(agentName, "!ask started")

	response, err := loop.RunOneShotStreaming(ctx, agentName, ch.AgentDir, message, onProgress)

	if err != nil {
		log.Printf("[keel] %s: ask error: %v", agentName, err)
		progress.Finalize(fmt.Sprintf("**%s** — Error: %v", agentName, err))
		b.sendStatus(agentName, fmt.Sprintf("!ask error: %v", err))
		return
	}

	if response == "" {
		response = "(empty response)"
	}

	if len(response) <= 1900 {
		progress.Finalize(response)
	} else {
		// Finalize the progress message with the first chunk, send the rest as follow-ups
		progress.Finalize(response[:1900])
		sendChunked(s, m.ChannelID, response[1900:])
	}
	b.sendStatus(agentName, "!ask complete")
}

func (b *Bot) cmdClear(ch config.ChannelConfig, name string) string {
	wasRunning := b.loopMgr.IsRunning(name)
	if wasRunning {
		b.loopMgr.Stop(name)
	}
	if err := workspace.ClearGoals(ch.AgentDir); err != nil {
		return fmt.Sprintf("Error clearing GOALS.md: %v", err)
	}
	if wasRunning {
		return fmt.Sprintf("GOALS.md cleared for **%s**. Loop stopped.", name)
	}
	return fmt.Sprintf("GOALS.md cleared for **%s**.", name)
}

func (b *Bot) cmdSchedule(ch config.ChannelConfig, args string) string {
	entries, err := schedule.ScanDir(ch.AgentDir)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if len(entries) == 0 {
		return "No scheduled goals."
	}
	var sb strings.Builder
	sb.WriteString("**Scheduled Goals**\n")
	for _, e := range entries {
		kind := "one-shot"
		when := e.TimeDir.At.Format("2006-01-02 15:04")
		if e.TimeDir.Kind == schedule.KindRecurring {
			kind = "recurring"
			when = e.TimeDir.CronExpr
		}
		preview := e.Content
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		sb.WriteString(fmt.Sprintf("`[%s]` **%s** @ %s\n%s\n\n", kind, e.Name, when, preview))
	}
	return sb.String()
}

func (b *Bot) cmdBinaryUpdate(s *discordgo.Session, m *discordgo.MessageCreate, name string) {
	bin := b.cfg.ManagedBinaries[name]
	if len(bin.UpdateCmd) == 0 {
		b.reply(s, m, fmt.Sprintf("No `update_cmd` configured for `%s`.", name))
		return
	}

	cmdStr := strings.Join(bin.UpdateCmd, " ")
	b.reply(s, m, fmt.Sprintf("Running `%s`...", cmdStr))
	log.Printf("[keel] %s-update: running %s", name, cmdStr)

	cmd := exec.Command(bin.UpdateCmd[0], bin.UpdateCmd[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.reply(s, m, fmt.Sprintf("`%s` failed: %v\n```\n%s\n```", cmdStr, err, string(out)))
		return
	}

	output := strings.TrimSpace(string(out))
	if output != "" {
		b.reply(s, m, fmt.Sprintf("`%s` done.\n```\n%s\n```", cmdStr, output))
	} else {
		b.reply(s, m, fmt.Sprintf("`%s` done.", cmdStr))
	}
}

func (b *Bot) cmdKeelUpdate(s *discordgo.Session, m *discordgo.MessageCreate) {
	result, err := update.Run(update.Version, func(msg string) {
		b.reply(s, m, msg)
	})
	if err != nil {
		b.reply(s, m, fmt.Sprintf("Update failed: %v", err))
		return
	}
	// Always reload PROGRAM.md for all agents with the built-in DefaultProgram.
	var reloaded int
	for name, ch := range b.cfg.Channels {
		if err := workspace.WriteDefaultProgram(ch.AgentDir); err != nil {
			log.Printf("[keel] keel-update: failed to write PROGRAM.md for %s: %v", name, err)
		} else {
			reloaded++
		}
	}
	b.reply(s, m, fmt.Sprintf("Reloaded PROGRAM.md for %d agent(s).", reloaded))

	if result.AlreadyCurrent {
		b.reply(s, m, fmt.Sprintf("Already on latest version (%s).", result.CurrentVersion))
		return
	}

	label := b.cfg.Bot.PlistLabel
	if label == "" {
		b.reply(s, m, fmt.Sprintf("Updated to %s. No `plist_label` configured — exiting (manual restart required).", result.NewVersion))
		log.Printf("[keel] keel-update: updated to %s, exiting for manual restart", result.NewVersion)
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
		return
	}

	b.reply(s, m, fmt.Sprintf("Updated to %s. Restarting via launchctl...", result.NewVersion))
	log.Printf("[keel] keel-update: updated to %s, restarting %s", result.NewVersion, label)

	u, err := user.Current()
	if err != nil {
		b.reply(s, m, fmt.Sprintf("Error getting current user: %v", err))
		return
	}
	target := fmt.Sprintf("gui/%s/%s", u.Uid, label)

	time.Sleep(500 * time.Millisecond)
	kick := exec.Command("launchctl", "kickstart", "-k", target)
	if out, err := kick.CombinedOutput(); err != nil {
		log.Printf("[keel] keel-update: kickstart failed: %s — %v", string(out), err)
	}
}

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
		} else {
			go b.cmdAsk(s, m, agentName, ch, args)
			return
		}
	case "note":
		if args == "" {
			response = "Usage: `!note <message>`"
		} else {
			subject := noteSubject(args)
			if err := workspace.WriteMailboxMessage(ch.AgentDir, m.Author.Username, subject, "all", "notification", args); err != nil {
				response = fmt.Sprintf("Error: %v", err)
			} else {
				response = "Note added to mailbox."
			}
		}
	case "priority":
		if args == "" {
			response = "Usage: `!priority <message>`"
		} else {
			subject := noteSubject(args)
			if err := workspace.WriteMailboxMessage(ch.AgentDir, m.Author.Username, subject, "priority", "notification", args); err != nil {
				response = fmt.Sprintf("Error: %v", err)
			} else {
				response = "Priority note added to mailbox."
			}
		}
	case "stop":
		response = b.cmdStop(agentName)
	case "start":
		response = b.cmdStart(agentName, ch)
	case "pause":
		if !b.loopMgr.IsRunning(agentName) {
			response = fmt.Sprintf("Agent **%s** is not running.", agentName)
		} else if b.loopMgr.IsPaused(agentName) {
			response = fmt.Sprintf("Agent **%s** is already paused.", agentName)
		} else {
			b.loopMgr.Pause(agentName)
			response = fmt.Sprintf("Agent **%s** will pause after the current session. Use `!resume` to continue.", agentName)
		}
	case "resume":
		if !b.loopMgr.IsRunning(agentName) {
			response = fmt.Sprintf("Agent **%s** is not running.", agentName)
		} else if !b.loopMgr.IsPaused(agentName) {
			response = fmt.Sprintf("Agent **%s** is not paused.", agentName)
		} else {
			b.loopMgr.Resume(agentName)
			response = fmt.Sprintf("Agent **%s** resumed.", agentName)
		}
	case "clear":
		response = b.cmdClear(ch, agentName)
	case "wrap-up", "wrapup":
		response = b.cmdWrapUp(agentName, ch, args)
	case "schedule":
		response = b.cmdSchedule(ch, args)
	case "dream":
		go b.cmdDream(s, m, agentName, ch)
		return
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
	if !workspace.HasGoals(ch.AgentDir) && !workspace.HasMailboxMessages(ch.AgentDir) {
		return fmt.Sprintf("Agent **%s** has no goals or messages. Send a message first.", name)
	}
	onOutput, onLifecycle, opts := b.sessionHandlers(name, ch.ChannelID, ch.AgentDir)
	err := b.loopMgr.Start(name, ch.AgentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery, onOutput, onLifecycle, opts)
	if err != nil {
		return fmt.Sprintf("Error starting **%s**: %v", name, err)
	}
	return fmt.Sprintf("Agent **%s** loop started.", name)
}

func cmdHelp() string {
	return "**Keel Commands**\n" +
		"`!ask <msg>` — quick one-shot question (works while loop is running)\n" +
		"`!note <msg>` — leave a note in the agent's mailbox\n" +
		"`!priority <msg>` — leave a priority note in the agent's mailbox\n" +
		"`!help` — show this message\n" +
		"`!status` — agent status (running, goals, memory)\n" +
		"`!goals` — show current GOALS.md\n" +
		"`!log [n]` — show last n log lines (default 20)\n" +
		"`!memory` — show MEMORY.md token count\n" +
		"`!start` — start the agent loop\n" +
		"`!stop` — stop the agent loop\n" +
		"`!pause` — pause the loop after the current session\n" +
		"`!resume` — resume a paused loop\n" +
		"`!wrap-up [msg]` — tell the agent to finish up and stop gracefully\n" +
		"`!schedule` — show scheduled goals\n" +
		"`!dream` — consolidate agent memory (runs cubit dream)\n" +
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

func (b *Bot) cmdDream(s *discordgo.Session, m *discordgo.MessageCreate, agentName string, ch config.ChannelConfig) {
	b.reply(s, m, fmt.Sprintf("Running dream for **%s**...", agentName))
	b.sendStatus(agentName, "!dream started")
	b.runAgentDream(agentName, ch.AgentDir)
	b.reply(s, m, fmt.Sprintf("Dream complete for **%s**.", agentName))
	b.sendStatus(agentName, "!dream complete")
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

func (b *Bot) cmdWrapUp(name string, ch config.ChannelConfig, extra string) string {
	if !b.loopMgr.IsRunning(name) {
		return fmt.Sprintf("Agent **%s** is not running.", name)
	}

	// Write sentinel so loop stops after current session.
	if err := workspace.WriteWrapUpSignal(ch.AgentDir); err != nil {
		return fmt.Sprintf("Error writing wrap-up signal: %v", err)
	}

	// Inject wrap-up goal so agent knows to summarize.
	wrapUpGoal := "Wrap up your current work. Summarize what you accomplished and what remains incomplete. Write your summary to DELIVER.md. Then remove all goals from GOALS.md and create .exit."
	if extra != "" {
		wrapUpGoal = fmt.Sprintf("Wrap up: %s\n\nSummarize what you accomplished and what remains. Write to DELIVER.md. Remove goals and create .exit.", extra)
	}
	if err := workspace.AppendGoal(ch.AgentDir, "keel", wrapUpGoal); err != nil {
		return fmt.Sprintf("Error adding wrap-up goal: %v", err)
	}

	b.loopMgr.Nudge(name)
	return fmt.Sprintf("Agent **%s** will wrap up after the current session.", name)
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

// noteSubject extracts a short subject from a message (first line, truncated to 60 chars).
func noteSubject(msg string) string {
	line := msg
	if idx := strings.IndexByte(msg, '\n'); idx >= 0 {
		line = msg[:idx]
	}
	if len(line) > 60 {
		line = line[:60]
	}
	return strings.TrimSpace(line)
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

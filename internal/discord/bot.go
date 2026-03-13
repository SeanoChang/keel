package discord

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/SeanoChang/keel/internal/config"
	"github.com/SeanoChang/keel/internal/loop"
	"github.com/SeanoChang/keel/internal/schedule"
	"github.com/SeanoChang/keel/internal/workspace"
)

type Bot struct {
	session      *discordgo.Session
	cfg          *config.Config
	loopMgr      *loop.Manager
	tailers      map[string]*LogTailer
	sleepBetween time.Duration
	archiveEvery int
	schedStop    chan struct{} // signals scheduler goroutine to stop
	schedDone    chan struct{} // closed when scheduler goroutine exits
}

func NewBot(cfg *config.Config, sleepBetween time.Duration, archiveEvery int) (*Bot, error) {
	token := os.Getenv(cfg.Bot.TokenEnv)
	if token == "" {
		return nil, fmt.Errorf("env var %s is empty or unset", cfg.Bot.TokenEnv)
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}

	b := &Bot{
		session:      session,
		cfg:          cfg,
		loopMgr:      loop.NewManager(),
		tailers:      make(map[string]*LogTailer),
		sleepBetween: sleepBetween,
		archiveEvery: archiveEvery,
		schedStop:    make(chan struct{}),
		schedDone:    make(chan struct{}),
	}

	session.AddHandler(b.onMessageCreate)
	session.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	return b, nil
}

func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open discord connection: %w", err)
	}
	log.Printf("[keel] Discord bot connected")

	for name, ch := range b.cfg.Channels {
		tailer := NewLogTailer(name, ch.AgentDir, ch.ChannelID, b.cfg.Bot.StatusChannelID, b.session)
		b.tailers[name] = tailer
		go tailer.Start()
	}

	go b.runScheduler()

	return nil
}

func (b *Bot) Stop() {
	close(b.schedStop)
	<-b.schedDone
	b.loopMgr.StopAll()
	for _, t := range b.tailers {
		t.Stop()
	}
	b.session.Close()
	log.Printf("[keel] Discord bot disconnected")
}

func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if !b.cfg.IsAdmin(m.Author.ID) {
		return
	}

	agentName, ch, ok := b.cfg.ResolveChannel(m.ChannelID)
	if !ok {
		return
	}

	content := strings.TrimSpace(m.Content)
	if content == "" {
		return
	}

	if isCmd, cmd, args := ParseCommand(content); isCmd {
		b.handleCommand(s, m, agentName, ch, cmd, args)
		return
	}

	username := m.Author.Username
	if err := workspace.AppendGoal(ch.AgentDir, username, content); err != nil {
		b.reply(s, m, fmt.Sprintf("Error writing goal: %v", err))
		return
	}

	if b.loopMgr.IsRunning(agentName) {
		b.loopMgr.Nudge(agentName)
	} else {
		onOutput, onLifecycle := b.sessionHandlers(agentName, m.ChannelID)
		err := b.loopMgr.Start(agentName, ch.AgentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery, onOutput, onLifecycle)
		if err != nil {
			log.Printf("[keel] error starting loop for %s: %v", agentName, err)
		}
	}
}

// sessionHandlers returns an (onOutput, onLifecycle) pair that manages a single
// ProgressMessage per session — edited in-place as claude streams tool activity.
func (b *Bot) sessionHandlers(agentName, channelID string) (onOutput func(loop.StreamEvent), onLifecycle func(string)) {
	var progress *ProgressMessage
	var mu sync.Mutex
	var tools []string
	var lastCost float64
	var lastDurationMs int64
	var lastResultText string

	onLifecycle = func(event string) {
		mu.Lock()
		defer mu.Unlock()

		switch event {
		case "session_start":
			progress = NewProgressMessage(b.session, channelID)
			tools = nil
			lastCost = 0
			lastDurationMs = 0
			lastResultText = ""
			_ = progress.Send(fmt.Sprintf("**%s** — Running...", agentName))
			b.sendStatus(agentName, "Session started")

		case "session_end":
			if progress != nil {
				progress.Flush()
				summary := fmt.Sprintf("**%s** — Session complete.", agentName)
				if len(tools) > 0 {
					summary += " " + sessionStats(len(tools), lastCost, lastDurationMs)
					summary += "\n" + formatToolTrail(tools)
				}
				progress.Finalize(summary)
				progress = nil
			}
			b.sendStatus(agentName, "Session complete")

		case "goals_empty":
			if progress != nil {
				progress.Flush()
				summary := fmt.Sprintf("**%s** — No goals remaining. Loop stopped.", agentName)
				if len(tools) > 0 {
					summary += " " + sessionStats(len(tools), lastCost, lastDurationMs)
					summary += "\n" + formatToolTrail(tools)
				}
				progress.Finalize(summary)
				progress = nil
			} else {
				b.session.ChannelMessageSend(channelID, fmt.Sprintf("**%s** — No goals remaining. Loop stopped.", agentName))
			}
			b.sendStatus(agentName, "No goals — loop stopped")
			if lastResultText != "" {
				report := fmt.Sprintf("**%s — Report**\n%s", agentName, lastResultText)
				sendChunked(b.session, channelID, report)
			}

		case "sleeping":
			if progress != nil {
				progress.Flush()
			}
			b.sendStatus(agentName, "Sleeping between sessions...")

		case "woke":
			b.sendStatus(agentName, "Woke up — new goals detected")

		case "agent_exit":
			if progress != nil {
				progress.Flush()
				summary := fmt.Sprintf("**%s** — Agent exited. Loop stopped.", agentName)
				if len(tools) > 0 {
					summary += " " + sessionStats(len(tools), lastCost, lastDurationMs)
					summary += "\n" + formatToolTrail(tools)
				}
				progress.Finalize(summary)
				progress = nil
			} else {
				b.session.ChannelMessageSend(channelID, fmt.Sprintf("**%s** — Agent exited. Loop stopped.", agentName))
			}
			b.sendStatus(agentName, "Agent requested exit — loop stopped")
			if lastResultText != "" {
				report := fmt.Sprintf("**%s — Report**\n%s", agentName, lastResultText)
				sendChunked(b.session, channelID, report)
			}

		case "stale":
			if progress != nil {
				progress.Flush()
				progress.Finalize(fmt.Sprintf("**%s** — No progress detected. Loop stopped.", agentName))
				progress = nil
			}
			b.sendStatus(agentName, "Stale — loop stopped")
			if lastResultText != "" {
				report := fmt.Sprintf("**%s — Report**\n%s", agentName, lastResultText)
				sendChunked(b.session, channelID, report)
			}

		case "too_many_errors":
			if progress != nil {
				progress.Flush()
				progress.Finalize(fmt.Sprintf("**%s** — Too many errors. Loop stopped.", agentName))
				progress = nil
			}
			b.sendStatus(agentName, "Too many errors — loop stopped")

		default:
			if strings.HasPrefix(event, "error:") {
				if progress != nil {
					progress.Flush()
					progress.Finalize(fmt.Sprintf("**%s** — %s", agentName, event))
					progress = nil
				}
			}
			b.sendStatus(agentName, event)
		}
	}

	onOutput = func(ev loop.StreamEvent) {
		mu.Lock()
		defer mu.Unlock()

		if progress == nil {
			return
		}

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

		case loop.EventResult:
			lastCost = ev.Cost
			lastDurationMs = ev.DurationMs
			lastResultText = ev.Text
		}
	}

	return
}

// formatToolTrail builds a compact tool sequence for Discord display.
func formatToolTrail(tools []string) string {
	if len(tools) == 0 {
		return ""
	}
	display := tools
	prefix := ""
	if len(display) > 15 {
		display = display[len(display)-15:]
		prefix = "… → "
	}
	formatted := make([]string, len(display))
	for i, t := range display {
		formatted[i] = "`" + t + "`"
	}
	return prefix + strings.Join(formatted, " → ")
}

// sessionStats formats a compact stats suffix like "(12 tools, $0.16, 45s)".
func sessionStats(toolCount int, cost float64, durationMs int64) string {
	parts := []string{fmt.Sprintf("%d tools", toolCount)}
	if cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", cost))
	}
	if durationMs > 0 {
		parts = append(parts, fmt.Sprintf("%ds", durationMs/1000))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// sendStatus sends a short event to the status channel (if configured).
func (b *Bot) sendStatus(agentName, event string) {
	if b.cfg.Bot.StatusChannelID == "" {
		return
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf("`[%s - %s]` %s", ts, agentName, event)
	if _, err := b.session.ChannelMessageSend(b.cfg.Bot.StatusChannelID, msg); err != nil {
		log.Printf("[keel] %s: status send error: %v", agentName, err)
	}
}

func (b *Bot) reply(s *discordgo.Session, m *discordgo.MessageCreate, msg string) {
	if len(msg) <= 1900 {
		if _, err := s.ChannelMessageSend(m.ChannelID, msg); err != nil {
			log.Printf("[keel] discord send error: %v", err)
		}
		return
	}
	sendChunked(s, m.ChannelID, msg)
}

// sendChunked splits a long message into ≤1900-char chunks and sends each as a
// separate Discord message. Splits on newline boundaries when possible.
func sendChunked(s *discordgo.Session, channelID, text string) {
	const maxLen = 1900
	for len(text) > 0 {
		if len(text) <= maxLen {
			s.ChannelMessageSend(channelID, text)
			return
		}
		// Find a newline to split on within the limit
		cut := strings.LastIndex(text[:maxLen], "\n")
		if cut <= 0 {
			cut = maxLen
		}
		s.ChannelMessageSend(channelID, text[:cut])
		text = text[cut:]
		// Trim leading newline from next chunk
		text = strings.TrimPrefix(text, "\n")
	}
}

// runScheduler sleeps until the top of each minute, then scans all agent
// schedule dirs and fires due entries. Re-aligns on every iteration so
// it never drifts from the wall clock regardless of checkSchedules duration.
func (b *Bot) runScheduler() {
	defer close(b.schedDone)

	for {
		delay := time.Until(time.Now().Truncate(time.Minute).Add(time.Minute))
		select {
		case <-b.schedStop:
			return
		case <-time.After(delay):
		}
		b.checkSchedules()
	}
}

func (b *Bot) checkSchedules() {
	for name, ch := range b.cfg.Channels {
		fired, err := schedule.FireDue(ch.AgentDir)
		if err != nil {
			log.Printf("[keel] scheduler: error scanning %s: %v", name, err)
			continue
		}
		if len(fired) == 0 {
			continue
		}

		var names []string
		for _, e := range fired {
			names = append(names, e.Name)
		}
		log.Printf("[keel] scheduler: fired %d entries for %s: %s", len(fired), name, strings.Join(names, ", "))
		b.sendStatus(name, fmt.Sprintf("Scheduled goals fired: %s", strings.Join(names, ", ")))

		if b.loopMgr.IsRunning(name) {
			if schedule.HasUrgent(fired) {
				log.Printf("[keel] scheduler: urgent entry for %s — interrupting current session", name)
				b.sendStatus(name, "Urgent schedule — interrupting session")
				agentDir := ch.AgentDir
				channelID := ch.ChannelID
				agentName := name
				go func() {
					b.loopMgr.Stop(agentName)
					onOutput, onLifecycle := b.sessionHandlers(agentName, channelID)
					if err := b.loopMgr.Start(agentName, agentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery, onOutput, onLifecycle); err != nil {
						log.Printf("[keel] scheduler: error restarting %s: %v", agentName, err)
					}
				}()
			} else {
				b.loopMgr.Nudge(name)
			}
		} else {
			onOutput, onLifecycle := b.sessionHandlers(name, ch.ChannelID)
			if err := b.loopMgr.Start(name, ch.AgentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery, onOutput, onLifecycle); err != nil {
				log.Printf("[keel] scheduler: error starting %s: %v", name, err)
			}
		}
	}
}

// ParseCommand checks if a message is a ! command.
func ParseCommand(content string) (bool, string, string) {
	rest, ok := strings.CutPrefix(content, "!")
	if !ok {
		return false, "", ""
	}
	parts := strings.SplitN(rest, " ", 2)
	cmd := strings.TrimSpace(parts[0])
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	return true, cmd, args
}

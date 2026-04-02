package discord

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
	session         *discordgo.Session
	cfg             *config.Config
	loopMgr         *loop.Manager
	tailers         map[string]*LogTailer
	mailboxWatchers map[string]*MailboxWatcher
	sleepBetween    time.Duration
	archiveEvery    int
	modelsMu        sync.RWMutex
	models          map[string]string // per-agent model override (empty = default)
	schedStop       chan struct{}      // signals scheduler goroutine to stop
	schedDone       chan struct{}      // closed when scheduler goroutine exits
	lastDreamDate   string            // "2006-01-02" — prevents double-fire
	initSession     *InitSession      // active init flow (nil when idle)
	initMu          sync.Mutex        // guards initSession
	initWatcher     *InitWatcher      // watches agents-home for .init-pending
	configPath      string            // path to discord.toml for config writes
}

func NewBot(cfg *config.Config, configPath string, sleepBetween time.Duration, archiveEvery int) (*Bot, error) {
	token := os.Getenv(cfg.Bot.TokenEnv)
	if token == "" {
		return nil, fmt.Errorf("env var %s is empty or unset", cfg.Bot.TokenEnv)
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}

	b := &Bot{
		session:         session,
		cfg:             cfg,
		configPath:      configPath,
		loopMgr:         loop.NewManager(),
		tailers:         make(map[string]*LogTailer),
		mailboxWatchers: make(map[string]*MailboxWatcher),
		models:          make(map[string]string),
		sleepBetween:    sleepBetween,
		archiveEvery:    archiveEvery,
		schedStop:       make(chan struct{}),
		schedDone:       make(chan struct{}),
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
		// Startup check: warn if agent has INBOX.md but no mailbox/
		b.checkMailboxMigration(name, ch.AgentDir)

		// Ensure mailbox directory tree exists
		if err := workspace.EnsureMailbox(ch.AgentDir); err != nil {
			log.Printf("[keel] %s: error creating mailbox: %v", name, err)
		}

		tailer := NewLogTailer(name, ch.AgentDir, ch.ChannelID, b.cfg.Bot.StatusChannelID, b.session)
		b.tailers[name] = tailer
		go tailer.Start()

		// Capture loop vars for closure
		agentName, agentCh := name, ch
		mw := NewMailboxWatcher(name, ch.AgentDir, func() {
			b.ensureRunning(agentName, agentCh)
		})
		b.mailboxWatchers[name] = mw
		go mw.Start()
	}

	go b.runScheduler()

	// Start init watcher for cubit init --keel
	agentsHome := b.resolveAgentsHome()
	if agentsHome != "" {
		b.initWatcher = NewInitWatcher(agentsHome, func(agentDir string, pending *InitPending) {
			setupChannelID := b.cfg.ResolveSetupChannel()
			if setupChannelID == "" {
				log.Printf("[keel] init watcher: no setup channel configured, ignoring .init-pending")
				return
			}
			b.initMu.Lock()
			if b.initSession != nil && !b.initSession.IsDone() {
				b.initMu.Unlock()
				log.Printf("[keel] init watcher: init already active for %s, ignoring .init-pending for %s", b.initSession.AgentName(), pending.Agent)
				return
			}
			b.initSession = NewInitSession(b.session, setupChannelID, pending.Agent, agentDir, "", b.configPath, b.cfg.Bot.GuildID, pending.ImportIdentity)
			b.initMu.Unlock()
			go b.initSession.Start()
		})
		go b.initWatcher.Start()
	}

	return nil
}

func (b *Bot) Stop() {
	close(b.schedStop)
	<-b.schedDone
	b.loopMgr.StopAll()
	for _, t := range b.tailers {
		t.Stop()
	}
	for _, mw := range b.mailboxWatchers {
		mw.Stop()
	}
	if b.initWatcher != nil {
		b.initWatcher.Stop()
	}
	b.session.Close()
	log.Printf("[keel] Discord bot disconnected")
}

func (b *Bot) resolveAgentsHome() string {
	for _, ch := range b.cfg.Channels {
		return filepath.Dir(ch.AgentDir) // agents-home is parent of any agent dir
	}
	return ""
}

// latestModels maps family short names to the latest model ID for each.
// Update these when new model versions are released.
var latestModels = map[string]string{
	"opus":   "claude-opus-4-6",
	"sonnet": "claude-sonnet-4-6",
	"haiku":  "claude-haiku-4-5-20251001",
}

// ResolveModel returns the full model ID for a short name or version-specific name.
// Accepts: "opus", "sonnet", "haiku", "opus-4-6", "sonnet-4-6", "haiku-4-5", or full IDs.
func ResolveModel(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	// Direct match on short name
	if id, ok := latestModels[name]; ok {
		return id
	}
	// Check if it's a version-specific name like "opus-4-6" → "claude-opus-4-6"
	candidate := "claude-" + name
	for _, id := range latestModels {
		if id == candidate || strings.HasPrefix(id, candidate) {
			return id
		}
	}
	// Check if it's already a full model ID
	for _, id := range latestModels {
		if id == name {
			return id
		}
	}
	return ""
}

// commandBuilder returns a CommandBuilder that injects the agent's model override.
// Priority: runtime override (!set-model) > config default > no --model flag.
func (b *Bot) commandBuilder(agentName string) loop.CommandBuilder {
	return func(ctx context.Context, name, dir, program string) *loop.CommandSpec {
		spec := loop.DefaultCommandBuilder(ctx, name, dir, program)
		// Check runtime override first, then config default
		b.modelsMu.RLock()
		model := b.models[agentName]
		b.modelsMu.RUnlock()
		if model == "" {
			if ch, ok := b.cfg.Channels[agentName]; ok {
				model = ch.Model
			}
		}
		if modelID := ResolveModel(model); modelID != "" {
			spec.Args = append(spec.Args, "--model", modelID)
		}
		return spec
	}
}

// ensureRunning nudges a running loop or starts a new one if idle.
func (b *Bot) ensureRunning(name string, ch config.ChannelConfig) {
	if b.loopMgr.IsRunning(name) {
		b.loopMgr.Nudge(name)
	} else {
		onOutput, onLifecycle, opts := b.sessionHandlers(name, ch.ChannelID, ch.AgentDir)
		if err := b.loopMgr.Start(name, ch.AgentDir, b.commandBuilder(name), b.sleepBetween, b.archiveEvery, onOutput, onLifecycle, opts); err != nil {
			log.Printf("[keel] %s: error starting loop: %v", name, err)
		}
	}
}

// checkMailboxMigration warns if an agent has INBOX.md but no mailbox directory.
func (b *Bot) checkMailboxMigration(name, dir string) {
	inboxPath := filepath.Join(dir, "INBOX.md")
	mailboxPath := filepath.Join(dir, "mailbox")

	_, inboxErr := os.Stat(inboxPath)
	_, mailboxErr := os.Stat(mailboxPath)

	if inboxErr == nil && os.IsNotExist(mailboxErr) {
		log.Printf("[keel] WARNING: agent %s has INBOX.md but no mailbox/ — run 'cubit migrate' to upgrade", name)
	}
}

func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if !b.cfg.IsAdmin(m.Author.ID) {
		return
	}

	content := strings.TrimSpace(m.Content)
	if content == "" {
		return
	}

	// Setup channel: handle active init session or !init command
	setupChannelID := b.cfg.ResolveSetupChannel()
	if m.ChannelID == setupChannelID {
		// Active init session consumes messages
		b.initMu.Lock()
		if b.initSession != nil && !b.initSession.IsDone() {
			session := b.initSession
			b.initMu.Unlock()
			if session.HandleMessage(m.Author.ID, content) {
				b.initMu.Lock()
				if b.initSession != nil && b.initSession.IsDone() {
					b.initSession = nil
				}
				b.initMu.Unlock()
				return
			}
		} else {
			b.initMu.Unlock()
		}

		// Parse !init command in setup channel
		if isCmd, cmd, args := ParseCommand(content); isCmd && cmd == "init" {
			b.handleInitCommand(s, m, args)
			return
		}

		// Other ! commands or messages in setup channel — fall through to normal routing
		// (setup channel might also be an agent channel for migration)
	}

	agentName, ch, ok := b.cfg.ResolveChannel(m.ChannelID)
	if !ok {
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

	b.ensureRunning(agentName, ch)
}

// sessionHandlers returns (onOutput, onLifecycle, opts) for a loop session.
// The ProgressMessage is edited in-place as claude streams tool activity.
func (b *Bot) sessionHandlers(agentName, channelID, agentDir string) (onOutput func(loop.StreamEvent), onLifecycle func(string), opts *loop.StartOpts) {
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
			b.sendDelivery(b.session, channelID, agentName, agentDir)

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
			b.sendDelivery(b.session, channelID, agentName, agentDir)

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
			b.sendDelivery(b.session, channelID, agentName, agentDir)

		case "too_many_errors":
			if progress != nil {
				progress.Flush()
				progress.Finalize(fmt.Sprintf("**%s** — Too many errors. Loop stopped.", agentName))
				progress = nil
			}
			b.sendError(agentName, "Too many errors — loop stopped")
			b.sendDelivery(b.session, channelID, agentName, agentDir)

		case "wrap_up":
			if progress != nil {
				progress.Flush()
				summary := fmt.Sprintf("**%s** — Wrapped up. Loop stopped.", agentName)
				if len(tools) > 0 {
					summary += " " + sessionStats(len(tools), lastCost, lastDurationMs)
					summary += "\n" + formatToolTrail(tools)
				}
				progress.Finalize(summary)
				progress = nil
			} else {
				b.session.ChannelMessageSend(channelID, fmt.Sprintf("**%s** — Wrapped up. Loop stopped.", agentName))
			}
			b.sendStatus(agentName, "Wrap-up — loop stopped")
			b.runAgentArchive(agentName, agentDir)
			if lastResultText != "" {
				report := fmt.Sprintf("**%s — Report**\n%s", agentName, lastResultText)
				sendChunked(b.session, channelID, report)
			}
			b.sendDelivery(b.session, channelID, agentName, agentDir)

		case "stopped":
			if progress != nil {
				progress.Flush()
				progress.Finalize(fmt.Sprintf("**%s** — Stopped.", agentName))
				progress = nil
			}
			b.sendStatus(agentName, "Stopped")

		case "paused":
			if progress != nil {
				progress.Flush()
			}
			b.session.ChannelMessageSend(channelID, fmt.Sprintf("**%s** — Paused. Use `!resume` to continue.", agentName))
			b.sendStatus(agentName, "Paused")

		case "resumed":
			b.sendStatus(agentName, "Resumed")

		case "eval_stopped":
			if progress != nil {
				progress.Flush()
				progress.Finalize(fmt.Sprintf("**%s** — Eval loop stopped.", agentName))
				progress = nil
			}
			b.sendStatus(agentName, "Eval stopped")
			b.sendDelivery(b.session, channelID, agentName, agentDir)

		default:
			if strings.HasPrefix(event, "error:") || strings.HasPrefix(event, "backoff:") {
				if progress != nil {
					progress.Flush()
					progress.Finalize(fmt.Sprintf("**%s** — %s", agentName, event))
					progress = nil
				}
				b.sendError(agentName, event)
			} else {
				b.sendStatus(agentName, event)
			}
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
			if ev.Text != "" {
				msg = fmt.Sprintf("**%s** — Working... %s", agentName, ev.Text)
			}
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
			msg := fmt.Sprintf("**%s** — Thinking...", agentName)
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

	// Build eval opts
	projectsDir := filepath.Join(agentDir, "projects")
	onEvalUpdate := func(update loop.EvalUpdate) {
		var msg string
		switch update.Event {
		case "improved":
			msg = fmt.Sprintf("**%s** [%s] Iter %d: %s = %.4f (best %.4f) — $%.2f",
				agentName, update.Project, update.Iteration, update.MetricName, update.Value, update.Best, update.CostSoFar)
		case "regressed":
			msg = fmt.Sprintf("**%s** [%s] Iter %d: %s regressed to %.4f (best %.4f). Context injected via mailbox.",
				agentName, update.Project, update.Iteration, update.MetricName, update.Value, update.Best)
		case "budget_exceeded":
			msg = fmt.Sprintf("**%s** [%s] Budget limit reached ($%.2f). Loop stopped. Best: %.4f",
				agentName, update.Project, update.CostSoFar, update.Best)
		case "converged":
			msg = fmt.Sprintf("**%s** [%s] Converged — no improvement in recent iterations. Best: %.4f",
				agentName, update.Project, update.Best)
		}
		if msg != "" {
			b.session.ChannelMessageSend(channelID, msg)
			b.sendStatus(agentName, fmt.Sprintf("eval: %s", update.Event))
		}
	}

	opts = &loop.StartOpts{
		ProjectsDir:  projectsDir,
		OnEvalUpdate: onEvalUpdate,
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

// sendError sends an error event to the error channel (if configured), otherwise status channel.
func (b *Bot) sendError(agentName, event string) {
	channelID := b.cfg.Bot.ErrorChannelID
	if channelID == "" {
		channelID = b.cfg.Bot.StatusChannelID
	}
	if channelID == "" {
		return
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf("`[%s - %s]` %s", ts, agentName, event)
	if _, err := b.session.ChannelMessageSend(channelID, msg); err != nil {
		log.Printf("[keel] %s: error send error: %v", agentName, err)
	}
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

// sendDelivery reads DELIVER.md from the agent directory, removes it
// immediately to avoid TOCTOU races, then sends contents to Discord.
func (b *Bot) sendDelivery(s *discordgo.Session, channelID, agentName, agentDir string) {
	content, err := workspace.ReadDeliver(agentDir)
	if err != nil {
		log.Printf("[keel] %s: error reading DELIVER.md: %v", agentName, err)
		return
	}
	if content == "" {
		return
	}
	// Remove immediately before posting to prevent a new loop from losing a future delivery
	if err := workspace.ClearDeliver(agentDir); err != nil {
		log.Printf("[keel] %s: error clearing DELIVER.md: %v", agentName, err)
	}
	header := fmt.Sprintf("**%s — Delivery**\n", agentName)
	sendChunked(s, channelID, header+content)
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

// runAgentArchive runs cubit archive in the agent directory to send logs to nark.
func (b *Bot) runAgentArchive(agentName, agentDir string) {
	log.Printf("[keel] %s: running cubit archive (wrap-up)", agentName)
	cmd := exec.Command("cubit", "archive")
	cmd.Dir = agentDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[keel] %s: archive error: %v\n%s", agentName, err, output)
	} else {
		log.Printf("[keel] %s: archive complete", agentName)
	}
}

// runAgentDream runs cubit dream to consolidate agent memory.
func (b *Bot) runAgentDream(agentName, agentDir string) {
	log.Printf("[keel] %s: running cubit dream", agentName)
	cmd := exec.Command("cubit", "dream", "--include-log")
	cmd.Dir = agentDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[keel] %s: dream error: %v\n%s", agentName, err, output)
	} else {
		log.Printf("[keel] %s: dream complete", agentName)
	}
}

// checkDream runs cubit dream for all idle agents at 4:00 AM daily.
func (b *Bot) checkDream() {
	now := time.Now()
	if now.Hour() != 4 || now.Minute() != 0 {
		return
	}
	today := now.Format("2006-01-02")
	if b.lastDreamDate == today {
		return
	}
	b.lastDreamDate = today
	for name, ch := range b.cfg.Channels {
		if b.loopMgr.IsRunning(name) {
			log.Printf("[keel] %s: skipping dream (loop running)", name)
			continue
		}
		b.sendStatus(name, "Running scheduled dream...")
		b.runAgentDream(name, ch.AgentDir)
		b.sendStatus(name, "Dream complete")
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
		b.checkDream()
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
					onOutput, onLifecycle, opts := b.sessionHandlers(agentName, channelID, agentDir)
					if err := b.loopMgr.Start(agentName, agentDir, b.commandBuilder(agentName), b.sleepBetween, b.archiveEvery, onOutput, onLifecycle, opts); err != nil {
						log.Printf("[keel] scheduler: error restarting %s: %v", agentName, err)
					}
				}()
			} else {
				b.loopMgr.Nudge(name)
			}
		} else {
			onOutput, onLifecycle, opts := b.sessionHandlers(name, ch.ChannelID, ch.AgentDir)
			if err := b.loopMgr.Start(name, ch.AgentDir, b.commandBuilder(name), b.sleepBetween, b.archiveEvery, onOutput, onLifecycle, opts); err != nil {
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

package discord

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/SeanoChang/keel/internal/config"
	"github.com/SeanoChang/keel/internal/loop"
	"github.com/SeanoChang/keel/internal/workspace"
)

type Bot struct {
	session      *discordgo.Session
	cfg          *config.Config
	loopMgr      *loop.Manager
	tailers      map[string]*LogTailer
	sleepBetween time.Duration
	archiveEvery int
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
		tailer := NewLogTailer(name, ch.AgentDir, ch.ChannelID, b.session)
		b.tailers[name] = tailer
		go tailer.Start()
	}

	return nil
}

func (b *Bot) Stop() {
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

	b.reply(s, m, fmt.Sprintf("Goal added for **%s**. Use `!goals` to see all.", agentName))

	if !b.loopMgr.IsRunning(agentName) {
		err := b.loopMgr.Start(agentName, ch.AgentDir, loop.DefaultCommandBuilder, b.sleepBetween, b.archiveEvery)
		if err != nil {
			log.Printf("[keel] error starting loop for %s: %v", agentName, err)
		} else {
			b.reply(s, m, fmt.Sprintf("Agent loop started for **%s**.", agentName))
		}
	}
}

func (b *Bot) reply(s *discordgo.Session, m *discordgo.MessageCreate, msg string) {
	_, err := s.ChannelMessageSend(m.ChannelID, msg)
	if err != nil {
		log.Printf("[keel] discord send error: %v", err)
	}
}

// ParseCommand checks if a message is a ! command.
func ParseCommand(content string) (bool, string, string) {
	if !strings.HasPrefix(content, "!") {
		return false, "", ""
	}
	rest := strings.TrimPrefix(content, "!")
	parts := strings.SplitN(rest, " ", 2)
	cmd := strings.TrimSpace(parts[0])
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	return true, cmd, args
}

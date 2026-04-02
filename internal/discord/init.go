package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"

	"github.com/SeanoChang/keel/internal/config"
)

const (
	phaseInterview      = 0
	phaseChannelMapping = 1
	phaseDone           = 2

	maxInitInterviewRounds = 5
	initTimeoutDuration    = 15 * time.Minute
)

type initSetupTarget struct {
	file    string
	purpose string
}

var setupTargets = []initSetupTarget{
	{"FLUCTLIGHT.md", "the agent's identity — their role, personality, expertise, and communication style"},
	{"MEMORY.md", "initial working context — project background, tools, constraints, and current state"},
}

// InitPending mirrors the JSON written by cubit init --keel.
type InitPending struct {
	Agent          string `json:"agent"`
	RequestedAt    string `json:"requested_at"`
	ImportIdentity bool   `json:"import_identity"`
}

// InitSession manages the state of a Discord-based agent init flow.
type InitSession struct {
	mu          sync.Mutex
	agentName   string
	agentDir    string
	initiatorID string
	channelID   string
	configPath  string
	guildID     string
	session     *discordgo.Session

	phase        int
	targets      []initSetupTarget
	targetIdx    int
	round        int
	conversation []string

	ctx      context.Context
	timeout  *time.Timer
	cancelFn context.CancelFunc
}

func NewInitSession(
	s *discordgo.Session,
	channelID, agentName, agentDir, initiatorID, configPath, guildID string,
	importIdentity bool,
) *InitSession {
	targets := make([]initSetupTarget, len(setupTargets))
	copy(targets, setupTargets)
	if importIdentity {
		targets = targets[1:] // skip FLUCTLIGHT.md
	}

	ctx, cancel := context.WithCancel(context.Background())

	is := &InitSession{
		agentName:   agentName,
		agentDir:    agentDir,
		initiatorID: initiatorID,
		channelID:   channelID,
		configPath:  configPath,
		guildID:     guildID,
		session:     s,
		phase:       phaseInterview,
		targets:     targets,
		targetIdx:   0,
		ctx:         ctx,
		cancelFn:    cancel,
	}
	is.resetTimeout()
	return is
}

// Start begins the init flow by posting the intro and first question.
func (is *InitSession) Start() {
	is.send(fmt.Sprintf("**Setting up agent '%s'** — I'll ask a few questions to build their identity and context.\nType `!skip` to skip a file, `!skip-all` to skip remaining, `!quit` to cancel.", is.agentName))

	if len(is.targets) == 0 {
		is.phase = phaseChannelMapping
		is.askChannelMapping()
		return
	}

	is.send(fmt.Sprintf("=== %s ===", is.targets[0].file))
	is.askNextQuestion()
}

// HandleMessage processes a user message during the init flow.
// Returns true if the message was consumed by the init session.
func (is *InitSession) HandleMessage(authorID, content string) bool {
	is.mu.Lock()
	defer is.mu.Unlock()

	// For CLI-triggered inits (no initiator), accept from any user
	if is.initiatorID != "" && authorID != is.initiatorID {
		return false
	}

	is.resetTimeout()
	content = strings.TrimSpace(content)

	// Handle !quit at any phase
	if content == "!quit" {
		is.cleanup()
		is.send(fmt.Sprintf("Init cancelled. Agent '%s' cleaned up.", is.agentName))
		is.phase = phaseDone
		return true
	}

	switch is.phase {
	case phaseInterview:
		return is.handleInterviewMessage(content)
	case phaseChannelMapping:
		return is.handleChannelMappingMessage(content)
	default:
		return false
	}
}

func (is *InitSession) handleInterviewMessage(content string) bool {
	if content == "!skip" {
		is.send(fmt.Sprintf("Skipped %s.", is.targets[is.targetIdx].file))
		is.targetIdx++
		is.round = 0
		is.conversation = nil
		is.advanceTarget()
		return true
	}

	if content == "!skip-all" {
		is.send("Skipping remaining interview.")
		is.phase = phaseChannelMapping
		is.askChannelMapping()
		return true
	}

	// Append answer to conversation
	is.conversation = append(is.conversation, content)
	is.round++

	if is.round >= maxInitInterviewRounds {
		is.generateFile()
		is.targetIdx++
		is.round = 0
		is.conversation = nil
		is.advanceTarget()
		return true
	}

	is.askNextQuestion()
	return true
}

func (is *InitSession) handleChannelMappingMessage(content string) bool {
	if !isValidSnowflake(content) {
		is.send("Invalid channel ID — must be a numeric Discord snowflake (17-20 digits). Try again, or type `!quit` to cancel.")
		return true
	}

	// Check if channel exists in this guild
	ch, err := is.session.Channel(content)
	if err != nil || ch == nil {
		is.send("Channel not found. Double-check the ID and try again.")
		return true
	}
	if ch.GuildID != is.guildID {
		is.send("That channel is not in this guild. Use a channel from this server.")
		return true
	}

	// Check if already mapped
	cfg, err := config.Load(is.configPath)
	if err == nil {
		for name, existing := range cfg.Channels {
			if existing.ChannelID == content {
				is.send(fmt.Sprintf("Channel already mapped to agent **%s**. Use a different channel.", name))
				return true
			}
		}
	}

	// Write channel mapping to discord.toml
	// Use ~/... shorthand if the path is under home directory
	agentDirShort := is.agentDir
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, is.agentDir); err == nil && !strings.HasPrefix(rel, "..") {
			agentDirShort = "~/" + rel
		}
	}
	if err := config.AppendChannel(is.configPath, is.agentName, content, agentDirShort); err != nil {
		is.send(fmt.Sprintf("Error writing config: %v", err))
		return true
	}

	is.finalize(content)
	return true
}

func (is *InitSession) advanceTarget() {
	if is.targetIdx >= len(is.targets) {
		is.phase = phaseChannelMapping
		is.askChannelMapping()
		return
	}
	is.send(fmt.Sprintf("=== %s ===", is.targets[is.targetIdx].file))
	is.askNextQuestion()
}

func (is *InitSession) askChannelMapping() {
	is.send(fmt.Sprintf("What Discord channel should **%s** use? Paste the channel ID.", is.agentName))
}

func (is *InitSession) askNextQuestion() {
	t := is.targets[is.targetIdx]
	prompt := buildInitInterviewPrompt(is.agentName, t, is.conversation, is.round)

	cmd := exec.CommandContext(is.ctx, "claude", "-p", prompt)
	cmd.Dir = is.agentDir
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[keel] init: claude error for %s/%s: %v", is.agentName, t.file, err)
		is.send(fmt.Sprintf("Claude error — skipping %s.", t.file))
		is.targetIdx++
		is.round = 0
		is.conversation = nil
		is.advanceTarget()
		return
	}

	result := strings.TrimSpace(string(out))

	if result == "DONE" {
		is.generateFile()
		is.targetIdx++
		is.round = 0
		is.conversation = nil
		is.advanceTarget()
		return
	}

	// Post question and prepend it to conversation for next round
	is.send(result)
	is.conversation = append(is.conversation, result)
	// Note: conversation is now [q1, a1, q2, a2, ...qN] — answer comes in HandleMessage
}

func (is *InitSession) generateFile() {
	t := is.targets[is.targetIdx]
	is.send(fmt.Sprintf("Generating %s...", t.file))

	prompt := buildInitGeneratePrompt(is.agentName, t, is.conversation)
	cmd := exec.CommandContext(is.ctx, "claude", "-p", prompt)
	cmd.Dir = is.agentDir
	out, err := cmd.Output()
	if err != nil {
		log.Printf("[keel] init: generation error for %s/%s: %v", is.agentName, t.file, err)
		is.send(fmt.Sprintf("Generation error for %s — skipping.", t.file))
		return
	}

	filePath := filepath.Join(is.agentDir, t.file)
	if err := os.WriteFile(filePath, out, 0o644); err != nil {
		log.Printf("[keel] init: write error for %s: %v", filePath, err)
		is.send(fmt.Sprintf("Error writing %s: %v", t.file, err))
		return
	}
	is.send(fmt.Sprintf("%s updated.", t.file))
}

func (is *InitSession) finalize(channelID string) {
	// Delete .init-pending
	os.Remove(filepath.Join(is.agentDir, ".init-pending"))

	is.send(fmt.Sprintf("**Agent '%s' is ready.** Channel <#%s> is now mapped. Use that channel to send goals.", is.agentName, channelID))
	is.phase = phaseDone
	is.stopTimeout()
}

func (is *InitSession) cleanup() {
	is.stopTimeout()
	is.cancelFn()
	os.RemoveAll(is.agentDir)
	log.Printf("[keel] init: cleaned up agent dir %s", is.agentDir)
}

// IsDone returns true when the init flow has completed or been cancelled.
func (is *InitSession) IsDone() bool {
	is.mu.Lock()
	defer is.mu.Unlock()
	return is.phase == phaseDone
}

// AgentName returns the name of the agent being initialized.
func (is *InitSession) AgentName() string {
	return is.agentName
}

func (is *InitSession) send(msg string) {
	if len(msg) <= 1900 {
		if _, err := is.session.ChannelMessageSend(is.channelID, msg); err != nil {
			log.Printf("[keel] init: discord send error: %v", err)
		}
		return
	}
	sendChunked(is.session, is.channelID, msg)
}

func (is *InitSession) resetTimeout() {
	is.stopTimeout()
	is.timeout = time.AfterFunc(initTimeoutDuration, func() {
		is.mu.Lock()
		defer is.mu.Unlock()
		if is.phase == phaseDone {
			return
		}
		is.cleanup()
		is.send(fmt.Sprintf("Init timed out (15 min no response). Agent '%s' cleaned up.", is.agentName))
		is.phase = phaseDone
	})
}

func (is *InitSession) stopTimeout() {
	if is.timeout != nil {
		is.timeout.Stop()
	}
}

// --- Prompt builders ---

func buildInitInterviewPrompt(agent string, t initSetupTarget, conversation []string, round int) string {
	if round == 0 {
		return fmt.Sprintf(
			"You are helping set up an AI agent workspace for an agent named %q. "+
				"You need to gather information about %s. "+
				"Ask ONE focused question to start understanding this. "+
				"Output ONLY the question text, nothing else.",
			agent, t.purpose)
	}

	convo := formatInitConversation(conversation)
	return fmt.Sprintf(
		"You are helping set up an AI agent named %q, gathering information about %s.\n\n"+
			"Interview so far:\n%s\n"+
			"If you have enough information to write a good %s, output exactly the word DONE (nothing else).\n"+
			"Otherwise, ask ONE focused follow-up question. Output ONLY the question or DONE.",
		agent, t.purpose, convo, t.file)
}

func buildInitGeneratePrompt(agent string, t initSetupTarget, conversation []string) string {
	convo := formatInitConversation(conversation)
	return fmt.Sprintf(
		"Generate a %s file for an AI agent named %q based on this interview:\n\n"+
			"%s\n"+
			"Create a well-structured markdown document that captures all the information gathered. "+
			"Output ONLY the raw markdown content — no code fences, no preamble, no explanations.",
		t.file, agent, convo)
}

func formatInitConversation(conversation []string) string {
	var b strings.Builder
	for i := 0; i < len(conversation)-1; i += 2 {
		fmt.Fprintf(&b, "Q: %s\nA: %s\n\n", conversation[i], conversation[i+1])
	}
	return b.String()
}

// isValidAgentName checks that an agent name is safe for use as a directory name.
// Allows alphanumeric, hyphens, and underscores only.
func isValidAgentName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func isValidSnowflake(s string) bool {
	if len(s) < 17 || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// ParseInitPending reads and parses a .init-pending JSON file.
func ParseInitPending(agentDir string) (*InitPending, error) {
	data, err := os.ReadFile(filepath.Join(agentDir, ".init-pending"))
	if err != nil {
		return nil, err
	}
	var p InitPending
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

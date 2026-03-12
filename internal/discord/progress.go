package discord

import (
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ProgressMessage manages a single Discord message that gets edited in-place
// to show live progress (e.g. "Running... [WebSearch ...]" → final result).
type ProgressMessage struct {
	session     *discordgo.Session
	channelID   string
	messageID   string
	mu          sync.Mutex
	lastEdit    time.Time
	minInterval time.Duration
	lastContent string
	pending     string // buffered content waiting for next edit window
}

func NewProgressMessage(session *discordgo.Session, channelID string) *ProgressMessage {
	return &ProgressMessage{
		session:     session,
		channelID:   channelID,
		minInterval: 2 * time.Second,
	}
}

// Send creates the initial message. Returns the message ID.
func (p *ProgressMessage) Send(content string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	msg, err := p.session.ChannelMessageSend(p.channelID, content)
	if err != nil {
		return err
	}
	p.messageID = msg.ID
	p.lastEdit = time.Now()
	p.lastContent = content
	return nil
}

// Update edits the message if enough time has passed since the last edit.
// Skips duplicate content. Throttled to avoid Discord rate limits.
func (p *ProgressMessage) Update(content string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.messageID == "" || content == p.lastContent {
		return
	}
	if time.Since(p.lastEdit) < p.minInterval {
		p.pending = content
		return
	}
	p.doEdit(content)
}

// Finalize forces one last edit regardless of throttle.
func (p *ProgressMessage) Finalize(content string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.messageID == "" {
		return
	}
	p.doEdit(content)
	p.pending = ""
}

// Flush sends any pending throttled update.
func (p *ProgressMessage) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending != "" && p.messageID != "" {
		p.doEdit(p.pending)
		p.pending = ""
	}
}

// MessageID returns the underlying Discord message ID.
func (p *ProgressMessage) MessageID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.messageID
}

func (p *ProgressMessage) doEdit(content string) {
	if _, err := p.session.ChannelMessageEdit(p.channelID, p.messageID, content); err != nil {
		log.Printf("[keel] progress edit error: %v", err)
	} else {
		p.lastEdit = time.Now()
		p.lastContent = content
	}
}

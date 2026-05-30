package comms

import (
	"log"
	"strings"
	"sync"
	"time"
)

// MessageSender is the narrow Discord interface Dashboard depends on. Backed
// by *discordgo.Session in production; a fake in tests.
type MessageSender interface {
	Send(channelID, content string) (messageID string, err error)
	Edit(channelID, messageID, content string) error
}

// Dashboard owns one Discord message that re-renders in place whenever the
// tracker notifies it. Multiple notifications within debounceDelay are
// coalesced into a single render, and concurrent renders serialize so two
// in-flight sends can never both observe messageID=="" and post duplicates.
type Dashboard struct {
	sender    MessageSender
	channelID string
	scope     Scope
	tracker   *Tracker
	state     *StateStore

	debounceDelay time.Duration

	mu        sync.Mutex // guards timer, inFlight, pending
	timer     *time.Timer
	pending   bool // a Notify arrived during an in-flight render

	// renderMu serializes the actual Discord I/O. Holding it across the whole
	// of render() means a racing second call sees the messageID written by
	// the first and does an Edit, not a duplicate Send.
	renderMu  sync.Mutex
	messageID string
	inFlight  bool // true while render() is executing
}

func NewDashboard(sender MessageSender, channelID string, scope Scope, tracker *Tracker, state *StateStore) *Dashboard {
	d := &Dashboard{
		sender:        sender,
		channelID:     channelID,
		scope:         scope,
		tracker:       tracker,
		state:         state,
		debounceDelay: 500 * time.Millisecond,
	}
	if state != nil {
		if id, ok := state.Get(d.stateKey()); ok {
			d.messageID = id
		}
	}
	return d
}

// Notify schedules a render. Production dashboards always have
// debounceDelay > 0, so multiple Notifies within the window coalesce into
// one render. The debounceDelay <= 0 branch is a test-only synchronous
// path; the boot-seed path uses d.Render() directly, not Notify.
//
// When a render is already in flight (e.g., Discord HTTP hanging), Notify
// does NOT schedule a new timer — it just sets the pending bit. The in-flight
// render's deferred cleanup re-triggers exactly one follow-up. This bounds
// the queued-render count at most to 1 regardless of Notify rate.
func (d *Dashboard) Notify() {
	if d.channelID == "" {
		return
	}
	if d.debounceDelay <= 0 {
		_ = d.render()
		return
	}
	d.mu.Lock()
	if d.inFlight {
		d.pending = true
		d.mu.Unlock()
		return
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.debounceDelay, func() { _ = d.render() })
	d.mu.Unlock()
}

// Render synchronously produces the body and pushes it to Discord. Use Notify
// in production paths; Render is exposed for boot-time first paint.
func (d *Dashboard) Render() error {
	if d.channelID == "" {
		return nil
	}
	return d.render()
}

// render serializes all Discord I/O for this dashboard through renderMu. That
// single point of serialization is what prevents two concurrent calls from
// both observing messageID == "" and both posting fresh messages — the second
// caller blocks until the first writes back the new messageID, then issues an
// Edit instead.
//
// On completion, render checks d.pending and spawns at most one follow-up
// goroutine to capture any Notifies that arrived while it was running. This
// keeps queued-render goroutines bounded under a slow/hung Discord.
func (d *Dashboard) render() error {
	d.renderMu.Lock()
	defer d.renderMu.Unlock()

	d.markInFlight(true)
	defer d.finishAndMaybeFollowup()

	body := Render(d.tracker.Snapshot(), d.scope, time.Now())

	if d.messageID != "" {
		if err := d.sender.Edit(d.channelID, d.messageID, body); err == nil {
			return nil
		} else if !isUnknownMessage(err) {
			log.Printf("[keel] comms: edit %s failed: %v", d.channelID, err)
			return err
		}
		// Fall through: Edit returned "Unknown Message" — the dashboard
		// message was deleted out from under us, so post a fresh one.
		d.messageID = ""
	}

	newID, err := d.sender.Send(d.channelID, body)
	if err != nil {
		log.Printf("[keel] comms: send %s failed: %v", d.channelID, err)
		return err
	}
	d.messageID = newID
	if d.state != nil {
		if err := d.state.Set(d.stateKey(), newID); err != nil {
			log.Printf("[keel] comms: state save failed: %v", err)
		}
	}
	return nil
}

func (d *Dashboard) markInFlight(v bool) {
	d.mu.Lock()
	d.inFlight = v
	d.mu.Unlock()
}

// finishAndMaybeFollowup clears inFlight and, if any Notifies arrived during
// the in-flight render, fires exactly one follow-up goroutine. The follow-up
// is async so the original render releases renderMu first.
func (d *Dashboard) finishAndMaybeFollowup() {
	d.mu.Lock()
	d.inFlight = false
	rerun := d.pending
	d.pending = false
	d.mu.Unlock()
	if rerun {
		go func() { _ = d.render() }()
	}
}

func (d *Dashboard) stateKey() string {
	return "channel." + d.channelID
}

func (d *Dashboard) rendering() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.inFlight
}

// isUnknownMessage detects Discord's 10008 "Unknown Message" response so we
// can recover by re-posting. We match on the substring to avoid importing
// discordgo's specific error type into this package (lets us keep the
// interface boundary clean).
func isUnknownMessage(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "10008") || strings.Contains(msg, "Unknown Message")
}

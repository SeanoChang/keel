package comms

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeSender struct {
	mu         sync.Mutex
	sends      []sendCall
	edits      []editCall
	nextSendID string
	editErr    error // returned by next Edit call (cleared after each)
	sendDelay  time.Duration // simulate slow Discord network
}

type sendCall struct {
	channelID string
	content   string
}
type editCall struct {
	channelID string
	messageID string
	content   string
}

func (f *fakeSender) Send(channelID, content string) (string, error) {
	f.mu.Lock()
	delay := f.sendDelay
	id := f.nextSendID
	if id == "" {
		id = "msg-" + channelID
	}
	f.nextSendID = ""
	f.sends = append(f.sends, sendCall{channelID, content})
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	return id, nil
}

func (f *fakeSender) Edit(channelID, messageID, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, editCall{channelID, messageID, content})
	if f.editErr != nil {
		err := f.editErr
		f.editErr = nil
		return err
	}
	return nil
}

// sendCount and editCount are thread-safe accessors so tests don't read the
// underlying slices directly.
func (f *fakeSender) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

func (f *fakeSender) editCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.edits)
}

func newDashboardForTest(t *testing.T, scope Scope) (*Dashboard, *Tracker, *fakeSender, *StateStore) {
	t.Helper()
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	store, err := OpenStateStore(filepath.Join(t.TempDir(), "comms.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	sender := &fakeSender{}
	d := NewDashboard(sender, "channel.1", scope, tr, store)
	d.debounceDelay = 0 // make notifications synchronous for tests
	return d, tr, sender, store
}

func TestDashboard_FirstRenderSendsAndPersistsID(t *testing.T) {
	d, tr, sender, store := newDashboardForTest(t, Scope{Title: "Agent Comms"})
	tr.RecordSent(Event{ID: "1", Kind: KindMessage, From: "noah", To: "ezra",
		Subject: "hi", Status: StatusSent, Timestamp: mustParse(t, "2026-05-22T11:00:00Z")})
	d.Notify()
	waitForDashboard(t, d)

	if sender.sendCount() != 1 {
		t.Fatalf("expected 1 send, got %d", sender.sendCount())
	}
	if sender.editCount() != 0 {
		t.Fatalf("expected 0 edits, got %d", sender.editCount())
	}
	got, ok := store.Get("channel.channel.1")
	if !ok || got != "msg-channel.1" {
		t.Fatalf("state not persisted: %q ok=%v", got, ok)
	}
}

func TestDashboard_SubsequentRendersEdit(t *testing.T) {
	d, tr, sender, _ := newDashboardForTest(t, Scope{Title: "Agent Comms"})
	tr.RecordSent(Event{ID: "1", Kind: KindMessage, From: "noah", To: "ezra",
		Status: StatusSent, Timestamp: mustParse(t, "2026-05-22T11:00:00Z")})
	d.Notify()
	waitForDashboard(t, d)
	tr.RecordDelivered("1")
	d.Notify()
	waitForDashboard(t, d)

	if sender.sendCount() != 1 || sender.editCount() != 1 {
		t.Fatalf("expected 1 send + 1 edit, got %d/%d", sender.sendCount(), sender.editCount())
	}
}

func TestDashboard_EditFailureFallsBackToSend(t *testing.T) {
	d, tr, sender, store := newDashboardForTest(t, Scope{Title: "Agent Comms"})
	tr.RecordSent(Event{ID: "1", Kind: KindMessage, From: "noah", To: "ezra",
		Status: StatusSent, Timestamp: mustParse(t, "2026-05-22T11:00:00Z")})
	d.Notify()
	waitForDashboard(t, d)

	// Now simulate the channel's dashboard message having been deleted.
	sender.editErr = errors.New("HTTP 404 Not Found, {\"code\": 10008, \"message\": \"Unknown Message\"}")
	sender.nextSendID = "msg-recovered"

	tr.RecordDelivered("1")
	d.Notify()
	waitForDashboard(t, d)

	if sender.sendCount() != 2 {
		t.Fatalf("expected fallback Send, got %d sends", sender.sendCount())
	}
	got, _ := store.Get("channel.channel.1")
	if got != "msg-recovered" {
		t.Fatalf("state not updated to recovered id; got %q", got)
	}
}

func TestDashboard_DebounceCoalesces(t *testing.T) {
	d, tr, sender, _ := newDashboardForTest(t, Scope{Title: "Agent Comms"})
	d.debounceDelay = 30 * time.Millisecond

	for i := 0; i < 5; i++ {
		tr.RecordSent(Event{
			ID:        "id" + string(rune('a'+i)),
			Kind:      KindMessage,
			From:      "noah", To: "ezra",
			Status:    StatusSent,
			Timestamp: mustParse(t, "2026-05-22T11:00:00Z").Add(time.Duration(i) * time.Second),
		})
		// Notify is called inside RecordSent → tracker.notify (which the bot
		// will wire to dashboard.Notify). Simulate that wiring here:
		d.Notify()
	}
	// Wait for the debounce timer to fire.
	time.Sleep(80 * time.Millisecond)

	if sender.sendCount()+sender.editCount() != 1 {
		t.Fatalf("expected one render after debounce, got sends=%d edits=%d",
			sender.sendCount(), sender.editCount())
	}
}

func TestDashboard_ConcurrentRendersDoNotDuplicateMessage(t *testing.T) {
	// Regression for the race where two render() calls can both observe
	// messageID == "" and both call Send, producing two Discord messages.
	// Trigger: slow Discord I/O during the first send, second Notify races in.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	store, _ := OpenStateStore(t.TempDir() + "/comms.json")
	sender := &fakeSender{sendDelay: 50 * time.Millisecond}
	d := NewDashboard(sender, "channel.race", Scope{Title: "Race"}, tr, store)
	d.debounceDelay = 0 // synchronous render path
	tr.RecordSent(Event{ID: "e1", Kind: KindMessage, From: "a", To: "b",
		Status: StatusSent, Timestamp: mustParse(t, "2026-05-22T11:00:00Z")})

	// Fire many Notify calls concurrently — they must serialize and produce
	// at most ONE Send (subsequent ones must Edit the established message).
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); d.Notify() }()
	}
	wg.Wait()

	if got := sender.sendCount(); got != 1 {
		t.Fatalf("expected exactly 1 Send under concurrent Notify, got %d", got)
	}
}

func TestDashboard_NotifyDuringInFlightRenderCoalescesToOneFollowup(t *testing.T) {
	// Regression for goroutine pile-up: while a render is hung on Discord I/O,
	// repeated Notify calls spaced longer than debounceDelay would (without
	// the fix) each schedule their own timer, each timer would fire and
	// queue another render goroutine on renderMu. With the fix, the
	// in-flight flag collapses all of them into a single follow-up.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	store, _ := OpenStateStore(t.TempDir() + "/comms.json")

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	sender := &gateSender{started: started, release: release}

	d := NewDashboard(sender, "channel.flood", Scope{Title: "Flood"}, tr, store)
	d.debounceDelay = 5 * time.Millisecond
	tr.RecordSent(Event{ID: "e1", Kind: KindMessage, From: "a", To: "b",
		Status: StatusSent, Timestamp: mustParse(t, "2026-05-22T11:00:00Z")})

	d.Notify()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first render never started")
	}

	// Space the Notifies 10ms apart (> 5ms debounce) so each one's timer
	// fires before the next Notify can Stop it. Without the fix this
	// produces 10 queued renders blocked on renderMu; with the fix they
	// collapse into a single pending bit.
	for i := 0; i < 10; i++ {
		d.Notify()
		time.Sleep(10 * time.Millisecond)
	}

	close(release)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sender.totalCalls() >= 2 && !d.rendering() {
			// Give any straggler renders a tick to land.
			time.Sleep(50 * time.Millisecond)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	total := sender.totalCalls()
	if total != 2 {
		t.Fatalf("expected exactly 2 sender calls (initial + 1 follow-up), got %d", total)
	}
}

// gateSender blocks every Send/Edit on a release channel and signals each
// start via the started channel. Used to deterministically simulate hung
// Discord HTTP for dashboard concurrency tests.
type gateSender struct {
	started chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
}

func (g *gateSender) Send(channelID, content string) (string, error) {
	select {
	case g.started <- struct{}{}:
	default:
	}
	<-g.release
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	return "msg-" + channelID, nil
}

func (g *gateSender) Edit(channelID, messageID, content string) error {
	select {
	case g.started <- struct{}{}:
	default:
	}
	<-g.release
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	return nil
}

func (g *gateSender) totalCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func TestDashboard_EmptyChannelIDIsNoop(t *testing.T) {
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	sender := &fakeSender{}
	d := NewDashboard(sender, "", Scope{Title: "Disabled"}, tr, nil)
	d.debounceDelay = 0
	tr.RecordSent(Event{ID: "1", Kind: KindMessage, From: "a", To: "b", Status: StatusSent,
		Timestamp: mustParse(t, "2026-05-22T11:00:00Z")})
	d.Notify()
	waitForDashboard(t, d)

	if sender.sendCount()+sender.editCount() != 0 {
		t.Fatalf("disabled dashboard should not call sender; got sends=%d edits=%d",
			sender.sendCount(), sender.editCount())
	}
}

// waitForDashboard waits until any in-flight debounced render has completed.
// With debounceDelay=0 it returns essentially immediately, but we still need
// to let the goroutine schedule.
func waitForDashboard(t *testing.T, d *Dashboard) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !d.rendering() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("dashboard render did not complete in time")
}


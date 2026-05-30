package comms

import (
	"sort"
	"sync"
	"time"
)

// Tracker is the in-memory store for the last `window` of comms events.
// All public methods are safe for concurrent use.
type Tracker struct {
	mu     sync.RWMutex
	events []Event
	byID   map[string]int
	window time.Duration
	now    func() time.Time
	notify func()

	// pendingDelivered buffers RecordDelivered calls whose ID hasn't been seen
	// yet — needed because cubit send writes the recipient's inbox before
	// TryShip returns to the sender's outbox watcher, so the delivered event
	// can race ahead of the sent event.
	pendingDelivered map[string]struct{}
}

func New(window time.Duration, notify func()) *Tracker {
	if notify == nil {
		notify = func() {}
	}
	return &Tracker{
		events:           nil,
		byID:             map[string]int{},
		window:           window,
		now:              time.Now,
		notify:           notify,
		pendingDelivered: map[string]struct{}{},
	}
}

// SetNotify swaps the notify callback. Used when dashboards are constructed
// after the tracker (dashboards depend on tracker, so the wiring is two-step).
func (t *Tracker) SetNotify(notify func()) {
	if notify == nil {
		notify = func() {}
	}
	t.mu.Lock()
	t.notify = notify
	t.mu.Unlock()
}

// RecordSent inserts (or upserts) a sent event. Calling twice with the same ID
// updates the existing row. If a RecordDelivered already arrived for this ID
// (race: cubit send fires the recipient's fsnotify before returning to the
// sender's watcher), the event is promoted to Delivered immediately.
func (t *Tracker) RecordSent(ev Event) {
	t.mu.Lock()
	if _, raced := t.pendingDelivered[ev.ID]; raced {
		ev.Status = StatusDelivered
		delete(t.pendingDelivered, ev.ID)
	}
	t.upsertLocked(ev)
	t.mu.Unlock()
	t.notify()
}

// RecordDelivered promotes Sent → Delivered on the matching ID. Unknown IDs
// are buffered in pendingDelivered so a racing RecordSent that arrives later
// can promote immediately.
func (t *Tracker) RecordDelivered(id string) {
	t.mu.Lock()
	if idx, ok := t.byID[id]; ok {
		t.events[idx].Status = StatusDelivered
		t.events[idx].Failure = ""
		t.mu.Unlock()
		t.notify()
		return
	}
	t.pendingDelivered[id] = struct{}{}
	t.mu.Unlock()
}

// RecordFailed marks the event with this ID as Failed and stores the failure
// detail. When the ID is unknown it inserts a fresh row — the common case,
// because validation/transport failures never produce a prior RecordSent
// (which only fires on Sent=true). The Event's Status is forced to
// StatusFailed and its Failure field is taken as-authoritative; everything
// else (From/To/Subject/Timestamp) is used as best-effort metadata for the
// inserted row.
func (t *Tracker) RecordFailed(ev Event) {
	ev.Status = StatusFailed
	if ev.Kind == "" {
		ev.Kind = KindMessage
	}
	t.mu.Lock()
	if idx, ok := t.byID[ev.ID]; ok {
		// Preserve existing position/metadata, just flip status and reason.
		t.events[idx].Status = StatusFailed
		t.events[idx].Failure = ev.Failure
	} else {
		t.upsertLocked(ev)
	}
	t.mu.Unlock()
	t.notify()
}

// RecordDelegation upserts a delegation lifecycle row. When the ID already
// exists, the original timestamp is preserved so the row stays in its
// chronological position as sub-task progress updates arrive.
func (t *Tracker) RecordDelegation(ev Event) {
	if ev.Kind == "" {
		ev.Kind = KindDelegation
	}
	t.mu.Lock()
	if idx, ok := t.byID[ev.ID]; ok {
		ev.Timestamp = t.events[idx].Timestamp
	}
	t.upsertLocked(ev)
	t.mu.Unlock()
	t.notify()
}

// Snapshot returns a copy of all events within the active window, sorted
// ascending by Timestamp.
func (t *Tracker) Snapshot() []Event {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cutoff := t.now().Add(-t.window)
	out := make([]Event, 0, len(t.events))
	for _, ev := range t.events {
		if ev.Timestamp.Before(cutoff) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// Evict drops events older than the window. Reduces memory pressure but
// Snapshot already filters, so this is purely a maintenance step. Also wipes
// any orphaned pendingDelivered entries (which are meant to live milliseconds
// — anything still pending is unrecoverable and would leak otherwise).
func (t *Tracker) Evict() {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := t.now().Add(-t.window)
	kept := t.events[:0]
	byID := make(map[string]int, len(t.events))
	for _, ev := range t.events {
		if ev.Timestamp.Before(cutoff) {
			continue
		}
		byID[ev.ID] = len(kept)
		kept = append(kept, ev)
	}
	t.events = kept
	t.byID = byID
	t.pendingDelivered = map[string]struct{}{}
}

// Reconcile diffs the in-memory tracker against a fresh filesystem scan and
// converges to disk. In-memory events that aren't on disk are dropped (the
// dashboard reflects current reality), with one exception: events marked
// StatusFailed are preserved regardless of disk presence because failed
// drafts are moved to .invalid.md or to drafts/ and won't show up in the
// sent/ or inbox/ scan paths.
//
// For events present in both, the in-memory copy wins so live status
// (e.g., StatusSent before delivery completes) isn't clobbered by the
// less-granular disk-scan default of StatusDelivered.
//
// Reconcile also performs eviction and prunes orphaned pendingDelivered
// entries in the same pass.
func (t *Tracker) Reconcile(agentDirs map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoff := t.now().Add(-t.window)
	scanned := map[string]Event{}
	for agentName, dir := range agentDirs {
		events, err := ScanAgent(agentName, dir, cutoff)
		if err != nil {
			continue
		}
		for _, ev := range events {
			if _, dup := scanned[ev.ID]; dup {
				continue
			}
			scanned[ev.ID] = ev
		}
	}

	// Rebuild events: keep in-memory rows that survive eviction AND either
	// still exist on disk OR are marked Failed (whose source file is moved
	// away by mailship.invalidate). Add disk rows that aren't in memory.
	kept := make([]Event, 0, len(t.events))
	memIDs := map[string]struct{}{}
	for _, ev := range t.events {
		if ev.Timestamp.Before(cutoff) {
			continue
		}
		if _, ok := scanned[ev.ID]; !ok && ev.Status != StatusFailed {
			continue // phantom — present in memory but not on disk
		}
		kept = append(kept, ev)
		memIDs[ev.ID] = struct{}{}
	}
	for id, ev := range scanned {
		if _, ok := memIDs[id]; ok {
			continue
		}
		kept = append(kept, ev)
	}

	// Re-sort and rebuild byID.
	sortByTimestamp(kept)
	t.events = kept
	t.byID = make(map[string]int, len(kept))
	for i, ev := range kept {
		t.byID[ev.ID] = i
	}

	// Drop stale pendingDelivered entries: any ID we've now ingested from
	// disk, or any ID outside the window, is no longer waiting on a Sent.
	for id := range t.pendingDelivered {
		if _, ok := t.byID[id]; ok {
			delete(t.pendingDelivered, id)
		}
	}
}

func sortByTimestamp(events []Event) {
	// Insertion sort is fine — events are typically <100 and mostly sorted.
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j].Timestamp.Before(events[j-1].Timestamp); j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
}

// upsertLocked inserts or updates an event keeping the slice sorted by
// timestamp ascending. Caller must hold mu.
func (t *Tracker) upsertLocked(ev Event) {
	if idx, ok := t.byID[ev.ID]; ok {
		// In-place update preserves position (ID and timestamp are stable).
		t.events[idx] = ev
		return
	}
	// Insert via sort: find first index whose timestamp is after ev.Timestamp.
	idx := sort.Search(len(t.events), func(i int) bool {
		return t.events[i].Timestamp.After(ev.Timestamp)
	})
	t.events = append(t.events, Event{})
	copy(t.events[idx+1:], t.events[idx:])
	t.events[idx] = ev
	// Rebuild byID indices for everything from idx onward.
	for i := idx; i < len(t.events); i++ {
		t.byID[t.events[i].ID] = i
	}
}

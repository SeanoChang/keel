package comms

import (
	"sync/atomic"
	"testing"
	"time"
)

func mustParse(t *testing.T, ts string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("parse ts: %v", err)
	}
	return v
}

func newTracker(t *testing.T, now string) (*Tracker, *int32) {
	t.Helper()
	cur := mustParse(t, now)
	var notifies int32
	tr := New(24*time.Hour, func() { atomic.AddInt32(&notifies, 1) })
	tr.now = func() time.Time { return cur }
	return tr, &notifies
}

func TestTracker_SentThenDelivered(t *testing.T) {
	tr, notifies := newTracker(t, "2026-05-22T12:00:00Z")

	tr.RecordSent(Event{
		ID:        "id1",
		Kind:      KindMessage,
		From:      "noah",
		To:        "ezra",
		Subject:   "ping",
		Status:    StatusSent,
		Timestamp: mustParse(t, "2026-05-22T11:00:00Z"),
	})
	if got := atomic.LoadInt32(notifies); got != 1 {
		t.Fatalf("notify count = %d, want 1", got)
	}
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Status != StatusSent {
		t.Fatalf("snapshot after sent: %+v", snap)
	}

	tr.RecordDelivered("id1")
	snap = tr.Snapshot()
	if len(snap) != 1 || snap[0].Status != StatusDelivered {
		t.Fatalf("snapshot after delivered: %+v", snap)
	}
}

func TestTracker_DeliveredBeforeSent(t *testing.T) {
	// Watcher race: inbox event fires before outbox event. RecordDelivered on
	// an unknown ID must not crash and must not create an entry — the sent/
	// scan or outbox event will arrive shortly.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	tr.RecordDelivered("never-seen")
	if got := len(tr.Snapshot()); got != 0 {
		t.Fatalf("snapshot has %d, want 0", got)
	}
}

func TestTracker_Evict(t *testing.T) {
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	old := mustParse(t, "2026-05-21T11:00:00Z") // 25h ago
	fresh := mustParse(t, "2026-05-22T11:30:00Z")
	tr.RecordSent(Event{ID: "old", Kind: KindMessage, From: "a", To: "b", Timestamp: old, Status: StatusSent})
	tr.RecordSent(Event{ID: "new", Kind: KindMessage, From: "a", To: "b", Timestamp: fresh, Status: StatusSent})

	tr.Evict()
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].ID != "new" {
		t.Fatalf("evict result: %+v", snap)
	}
}

func TestTracker_SortedByTimestamp(t *testing.T) {
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	tr.RecordSent(Event{ID: "b", Kind: KindMessage, Timestamp: mustParse(t, "2026-05-22T11:30:00Z"), Status: StatusSent})
	tr.RecordSent(Event{ID: "a", Kind: KindMessage, Timestamp: mustParse(t, "2026-05-22T11:00:00Z"), Status: StatusSent})
	tr.RecordSent(Event{ID: "c", Kind: KindMessage, Timestamp: mustParse(t, "2026-05-22T11:45:00Z"), Status: StatusSent})
	snap := tr.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len = %d", len(snap))
	}
	if snap[0].ID != "a" || snap[1].ID != "b" || snap[2].ID != "c" {
		t.Fatalf("order = %s,%s,%s; want a,b,c", snap[0].ID, snap[1].ID, snap[2].ID)
	}
}

func TestTracker_SnapshotFiltersWindow(t *testing.T) {
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	// Insert without Evict — Snapshot should still hide stale rows.
	tr.events = []Event{
		{ID: "old", Timestamp: mustParse(t, "2026-05-21T11:00:00Z"), Status: StatusSent},
		{ID: "new", Timestamp: mustParse(t, "2026-05-22T11:00:00Z"), Status: StatusSent},
	}
	tr.byID = map[string]int{"old": 0, "new": 1}
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].ID != "new" {
		t.Fatalf("snapshot filter: %+v", snap)
	}
}

func TestTracker_RecordDelegationUpsert(t *testing.T) {
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	original := mustParse(t, "2026-05-22T11:00:00Z")
	tr.RecordDelegation(Event{
		ID: "deleg-abc", Kind: KindDelegation, From: "noah", To: "ezra",
		Subject: "review", Status: StatusPending, Timestamp: original,
		DelegationID: "abc", SubTasksDone: 0, SubTasksTotal: 3,
	})
	// Upsert with a different (later) timestamp — should keep ORIGINAL so
	// the row stays in chronological position.
	later := mustParse(t, "2026-05-22T11:45:00Z")
	tr.RecordDelegation(Event{
		ID: "deleg-abc", Kind: KindDelegation, From: "noah", To: "ezra",
		Subject: "review", Status: StatusPending, Timestamp: later,
		DelegationID: "abc", SubTasksDone: 2, SubTasksTotal: 3,
	})
	snap := tr.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected upsert, got %d rows", len(snap))
	}
	if snap[0].SubTasksDone != 2 {
		t.Fatalf("expected updated done=2, got %d", snap[0].SubTasksDone)
	}
	if !snap[0].Timestamp.Equal(original) {
		t.Fatalf("expected timestamp preserved (%v), got %v", original, snap[0].Timestamp)
	}
}

func TestTracker_DeliveredBeforeSent_Promotes(t *testing.T) {
	// cubit send writes the recipient's inbox BEFORE returning to the sender's
	// outbox watcher, so RecordDelivered can race ahead of RecordSent. The
	// late RecordSent must promote to Delivered, not fall back to Sent.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	tr.RecordDelivered("racy")
	tr.RecordSent(Event{
		ID: "racy", Kind: KindMessage, From: "noah", To: "ezra",
		Subject: "race", Status: StatusSent,
		Timestamp: mustParse(t, "2026-05-22T11:00:00Z"),
	})
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Status != StatusDelivered {
		t.Fatalf("expected promotion to delivered, got %+v", snap)
	}
}

func TestTracker_RecordFailed_PromotesExisting(t *testing.T) {
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	ts := mustParse(t, "2026-05-22T11:00:00Z")
	tr.RecordSent(Event{ID: "fid", Kind: KindMessage, From: "noah", To: "ezra", Timestamp: ts, Status: StatusSent})
	tr.RecordFailed(Event{
		ID:        "fid",
		Kind:      KindMessage,
		From:      "noah",
		To:        "ezra",
		Status:    StatusFailed,
		Failure:   "missing 'to' field",
		Timestamp: ts,
	})
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Status != StatusFailed || snap[0].Failure != "missing 'to' field" {
		t.Fatalf("failed not promoted: %+v", snap)
	}
}

func TestTracker_RecordFailed_InsertsUnknownID(t *testing.T) {
	// The dominant failure path is validation/transport failure with NO prior
	// RecordSent (onShipped only fires on Sent=true). RecordFailed must insert
	// a row in that case — otherwise the dashboard silently hides bad drafts.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	ts := mustParse(t, "2026-05-22T11:00:00Z")
	tr.RecordFailed(Event{
		ID:        "never-seen",
		Kind:      KindMessage,
		From:      "noah",
		To:        "ezra",
		Subject:   "bad draft",
		Status:    StatusFailed,
		Failure:   "missing 'to' field",
		Timestamp: ts,
	})
	snap := tr.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 inserted row, got %d", len(snap))
	}
	if snap[0].Status != StatusFailed || snap[0].Failure != "missing 'to' field" || snap[0].From != "noah" {
		t.Fatalf("inserted row wrong: %+v", snap[0])
	}
}

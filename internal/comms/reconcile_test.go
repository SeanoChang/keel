package comms

import (
	"testing"
	"time"
)

func TestReconcile_AddsMissingFromDisk(t *testing.T) {
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	agentDir := t.TempDir()
	writeMail(t, agentDir, "sent", "2026-05-22T11-00-00-noah-disk-only.md",
		map[string]string{"from": "noah", "to": "ezra", "subject": "disk-only"}, "")

	tr.Reconcile(map[string]string{"noah": agentDir})
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Subject != "disk-only" {
		t.Fatalf("expected disk-only event added, got: %+v", snap)
	}
}

func TestReconcile_PreservesInMemoryStatus(t *testing.T) {
	// Memory has a "sent" event for ID X; disk shows the same file (so scan
	// would say "delivered"). Reconcile should preserve the in-memory status
	// because it's the more up-to-date signal from a live watcher.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	agentDir := t.TempDir()
	stem := "2026-05-22T11-00-00-noah-ping"
	writeMail(t, agentDir, "sent", stem+".md",
		map[string]string{"from": "noah", "to": "ezra", "subject": "ping"}, "")

	tr.RecordSent(Event{
		ID: stem, Kind: KindMessage, From: "noah", To: "ezra",
		Subject: "ping", Status: StatusSent,
		Timestamp: mustParse(t, "2026-05-22T11:00:00Z"),
	})
	tr.Reconcile(map[string]string{"noah": agentDir})
	snap := tr.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("got %d events, want 1", len(snap))
	}
	if snap[0].Status != StatusSent {
		t.Fatalf("status overwritten by reconcile: %v", snap[0].Status)
	}
}

func TestReconcile_DropsOutOfWindow(t *testing.T) {
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	// Memory has an event that's older than 24h.
	tr.events = []Event{{
		ID:        "old",
		Kind:      KindMessage,
		From:      "a", To: "b",
		Status:    StatusDelivered,
		Timestamp: mustParse(t, "2026-05-20T11:00:00Z"),
	}}
	tr.byID = map[string]int{"old": 0}

	agentDir := t.TempDir() // no mail
	tr.Reconcile(map[string]string{"noah": agentDir})

	// Reconcile should evict stale events while it has the lock.
	if got := len(tr.events); got != 0 {
		t.Fatalf("expected eviction, got %d events", got)
	}
}

func TestReconcile_MultipleAgents(t *testing.T) {
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	a := t.TempDir()
	b := t.TempDir()
	writeMail(t, a, "sent", "2026-05-22T11-00-00-noah-a.md",
		map[string]string{"from": "noah", "to": "ezra", "subject": "from-a"}, "")
	writeMail(t, b, "sent", "2026-05-22T11-01-00-ezra-b.md",
		map[string]string{"from": "ezra", "to": "noah", "subject": "from-b"}, "")

	tr.Reconcile(map[string]string{"noah": a, "ezra": b})
	snap := tr.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("got %d, want 2", len(snap))
	}
}

func TestReconcile_DedupAcrossAgents(t *testing.T) {
	// noah's sent/ and ezra's inbox/ both record the same message. Reconcile
	// must dedup by ID so the dashboard shows one row, not two.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	a := t.TempDir()
	b := t.TempDir()
	fname := "2026-05-22T11-00-00-noah-cross.md"
	writeMail(t, a, "sent", fname,
		map[string]string{"from": "noah", "to": "ezra", "subject": "cross"}, "")
	writeMail(t, b, "inbox/all", fname,
		map[string]string{"from": "noah", "to": "ezra", "subject": "cross"}, "")

	tr.Reconcile(map[string]string{"noah": a, "ezra": b})
	snap := tr.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("got %d, want 1 (dedup); snap=%+v", len(snap), snap)
	}
}

func TestReconcile_RemovesPhantoms(t *testing.T) {
	// Memory has an event with no on-disk counterpart. Reconcile should drop
	// it so the dashboard doesn't show phantom rows after manual deletion.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	tr.events = []Event{{
		ID:        "phantom",
		Kind:      KindMessage,
		From:      "noah", To: "ezra",
		Status:    StatusDelivered,
		Timestamp: mustParse(t, "2026-05-22T11:00:00Z"),
	}}
	tr.byID = map[string]int{"phantom": 0}
	agentDir := t.TempDir() // empty mailbox

	tr.Reconcile(map[string]string{"noah": agentDir})
	if got := len(tr.events); got != 0 {
		t.Fatalf("phantom not removed; got %d events", got)
	}
}

func TestReconcile_PreservesFailedRowsWithoutDisk(t *testing.T) {
	// A Failed event has no on-disk counterpart (mailship moves the file to
	// .invalid.md or leaves it in drafts/). Reconcile must NOT drop it.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	tr.events = []Event{{
		ID:        "failed-1",
		Kind:      KindMessage,
		From:      "noah", To: "ezra",
		Status:    StatusFailed,
		Failure:   "missing to:",
		Timestamp: mustParse(t, "2026-05-22T11:00:00Z"),
	}}
	tr.byID = map[string]int{"failed-1": 0}
	agentDir := t.TempDir() // empty mailbox — disk shows nothing

	tr.Reconcile(map[string]string{"noah": agentDir})

	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Status != StatusFailed {
		t.Fatalf("expected failed row preserved, got %+v", snap)
	}
}

func TestEvict_ClearsPendingDelivered(t *testing.T) {
	// pendingDelivered entries that never paired up with a Sent shouldn't
	// leak across hourly maintenance passes.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	tr.RecordDelivered("orphan-1")
	tr.RecordDelivered("orphan-2")
	if len(tr.pendingDelivered) != 2 {
		t.Fatalf("setup: expected 2 pending, got %d", len(tr.pendingDelivered))
	}
	tr.Evict()
	if len(tr.pendingDelivered) != 0 {
		t.Fatalf("Evict must clear pendingDelivered, got %d", len(tr.pendingDelivered))
	}
}

func TestReconcile_UsesWindowAsSince(t *testing.T) {
	// Disk has events both inside and outside the 24h window. Reconcile must
	// only ingest in-window ones.
	tr, _ := newTracker(t, "2026-05-22T12:00:00Z")
	agentDir := t.TempDir()
	writeMail(t, agentDir, "sent", "2026-05-20T11-00-00-noah-old.md",
		map[string]string{"from": "noah", "to": "ezra", "subject": "old"}, "")
	writeMail(t, agentDir, "sent", "2026-05-22T11-00-00-noah-new.md",
		map[string]string{"from": "noah", "to": "ezra", "subject": "new"}, "")

	tr.Reconcile(map[string]string{"noah": agentDir})
	snap := tr.Snapshot()
	if len(snap) != 1 || snap[0].Subject != "new" {
		t.Fatalf("window filter failed: %+v", snap)
	}
	_ = time.Now // keep import
}

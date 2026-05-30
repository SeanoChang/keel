package comms

import (
	"strings"
	"testing"
	"time"
)

func sampleEvents(t *testing.T) []Event {
	t.Helper()
	return []Event{
		{
			ID: "1", Kind: KindMessage, From: "noah", To: "ezra",
			Subject: "build-status", Category: "all", Status: StatusDelivered,
			Timestamp: mustParse(t, "2026-05-22T09:14:00Z"),
		},
		{
			ID: "2", Kind: KindMessage, From: "ezra", To: "noah",
			Subject: "ack", Category: "all", Status: StatusDelivered,
			Timestamp: mustParse(t, "2026-05-22T09:22:00Z"),
		},
		{
			ID: "3", Kind: KindMessage, From: "noah", To: "eli",
			Subject: "review-request", Category: "priority", Status: StatusSent,
			Timestamp: mustParse(t, "2026-05-22T10:01:00Z"),
		},
		{
			ID: "4", Kind: KindDelegation, From: "noah", To: "team",
			Subject: "review-done", Status: StatusComplete,
			DelegationID: "abc", SubTasksDone: 3, SubTasksTotal: 3,
			Timestamp: mustParse(t, "2026-05-22T11:30:00Z"),
		},
	}
}

func TestRender_GlobalContainsCoreColumns(t *testing.T) {
	now := mustParse(t, "2026-05-22T14:32:00Z")
	out := Render(sampleEvents(t), Scope{Title: "Agent Comms"}, now)
	for _, want := range []string{
		"```",
		"Agent Comms",
		"last 24h",
		"updated",
		"time", "from", "to", "subject", "status",
		"noah", "ezra", "eli",
		"build-status", "ack", "review-request",
		"delivered", "sent", "complete",
		"[deleg]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("global render missing %q in:\n%s", want, out)
		}
	}
}

func TestRender_PerAgent_FiltersAndDirection(t *testing.T) {
	now := mustParse(t, "2026-05-22T14:32:00Z")
	out := Render(sampleEvents(t), Scope{Title: "noah comms", AgentFilter: "noah"}, now)

	// noah is in all 4 events — all should appear. The 2nd event (ezra→noah) has dir ←.
	if !strings.Contains(out, "→") || !strings.Contains(out, "←") {
		t.Errorf("expected both arrows in per-agent render:\n%s", out)
	}
	// Per-agent table should NOT have a separate "from" column header — direction
	// replaces it.
	if strings.Contains(out, "from   to") {
		t.Errorf("per-agent render should not have from/to columns:\n%s", out)
	}
	// "other" column should appear.
	if !strings.Contains(out, "other") {
		t.Errorf("per-agent render missing 'other' column:\n%s", out)
	}
	// Subject text from the sample should show.
	if !strings.Contains(out, "build-status") {
		t.Errorf("per-agent render missing subject:\n%s", out)
	}
}

func TestRender_PerAgent_ExcludesUnrelated(t *testing.T) {
	now := mustParse(t, "2026-05-22T14:32:00Z")
	events := []Event{
		{ID: "1", Kind: KindMessage, From: "noah", To: "ezra", Subject: "hi", Status: StatusSent,
			Timestamp: mustParse(t, "2026-05-22T09:00:00Z")},
		{ID: "2", Kind: KindMessage, From: "alice", To: "bob", Subject: "irrelevant", Status: StatusSent,
			Timestamp: mustParse(t, "2026-05-22T09:01:00Z")},
	}
	out := Render(events, Scope{Title: "noah comms", AgentFilter: "noah"}, now)
	if strings.Contains(out, "irrelevant") {
		t.Errorf("noah-filter should drop alice→bob row:\n%s", out)
	}
	if !strings.Contains(out, "hi") {
		t.Errorf("noah-filter should keep noah→ezra row:\n%s", out)
	}
}

func TestRender_EmptyState(t *testing.T) {
	now := mustParse(t, "2026-05-22T14:32:00Z")
	out := Render(nil, Scope{Title: "Agent Comms"}, now)
	if !strings.Contains(out, "no traffic") {
		t.Errorf("empty render should say 'no traffic':\n%s", out)
	}
	if !strings.Contains(out, "Agent Comms") {
		t.Errorf("empty render still shows title:\n%s", out)
	}
}

func TestRender_OverflowTruncatesOldest(t *testing.T) {
	now := mustParse(t, "2026-05-22T23:59:00Z")
	// Generate enough rows to exceed the ~1900 char body cap.
	events := make([]Event, 0, 80)
	for i := 0; i < 80; i++ {
		ts := mustParse(t, "2026-05-22T00:00:00Z").Add(time.Duration(i) * time.Minute)
		events = append(events, Event{
			ID:        "id" + string(rune('a'+(i%26))) + string(rune('0'+(i/26))),
			Kind:      KindMessage,
			From:      "agentlong",
			To:        "otherlong",
			Subject:   "long-subject-text-here",
			Status:    StatusDelivered,
			Timestamp: ts,
		})
	}
	out := Render(events, Scope{Title: "Agent Comms"}, now)
	if len(out) > 2000 {
		t.Errorf("render exceeded Discord limit: %d chars", len(out))
	}
	if !strings.Contains(out, "earlier omitted") {
		t.Errorf("expected truncation footer, got:\n%s", out)
	}
}

func TestRender_DelegationProgress(t *testing.T) {
	now := mustParse(t, "2026-05-22T14:32:00Z")
	events := []Event{
		{
			ID: "d1", Kind: KindDelegation, From: "noah", To: "team",
			Subject: "fanout", Status: StatusPending,
			DelegationID: "xyz", SubTasksDone: 1, SubTasksTotal: 4,
			Timestamp: mustParse(t, "2026-05-22T10:00:00Z"),
		},
	}
	out := Render(events, Scope{Title: "Agent Comms"}, now)
	if !strings.Contains(out, "1/4") {
		t.Errorf("delegation progress missing (want 1/4):\n%s", out)
	}
	if !strings.Contains(out, "pending") {
		t.Errorf("pending status missing:\n%s", out)
	}
}

func TestRender_FailedShowsReason(t *testing.T) {
	now := mustParse(t, "2026-05-22T14:32:00Z")
	events := []Event{
		{ID: "f1", Kind: KindMessage, From: "noah", To: "ezra", Subject: "boom",
			Status: StatusFailed, Failure: "missing to-field",
			Timestamp: mustParse(t, "2026-05-22T10:00:00Z")},
	}
	out := Render(events, Scope{Title: "Agent Comms"}, now)
	if !strings.Contains(out, "failed") {
		t.Errorf("expected failed status in render:\n%s", out)
	}
}

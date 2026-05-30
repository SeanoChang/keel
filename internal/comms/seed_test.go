package comms

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeMail constructs a canonical mail file at <agentDir>/mailbox/<rel>/<filename>.
func writeMail(t *testing.T, agentDir, rel, filename string, frontmatter map[string]string, body string) string {
	t.Helper()
	dir := filepath.Join(agentDir, "mailbox", rel)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var sb []byte
	sb = append(sb, "---\n"...)
	for k, v := range frontmatter {
		sb = append(sb, (k + ": " + v + "\n")...)
	}
	sb = append(sb, "---\n\n"...)
	sb = append(sb, body...)
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, sb, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func writeDelegation(t *testing.T, agentDir, state, delID, contents string) {
	t.Helper()
	dir := filepath.Join(agentDir, "mailbox", "delegations", state, delID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "delegation.json"), []byte(contents), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestScanAgent_BasicSent(t *testing.T) {
	agentDir := t.TempDir()
	writeMail(t, agentDir, "sent", "2026-05-22T11-00-00-noah-build-status.md",
		map[string]string{"from": "noah", "to": "ezra", "subject": `"build status"`, "type": "notification"},
		"hello\n")

	since := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	events, err := ScanAgent("noah", agentDir, since)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.From != "noah" || ev.To != "ezra" {
		t.Errorf("from/to = %q/%q", ev.From, ev.To)
	}
	if ev.Subject != "build status" {
		t.Errorf("subject = %q (want unquoted)", ev.Subject)
	}
	if ev.Kind != KindMessage {
		t.Errorf("kind = %v", ev.Kind)
	}
	if ev.Status != StatusDelivered {
		t.Errorf("sent/ entries default to delivered; got %v", ev.Status)
	}
	if !ev.Timestamp.Equal(time.Date(2026, 5, 22, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("ts = %v", ev.Timestamp)
	}
}

func TestScanAgent_InboxIncluded(t *testing.T) {
	agentDir := t.TempDir()
	writeMail(t, agentDir, "inbox/all", "2026-05-22T11-00-00-alice-greeting.md",
		map[string]string{"from": "alice", "to": "noah", "subject": "hello", "type": "notification"},
		"hi")

	events, err := ScanAgent("noah", agentDir, time.Time{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.From != "alice" || ev.To != "noah" {
		t.Errorf("from/to = %q/%q", ev.From, ev.To)
	}
	if ev.Category != "all" {
		t.Errorf("category = %q", ev.Category)
	}
}

func TestScanAgent_DedupsSentAndInbox(t *testing.T) {
	// If both sender's sent/ and recipient's inbox/ are scanned and produce
	// the same ID, only one event survives.
	agentDir := t.TempDir()
	fname := "2026-05-22T11-00-00-noah-ping.md"
	writeMail(t, agentDir, "sent", fname,
		map[string]string{"from": "noah", "to": "ezra", "subject": "ping"}, "")
	writeMail(t, agentDir, "inbox/all", fname,
		map[string]string{"from": "noah", "to": "ezra", "subject": "ping"}, "")

	events, err := ScanAgent("noah", agentDir, time.Time{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 (dedup); events=%+v", len(events), events)
	}
}

func TestScanAgent_SinceFilter(t *testing.T) {
	agentDir := t.TempDir()
	writeMail(t, agentDir, "sent", "2026-05-20T11-00-00-noah-old.md",
		map[string]string{"from": "noah", "to": "ezra", "subject": "old"}, "")
	writeMail(t, agentDir, "sent", "2026-05-22T11-00-00-noah-new.md",
		map[string]string{"from": "noah", "to": "ezra", "subject": "new"}, "")

	since := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	events, err := ScanAgent("noah", agentDir, since)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(events) != 1 || events[0].Subject != "new" {
		t.Fatalf("filter failed: %+v", events)
	}
}

func TestScanAgent_DelegationActive(t *testing.T) {
	agentDir := t.TempDir()
	writeDelegation(t, agentDir, "active", "abc", `{
		"id": "abc",
		"created": "2026-05-22T10:00:00Z",
		"owner": "noah",
		"goal_context": "review the PR",
		"status": "in_progress",
		"sub_tasks": [
			{"to": "ezra", "task": "frontend", "status": "complete"},
			{"to": "eli",  "task": "backend",  "status": "pending"}
		]
	}`)

	events, err := ScanAgent("noah", agentDir, time.Time{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Kind != KindDelegation {
		t.Errorf("kind = %v", ev.Kind)
	}
	if ev.SubTasksDone != 1 || ev.SubTasksTotal != 2 {
		t.Errorf("progress = %d/%d", ev.SubTasksDone, ev.SubTasksTotal)
	}
	if ev.Status != StatusPending {
		t.Errorf("status = %v (want pending)", ev.Status)
	}
	if ev.From != "noah" {
		t.Errorf("from = %q", ev.From)
	}
	if ev.DelegationID != "abc" {
		t.Errorf("delID = %q", ev.DelegationID)
	}
}

func TestScanAgent_DelegationDone(t *testing.T) {
	agentDir := t.TempDir()
	writeDelegation(t, agentDir, "done", "xyz", `{
		"id": "xyz",
		"created": "2026-05-22T10:00:00Z",
		"owner": "noah",
		"status": "complete",
		"sub_tasks": [
			{"to": "ezra", "task": "x", "status": "complete"},
			{"to": "eli",  "task": "y", "status": "complete"}
		]
	}`)

	events, err := ScanAgent("noah", agentDir, time.Time{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d, want 1", len(events))
	}
	if events[0].Status != StatusComplete {
		t.Errorf("status = %v, want complete", events[0].Status)
	}
}

func TestScanAgent_SkipsMissingDir(t *testing.T) {
	dir := t.TempDir() // no mailbox/ inside
	events, err := ScanAgent("ghost", dir, time.Time{})
	if err != nil {
		t.Fatalf("missing mailbox should not error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events, want 0", len(events))
	}
}

func TestScanAgent_DirFormDraft(t *testing.T) {
	agentDir := t.TempDir()
	// dir-form: <ts>-<from>-<slug>/mail.md
	dir := filepath.Join(agentDir, "mailbox", "sent", "2026-05-22T11-00-00-noah-with-attach")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nfrom: noah\nto: ezra\nsubject: \"with attach\"\n---\n\nhi"
	if err := os.WriteFile(filepath.Join(dir, "mail.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	events, err := ScanAgent("noah", agentDir, time.Time{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(events) != 1 || events[0].Subject != "with attach" {
		t.Fatalf("dir-form parse: %+v", events)
	}
}

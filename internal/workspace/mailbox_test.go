package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureMailbox(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureMailbox(dir); err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{
		"mailbox/inbox/important",
		"mailbox/inbox/priority",
		"mailbox/inbox/all",
		"mailbox/starred",
		"mailbox/drafts",
		"mailbox/sent",
		"mailbox/read",
	} {
		path := filepath.Join(dir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}

	if err := EnsureMailbox(dir); err != nil {
		t.Errorf("second call should be idempotent: %v", err)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Found a regression in auth module", "found-a-regression-in-auth-module"},
		{"Review API Endpoints!", "review-api-endpoints"},
		{"phase-1 complete", "phase-1-complete"},
		{"  extra   spaces  ", "extra-spaces"},
		{"UPPERCASE", "uppercase"},
	}
	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWriteMailboxMessage(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureMailbox(dir); err != nil {
		t.Fatal(err)
	}

	err := WriteMailboxMessage(dir, "sean", "Review API endpoints", "priority", "notification", "Please review the new endpoints.")
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "mailbox/inbox/priority"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in inbox/priority, got %d", len(entries))
	}

	name := entries[0].Name()
	if !strings.HasSuffix(name, "-sean-review-api-endpoints.md") {
		t.Errorf("unexpected filename: %s", name)
	}

	content, _ := os.ReadFile(filepath.Join(dir, "mailbox/inbox/priority", name))
	s := string(content)
	if !strings.Contains(s, "from: sean") {
		t.Error("missing 'from' in frontmatter")
	}
	if !strings.Contains(s, "to: ") {
		t.Error("missing 'to' in frontmatter")
	}
	if !strings.Contains(s, `subject: "Review API endpoints"`) {
		t.Error("missing 'subject' in frontmatter")
	}
	if !strings.Contains(s, "category: priority") {
		t.Error("missing 'category' in frontmatter")
	}
	if !strings.Contains(s, "type: notification") {
		t.Error("missing 'type' in frontmatter")
	}
	if !strings.Contains(s, "timestamp:") {
		t.Error("missing 'timestamp' in frontmatter")
	}
	if !strings.Contains(s, "Please review the new endpoints.") {
		t.Error("missing body content")
	}
}

func TestWriteMailboxMessageDefaultCategory(t *testing.T) {
	dir := t.TempDir()
	EnsureMailbox(dir)

	err := WriteMailboxMessage(dir, "alice", "Hello", "", "", "Hi there")
	if err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(filepath.Join(dir, "mailbox/inbox/all"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in inbox/all, got %d", len(entries))
	}
}

func TestHasMailboxMessages(t *testing.T) {
	dir := t.TempDir()
	EnsureMailbox(dir)

	if HasMailboxMessages(dir) {
		t.Error("expected false for empty mailbox")
	}

	WriteMailboxMessage(dir, "sean", "test", "all", "", "hello")
	if !HasMailboxMessages(dir) {
		t.Error("expected true after adding message")
	}
}

func TestHasMailboxMessagesNoMailbox(t *testing.T) {
	dir := t.TempDir()
	if HasMailboxMessages(dir) {
		t.Error("expected false when mailbox doesn't exist")
	}
}

func TestLogMailboxEvent(t *testing.T) {
	dir := t.TempDir()
	EnsureMailbox(dir)

	err := LogMailboxEvent(dir, "alice", "request", "Help with auth module")
	if err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, "mailbox", "system.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	s := string(data)
	if !strings.Contains(s, "received request from alice: Help with auth module") {
		t.Errorf("unexpected log content: %q", s)
	}

	LogMailboxEvent(dir, "sean", "notification", "Check this out")
	data, _ = os.ReadFile(logPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 log lines, got %d", len(lines))
	}
}

func TestEnsureAskDirs(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureAskDirs(dir); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"asks/pending", "asks/done"} {
		path := filepath.Join(dir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}
	if err := EnsureAskDirs(dir); err != nil {
		t.Errorf("second call should be idempotent: %v", err)
	}
}

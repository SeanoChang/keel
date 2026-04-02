package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Mailbox subdirectory layout.
var mailboxDirs = []string{
	"mailbox/inbox/important",
	"mailbox/inbox/priority",
	"mailbox/inbox/all",
	"mailbox/starred",
	"mailbox/drafts",
	"mailbox/sent",
	"mailbox/read",
}

// EnsureMailbox creates the full mailbox directory tree if any part is missing.
func EnsureMailbox(dir string) error {
	for _, sub := range mailboxDirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			return fmt.Errorf("create %s: %w", sub, err)
		}
	}
	return nil
}

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a string to a URL-safe slug.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// WriteMailboxMessage writes a message file to targetDir's mailbox inbox.
// Category defaults to "all" if empty. MsgType defaults to "notification" if empty.
func WriteMailboxMessage(targetDir, from, subject, category, msgType, body string) error {
	if category == "" {
		category = "all"
	}
	if msgType == "" {
		msgType = "notification"
	}

	now := time.Now().UTC()
	ts := now.Format("2006-01-02T15-04-05")
	slug := slugify(subject)
	filename := fmt.Sprintf("%s-%s-%s.md", ts, from, slug)

	inboxDir := filepath.Join(targetDir, "mailbox", "inbox", category)
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return fmt.Errorf("ensure inbox dir: %w", err)
	}

	to := filepath.Base(targetDir)
	content := fmt.Sprintf("---\nfrom: %s\nto: %s\ntimestamp: %s\ncategory: %s\nsubject: \"%s\"\ntype: %s\n---\n\n%s\n",
		from, to, now.Format(time.RFC3339), category, strings.ReplaceAll(subject, "\"", "'"), msgType, body)

	return os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0644)
}

// HasMailboxMessages returns true if any .md files exist in the inbox subdirectories.
func HasMailboxMessages(dir string) bool {
	for _, sub := range []string{"important", "priority", "all"} {
		inboxDir := filepath.Join(dir, "mailbox", "inbox", sub)
		entries, err := os.ReadDir(inboxDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				return true
			}
		}
	}
	return false
}

// LogMailboxEvent appends a line to the mailbox system log.
func LogMailboxEvent(dir, from, msgType, subject string) error {
	logPath := filepath.Join(dir, "mailbox", "system.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	ts := time.Now().UTC().Format(time.RFC3339)
	_, err = fmt.Fprintf(f, "[%s] received %s from %s: %s\n", ts, msgType, from, subject)
	return err
}

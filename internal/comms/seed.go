package comms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SeanoChang/keel/internal/delegation"
)

// ScanAgent reads the filesystem mailbox of one agent and returns all comms
// events at-or-after `since`. A zero `since` means no time filter. Events are
// deduped by ID: if both sender's `sent/` and recipient's `inbox/` produce
// the same filename, only the first wins.
func ScanAgent(agentName, agentDir string, since time.Time) ([]Event, error) {
	mailboxDir := filepath.Join(agentDir, "mailbox")
	if _, err := os.Stat(mailboxDir); os.IsNotExist(err) {
		return nil, nil
	}

	seen := map[string]struct{}{}
	out := []Event{}

	// sent/ — outbound messages where this agent is the sender.
	if events, err := scanMailDir(filepath.Join(mailboxDir, "sent"), "", since, seen); err != nil {
		return nil, err
	} else {
		out = append(out, events...)
	}

	// inbox/<category>/ — inbound messages.
	for _, cat := range []string{"important", "priority", "all"} {
		events, err := scanMailDir(filepath.Join(mailboxDir, "inbox", cat), cat, since, seen)
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}

	// delegations/{active,done}/<delID>/delegation.json
	for _, state := range []string{"active", "done"} {
		events, err := scanDelegationDir(filepath.Join(mailboxDir, "delegations", state), agentName, since)
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}

	return out, nil
}

func scanMailDir(dir, category string, since time.Time, seen map[string]struct{}) ([]Event, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("readdir %s: %w", dir, err)
	}
	var out []Event
	for _, e := range entries {
		ev, ok := parseMailEntry(dir, e, category)
		if !ok {
			continue
		}
		if !since.IsZero() && ev.Timestamp.Before(since) {
			continue
		}
		if _, dup := seen[ev.ID]; dup {
			continue
		}
		seen[ev.ID] = struct{}{}
		out = append(out, ev)
	}
	return out, nil
}

func parseMailEntry(dir string, e os.DirEntry, category string) (Event, bool) {
	name := e.Name()

	var (
		filePath string
		stem     string
	)
	switch {
	case e.IsDir():
		filePath = filepath.Join(dir, name, "mail.md")
		if _, err := os.Stat(filePath); err != nil {
			return Event{}, false
		}
		stem = name
	case strings.HasSuffix(name, ".md"):
		filePath = filepath.Join(dir, name)
		stem = strings.TrimSuffix(name, ".md")
	default:
		return Event{}, false
	}

	ts, from, _, ok := parseFilenameStem(stem)
	if !ok {
		return Event{}, false
	}

	fm := readFrontmatter(filePath)
	to := stripQuotes(fm["to"])
	subject := stripQuotes(fm["subject"])
	if fmFrom := stripQuotes(fm["from"]); fmFrom != "" {
		from = fmFrom
	}

	// Drop human/system traffic from the dashboard — agent↔agent only.
	if from == "" || to == "" || from == "human" || to == "human" {
		return Event{}, false
	}

	return Event{
		ID:        stem,
		Kind:      KindMessage,
		From:      from,
		To:        to,
		Subject:   subject,
		Category:  category,
		Status:    StatusDelivered,
		Timestamp: ts,
	}, true
}

// ParseFilenameStem is the exported variant used by the discord package to
// build placeholder events for failed drafts (where the file frontmatter
// may not be parseable). Returns (timestamp, from, slug, true) on success.
func ParseFilenameStem(stem string) (time.Time, string, string, bool) {
	return parseFilenameStem(stem)
}

// parseFilenameStem reads `2026-05-22T11-00-00-<from>-<slug>` and returns
// (timestamp, from, slug, true) on success.
func parseFilenameStem(stem string) (time.Time, string, string, bool) {
	// Need at least "YYYY-MM-DDTHH-MM-SS-x-y" → 19 + 1 + 1 + 1 + 1 = 23.
	if len(stem) < 21 {
		return time.Time{}, "", "", false
	}
	// Convert HH-MM-SS to HH:MM:SS in the timestamp half.
	tsRaw := stem[:19]
	if tsRaw[10] != 'T' || tsRaw[13] != '-' || tsRaw[16] != '-' {
		return time.Time{}, "", "", false
	}
	tsISO := tsRaw[:13] + ":" + tsRaw[14:16] + ":" + tsRaw[17:19] + "Z"
	ts, err := time.Parse(time.RFC3339, tsISO)
	if err != nil {
		return time.Time{}, "", "", false
	}
	if stem[19] != '-' {
		return time.Time{}, "", "", false
	}
	rest := stem[20:]
	idx := strings.IndexByte(rest, '-')
	if idx <= 0 {
		return ts, rest, "", true
	}
	return ts, rest[:idx], rest[idx+1:], true
}

func readFrontmatter(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return delegation.ParseFrontmatter(string(data))
}

func stripQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// delegationTracker mirrors the on-disk shape written by cubit. We define a
// local copy because the cubit-side type isn't exported.
type delegationTracker struct {
	ID       string `json:"id"`
	Created  string `json:"created"`
	Owner    string `json:"owner"`
	Status   string `json:"status"`
	SubTasks []struct {
		To     string `json:"to"`
		Task   string `json:"task"`
		Status string `json:"status"`
	} `json:"sub_tasks"`
}

func scanDelegationDir(dir, agentName string, since time.Time) ([]Event, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("readdir %s: %w", dir, err)
	}
	var out []Event
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		jsonPath := filepath.Join(dir, e.Name(), "delegation.json")
		data, err := os.ReadFile(jsonPath)
		if err != nil {
			continue
		}
		var d delegationTracker
		if err := json.Unmarshal(data, &d); err != nil {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, d.Created)
		if !since.IsZero() && ts.Before(since) {
			continue
		}
		done := 0
		for _, st := range d.SubTasks {
			if st.Status == "complete" {
				done++
			}
		}
		total := len(d.SubTasks)
		var status Status
		if done == total && total > 0 {
			status = StatusComplete
		} else {
			status = StatusPending
		}
		to := "team"
		if total == 1 {
			to = d.SubTasks[0].To
		}
		from := agentName
		if d.Owner != "" {
			from = d.Owner
		}
		subject := "delegation"
		out = append(out, Event{
			ID:            "deleg-" + d.ID,
			Kind:          KindDelegation,
			From:          from,
			To:            to,
			Subject:       subject,
			Status:        status,
			Timestamp:     ts,
			DelegationID:  d.ID,
			SubTasksDone:  done,
			SubTasksTotal: total,
		})
	}
	return out, nil
}

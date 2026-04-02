package discord

import (
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/SeanoChang/keel/internal/workspace"
)

// MailboxWatcher watches an agent's mailbox/inbox/ for new messages
// and calls onMessage when a new .md file is created.
type MailboxWatcher struct {
	agentName string
	dir       string
	onMessage func() // called when new message arrives
	stop      chan struct{}
	once      sync.Once
}

func NewMailboxWatcher(agentName, dir string, onMessage func()) *MailboxWatcher {
	return &MailboxWatcher{
		agentName: agentName,
		dir:       dir,
		onMessage: onMessage,
		stop:      make(chan struct{}),
	}
}

func (w *MailboxWatcher) Start() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[keel] %s: mailbox fsnotify error: %v", w.agentName, err)
		return
	}
	defer watcher.Close()

	// Watch inbox root and each category subdirectory.
	inboxDir := filepath.Join(w.dir, "mailbox", "inbox")
	for _, sub := range []string{"", "important", "priority", "all"} {
		path := filepath.Join(inboxDir, sub)
		if err := watcher.Add(path); err != nil {
			log.Printf("[keel] %s: mailbox watch error on %s: %v", w.agentName, path, err)
		}
	}

	for {
		select {
		case <-w.stop:
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) && strings.HasSuffix(event.Name, ".md") {
				name := filepath.Base(event.Name)
				log.Printf("[keel] %s: new mailbox message: %s", w.agentName, name)
				// Parse from/subject from canonical filename: <ts>-<from>-<slug>.md
				if from, slug, ok := parseMessageFilename(name); ok {
					if err := workspace.LogMailboxEvent(w.dir, from, "message", slug); err != nil {
						log.Printf("[keel] %s: mailbox log error: %v", w.agentName, err)
					}
				}
				w.onMessage()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[keel] %s: mailbox watcher error: %v", w.agentName, err)
		}
	}
}

func (w *MailboxWatcher) Stop() {
	w.once.Do(func() { close(w.stop) })
}

// parseMessageFilename extracts from and subject-slug from a canonical filename.
// Format: <timestamp>-<from>-<slug>.md → returns (from, slug, true).
func parseMessageFilename(name string) (string, string, bool) {
	name = strings.TrimSuffix(name, ".md")
	// Timestamp is fixed-width: 2006-01-02T15-04-05 (19 chars)
	if len(name) < 21 || name[19] != '-' {
		return "", "", false
	}
	rest := name[20:] // "<from>-<slug>"
	idx := strings.IndexByte(rest, '-')
	if idx <= 0 {
		return rest, "", true
	}
	return rest[:idx], rest[idx+1:], true
}

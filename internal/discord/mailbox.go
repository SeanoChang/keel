package discord

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/SeanoChang/keel/internal/delegation"
	"github.com/SeanoChang/keel/internal/workspace"
)

// MailboxWatcher watches an agent's mailbox/inbox/ for new messages
// and calls onMessage when a new message is created.
// Delegation-responses are intercepted and routed via the DelegationRouter.
type MailboxWatcher struct {
	agentName    string
	dir          string
	onMessage    func()                              // called for regular messages + always after routing
	onDelegation func(result *delegation.RouteResult) // called after delegation routing
	stop         chan struct{}
	once         sync.Once
}

func NewMailboxWatcher(agentName, dir string, onMessage func(), onDelegation func(*delegation.RouteResult)) *MailboxWatcher {
	return &MailboxWatcher{
		agentName:    agentName,
		dir:          dir,
		onMessage:    onMessage,
		onDelegation: onDelegation,
		stop:         make(chan struct{}),
	}
}

func (w *MailboxWatcher) Start() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[keel] %s: mailbox fsnotify error: %v", w.agentName, err)
		return
	}
	defer watcher.Close()

	router := delegation.NewRouter(w.dir)

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
			if !event.Has(fsnotify.Create) {
				continue
			}

			name := filepath.Base(event.Name)
			mailPath := event.Name

			// Skip staging directories (written atomically by cubit send).
			// On macOS (kqueue), os.Rename fires a Create event for the new name,
			// so the final directory will be detected. On Linux (inotify), Rename
			// fires IN_MOVED_TO which maps to fsnotify.Rename, not Create — if Linux
			// support is needed, also handle fsnotify.Rename events here.
			if strings.HasPrefix(name, ".staging-") {
				continue
			}

			// Detect: flat .md file or directory containing mail.md
			isFlat := strings.HasSuffix(name, ".md")
			isDir := false
			if !isFlat {
				candidate := filepath.Join(event.Name, "mail.md")
				if fileExists(candidate) {
					isDir = true
				}
			}

			if !isFlat && !isDir {
				continue
			}

			log.Printf("[keel] %s: new mailbox message: %s", w.agentName, name)

			// Log the event for flat files
			if isFlat {
				if from, slug, ok := parseMessageFilename(name); ok {
					if err := workspace.LogMailboxEvent(w.dir, from, "message", slug); err != nil {
						log.Printf("[keel] %s: mailbox log error: %v", w.agentName, err)
					}
				}
			}

			// Check if this is a delegation-response
			if delID, from, ok := router.CheckResponse(mailPath); ok {
				log.Printf("[keel] %s: delegation-response from %s for %s — routing", w.agentName, from, delID)
				result, err := router.RouteResponse(mailPath)
				if err != nil {
					log.Printf("[keel] %s: delegation routing error: %v", w.agentName, err)
					w.onMessage()
					continue
				}
				if w.onDelegation != nil {
					w.onDelegation(result)
				}
				w.onMessage()
				continue
			}

			// Regular message — just nudge
			w.onMessage()

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
func parseMessageFilename(name string) (string, string, bool) {
	name = strings.TrimSuffix(name, ".md")
	if len(name) < 21 || name[19] != '-' {
		return "", "", false
	}
	rest := name[20:]
	idx := strings.IndexByte(rest, '-')
	if idx <= 0 {
		return rest, "", true
	}
	return rest[:idx], rest[idx+1:], true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

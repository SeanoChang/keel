package discord

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/SeanoChang/keel/internal/mailship"
)

// dirDebounce delays shipping a directory-form draft after the most recent inner
// write so attachments arriving alongside mail.md land before cubit send runs.
const dirDebounce = 1 * time.Second

// OutboxWatcher watches <agentDir>/outbox/ and routes new drafts through
// mailship.TryShip. It is the real-time complement to the session-end sweep.
type OutboxWatcher struct {
	agentName string
	agentDir  string
	onFailure func(reason, relName string, err error)
	stop      chan struct{}
	once      sync.Once

	mu       sync.Mutex
	timers   map[string]*time.Timer // dir-form debounce, keyed by top-level relName
	inFlight map[string]bool        // relNames currently being shipped
}

func NewOutboxWatcher(agentName, agentDir string, onFailure func(reason, relName string, err error)) *OutboxWatcher {
	return &OutboxWatcher{
		agentName: agentName,
		agentDir:  agentDir,
		onFailure: onFailure,
		stop:      make(chan struct{}),
		timers:    make(map[string]*time.Timer),
		inFlight:  make(map[string]bool),
	}
}

func (w *OutboxWatcher) Start() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[keel] %s: outbox fsnotify error: %v", w.agentName, err)
		return
	}
	defer watcher.Close()

	outbox := filepath.Join(w.agentDir, "outbox")
	if err := os.MkdirAll(outbox, 0755); err != nil {
		log.Printf("[keel] %s: outbox mkdir error: %v", w.agentName, err)
		return
	}
	if err := watcher.Add(outbox); err != nil {
		log.Printf("[keel] %s: outbox watch error on %s: %v", w.agentName, outbox, err)
		return
	}

	for {
		select {
		case <-w.stop:
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Linux (inotify) atomic-rename lands in Rename; macOS (kqueue) in Create.
			// Treat Write similarly so partially-written drafts trigger debounce.
			if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Rename) && !event.Has(fsnotify.Write) {
				continue
			}
			w.handleEvent(event.Name, outbox)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[keel] %s: outbox watcher error: %v", w.agentName, err)
		}
	}
}

func (w *OutboxWatcher) Stop() {
	w.once.Do(func() {
		close(w.stop)
		w.mu.Lock()
		for _, t := range w.timers {
			t.Stop()
		}
		w.timers = nil
		w.mu.Unlock()
	})
}

// handleEvent decides whether an event is a top-level flat .md (ship immediately)
// or part of a directory-form draft (debounced).
func (w *OutboxWatcher) handleEvent(eventPath, outbox string) {
	rel, err := filepath.Rel(outbox, eventPath)
	if err != nil || rel == "." || rel == ".." {
		return
	}
	// Top-level component is what we ship.
	parts := strings.SplitN(rel, string(os.PathSeparator), 2)
	top := parts[0]
	if !mailship.Eligible(top, false) && !mailship.Eligible(top, true) {
		return
	}
	topPath := filepath.Join(outbox, top)
	info, err := os.Stat(topPath)
	if err != nil {
		return
	}
	if info.IsDir() {
		w.debounceDir(top)
		return
	}
	// Flat .md — ship immediately.
	if !strings.HasSuffix(top, ".md") {
		return
	}
	go w.ship(top)
}

func (w *OutboxWatcher) debounceDir(relName string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.timers[relName]; ok {
		t.Stop()
	}
	w.timers[relName] = time.AfterFunc(dirDebounce, func() {
		w.mu.Lock()
		delete(w.timers, relName)
		w.mu.Unlock()
		w.ship(relName)
	})
}

// ship invokes mailship.TryShip with an in-flight guard so a sweep + watcher
// race can't ship the same entry twice.
func (w *OutboxWatcher) ship(relName string) {
	w.mu.Lock()
	if w.inFlight[relName] {
		w.mu.Unlock()
		return
	}
	w.inFlight[relName] = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.inFlight, relName)
		w.mu.Unlock()
	}()

	res := mailship.TryShip(w.agentName, w.agentDir, relName)
	if res.Sent {
		log.Printf("[keel] %s: outbox shipped %s", w.agentName, relName)
		return
	}
	if res.Err != nil {
		log.Printf("[keel] %s: outbox ship error for %s: %v", w.agentName, relName, res.Err)
	} else if res.Reason != "" {
		log.Printf("[keel] %s: outbox invalid %s: %s", w.agentName, relName, res.Reason)
	}
	if w.onFailure != nil && (res.Reason != "" || res.Err != nil) {
		w.onFailure(res.Reason, relName, res.Err)
	}
}

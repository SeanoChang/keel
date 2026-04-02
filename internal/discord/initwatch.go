package discord

import (
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// InitWatcher watches agents-home/ for .init-pending files
// created by `cubit init --keel`.
type InitWatcher struct {
	agentsHome string
	onPending  func(agentDir string, pending *InitPending)
	stop       chan struct{}
	once       sync.Once
}

func NewInitWatcher(agentsHome string, onPending func(string, *InitPending)) *InitWatcher {
	return &InitWatcher{
		agentsHome: agentsHome,
		onPending:  onPending,
		stop:       make(chan struct{}),
	}
}

func (w *InitWatcher) Start() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[keel] init watcher: fsnotify error: %v", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(w.agentsHome); err != nil {
		log.Printf("[keel] init watcher: watch error on %s: %v", w.agentsHome, err)
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
			// New directory created under agents-home — watch it for .init-pending
			if event.Has(fsnotify.Create) {
				name := filepath.Base(event.Name)
				if !strings.HasPrefix(name, ".") {
					// Might be a new agent dir — add watch
					watcher.Add(event.Name)
				}
				// Check if this is .init-pending itself
				if name == ".init-pending" {
					agentDir := filepath.Dir(event.Name)
					pending, err := ParseInitPending(agentDir)
					if err != nil {
						log.Printf("[keel] init watcher: parse error: %v", err)
						continue
					}
					w.onPending(agentDir, pending)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[keel] init watcher error: %v", err)
		}
	}
}

func (w *InitWatcher) Stop() {
	w.once.Do(func() { close(w.stop) })
}

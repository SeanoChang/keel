package discord

import (
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const askDebounce = 5 * time.Second

// AskWatcher watches asks/pending/ for all managed agents and triggers
// cubit ask process with a per-agent debounce when new ask JSON files appear.
type AskWatcher struct {
	agentDirs  map[string]string // agent name → workspace dir
	dirToName  map[string]string // pending dir path → agent name (reverse lookup)
	watcher    *fsnotify.Watcher
	timers     map[string]*time.Timer
	processing map[string]bool // true while cubit ask process is running for agent
	stop       chan struct{}
	once       sync.Once
	mu         sync.Mutex
}

func NewAskWatcher(agentDirs map[string]string) (*AskWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &AskWatcher{
		agentDirs:  agentDirs,
		dirToName:  make(map[string]string),
		watcher:    w,
		timers:     make(map[string]*time.Timer),
		processing: make(map[string]bool),
		stop:       make(chan struct{}),
	}, nil
}

func (aw *AskWatcher) Start() {
	for name, dir := range aw.agentDirs {
		pendingDir := filepath.Join(dir, "asks", "pending")
		if err := aw.watcher.Add(pendingDir); err != nil {
			log.Printf("[keel] %s: ask watcher skipping (no asks/pending/): %v", name, err)
			continue
		}
		aw.dirToName[pendingDir] = name
	}

	for {
		select {
		case <-aw.stop:
			return
		case event, ok := <-aw.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) && strings.HasSuffix(event.Name, ".json") {
				pendingDir := filepath.Dir(event.Name)
				name, ok := aw.dirToName[pendingDir]
				if !ok {
					continue
				}
				log.Printf("[keel] %s: new ask detected: %s", name, filepath.Base(event.Name))
				aw.debounce(name)
			}
		case err, ok := <-aw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[keel] ask watcher error: %v", err)
		}
	}
}

func (aw *AskWatcher) Stop() {
	aw.once.Do(func() {
		close(aw.stop)
		aw.watcher.Close()
	})
}

// debounce resets the per-agent timer. When it fires, processAsks runs.
func (aw *AskWatcher) debounce(agentName string) {
	aw.mu.Lock()
	defer aw.mu.Unlock()

	if t, ok := aw.timers[agentName]; ok {
		t.Stop()
	}
	aw.timers[agentName] = time.AfterFunc(askDebounce, func() {
		aw.mu.Lock()
		delete(aw.timers, agentName)
		aw.mu.Unlock()
		aw.processAsks(agentName)
	})
}

// processAsks runs cubit ask process for the given agent.
// Skips if already processing for this agent (prevents duplicate runs from timer races).
func (aw *AskWatcher) processAsks(agentName string) {
	aw.mu.Lock()
	if aw.processing[agentName] {
		aw.mu.Unlock()
		log.Printf("[keel] %s: ask process already running, skipping", agentName)
		return
	}
	aw.processing[agentName] = true
	aw.mu.Unlock()

	defer func() {
		aw.mu.Lock()
		delete(aw.processing, agentName)
		aw.mu.Unlock()
	}()

	dir, ok := aw.agentDirs[agentName]
	if !ok {
		return
	}
	log.Printf("[keel] %s: running cubit ask process", agentName)
	cmd := exec.Command("cubit", "ask", "process", agentName)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[keel] %s: ask process error: %v\n%s", agentName, err, output)
	} else {
		log.Printf("[keel] %s: ask process complete", agentName)
	}
}

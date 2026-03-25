package loop

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type runningLoop struct {
	cancel  context.CancelFunc
	done    chan struct{}
	wake    chan struct{}
	paused  atomic.Bool  // safe for concurrent read from Run() goroutine
	resumed chan struct{} // signaled by Resume()
}

type Manager struct {
	mu    sync.Mutex
	loops map[string]*runningLoop
}

func NewManager() *Manager {
	return &Manager{
		loops: make(map[string]*runningLoop),
	}
}

// StartOpts holds optional parameters for Start.
type StartOpts struct {
	ProjectsDir  string           // path to projects/ directory (enables eval)
	OnEvalUpdate func(EvalUpdate) // callback for eval metric notifications
}

func (m *Manager) Start(name, dir string, builder CommandBuilder, sleep time.Duration, archiveEvery int, onOutput func(StreamEvent), onLifecycle func(string), opts *StartOpts) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rl, ok := m.loops[name]; ok {
		select {
		case <-rl.done:
			delete(m.loops, name)
		default:
			return fmt.Errorf("agent %s is already running", name)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	resumed := make(chan struct{}, 1)

	rl := &runningLoop{cancel: cancel, done: done, wake: wake, resumed: resumed}

	loop := &AgentLoop{
		Name:           name,
		Dir:            dir,
		CommandBuilder: builder,
		SleepDuration:  sleep,
		ArchiveEvery:   archiveEvery,
		OnOutput:       onOutput,
		OnLifecycle:    onLifecycle,
		Wake:           wake,
		Paused:         &rl.paused,  // atomic.Bool, safe for lock-free read in Run()
		Resumed:        resumed,
	}

	if opts != nil {
		loop.ProjectsDir = opts.ProjectsDir
		loop.OnEvalUpdate = opts.OnEvalUpdate
	}

	m.loops[name] = rl

	go func() {
		defer close(done)
		loop.Run(ctx)
	}()

	return nil
}

// Nudge wakes a sleeping loop so it checks for new goals immediately.
func (m *Manager) Nudge(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rl, ok := m.loops[name]
	if !ok {
		return
	}
	select {
	case <-rl.done:
		return
	default:
	}
	select {
	case rl.wake <- struct{}{}:
	default: // already pending
	}
}

// Pause sets the paused flag so the loop stops between iterations.
func (m *Manager) Pause(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rl, ok := m.loops[name]; ok {
		rl.paused.Store(true)
	}
}

// Resume clears the paused flag and signals the loop to continue.
func (m *Manager) Resume(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rl, ok := m.loops[name]; ok {
		rl.paused.Store(false)
		select {
		case rl.resumed <- struct{}{}:
		default:
		}
	}
}

// IsPaused returns true if the named loop is paused.
func (m *Manager) IsPaused(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rl, ok := m.loops[name]; ok {
		return rl.paused.Load()
	}
	return false
}

func (m *Manager) Stop(name string) {
	m.mu.Lock()
	rl, ok := m.loops[name]
	m.mu.Unlock()

	if ok {
		rl.cancel()
		<-rl.done
		m.mu.Lock()
		delete(m.loops, name)
		m.mu.Unlock()
	}
}

func (m *Manager) IsRunning(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	rl, ok := m.loops[name]
	if !ok {
		return false
	}
	select {
	case <-rl.done:
		delete(m.loops, name)
		return false
	default:
		return true
	}
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	names := make([]string, 0, len(m.loops))
	for name := range m.loops {
		names = append(names, name)
	}
	m.mu.Unlock()

	for _, name := range names {
		m.Stop(name)
	}
}

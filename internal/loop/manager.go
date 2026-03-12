package loop

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type runningLoop struct {
	cancel context.CancelFunc
	done   chan struct{}
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

func (m *Manager) Start(name, dir string, builder CommandBuilder, sleep time.Duration, archiveEvery int, onOutput func(string)) error {
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

	loop := &AgentLoop{
		Name:           name,
		Dir:            dir,
		CommandBuilder: builder,
		SleepDuration:  sleep,
		ArchiveEvery:   archiveEvery,
		OnOutput:       onOutput,
	}

	m.loops[name] = &runningLoop{cancel: cancel, done: done}

	go func() {
		defer close(done)
		loop.Run(ctx)
	}()

	return nil
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

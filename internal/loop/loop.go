package loop

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/SeanoChang/keel/internal/eval"
	"github.com/SeanoChang/keel/internal/workspace"
)

// bootstrapPrompt is a short prompt that tells Claude to read its instructions from
// disk rather than inlining the full PROGRAM.md. This produces fast first output
// (file read tool calls) so the stuck watchdog sees liveness immediately.
const bootstrapPrompt = "Read PROGRAM.md for your session instructions. Then read GOALS.md and INBOX.md, and follow the program."

// CommandSpec describes a command to execute. Tests replace real claude with mocks.
type CommandSpec struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

// CommandBuilder creates a CommandSpec for a given agent invocation.
type CommandBuilder func(ctx context.Context, name, dir, program string) *CommandSpec

// DefaultCommandBuilder returns the real claude CLI invocation.
func DefaultCommandBuilder(ctx context.Context, name, dir, program string) *CommandSpec {
	return &CommandSpec{
		Name: "claude",
		Args: []string{
			"--agent", name,
			"--permission-mode", "dontAsk",
			"--verbose",
			"-p", program,
		},
		Dir: dir,
	}
}

type AgentLoop struct {
	Name             string
	Dir              string
	CommandBuilder   CommandBuilder
	SleepDuration    time.Duration
	ArchiveEvery     int           // run cubit archive every N sessions (0 = disabled)
	MaxErrors        int           // exit loop after N consecutive session errors (0 = default 3)
	MaxStale         int           // exit loop after N consecutive sessions with unchanged goals (0 = default 2)
	StuckTimeout     time.Duration // kill session if no output events for this long (0 = default 5m)
	OnOutput         func(event StreamEvent)
	OnLifecycle      func(event string) // session_start, session_end, sleeping, woke, goals_empty, agent_exit, wrap_up, stale, stopped, too_many_errors, paused, resumed, eval_stopped, error:*
	Wake             chan struct{}       // signal to interrupt sleep early (new goals arrived)
	Paused           *atomic.Bool       // pointer to Manager's paused flag (nil = never pause)
	Resumed          chan struct{}       // signal from Manager.Resume()
	ProjectsDir      string             // path to projects/ directory (empty = eval disabled)
	CumulativeCost   float64            // accumulated session costs
	LastSessionCost  float64            // cost from most recent session
	OnEvalUpdate     func(EvalUpdate)   // callback for eval metric notifications
	evalStates       map[string]*eval.EvalState // lazy-init, keyed by project name
}

func (l *AgentLoop) maxErrors() int {
	if l.MaxErrors > 0 {
		return l.MaxErrors
	}
	return 3
}

func (l *AgentLoop) maxStale() int {
	if l.MaxStale > 0 {
		return l.MaxStale
	}
	return 2
}

func (l *AgentLoop) stuckTimeout() time.Duration {
	if l.StuckTimeout > 0 {
		return l.StuckTimeout
	}
	return 5 * time.Minute
}

// errorBackoff returns an exponential backoff duration: 30s, 60s, 120s... capped at 5 min.
func (l *AgentLoop) errorBackoff(consecutive int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < consecutive; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

func (l *AgentLoop) lifecycle(event string) {
	if l.OnLifecycle != nil {
		l.OnLifecycle(event)
	}
}

// RunOnce executes one agent session.
func (l *AgentLoop) RunOnce(ctx context.Context) error {
	if err := workspace.EnsureProgram(l.Dir); err != nil {
		return fmt.Errorf("ensure program: %w", err)
	}

	spec := l.CommandBuilder(ctx, l.Name, l.Dir, bootstrapPrompt)

	// When callbacks are active, use stream-json for structured event parsing.
	if l.OnOutput != nil {
		spec.Args = append(spec.Args, "--output-format", "stream-json")
	}

	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = spec.Env
	}

	// Graceful shutdown: SIGTERM first, then SIGKILL after WaitDelay.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	if l.OnOutput != nil {
		return l.runStreaming(ctx, cmd)
	}

	// CLI mode: direct pipe to terminal
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("agent %s exited: %w", l.Name, err)
	}
	return nil
}

// runStreaming parses stream-json from stdout and dispatches StreamEvents through OnOutput.
func (l *AgentLoop) runStreaming(ctx context.Context, cmd *exec.Cmd) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("command not found: %w", err)
		}
		return fmt.Errorf("start agent: %w", err)
	}

	// Track last real event time for stuck detection.
	var lastEventNano atomic.Int64
	lastEventNano.Store(time.Now().UnixNano())

	var ioWg sync.WaitGroup
	ioWg.Add(2)

	// stdout → parse JSON stream events
	go func() {
		defer ioWg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			lastEventNano.Store(time.Now().UnixNano())
			for _, ev := range ParseStreamJSON(scanner.Text()) {
				if ev.Kind == EventResult {
					// Safe: written before ioWg.Done(), read after RunOnce returns.
					l.LastSessionCost = ev.Cost
				}
				l.OnOutput(ev)
			}
		}
	}()

	// stderr → capture for diagnostics and track liveness (prevents false stuck kills)
	var stderrMu sync.Mutex
	var stderrLines []string
	go func() {
		defer ioWg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for scanner.Scan() {
			lastEventNano.Store(time.Now().UnixNano())
			line := scanner.Text()
			log.Printf("[keel] %s stderr: %s", l.Name, line)
			stderrMu.Lock()
			stderrLines = append(stderrLines, line)
			if len(stderrLines) > 50 {
				stderrLines = stderrLines[len(stderrLines)-50:]
			}
			stderrMu.Unlock()
		}
	}()

	// Stuck watchdog: kill the process if no output events arrive within StuckTimeout.
	watchdogDone := make(chan struct{})
	if l.OnOutput != nil {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			stuckLimit := l.stuckTimeout()
			for {
				select {
				case <-watchdogDone:
					return
				case now := <-ticker.C:
					last := time.Unix(0, lastEventNano.Load())
					silent := now.Sub(last)
					if silent >= stuckLimit {
						log.Printf("[keel] %s: no output for %s, killing stuck session", l.Name, silent.Round(time.Second))
						cmd.Process.Kill()
						return
					}
				}
			}
		}()
	}

	ioWg.Wait()
	close(watchdogDone)

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stderrMu.Lock()
		tail := stderrLines
		if len(tail) > 5 {
			tail = tail[len(tail)-5:]
		}
		stderrTail := strings.Join(tail, "\n")
		stderrMu.Unlock()
		if stderrTail != "" {
			return fmt.Errorf("agent %s exited: %w\nstderr (last lines):\n%s", l.Name, err, stderrTail)
		}
		return fmt.Errorf("agent %s exited: %w", l.Name, err)
	}
	return nil
}

// Run loops: check goals -> runOnce -> maybe archive -> sleep -> repeat.
// Exits when goals empty, ctx cancelled, too many consecutive errors, stale, or wrap-up.
func (l *AgentLoop) Run(ctx context.Context) {
	// Clear stale sentinels from previous sessions to prevent poisoning.
	workspace.ClearExitSignal(l.Dir)
	workspace.ClearWrapUpSignal(l.Dir)

	var sessions int
	var consecutiveErrors int
	var staleCount int
	for {
		if ctx.Err() != nil {
			l.lifecycle("stopped")
			return
		}
		if !workspace.HasGoals(l.Dir) {
			log.Printf("[keel] %s: no goals, loop exiting", l.Name)
			l.lifecycle("goals_empty")
			return
		}

		// Snapshot goals before session to detect stale loops.
		goalsBefore, _ := workspace.ReadGoals(l.Dir)

		log.Printf("[keel] %s: starting session", l.Name)
		l.lifecycle("session_start")
		err := l.RunOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				l.lifecycle("stopped")
				return
			}
			consecutiveErrors++
			log.Printf("[keel] %s: session error (%d/%d): %v", l.Name, consecutiveErrors, l.maxErrors(), err)
			l.lifecycle(fmt.Sprintf("error: %v (%d/%d)", err, consecutiveErrors, l.maxErrors()))
			if consecutiveErrors >= l.maxErrors() {
				log.Printf("[keel] %s: too many consecutive errors, exiting loop", l.Name)
				l.lifecycle("too_many_errors")
				return
			}
			// Exponential backoff after error. Not interruptible by Wake
			// to prevent retry storms from Discord messages during cooldown.
			backoff := l.errorBackoff(consecutiveErrors)
			log.Printf("[keel] %s: backing off %s before retry", l.Name, backoff)
			l.lifecycle(fmt.Sprintf("backoff: %s", backoff.Round(time.Second)))
			select {
			case <-ctx.Done():
				l.lifecycle("stopped")
				return
			case <-time.After(backoff):
			}
			continue
		}
		consecutiveErrors = 0
		l.lifecycle("session_end")

		// Agent requested exit via sentinel file.
		if workspace.HasExitSignal(l.Dir) {
			workspace.ClearExitSignal(l.Dir)
			wrapUp := workspace.HasWrapUpSignal(l.Dir)
			if wrapUp {
				workspace.ClearWrapUpSignal(l.Dir)
			}
			if !workspace.HasGoalHeaders(l.Dir) {
				workspace.ClearGoals(l.Dir)
			}
			// If wrap-up was also requested, emit wrap_up (triggers archive).
			if wrapUp {
				log.Printf("[keel] %s: agent exited during wrap-up, loop stopping", l.Name)
				l.lifecycle("wrap_up")
			} else {
				log.Printf("[keel] %s: agent requested exit (.exit), loop stopping", l.Name)
				l.lifecycle("agent_exit")
			}
			return
		}

		// User requested wrap-up via !wrap-up command (agent didn't create .exit).
		if workspace.HasWrapUpSignal(l.Dir) {
			workspace.ClearWrapUpSignal(l.Dir)
			if !workspace.HasGoalHeaders(l.Dir) {
				workspace.ClearGoals(l.Dir)
			}
			log.Printf("[keel] %s: wrap-up signal, loop stopping", l.Name)
			l.lifecycle("wrap_up")
			return
		}

		// Check evaluation results from projects.
		l.CumulativeCost += l.LastSessionCost
		evalMetricsUpdated := false
		if l.checkEvalResults(&evalMetricsUpdated) {
			l.lifecycle("eval_stopped")
			return
		}
		l.LastSessionCost = 0

		// Detect stale loops: if goals didn't change, the agent made no progress.
		goalsAfter, _ := workspace.ReadGoals(l.Dir)
		if goalsAfter == goalsBefore {
			// If no real goal headers remain, agent left status junk — treat as empty.
			if !workspace.HasGoalHeaders(l.Dir) {
				workspace.ClearGoals(l.Dir)
				log.Printf("[keel] %s: no goal headers remain, cleaning up", l.Name)
				l.lifecycle("goals_empty")
				return
			}
			// Eval metrics updating suppresses stale detection — agent is making progress via eval.
			if evalMetricsUpdated {
				staleCount = 0
			} else {
				staleCount++
			}
			log.Printf("[keel] %s: goals unchanged (%d/%d)", l.Name, staleCount, l.maxStale())
			if staleCount >= l.maxStale() {
				log.Printf("[keel] %s: goals stale for %d sessions, exiting loop", l.Name, staleCount)
				l.lifecycle("stale")
				return
			}
		} else {
			staleCount = 0
		}

		// Quick exit if goals were cleared during session (avoids unnecessary sleep).
		if !workspace.HasGoals(l.Dir) {
			log.Printf("[keel] %s: goals cleared during session, loop exiting", l.Name)
			l.lifecycle("goals_empty")
			return
		}

		sessions++
		if l.ArchiveEvery > 0 && sessions%l.ArchiveEvery == 0 {
			l.runArchive()
		}

		l.lifecycle("sleeping")
		select {
		case <-ctx.Done():
			l.lifecycle("stopped")
			return
		case <-l.Wake:
			staleCount = 0 // new goals arrived, reset stale counter
			l.lifecycle("woke")
		case <-time.After(l.SleepDuration):
		}

		// Check pause between iterations.
		if l.Paused != nil && l.Paused.Load() {
			l.lifecycle("paused")
			select {
			case <-ctx.Done():
				l.lifecycle("stopped")
				return
			case <-l.Resumed:
				l.lifecycle("resumed")
			}
		}
	}
}

// EvalUpdate is emitted to OnEvalUpdate when a project's metric changes.
type EvalUpdate struct {
	Project    string
	Iteration  int
	MetricName string
	Value      float64
	Best       float64
	Baseline   float64
	CostSoFar  float64
	Event      string // "improved", "regressed", "budget_exceeded", "converged"
}

// checkEvalResults scans projects/ for EVAL.md and checks for new metrics.
// Sets *metricsUpdated to true if any metrics were processed.
// Returns true if the loop should stop (budget or convergence).
func (l *AgentLoop) checkEvalResults(metricsUpdated *bool) bool {
	if l.ProjectsDir == "" {
		return false
	}
	if l.evalStates == nil {
		l.evalStates = make(map[string]*eval.EvalState)
	}

	projects, err := os.ReadDir(l.ProjectsDir)
	if err != nil {
		return false
	}

	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		projectDir := filepath.Join(l.ProjectsDir, p.Name())
		evalPath := filepath.Join(projectDir, "EVAL.md")

		cfg, err := eval.ParseEval(evalPath)
		if err != nil {
			log.Printf("[keel] %s: error parsing %s: %v", l.Name, evalPath, err)
			continue
		}
		if cfg == nil {
			continue
		}

		// Get or create state for this project.
		state, ok := l.evalStates[p.Name()]
		if !ok {
			state = &eval.EvalState{
				Config:     *cfg,
				ProjectDir: projectDir,
				Best:       cfg.Baseline,
				Previous:   cfg.Baseline,
			}
			l.evalStates[p.Name()] = state
		}

		metricsDir := filepath.Join(projectDir, "metrics")
		metric, err := eval.ReadLatestMetric(metricsDir)
		if err != nil || metric == nil {
			continue
		}
		if metric.Iteration <= state.Iteration {
			continue // already processed
		}

		*metricsUpdated = true
		state.Iteration = metric.Iteration
		// CostSoFar tracks cost of sessions where this project had eval activity.
		// In multi-project setups, each project accumulates the full session cost
		// independently, so the sum across projects may exceed actual total spend.
		state.CostSoFar += l.LastSessionCost

		improved := eval.IsImproved(state.Previous, metric.Value, cfg.Direction)
		if improved {
			if (cfg.Direction == "higher" && metric.Value > state.Best) ||
				(cfg.Direction == "lower" && metric.Value < state.Best) {
				state.Best = metric.Value
			}
			state.NoImproveCount = 0
			l.emitEvalUpdate(p.Name(), "improved", state, metric.Value)
		} else {
			state.NoImproveCount++
			// Inject regression context into INBOX.md so the agent can decide how to respond.
			msg := fmt.Sprintf("Eval regression in project %s: %s went from %.4f to %.4f (best: %.4f, baseline: %.4f). Consider reverting, adjusting your approach, or trying a different strategy.",
				p.Name(), cfg.Metric, state.Previous, metric.Value, state.Best, cfg.Baseline)
			if err := workspace.AppendInbox(l.Dir, true, "keel", msg); err != nil {
				log.Printf("[keel] %s: error writing regression to INBOX.md: %v", l.Name, err)
			}
			l.emitEvalUpdate(p.Name(), "regressed", state, metric.Value)
		}

		state.Previous = metric.Value

		if stop, reason := eval.ShouldStop(state); stop {
			l.emitEvalUpdate(p.Name(), reason, state, metric.Value)
			return true
		}
	}
	return false
}

func (l *AgentLoop) emitEvalUpdate(project, event string, state *eval.EvalState, currentValue float64) {
	if l.OnEvalUpdate == nil {
		return
	}
	l.OnEvalUpdate(EvalUpdate{
		Project:    project,
		Iteration:  state.Iteration,
		MetricName: state.Config.Metric,
		Value:      currentValue,
		Baseline:   state.Config.Baseline,
		Best:       state.Best,
		CostSoFar:  state.CostSoFar,
		Event:      event,
	})
}

func (l *AgentLoop) runArchive() {
	log.Printf("[keel] %s: running cubit archive", l.Name)
	cmd := exec.Command("cubit", "archive")
	cmd.Dir = l.Dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[keel] %s: archive error: %v\n%s", l.Name, err, output)
	} else {
		log.Printf("[keel] %s: archive complete", l.Name)
	}
}

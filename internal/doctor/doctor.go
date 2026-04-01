package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/SeanoChang/keel/internal/config"
	"github.com/SeanoChang/keel/internal/schedule"
	"github.com/SeanoChang/keel/internal/workspace"
)

type Status int

const (
	Pass Status = iota
	Warn
	Fail
)

type Check struct {
	Name    string
	Status  Status
	Message string
}

func (c Check) symbol() string {
	switch c.Status {
	case Pass:
		return "✓"
	case Warn:
		return "!"
	case Fail:
		return "✗"
	}
	return "?"
}

// Run executes all health checks and prints results.
// Returns true if no checks failed.
func Run(configPath string, agentFilter string) bool {
	var failed, warned int
	emit := func(c Check) {
		fmt.Printf("  %s %-22s %s\n", c.symbol(), c.Name, c.Message)
		switch c.Status {
		case Fail:
			failed++
		case Warn:
			warned++
		}
	}

	// System
	fmt.Println("System")
	emit(checkClaude())
	emit(checkDiskSpace())
	fmt.Println()

	// Config
	fmt.Printf("Config (%s)\n", configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		emit(Check{"config file", Fail, err.Error()})
		fmt.Println()
		if agentFilter != "" {
			// Still check the agent dir via default path
			dir := defaultAgentDir(agentFilter)
			runAgentChecks(agentFilter, dir, emit)
		}
		printSummary(failed, warned)
		return failed == 0
	}
	emit(Check{"config file", Pass, "parsed successfully"})
	emit(checkDiscordToken(cfg))

	// Resolve agents from config, sorted for stable output
	agents := resolveAgents(cfg, agentFilter)
	names := sortedKeys(agents)
	for _, name := range names {
		emit(checkAgentDir(name, agents[name]))
	}
	fmt.Println()

	// Per-agent checks
	for _, name := range names {
		dir := agents[name]
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue // already reported as Fail above
		}
		runAgentChecks(name, dir, emit)
	}

	printSummary(failed, warned)
	return failed == 0
}

func runAgentChecks(name, dir string, emit func(Check)) {
	fmt.Printf("Agent: %s (%s)\n", name, dir)
	emit(checkFile(dir, "GOALS.md"))
	emit(checkFile(dir, "PROGRAM.md"))
	emit(checkFile(dir, "MEMORY.md"))
	emit(checkSentinels(dir))
	emit(checkSchedule(dir))
	emit(checkRecentErrors(dir))
	fmt.Println()
}

func printSummary(failed, warned int) {
	switch {
	case failed == 0 && warned == 0:
		fmt.Println("All checks passed.")
	case failed == 0:
		fmt.Printf("All checks passed (%d warning(s)).\n", warned)
	default:
		fmt.Printf("%d check(s) failed, %d warning(s).\n", failed, warned)
	}
}

// --- System checks ---

func checkClaude() Check {
	path, err := exec.LookPath("claude")
	if err != nil {
		return Check{"claude binary", Fail, "not found in PATH"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return Check{"claude binary", Warn, fmt.Sprintf("found but --version failed: %v", err)}
	}
	version := strings.TrimSpace(string(out))
	return Check{"claude binary", Pass, version}
}

func checkDiskSpace() Check {
	home, err := os.UserHomeDir()
	if err != nil {
		return Check{"disk space", Warn, fmt.Sprintf("cannot determine home dir: %v", err)}
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(home, &stat); err != nil {
		return Check{"disk space", Warn, fmt.Sprintf("statfs failed: %v", err)}
	}
	avail := stat.Bavail * uint64(stat.Bsize)
	gb := float64(avail) / (1 << 30)
	if gb < 1.0 {
		return Check{"disk space", Fail, fmt.Sprintf("%.1f GB available (< 1 GB)", gb)}
	}
	return Check{"disk space", Pass, fmt.Sprintf("%.1f GB available", gb)}
}

// --- Config checks ---

func checkDiscordToken(cfg *config.Config) Check {
	envName := cfg.Bot.TokenEnv
	if envName == "" {
		return Check{"discord token", Warn, "no token_env configured"}
	}
	val := os.Getenv(envName)
	if val == "" {
		return Check{"discord token", Fail, fmt.Sprintf("$%s is empty or unset", envName)}
	}
	return Check{"discord token", Pass, fmt.Sprintf("%s is set", envName)}
}

func checkAgentDir(name, dir string) Check {
	label := "agent dir " + name
	info, err := os.Stat(dir)
	if err != nil {
		return Check{label, Fail, fmt.Sprintf("%s not found", dir)}
	}
	if !info.IsDir() {
		return Check{label, Fail, fmt.Sprintf("%s is not a directory", dir)}
	}
	return Check{label, Pass, "exists"}
}

// --- Per-agent checks ---

func checkFile(dir, name string) Check {
	_, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return Check{name, Warn, "missing"}
	}
	return Check{name, Pass, "present"}
}

func checkSentinels(dir string) Check {
	hasExit := workspace.HasExitSignal(dir)
	hasWrap := workspace.HasWrapUpSignal(dir)
	switch {
	case hasExit && hasWrap:
		return Check{"sentinels", Warn, ".exit and .wrap-up both present"}
	case hasExit:
		return Check{"sentinels", Warn, ".exit present (stale from previous run?)"}
	case hasWrap:
		return Check{"sentinels", Warn, ".wrap-up present (stale from previous run?)"}
	default:
		return Check{"sentinels", Pass, "clean"}
	}
}

func checkSchedule(dir string) Check {
	entries, err := schedule.ScanDir(dir)
	if err != nil {
		return Check{"schedule", Warn, err.Error()}
	}
	return Check{"schedule", Pass, fmt.Sprintf("%d entries", len(entries))}
}

func checkRecentErrors(dir string) Check {
	lines, err := workspace.ReadLogTail(dir, 20)
	if err != nil {
		return Check{"log.md", Warn, fmt.Sprintf("read error: %v", err)}
	}
	if len(lines) == 0 {
		return Check{"log.md", Pass, "no log yet"}
	}
	var errCount int
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "[error]") || strings.Contains(lower, "error:") {
			errCount++
		}
	}
	if errCount > 3 {
		return Check{"log.md", Warn, fmt.Sprintf("%d errors in last %d lines", errCount, len(lines))}
	}
	if errCount > 0 {
		return Check{"log.md", Pass, fmt.Sprintf("%d errors in last %d lines", errCount, len(lines))}
	}
	return Check{"log.md", Pass, "clean"}
}

// --- Helpers ---

func expandPath(path string) string {
	if after, ok := strings.CutPrefix(path, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, after)
	}
	return path
}

func defaultAgentDir(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ark", "agents-home", name)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func resolveAgents(cfg *config.Config, filter string) map[string]string {
	agents := make(map[string]string)
	for name, ch := range cfg.Channels {
		if filter != "" && name != filter {
			continue
		}
		agents[name] = expandPath(ch.AgentDir)
	}
	// If filter was set but not found in config, use default path
	if filter != "" && len(agents) == 0 {
		agents[filter] = defaultAgentDir(filter)
	}
	return agents
}

package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// EvalConfig holds the parsed frontmatter from a project's EVAL.md.
type EvalConfig struct {
	Metric       string  `yaml:"metric"`
	Direction    string  `yaml:"direction"`     // "higher" or "lower"
	Baseline     float64 `yaml:"baseline"`
	Budget       float64 `yaml:"budget"`         // max cumulative USD (0 = unlimited)
	MaxNoImprove int     `yaml:"max_no_improve"` // iterations with no improvement before stop (0 = default 10)
}

func (c *EvalConfig) maxNoImprove() int {
	if c.MaxNoImprove > 0 {
		return c.MaxNoImprove
	}
	return 10
}

// MetricRecord is one iteration's metric written by the agent to metrics/<n>.json.
type MetricRecord struct {
	Value     float64 `json:"value"`
	Iteration int     `json:"iteration"`
	Timestamp string  `json:"timestamp,omitempty"`
}

// ParseEval reads EVAL.md and parses its YAML frontmatter.
// Returns (nil, nil) if the file does not exist.
func ParseEval(path string) (*EvalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	content := string(data)
	// Extract YAML between --- delimiters
	if !strings.HasPrefix(content, "---") {
		return nil, fmt.Errorf("EVAL.md missing YAML frontmatter")
	}
	end := strings.Index(content[3:], "---")
	if end == -1 {
		return nil, fmt.Errorf("EVAL.md missing closing --- delimiter")
	}
	frontmatter := content[3 : 3+end]

	var cfg EvalConfig
	if err := yaml.Unmarshal([]byte(frontmatter), &cfg); err != nil {
		return nil, fmt.Errorf("parsing EVAL.md frontmatter: %w", err)
	}

	if cfg.Metric == "" {
		return nil, fmt.Errorf("EVAL.md missing required field: metric")
	}
	if cfg.Direction != "higher" && cfg.Direction != "lower" {
		return nil, fmt.Errorf("EVAL.md direction must be 'higher' or 'lower', got %q", cfg.Direction)
	}

	return &cfg, nil
}

// ReadLatestMetric reads the newest JSON metric file from a metrics/ directory.
// Returns (nil, nil) if the directory is empty or doesn't exist.
func ReadLatestMetric(metricsDir string) (*MetricRecord, error) {
	entries, err := os.ReadDir(metricsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Filter to .json files and sort lexically (filenames should be sortable)
	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}
	if len(jsonFiles) == 0 {
		return nil, nil
	}
	sort.Strings(jsonFiles)

	latest := jsonFiles[len(jsonFiles)-1]
	data, err := os.ReadFile(filepath.Join(metricsDir, latest))
	if err != nil {
		return nil, err
	}

	var record MetricRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", latest, err)
	}

	return &record, nil
}

// IsImproved returns true if curr is better than prev given the direction.
func IsImproved(prev, curr float64, direction string) bool {
	if direction == "lower" {
		return curr < prev
	}
	return curr > prev
}

// EvalState tracks the running state of evaluation for one project.
type EvalState struct {
	Config         EvalConfig
	ProjectDir     string
	Best           float64
	Previous       float64
	Iteration      int
	CostSoFar      float64
	NoImproveCount int
}

// ShouldStop returns true and a reason string if the eval loop should terminate.
func ShouldStop(state *EvalState) (bool, string) {
	if state.Config.Budget > 0 && state.CostSoFar >= state.Config.Budget {
		return true, "budget_exceeded"
	}
	if state.NoImproveCount >= state.Config.maxNoImprove() {
		return true, "converged"
	}
	return false, ""
}

package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestParseEval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "EVAL.md")
	os.WriteFile(path, []byte(`---
metric: conversion_rate
direction: higher
baseline: 0.41
budget: 50.0
max_no_improve: 5
---

# Evaluation Protocol
Run eval.py after each change.
`), 0644)

	cfg, err := ParseEval(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Metric != "conversion_rate" {
		t.Errorf("metric = %q, want conversion_rate", cfg.Metric)
	}
	if cfg.Direction != "higher" {
		t.Errorf("direction = %q, want higher", cfg.Direction)
	}
	if cfg.Baseline != 0.41 {
		t.Errorf("baseline = %f, want 0.41", cfg.Baseline)
	}
	if cfg.Budget != 50.0 {
		t.Errorf("budget = %f, want 50.0", cfg.Budget)
	}
	if cfg.MaxNoImprove != 5 {
		t.Errorf("max_no_improve = %d, want 5", cfg.MaxNoImprove)
	}
}

func TestParseEvalMissing(t *testing.T) {
	cfg, err := ParseEval("/nonexistent/EVAL.md")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if cfg != nil {
		t.Error("expected nil config for missing file")
	}
}

func TestParseEvalMinimal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "EVAL.md")
	os.WriteFile(path, []byte("---\nmetric: score\ndirection: lower\n---\n"), 0644)

	cfg, err := ParseEval(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Metric != "score" {
		t.Errorf("metric = %q, want score", cfg.Metric)
	}
	if cfg.Direction != "lower" {
		t.Errorf("direction = %q, want lower", cfg.Direction)
	}
	if cfg.maxNoImprove() != 10 {
		t.Errorf("default maxNoImprove = %d, want 10", cfg.maxNoImprove())
	}
}

func TestParseEvalBadDirection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "EVAL.md")
	os.WriteFile(path, []byte("---\nmetric: score\ndirection: sideways\n---\n"), 0644)

	_, err := ParseEval(path)
	if err == nil {
		t.Error("expected error for invalid direction")
	}
}

func TestReadLatestMetric(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	os.MkdirAll(metricsDir, 0755)

	// Write two metric files
	for i, v := range []float64{0.5, 0.67} {
		data, _ := json.Marshal(MetricRecord{Value: v, Iteration: i + 1})
		name := filepath.Join(metricsDir, fmt.Sprintf("%03d.json", i+1))
		os.WriteFile(name, data, 0644)
	}

	record, err := ReadLatestMetric(metricsDir)
	if err != nil {
		t.Fatal(err)
	}
	if record == nil {
		t.Fatal("expected non-nil record")
	}
	if record.Value != 0.67 {
		t.Errorf("value = %f, want 0.67", record.Value)
	}
	if record.Iteration != 2 {
		t.Errorf("iteration = %d, want 2", record.Iteration)
	}
}

func TestReadLatestMetricEmpty(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	os.MkdirAll(metricsDir, 0755)

	record, err := ReadLatestMetric(metricsDir)
	if err != nil {
		t.Fatal(err)
	}
	if record != nil {
		t.Error("expected nil record for empty dir")
	}
}

func TestReadLatestMetricMissing(t *testing.T) {
	record, err := ReadLatestMetric("/nonexistent/metrics")
	if err != nil {
		t.Fatal(err)
	}
	if record != nil {
		t.Error("expected nil record for missing dir")
	}
}

func TestIsImproved(t *testing.T) {
	tests := []struct {
		prev, curr float64
		direction  string
		want       bool
	}{
		{0.5, 0.7, "higher", true},
		{0.7, 0.5, "higher", false},
		{0.5, 0.5, "higher", false},
		{0.5, 0.3, "lower", true},
		{0.3, 0.5, "lower", false},
		{0.5, 0.5, "lower", false},
	}
	for _, tt := range tests {
		got := IsImproved(tt.prev, tt.curr, tt.direction)
		if got != tt.want {
			t.Errorf("IsImproved(%f, %f, %q) = %v, want %v", tt.prev, tt.curr, tt.direction, got, tt.want)
		}
	}
}

func TestShouldStopBudget(t *testing.T) {
	state := &EvalState{
		Config:    EvalConfig{Budget: 10.0},
		CostSoFar: 12.0,
	}
	stop, reason := ShouldStop(state)
	if !stop {
		t.Error("expected stop on budget exceeded")
	}
	if reason != "budget_exceeded" {
		t.Errorf("reason = %q, want budget_exceeded", reason)
	}
}

func TestShouldStopConverged(t *testing.T) {
	state := &EvalState{
		Config:         EvalConfig{MaxNoImprove: 3},
		NoImproveCount: 3,
	}
	stop, reason := ShouldStop(state)
	if !stop {
		t.Error("expected stop on convergence")
	}
	if reason != "converged" {
		t.Errorf("reason = %q, want converged", reason)
	}
}

func TestShouldStopNot(t *testing.T) {
	state := &EvalState{
		Config:         EvalConfig{Budget: 50.0, MaxNoImprove: 10},
		CostSoFar:      5.0,
		NoImproveCount: 2,
	}
	stop, _ := ShouldStop(state)
	if stop {
		t.Error("should not stop yet")
	}
}


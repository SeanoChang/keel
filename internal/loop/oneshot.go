package loop

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// RunOneShot executes a single claude invocation with the given message and returns the response.
func RunOneShot(ctx context.Context, name, dir, message string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude",
		"--agent", name,
		"--permission-mode", "dontAsk",
		"--verbose",
		"-p", message,
	)
	cmd.Dir = dir

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("claude exited: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// RunOneShotStreaming executes claude with --output-format stream-json,
// parses structured events through onProgress, and returns the final result text.
func RunOneShotStreaming(ctx context.Context, name, dir, message string, onProgress func(StreamEvent)) (string, error) {
	cmd := exec.CommandContext(ctx, "claude",
		"--agent", name,
		"--permission-mode", "dontAsk",
		"--verbose",
		"--output-format", "stream-json",
		"-p", message,
	)
	cmd.Dir = dir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

	var wg sync.WaitGroup
	var result string
	var mu sync.Mutex

	wg.Add(2)

	// Parse JSON stream from stdout
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			for _, ev := range ParseStreamJSON(scanner.Text()) {
				if ev.Kind == EventResult {
					mu.Lock()
					result = ev.Text
					mu.Unlock()
				}
				if onProgress != nil {
					onProgress(ev)
				}
			}
		}
	}()

	// Drain stderr
	go func() {
		defer wg.Done()
		io.Copy(io.Discard, stderr)
	}()

	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("claude exited: %w", err)
	}

	return strings.TrimSpace(result), nil
}

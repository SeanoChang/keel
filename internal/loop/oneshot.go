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

// RunOneShotStreaming executes claude and streams stderr through onProgress while capturing stdout.
func RunOneShotStreaming(ctx context.Context, name, dir, message string, onProgress func(string)) (string, error) {
	cmd := exec.CommandContext(ctx, "claude",
		"--agent", name,
		"--permission-mode", "dontAsk",
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
	var result strings.Builder

	wg.Add(2)

	// Capture stdout for final result
	go func() {
		defer wg.Done()
		io.Copy(&result, stdout)
	}()

	// Stream stderr lines through callback
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			if onProgress != nil {
				onProgress(scanner.Text())
			}
		}
	}()

	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("claude exited: %w", err)
	}

	return strings.TrimSpace(result.String()), nil
}

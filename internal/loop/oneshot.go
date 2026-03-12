package loop

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
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

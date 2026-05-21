// Package mailship validates and ships drafts found in an agent's outbox/.
// It is invoked by the real-time outbox watcher and by the session-end /
// startup sweep so both paths share one shipping implementation.
package mailship

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SeanoChang/keel/internal/delegation"
	"github.com/SeanoChang/keel/internal/workspace"
)

// ShipResult reports the outcome of a single TryShip invocation.
type ShipResult struct {
	Sent   bool
	Reason string // validation failure description (empty when transport failed)
	Err    error  // transport error (cubit nonzero, timeout, missing binary)
}

// shipTimeout caps how long a single cubit send may run.
const shipTimeout = 15 * time.Second

// TryShip validates an outbox entry at <agentDir>/outbox/<relName> and ships it
// via `cubit send`. Returns Sent=true on success. On validation failure, renames
// to <relName>.invalid.md and returns Sent=false with Reason set. On transport
// failure, leaves the file in <agentDir>/mailbox/drafts/<relName>; the next
// sweep or watcher fire retries.
func TryShip(agentName, agentDir, relName string) ShipResult {
	outbox := filepath.Join(agentDir, "outbox")
	srcPath := filepath.Join(outbox, relName)

	info, err := os.Stat(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Already gone — a concurrent watcher or sweep beat us to it.
			// Return a zero ShipResult so the caller's failure-callback gate
			// (Reason != "" || Err != nil) does not fire a false alarm.
			return ShipResult{}
		}
		return ShipResult{Err: fmt.Errorf("stat %s: %w", relName, err)}
	}

	var fmFilePath string
	if info.IsDir() {
		fmFilePath = filepath.Join(srcPath, "mail.md")
		if _, err := os.Stat(fmFilePath); err != nil {
			if os.IsNotExist(err) {
				// Either the watcher's debounce caught the dir between mkdir and
				// mail.md write, or the dir was moved out from under us. In
				// both cases the next watcher fire or sweep retry will handle it.
				return ShipResult{}
			}
			return ShipResult{Err: fmt.Errorf("stat mail.md in %s: %w", relName, err)}
		}
	} else {
		fmFilePath = srcPath
	}

	data, err := os.ReadFile(fmFilePath)
	if err != nil {
		return ShipResult{Err: fmt.Errorf("read %s: %w", relName, err)}
	}

	fm := delegation.ParseFrontmatter(string(data))
	if fm == nil {
		return invalidate(srcPath, relName, info.IsDir(), data, "missing or malformed frontmatter (--- block)")
	}
	if strings.TrimSpace(fm["to"]) == "" {
		return invalidate(srcPath, relName, info.IsDir(), data, "missing 'to:' field")
	}
	if fm["type"] == "delegation" {
		return invalidate(srcPath, relName, info.IsDir(), data, "delegations must use 'cubit delegate', not outbox/")
	}

	// Move outbox → mailbox/drafts so cubit send receives the canonical path.
	dstRel := filepath.Join("mailbox", "drafts", relName)
	dstPath := filepath.Join(agentDir, dstRel)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return ShipResult{Err: fmt.Errorf("ensure drafts dir: %w", err)}
	}
	// If a stale entry exists at the destination from a prior failed run,
	// remove it so os.Rename can succeed atomically.
	_ = os.RemoveAll(dstPath)
	if err := os.Rename(srcPath, dstPath); err != nil {
		return ShipResult{Err: fmt.Errorf("move outbox→drafts %s: %w", relName, err)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), shipTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cubit", "send", dstRel)
	cmd.Dir = agentDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ShipResult{Err: fmt.Errorf("cubit send %s: %w\n%s", relName, err, strings.TrimSpace(string(out)))}
	}

	subject := strings.TrimSpace(fm["subject"])
	if subject == "" {
		subject = relName
	}
	_ = workspace.LogMailboxEvent(agentDir, agentName, "outbox-ship", subject)
	return ShipResult{Sent: true}
}

// invalidate renames a draft to its .invalid form so the watcher and sweep stop
// retrying it, and prepends a reason comment so the agent sees the diagnosis
// inline when it opens the file.
func invalidate(srcPath, relName string, isDir bool, fmData []byte, reason string) ShipResult {
	dstRel := relName + ".invalid.md"
	dstPath := filepath.Join(filepath.Dir(srcPath), dstRel)
	if isDir {
		// Keep the dir form but rename the parent dir so callers can find it.
		dstPath = filepath.Join(filepath.Dir(srcPath), relName+".invalid")
		_ = os.RemoveAll(dstPath)
		if err := os.Rename(srcPath, dstPath); err != nil {
			// Rename failed: do not set Reason (callers branch on Reason to
			// decide the recovery instructions, and the file is still at its
			// original outbox/ path, not at .invalid/). Embed the validation
			// reason in the error so it isn't lost.
			return ShipResult{Err: fmt.Errorf("rename invalid dir (%s): %w", reason, err)}
		}
		// Prepend reason to mail.md so the agent sees it on open.
		mailPath := filepath.Join(dstPath, "mail.md")
		_ = prependReason(mailPath, reason)
		return ShipResult{Reason: reason}
	}
	_ = os.RemoveAll(dstPath)
	if err := os.Rename(srcPath, dstPath); err != nil {
		return ShipResult{Err: fmt.Errorf("rename invalid (%s): %w", reason, err)}
	}
	// fmData was the file's pre-rename contents; rewrite with reason on top.
	_ = prependReason(dstPath, reason)
	_ = fmData // retained for symmetry with isDir branch
	return ShipResult{Reason: reason}
}

func prependReason(path, reason string) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("<!-- keel: %s -->\n", reason)
	return os.WriteFile(path, append([]byte(header), existing...), 0644)
}

// SweepDir walks <agentDir>/outbox/ and invokes TryShip on every eligible entry.
// Skips dotfiles and previously-invalidated entries. Failures are forwarded to
// onFailure (may be nil).
func SweepDir(agentName, agentDir string, onFailure func(reason, relName string, err error)) {
	outbox := filepath.Join(agentDir, "outbox")
	entries, err := os.ReadDir(outbox)
	if err != nil {
		return // missing dir = nothing to sweep
	}
	for _, e := range entries {
		name := e.Name()
		if !Eligible(name, e.IsDir()) {
			continue
		}
		// For directory entries we still need a mail.md inside.
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(outbox, name, "mail.md")); err != nil {
				continue
			}
		}
		res := TryShip(agentName, agentDir, name)
		if !res.Sent && onFailure != nil && (res.Reason != "" || res.Err != nil) {
			onFailure(res.Reason, name, res.Err)
		}
	}
}

// Eligible reports whether an outbox entry name should be considered for shipping.
// It excludes dotfiles, staging directories, and previously-invalidated entries.
func Eligible(name string, isDir bool) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	if strings.HasPrefix(name, "staging-") || strings.HasPrefix(name, ".staging-") {
		return false
	}
	if strings.HasSuffix(name, ".invalid.md") || strings.HasSuffix(name, ".invalid") {
		return false
	}
	if isDir {
		return true
	}
	return strings.HasSuffix(name, ".md")
}

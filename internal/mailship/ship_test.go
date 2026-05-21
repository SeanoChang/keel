package mailship

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeCubit installs a shell-script `cubit` shim on PATH for the test's
// lifetime. The script writes its args to <agentDir>/.cubit-args so tests can
// assert how cubit was invoked, and either exits 0 (succeed=true) or 1.
func withFakeCubit(t *testing.T, succeed bool) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$KEEL_TEST_ARGS\"\n"
	if succeed {
		body += "exit 0\n"
	} else {
		body += "echo 'fake cubit failure' >&2\nexit 1\n"
	}
	path := filepath.Join(dir, "cubit")
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
}

// agentSkeleton sets up a minimal agent dir with outbox/ and mailbox/drafts/.
func agentSkeleton(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"outbox", "mailbox/drafts", "mailbox/sent"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("KEEL_TEST_ARGS", filepath.Join(dir, ".cubit-args"))
	return dir
}

func validDraft(to string) string {
	return "---\nfrom: alice\nto: " + to + "\nsubject: \"hi\"\ntype: notification\ncategory: all\n---\n\nbody\n"
}

func TestTryShip_ValidFlat(t *testing.T) {
	withFakeCubit(t, true)
	dir := agentSkeleton(t)

	if err := os.WriteFile(filepath.Join(dir, "outbox", "hello.md"), []byte(validDraft("bob")), 0644); err != nil {
		t.Fatal(err)
	}
	res := TryShip("alice", dir, "hello.md")
	if !res.Sent {
		t.Fatalf("expected Sent=true, got reason=%q err=%v", res.Reason, res.Err)
	}
	// File should have been moved out of outbox/ into mailbox/drafts/.
	if _, err := os.Stat(filepath.Join(dir, "outbox", "hello.md")); !os.IsNotExist(err) {
		t.Errorf("outbox/hello.md should be gone after ship")
	}
	if _, err := os.Stat(filepath.Join(dir, "mailbox", "drafts", "hello.md")); err != nil {
		t.Errorf("expected mailbox/drafts/hello.md to exist: %v", err)
	}
	// Verify cubit was invoked with the canonical path.
	args, _ := os.ReadFile(filepath.Join(dir, ".cubit-args"))
	if !strings.Contains(string(args), "mailbox/drafts/hello.md") {
		t.Errorf("expected cubit args to include mailbox/drafts/hello.md, got:\n%s", args)
	}
	if !strings.Contains(string(args), "send") {
		t.Errorf("expected cubit args to include 'send', got:\n%s", args)
	}
}

func TestTryShip_ValidDirForm(t *testing.T) {
	withFakeCubit(t, true)
	dir := agentSkeleton(t)

	dirDraft := filepath.Join(dir, "outbox", "report")
	if err := os.MkdirAll(dirDraft, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirDraft, "mail.md"), []byte(validDraft("bob")), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirDraft, "attachment.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	res := TryShip("alice", dir, "report")
	if !res.Sent {
		t.Fatalf("expected Sent=true, got reason=%q err=%v", res.Reason, res.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mailbox", "drafts", "report", "attachment.txt")); err != nil {
		t.Errorf("attachment should follow draft to mailbox/drafts/report/: %v", err)
	}
}

func TestTryShip_MissingTo(t *testing.T) {
	withFakeCubit(t, true)
	dir := agentSkeleton(t)

	bad := "---\nfrom: alice\nsubject: \"hi\"\ntype: notification\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "outbox", "bad.md"), []byte(bad), 0644); err != nil {
		t.Fatal(err)
	}
	res := TryShip("alice", dir, "bad.md")
	if res.Sent {
		t.Fatalf("expected Sent=false")
	}
	if !strings.Contains(res.Reason, "missing 'to:'") {
		t.Errorf("unexpected reason: %q", res.Reason)
	}
	if _, err := os.Stat(filepath.Join(dir, "outbox", "bad.md")); !os.IsNotExist(err) {
		t.Errorf("bad.md should have been renamed")
	}
	invPath := filepath.Join(dir, "outbox", "bad.md.invalid.md")
	data, err := os.ReadFile(invPath)
	if err != nil {
		t.Fatalf("expected invalid file: %v", err)
	}
	if !strings.HasPrefix(string(data), "<!-- keel:") {
		t.Errorf("expected reason header at top, got:\n%s", data)
	}
}

func TestTryShip_DelegationRejected(t *testing.T) {
	withFakeCubit(t, true)
	dir := agentSkeleton(t)

	d := "---\nfrom: alice\nto: bob\nsubject: \"hi\"\ntype: delegation\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "outbox", "del.md"), []byte(d), 0644); err != nil {
		t.Fatal(err)
	}
	res := TryShip("alice", dir, "del.md")
	if res.Sent {
		t.Fatal("delegations must not auto-ship")
	}
	if !strings.Contains(res.Reason, "cubit delegate") {
		t.Errorf("unexpected reason: %q", res.Reason)
	}
}

func TestTryShip_SelfSendAllowed(t *testing.T) {
	withFakeCubit(t, true)
	dir := agentSkeleton(t)
	if err := os.WriteFile(filepath.Join(dir, "outbox", "self.md"), []byte(validDraft("alice")), 0644); err != nil {
		t.Fatal(err)
	}
	res := TryShip("alice", dir, "self.md")
	if !res.Sent {
		t.Fatalf("self-send should be allowed; got reason=%q err=%v", res.Reason, res.Err)
	}
}

func TestTryShip_CubitFailureLeavesFileInDrafts(t *testing.T) {
	withFakeCubit(t, false)
	dir := agentSkeleton(t)
	if err := os.WriteFile(filepath.Join(dir, "outbox", "retry.md"), []byte(validDraft("bob")), 0644); err != nil {
		t.Fatal(err)
	}
	res := TryShip("alice", dir, "retry.md")
	if res.Sent {
		t.Fatal("expected ship failure")
	}
	if res.Err == nil {
		t.Fatal("expected transport error")
	}
	// File should have moved out of outbox/ (we did the rename before invoking cubit),
	// but should remain in mailbox/drafts/ for retry/recovery.
	if _, err := os.Stat(filepath.Join(dir, "mailbox", "drafts", "retry.md")); err != nil {
		t.Errorf("file should remain in mailbox/drafts/ after cubit failure: %v", err)
	}
}

func TestTryShip_StaleEntryNoFailureCallback(t *testing.T) {
	// Simulates the watcher+sweep race: a file the sweep iterates over has
	// already been shipped (moved) by the watcher. The stale TryShip call
	// must not fire a failure callback or it raises a false alarm.
	withFakeCubit(t, true)
	dir := agentSkeleton(t)

	res := TryShip("alice", dir, "ghost.md")
	if res.Sent {
		t.Fatal("nothing should have shipped")
	}
	if res.Err != nil {
		t.Errorf("expected no Err for stale entry (would fire false-alarm failure callback): %v", res.Err)
	}
	if res.Reason != "" {
		t.Errorf("expected no Reason for stale entry: %q", res.Reason)
	}
}

func TestTryShip_DirFormMissingMailNoFailureCallback(t *testing.T) {
	// Parallel to the stale-entry case for directory-form drafts: the dir
	// exists but mail.md is absent (watcher caught the debounce between
	// mkdir and mail.md write, or the dir was moved by another path). Must
	// stay silent rather than firing a "missing mail.md" false alarm.
	withFakeCubit(t, true)
	dir := agentSkeleton(t)
	if err := os.MkdirAll(filepath.Join(dir, "outbox", "half-written"), 0755); err != nil {
		t.Fatal(err)
	}

	res := TryShip("alice", dir, "half-written")
	if res.Sent {
		t.Fatal("nothing should have shipped")
	}
	if res.Err != nil {
		t.Errorf("expected no Err for dir without mail.md: %v", res.Err)
	}
	if res.Reason != "" {
		t.Errorf("expected no Reason: %q", res.Reason)
	}
}

func TestSweepDir_SkipsInvalidAndDotfiles(t *testing.T) {
	withFakeCubit(t, true)
	dir := agentSkeleton(t)

	// One valid file, one already-invalidated, one dotfile.
	os.WriteFile(filepath.Join(dir, "outbox", "ok.md"), []byte(validDraft("bob")), 0644)
	os.WriteFile(filepath.Join(dir, "outbox", "stale.md.invalid.md"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "outbox", ".hidden"), []byte("x"), 0644)

	var failures int
	SweepDir("alice", dir, func(reason, relName string, err error) { failures++ })
	if failures != 0 {
		t.Errorf("expected 0 failures, got %d", failures)
	}
	if _, err := os.Stat(filepath.Join(dir, "mailbox", "drafts", "ok.md")); err != nil {
		t.Errorf("ok.md should have been shipped: %v", err)
	}
	// Invalid + hidden should still be present, untouched.
	for _, name := range []string{"stale.md.invalid.md", ".hidden"} {
		if _, err := os.Stat(filepath.Join(dir, "outbox", name)); err != nil {
			t.Errorf("%s should remain after sweep: %v", name, err)
		}
	}
}

func TestEligible(t *testing.T) {
	cases := []struct {
		name  string
		isDir bool
		want  bool
	}{
		{"foo.md", false, true},
		{"foo", true, true},
		{".staging-x", true, false},
		{".hidden.md", false, false},
		{"foo.txt", false, false},
		{"foo.md.invalid.md", false, false},
		{"foo.invalid", true, false},
	}
	for _, c := range cases {
		if got := Eligible(c.name, c.isDir); got != c.want {
			t.Errorf("Eligible(%q, %v) = %v, want %v", c.name, c.isDir, got, c.want)
		}
	}
}

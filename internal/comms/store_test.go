package comms

import (
	"path/filepath"
	"testing"
)

func TestStateStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comms.json")

	s, err := OpenStateStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if got, ok := s.Get("channel.123"); ok || got != "" {
		t.Fatalf("expected empty, got %q ok=%v", got, ok)
	}

	if err := s.Set("channel.123", "msg-abc"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.Set("channel.456", "msg-def"); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Reopen — should round-trip from disk.
	s2, err := OpenStateStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got, ok := s2.Get("channel.123"); !ok || got != "msg-abc" {
		t.Fatalf("got %q ok=%v, want msg-abc/true", got, ok)
	}
	if got, ok := s2.Get("channel.456"); !ok || got != "msg-def" {
		t.Fatalf("got %q ok=%v, want msg-def/true", got, ok)
	}
}

func TestStateStore_OpenMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doesnt-exist.json")

	s, err := OpenStateStore(path)
	if err != nil {
		t.Fatalf("open missing should be ok, got %v", err)
	}
	if _, ok := s.Get("anything"); ok {
		t.Fatal("expected empty store")
	}
}

func TestStateStore_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comms.json")
	s, _ := OpenStateStore(path)
	_ = s.Set("k", "v1")
	_ = s.Set("k", "v2")
	got, _ := s.Get("k")
	if got != "v2" {
		t.Fatalf("got %q, want v2", got)
	}
}

// Package migrate applies idempotent workspace migrations when keel is updated.
package migrate

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/SeanoChang/keel/internal/workspace"
)

// AgentDir runs all migrations for a single agent workspace directory.
func AgentDir(dir string) error {
	if err := m001EnsureFiles(dir); err != nil {
		return fmt.Errorf("m001: %w", err)
	}
	return nil
}

// ScanAndMigrate finds all agent directories under root and migrates each.
// root defaults to ~/.ark if empty.
func ScanAndMigrate(root string) error {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home dir: %w", err)
		}
		root = filepath.Join(home, ".ark")
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[migrate] %s does not exist, nothing to migrate", root)
			return nil
		}
		return fmt.Errorf("read %s: %w", root, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		log.Printf("[migrate] %s", dir)
		if err := AgentDir(dir); err != nil {
			log.Printf("[migrate] warning: %s: %v", dir, err)
		}
	}
	return nil
}

// m001EnsureFiles ensures all required workspace files exist with safe defaults.
func m001EnsureFiles(dir string) error {
	required := []struct {
		name    string
		content string
	}{
		{"GOALS.md", ""},
		{"MEMORY.md", ""},
		{"log.md", ""},
		{"PROGRAM.md", workspace.DefaultProgram + "\n"},
	}
	for _, f := range required {
		path := filepath.Join(dir, f.name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			log.Printf("[migrate] creating %s", path)
			if err := os.WriteFile(path, []byte(f.content), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

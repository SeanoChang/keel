package discord

import (
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input string
		isCmd bool
		cmd   string
		args  string
	}{
		{"!status", true, "status", ""},
		{"!goals", true, "goals", ""},
		{"!log 10", true, "log", "10"},
		{"!stop", true, "stop", ""},
		{"hello world", false, "", ""},
		{"", false, "", ""},
		{"! ", true, "", ""},
	}
	for _, tt := range tests {
		isCmd, cmd, args := ParseCommand(tt.input)
		if isCmd != tt.isCmd {
			t.Errorf("ParseCommand(%q) isCmd = %v, want %v", tt.input, isCmd, tt.isCmd)
		}
		if cmd != tt.cmd {
			t.Errorf("ParseCommand(%q) cmd = %q, want %q", tt.input, cmd, tt.cmd)
		}
		if args != tt.args {
			t.Errorf("ParseCommand(%q) args = %q, want %q", tt.input, args, tt.args)
		}
	}
}

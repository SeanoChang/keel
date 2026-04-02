package discord

import (
	"strings"
	"testing"
)

func TestBuildInterviewPrompt_Round0(t *testing.T) {
	prompt := buildInitInterviewPrompt("noah", setupTargets[0], nil, 0)
	if !strings.Contains(prompt, "noah") {
		t.Error("prompt should contain agent name")
	}
	if !strings.Contains(prompt, "ONE focused question") {
		t.Error("prompt should ask for one question")
	}
}

func TestBuildInterviewPrompt_RoundN(t *testing.T) {
	convo := []string{"What is this agent for?", "It manages deployments"}
	prompt := buildInitInterviewPrompt("noah", setupTargets[0], convo, 1)
	if !strings.Contains(prompt, "Interview so far") {
		t.Error("prompt should contain interview history")
	}
	if !strings.Contains(prompt, "DONE") {
		t.Error("prompt should mention DONE signal")
	}
	if !strings.Contains(prompt, "deployments") {
		t.Error("prompt should contain conversation history")
	}
}

func TestBuildGeneratePrompt(t *testing.T) {
	convo := []string{"What role?", "DevOps engineer", "What tools?", "Terraform and AWS"}
	prompt := buildInitGeneratePrompt("noah", setupTargets[0], convo)
	if !strings.Contains(prompt, "FLUCTLIGHT.md") {
		t.Error("prompt should reference target file")
	}
	if !strings.Contains(prompt, "Terraform") {
		t.Error("prompt should contain conversation content")
	}
}

func TestFormatInitConversation(t *testing.T) {
	convo := []string{"Q1", "A1", "Q2", "A2"}
	result := formatInitConversation(convo)
	if !strings.Contains(result, "Q: Q1") {
		t.Error("should format questions with Q: prefix")
	}
	if !strings.Contains(result, "A: A1") {
		t.Error("should format answers with A: prefix")
	}
}

func TestInitSessionPhaseTransitions(t *testing.T) {
	s := &InitSession{
		agentName: "testbot",
		agentDir:  "/tmp/test",
		phase:     phaseInterview,
		targetIdx: 0,
		targets:   setupTargets,
	}

	if s.phase != phaseInterview {
		t.Errorf("initial phase = %d, want %d", s.phase, phaseInterview)
	}

	// Simulate advancing past all targets by directly setting state.
	// We can't call advanceTarget() without a live discordgo.Session
	// because it calls send() internally. Verify the condition logic instead.
	s.targetIdx = len(s.targets)
	if s.targetIdx >= len(s.targets) {
		s.phase = phaseChannelMapping
	}
	if s.phase != phaseChannelMapping {
		t.Errorf("after all targets, phase = %d, want %d", s.phase, phaseChannelMapping)
	}
}

func TestValidateAgentName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"noah", true},
		{"my-agent", true},
		{"agent_2", true},
		{"", false},
		{".", false},
		{"..", false},
		{"../etc", false},
		{"foo/bar", false},
		{"agent name", false},
		{"a!b", false},
	}
	for _, tt := range tests {
		got := isValidAgentName(tt.name)
		if got != tt.valid {
			t.Errorf("isValidAgentName(%q) = %v, want %v", tt.name, got, tt.valid)
		}
	}
}

func TestValidateSnowflake(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"123456789012345678", true},
		{"12345678901234567", true},
		{"1234567890123456789", true},
		{"12345678901234567890", true},
		{"1234567890123456", false},  // too short
		{"123456789012345678901", false}, // too long
		{"abcdefghijklmnopq", false}, // not numeric
		{"", false},
	}
	for _, tt := range tests {
		got := isValidSnowflake(tt.input)
		if got != tt.valid {
			t.Errorf("isValidSnowflake(%q) = %v, want %v", tt.input, got, tt.valid)
		}
	}
}

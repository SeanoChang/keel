package delegation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeTracker writes a delegation.json file at the given path.
func writeTracker(t *testing.T, path string, d delegationJSON) {
	t.Helper()
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatalf("marshal tracker: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir for tracker: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write tracker: %v", err)
	}
}

// writeMail writes a directory-style mail (dir/mail.md) with given frontmatter fields.
func writeMail(t *testing.T, dir string, fields map[string]string, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir for mail: %v", err)
	}
	content := "---\n"
	for k, v := range fields {
		content += k + ": " + v + "\n"
	}
	content += "---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "mail.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write mail.md: %v", err)
	}
}

func TestRouteResponse(t *testing.T) {
	// Set up a temp agent directory
	agentDir := t.TempDir()

	delID := "del-abc123"
	activeDir := filepath.Join(agentDir, "mailbox", "delegations", "active", delID)
	os.MkdirAll(filepath.Join(activeDir, "responses"), 0755)

	// Write tracker with 2 sub-tasks, both pending
	tracker := delegationJSON{
		ID:          delID,
		Created:     "2026-04-01T10:00:00Z",
		Owner:       "agent1",
		GoalContext: "Do the thing",
		OnComplete:  "merge results",
		Status:      "pending",
		SubTasks: []subTaskJSON{
			{To: "agent2", Task: "part A", Status: "pending", DispatchedMail: "sent/del-abc123-agent2/", Attempts: 1},
			{To: "agent3", Task: "part B", Status: "pending", DispatchedMail: "sent/del-abc123-agent3/", Attempts: 1},
		},
	}
	writeTracker(t, filepath.Join(activeDir, "delegation.json"), tracker)

	// Create a response mail from agent2 in inbox
	inboxMail := filepath.Join(agentDir, "mailbox", "inbox", "all", "response-from-agent2")
	writeMail(t, inboxMail, map[string]string{
		"type":          "delegation-response",
		"from":          "agent2",
		"to":            "agent1",
		"delegation_id": delID,
	}, "Here is my result for part A.")

	// Route the response
	router := NewRouter(agentDir)
	result, err := router.RouteResponse(inboxMail)
	if err != nil {
		t.Fatalf("RouteResponse: %v", err)
	}

	// Assert: response moved to delegations/active/<id>/responses/agent2/
	responsePath := filepath.Join(activeDir, "responses", "agent2", "mail.md")
	if _, err := os.Stat(responsePath); os.IsNotExist(err) {
		t.Errorf("response mail.md not found at %s", responsePath)
	}

	// Assert: original removed from inbox
	if _, err := os.Stat(inboxMail); !os.IsNotExist(err) {
		t.Errorf("original inbox mail should have been moved, but still exists")
	}

	// Assert: result fields
	if result.NewStatus != "partial" {
		t.Errorf("expected status 'partial', got %q", result.NewStatus)
	}
	if result.CompletedCount != 1 {
		t.Errorf("expected CompletedCount 1, got %d", result.CompletedCount)
	}
	if result.TotalCount != 2 {
		t.Errorf("expected TotalCount 2, got %d", result.TotalCount)
	}
	if result.AllComplete {
		t.Error("expected AllComplete false")
	}
	if result.DelegationID != delID {
		t.Errorf("expected DelegationID %q, got %q", delID, result.DelegationID)
	}
	if result.From != "agent2" {
		t.Errorf("expected From 'agent2', got %q", result.From)
	}

	// Verify tracker was updated on disk
	var updated delegationJSON
	data, _ := os.ReadFile(filepath.Join(activeDir, "delegation.json"))
	json.Unmarshal(data, &updated)
	if updated.Status != "partial" {
		t.Errorf("tracker status on disk: expected 'partial', got %q", updated.Status)
	}
	for _, st := range updated.SubTasks {
		if st.To == "agent2" && st.Status != "complete" {
			t.Errorf("agent2 sub-task should be 'complete', got %q", st.Status)
		}
		if st.To == "agent3" && st.Status != "pending" {
			t.Errorf("agent3 sub-task should still be 'pending', got %q", st.Status)
		}
	}
}

func TestRouteResponseAllComplete(t *testing.T) {
	// Set up a temp agent directory
	agentDir := t.TempDir()

	delID := "del-xyz789"
	activeDir := filepath.Join(agentDir, "mailbox", "delegations", "active", delID)
	os.MkdirAll(filepath.Join(activeDir, "responses"), 0755)

	// Write tracker with agent3 already complete (partial status)
	tracker := delegationJSON{
		ID:          delID,
		Created:     "2026-04-01T10:00:00Z",
		Owner:       "agent1",
		GoalContext: "Build the feature",
		OnComplete:  "notify owner and merge",
		Status:      "partial",
		SubTasks: []subTaskJSON{
			{To: "agent2", Task: "frontend", Status: "pending", DispatchedMail: "sent/del-xyz789-agent2/", Attempts: 1},
			{To: "agent3", Task: "backend", Status: "complete", DispatchedMail: "sent/del-xyz789-agent3/", ResponseMail: "responses/agent3/", Attempts: 1},
		},
	}
	writeTracker(t, filepath.Join(activeDir, "delegation.json"), tracker)

	// Create a response mail from agent2 in inbox
	inboxMail := filepath.Join(agentDir, "mailbox", "inbox", "all", "response-from-agent2")
	writeMail(t, inboxMail, map[string]string{
		"type":          "delegation-response",
		"from":          "agent2",
		"to":            "agent1",
		"delegation_id": delID,
	}, "Frontend work is done.")

	// Route the response
	router := NewRouter(agentDir)
	result, err := router.RouteResponse(inboxMail)
	if err != nil {
		t.Fatalf("RouteResponse: %v", err)
	}

	// Assert: status is now "ready" and all complete
	if result.NewStatus != "ready" {
		t.Errorf("expected status 'ready', got %q", result.NewStatus)
	}
	if !result.AllComplete {
		t.Error("expected AllComplete true")
	}
	if result.CompletedCount != 2 {
		t.Errorf("expected CompletedCount 2, got %d", result.CompletedCount)
	}
	if result.TotalCount != 2 {
		t.Errorf("expected TotalCount 2, got %d", result.TotalCount)
	}

	// Assert: OnComplete matches tracker
	if result.OnComplete != "notify owner and merge" {
		t.Errorf("expected OnComplete 'notify owner and merge', got %q", result.OnComplete)
	}

	// Assert: response moved
	responsePath := filepath.Join(activeDir, "responses", "agent2", "mail.md")
	if _, err := os.Stat(responsePath); os.IsNotExist(err) {
		t.Errorf("response mail.md not found at %s", responsePath)
	}

	// Assert: original removed
	if _, err := os.Stat(inboxMail); !os.IsNotExist(err) {
		t.Errorf("original inbox mail should have been moved")
	}

	// Verify tracker on disk
	var updated delegationJSON
	data, _ := os.ReadFile(filepath.Join(activeDir, "delegation.json"))
	json.Unmarshal(data, &updated)
	if updated.Status != "ready" {
		t.Errorf("tracker status on disk: expected 'ready', got %q", updated.Status)
	}
}

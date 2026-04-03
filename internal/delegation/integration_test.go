package delegation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFullDelegationFlow(t *testing.T) {
	agentDir := t.TempDir()
	delID := "del-20260402T1430-integration"

	// Setup mailbox structure
	for _, d := range []string{
		"mailbox/inbox/priority",
		"mailbox/delegations/active/" + delID + "/responses",
	} {
		os.MkdirAll(filepath.Join(agentDir, d), 0755)
	}

	// Step 1: Write tracker (simulates cubit delegate)
	tracker := delegationJSON{
		ID:         delID,
		Owner:      "agent1",
		OnComplete: "Synthesize and deliver",
		Status:     "pending",
		SubTasks: []subTaskJSON{
			{To: "agent2", Task: "research pricing", Status: "pending", Attempts: 1},
			{To: "agent3", Task: "review financials", Status: "pending", Attempts: 1},
		},
	}
	data, _ := json.MarshalIndent(tracker, "", "  ")
	os.WriteFile(filepath.Join(agentDir, "mailbox", "delegations", "active", delID, "delegation.json"), data, 0644)

	router := NewRouter(agentDir)

	// Step 2: agent3 responds first
	resp3 := filepath.Join(agentDir, "mailbox", "inbox", "priority", "2026-04-02T14-35-00-agent3-response")
	os.MkdirAll(resp3, 0755)
	os.WriteFile(filepath.Join(resp3, "mail.md"), []byte("---\nfrom: agent3\nto: agent1\ntype: delegation-response\ndelegation_id: "+delID+"\nsubject: Results\n---\n\nFinancials report."), 0644)
	os.WriteFile(filepath.Join(resp3, "financials.csv"), []byte("data"), 0644)

	r1, err := router.RouteResponse(resp3)
	if err != nil {
		t.Fatalf("RouteResponse agent3: %v", err)
	}
	if r1.NewStatus != "partial" {
		t.Errorf("after agent3: status = %q, want partial", r1.NewStatus)
	}
	if r1.AllComplete {
		t.Error("should not be AllComplete after first response")
	}
	// Verify attachment moved
	if _, err := os.Stat(filepath.Join(agentDir, "mailbox", "delegations", "active", delID, "responses", "agent3", "financials.csv")); err != nil {
		t.Error("attachment not moved with response")
	}

	// Step 3: agent2 responds
	resp2 := filepath.Join(agentDir, "mailbox", "inbox", "priority", "2026-04-02T14-55-00-agent2-response")
	os.MkdirAll(resp2, 0755)
	os.WriteFile(filepath.Join(resp2, "mail.md"), []byte("---\nfrom: agent2\nto: agent1\ntype: delegation-response\ndelegation_id: "+delID+"\nsubject: Results\n---\n\nPricing data."), 0644)

	r2, err := router.RouteResponse(resp2)
	if err != nil {
		t.Fatalf("RouteResponse agent2: %v", err)
	}
	if r2.NewStatus != "ready" {
		t.Errorf("after agent2: status = %q, want ready", r2.NewStatus)
	}
	if !r2.AllComplete {
		t.Error("should be AllComplete after both responses")
	}
	if r2.OnComplete != "Synthesize and deliver" {
		t.Errorf("OnComplete = %q", r2.OnComplete)
	}
}

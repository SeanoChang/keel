package delegation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RouteResult contains the outcome of routing a delegation response.
type RouteResult struct {
	DelegationID   string
	From           string
	NewStatus      string
	CompletedCount int
	TotalCount     int
	AllComplete    bool
	OnComplete     string
}

// Router handles delegation-response mail routing.
type Router struct {
	agentDir string
}

func NewRouter(agentDir string) *Router {
	return &Router{agentDir: agentDir}
}

// CheckResponse reads a mail's frontmatter and returns (delegationID, from, true)
// if it's a delegation-response, or ("", "", false) otherwise.
func (r *Router) CheckResponse(mailPath string) (string, string, bool) {
	var data []byte
	var err error

	info, statErr := os.Stat(mailPath)
	if statErr != nil {
		return "", "", false
	}

	if info.IsDir() {
		data, err = os.ReadFile(filepath.Join(mailPath, "mail.md"))
	} else {
		data, err = os.ReadFile(mailPath)
	}
	if err != nil {
		return "", "", false
	}

	fm := parseFrontmatter(string(data))
	if fm["type"] != "delegation-response" {
		return "", "", false
	}
	delID := fm["delegation_id"]
	from := fm["from"]
	if delID == "" || from == "" {
		return "", "", false
	}
	return delID, from, true
}

// RouteResponse moves a delegation-response mail from inbox to the delegation's
// responses directory and updates the tracker.
func (r *Router) RouteResponse(mailPath string) (*RouteResult, error) {
	delID, from, ok := r.CheckResponse(mailPath)
	if !ok {
		return nil, fmt.Errorf("not a delegation-response or missing fields")
	}

	activeDir := filepath.Join(r.agentDir, "mailbox", "delegations", "active")
	delDir := filepath.Join(activeDir, delID)
	trackerPath := filepath.Join(delDir, "delegation.json")

	trackerData, err := os.ReadFile(trackerPath)
	if err != nil {
		return nil, fmt.Errorf("read tracker %s: %w", delID, err)
	}
	var d delegationJSON
	if err := json.Unmarshal(trackerData, &d); err != nil {
		return nil, fmt.Errorf("parse tracker: %w", err)
	}

	// Move mail to responses/<from>/
	// Note: mail is moved before the tracker is updated. If the tracker write
	// fails, the mail is already in responses/ but the tracker shows "pending."
	// This self-heals on retry: os.RemoveAll clears the orphaned response,
	// and the next response delivery re-triggers the full flow.
	responseDst := filepath.Join(delDir, "responses", from)
	os.RemoveAll(responseDst) // clean old response if retry
	if err := os.Rename(mailPath, responseDst); err != nil {
		return nil, fmt.Errorf("move response: %w", err)
	}

	// Update sub-task
	for i := range d.SubTasks {
		if d.SubTasks[i].To == from {
			d.SubTasks[i].Status = "complete"
			d.SubTasks[i].ResponseMail = "responses/" + from + "/"
			break
		}
	}

	// Recalculate status
	complete := 0
	for _, st := range d.SubTasks {
		if st.Status == "complete" {
			complete++
		}
	}
	total := len(d.SubTasks)

	switch {
	case complete == total:
		d.Status = "ready"
	case complete > 0:
		d.Status = "partial"
	default:
		d.Status = "pending"
	}

	// Write updated tracker atomically
	updatedData, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal tracker: %w", err)
	}
	updatedData = append(updatedData, '\n')
	tmp := filepath.Join(delDir, ".tmp-delegation.json")
	if err := os.WriteFile(tmp, updatedData, 0644); err != nil {
		return nil, fmt.Errorf("write tracker temp: %w", err)
	}
	if err := os.Rename(tmp, trackerPath); err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("rename tracker: %w", err)
	}

	return &RouteResult{
		DelegationID:   delID,
		From:           from,
		NewStatus:      d.Status,
		CompletedCount: complete,
		TotalCount:     total,
		AllComplete:    complete == total,
		OnComplete:     d.OnComplete,
	}, nil
}

// delegationJSON mirrors the cubit delegation struct for keel-side reading.
type delegationJSON struct {
	ID          string        `json:"id"`
	Created     string        `json:"created"`
	Owner       string        `json:"owner"`
	GoalContext string        `json:"goal_context"`
	OnComplete  string        `json:"on_complete"`
	Status      string        `json:"status"`
	SubTasks    []subTaskJSON `json:"sub_tasks"`
}

type subTaskJSON struct {
	To             string `json:"to"`
	Task           string `json:"task"`
	Status         string `json:"status"`
	DispatchedMail string `json:"dispatched_mail"`
	ResponseMail   string `json:"response_mail,omitempty"`
	Attempts       int    `json:"attempts"`
}

// parseFrontmatter is a minimal YAML frontmatter parser for mail.md files.
func parseFrontmatter(content string) map[string]string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return nil
	}
	rest := content[3:]
	rest = strings.TrimLeft(rest, "\n\r")
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return nil
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		fields[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return fields
}

// Package comms tracks agent-to-agent message and delegation activity for the
// Discord comms dashboard.
package comms

import "time"

type Kind string

const (
	KindMessage    Kind = "message"
	KindDelegation Kind = "delegation"
)

type Status string

const (
	StatusSent      Status = "sent"
	StatusDelivered Status = "delivered"
	StatusFailed    Status = "failed"
	StatusPending   Status = "pending"
	StatusComplete  Status = "complete"
)

// Event is one row in the comms dashboard. For KindMessage it represents a
// single piece of agent-to-agent mail; for KindDelegation it represents the
// aggregate lifecycle of a delegation tracker.
type Event struct {
	ID        string
	Kind      Kind
	From      string
	To        string
	Subject   string
	Category  string // important | priority | all (empty for delegation)
	Status    Status
	Timestamp time.Time
	Failure   string // populated when Status == StatusFailed

	// Delegation-only.
	DelegationID  string
	SubTasksDone  int
	SubTasksTotal int
}

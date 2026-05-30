package wire

import (
	"encoding/json"
	"fmt"
)

type Kind string
type Priority string

const (
	KindNotify  Kind = "notify"
	KindRequest Kind = "request"
	KindReply   Kind = "reply"

	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

// Body is the application payload; broker-opaque.
type Body struct {
	Subject     string         `json:"subject,omitempty"`
	Text        string         `json:"text,omitempty"`
	Attachments []string       `json:"attachments,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
}

// Envelope is the one wire message. From/TS are STAMPED by the broker.
type Envelope struct {
	ID            string   `json:"id"`
	From          string   `json:"from"`
	To            string   `json:"to"`
	Kind          Kind     `json:"kind"`
	Intent        string   `json:"intent,omitempty"`
	CorrelationID string   `json:"correlation_id,omitempty"`
	ReplyCap      string   `json:"reply_cap,omitempty"`
	TTL           string   `json:"ttl,omitempty"`
	TS            string   `json:"ts,omitempty"`
	Priority      Priority `json:"priority,omitempty"`
	Body          Body     `json:"body"`
}

func (e Envelope) Marshal() ([]byte, error) { return json.Marshal(e) }

func Unmarshal(b []byte) (Envelope, error) {
	var e Envelope
	err := json.Unmarshal(b, &e)
	return e, err
}

func (e Envelope) Validate() error {
	if e.From == "" || e.To == "" {
		return fmt.Errorf("wire: envelope requires from and to")
	}
	switch e.Kind {
	case KindNotify, KindRequest, KindReply:
	default:
		return fmt.Errorf("wire: invalid kind %q", e.Kind)
	}
	if e.ID == "" {
		return fmt.Errorf("wire: envelope requires id")
	}
	return nil
}

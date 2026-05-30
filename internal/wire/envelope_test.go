package wire

import (
	"strings"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	e := Envelope{
		ID:       "msg-20260530T120000Z-alice-ab12",
		From:     "alice",
		To:       "neo",
		Kind:     KindNotify,
		Intent:   "handoff",
		TTL:      "72h",
		TS:       "2026-05-30T12:00:00Z",
		Priority: PriorityNormal,
		Body:     Body{Subject: "hi", Text: "ping"},
	}

	b, err := e.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != "msg-20260530T120000Z-alice-ab12" {
		t.Errorf("ID = %q, want %q", got.ID, "msg-20260530T120000Z-alice-ab12")
	}
	if got.Kind != KindNotify {
		t.Errorf("Kind = %q, want %q", got.Kind, KindNotify)
	}
	if got.Body.Subject != "hi" {
		t.Errorf("Body.Subject = %q, want %q", got.Body.Subject, "hi")
	}
}

func TestEnvelopeValidateRejectsBadKind(t *testing.T) {
	e := Envelope{ID: "x", From: "a", To: "b", Kind: "bogus"}
	if err := e.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for bad kind")
	}
}

func TestEnvelopeValidateRequiresToFrom(t *testing.T) {
	e := Envelope{ID: "x", Kind: KindNotify}
	if err := e.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for missing to/from")
	}
}

// TestValidateOK is a positive case: a well-formed envelope returns nil.
func TestValidateOK(t *testing.T) {
	e := Envelope{
		ID:   "msg-1",
		From: "alice",
		To:   "neo",
		Kind: KindNotify,
		Body: Body{Text: "ping"},
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("expected valid envelope, got error: %v", err)
	}
}

// TestValidateInvalidPriority ensures the lenient Go validator rejects an
// unknown priority, matching Rust's strict enum.
func TestValidateInvalidPriority(t *testing.T) {
	e := Envelope{
		ID:       "msg-1",
		From:     "alice",
		To:       "neo",
		Kind:     KindNotify,
		Priority: Priority("urgent"),
		Body:     Body{Text: "ping"},
	}
	if err := e.Validate(); err == nil {
		t.Fatal("expected error for invalid priority, got nil")
	}
}

// TestDefaultPriorityOmitted is a golden-shape assertion: a default-priority
// envelope's JSON contains no "priority" key (omitempty), so it stays off the
// wire and matches the Rust serializer for byte-identical fixtures.
func TestDefaultPriorityOmitted(t *testing.T) {
	e := Envelope{
		ID:   "msg-1",
		From: "alice",
		To:   "neo",
		Kind: KindNotify,
		Body: Body{Text: "ping"},
	}
	b, err := e.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "priority") {
		t.Fatalf("default-priority envelope should omit priority key, got: %s", string(b))
	}
}

// TestHighPriorityPresent sanity: a non-default priority does serialize.
func TestHighPriorityPresent(t *testing.T) {
	e := Envelope{
		ID:       "msg-1",
		From:     "alice",
		To:       "neo",
		Kind:     KindNotify,
		Priority: PriorityHigh,
		Body:     Body{Text: "ping"},
	}
	b, err := e.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"priority":"high"`) {
		t.Fatalf("high-priority envelope should include priority key, got: %s", string(b))
	}
}

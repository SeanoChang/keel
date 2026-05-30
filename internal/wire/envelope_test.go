package wire

import "testing"

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

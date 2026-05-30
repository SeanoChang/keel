package wire

import (
	"encoding/json"
	"testing"
)

func TestRPCRequestRoundTrip(t *testing.T) {
	req := NewRequest("req-1", "nark/search", map[string]any{"query": "blake3"})

	b, err := req.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := ParseRequest(b)
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}

	if got.Method != "nark/search" {
		t.Errorf("Method = %q, want %q", got.Method, "nark/search")
	}
	if got.JSONRPC != "2.0" {
		t.Errorf("JSONRPC = %q, want %q", got.JSONRPC, "2.0")
	}
}

func TestRPCErrorShape(t *testing.T) {
	resp := ErrorResponse("req-1", 4002, "Forbidden", nil)

	b, err := resp.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := ParseResponse(b)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}

	if got.Error == nil {
		t.Fatal("Error = nil, want non-nil")
	}
	if got.Error.Code != 4002 {
		t.Errorf("Error.Code = %d, want %d", got.Error.Code, 4002)
	}
}

// TestRPCResultResponse round-trips a non-trivial result and asserts the
// response carries no error and that Result decodes back to the input.
func TestRPCResultResponse(t *testing.T) {
	type payload struct {
		Name  string   `json:"name"`
		Count int      `json:"count"`
		Tags  []string `json:"tags"`
	}
	in := payload{Name: "neo", Count: 3, Tags: []string{"a", "b"}}

	resp := ResultResponse("req-1", in)

	if resp.Error != nil {
		t.Fatalf("expected nil error, got %+v", resp.Error)
	}
	if resp.JSONRPC != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %q", resp.JSONRPC)
	}
	if resp.ID != "req-1" {
		t.Fatalf("expected id req-1, got %q", resp.ID)
	}

	var got payload
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.Name != in.Name || got.Count != in.Count || len(got.Tags) != len(in.Tags) {
		t.Fatalf("result round-trip mismatch: got %+v want %+v", got, in)
	}
	for i := range in.Tags {
		if got.Tags[i] != in.Tags[i] {
			t.Fatalf("tag %d mismatch: got %q want %q", i, got.Tags[i], in.Tags[i])
		}
	}
}

// TestRPCResultResponseMarshalFailure ensures a result that cannot be
// marshalled produces a well-formed internal-error response (never a
// response with neither result nor error).
func TestRPCResultResponseMarshalFailure(t *testing.T) {
	// A channel cannot be marshalled to JSON.
	resp := ResultResponse("req-2", make(chan int))

	if resp.Error == nil {
		t.Fatalf("expected internal-error response, got result %s", string(resp.Result))
	}
	if resp.Error.Code != -32603 {
		t.Fatalf("expected code -32603, got %d", resp.Error.Code)
	}
	if resp.Result != nil {
		t.Fatalf("expected nil result on marshal failure, got %s", string(resp.Result))
	}
}

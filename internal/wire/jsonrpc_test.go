package wire

import "testing"

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

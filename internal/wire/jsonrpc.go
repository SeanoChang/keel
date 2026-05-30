package wire

import "encoding/json"

type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type RPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

func NewRequest(id, method string, params any) RPCRequest {
	// Swallow is safe here: params is caller-controlled, and the recipient
	// validates the request before acting on it.
	p, _ := json.Marshal(params)
	return RPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: p}
}
func (r RPCRequest) Marshal() ([]byte, error) { return json.Marshal(r) }
func ParseRequest(b []byte) (RPCRequest, error) {
	var r RPCRequest
	e := json.Unmarshal(b, &r)
	return r, e
}

func ErrorResponse(id string, code int, msg string, data any) RPCResponse {
	return RPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg, Data: data}}
}
func ResultResponse(id string, result any) RPCResponse {
	r, err := json.Marshal(result)
	if err != nil {
		return ErrorResponse(id, -32603, "internal error: marshal result", err.Error())
	}
	return RPCResponse{JSONRPC: "2.0", ID: id, Result: r}
}
func (r RPCResponse) Marshal() ([]byte, error) { return json.Marshal(r) }
func ParseResponse(b []byte) (RPCResponse, error) {
	var r RPCResponse
	e := json.Unmarshal(b, &r)
	return r, e
}

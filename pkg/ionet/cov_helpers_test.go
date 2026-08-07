package ionet

// Test helpers shared across the cov_*_test.go files in this package.
// This file intentionally contains no test functions of its own.

// mockHTTPClient is a controllable stand-in for HTTPClient used to exercise
// Client's business logic (validation, endpoint construction, response
// parsing/mapping) without touching real sockets.
type mockHTTPClient struct {
	// DoFunc, when set, is invoked for every call to Do.
	DoFunc func(req *HTTPRequest) (*HTTPResponse, error)
	// Requests records every request passed to Do, in call order.
	Requests []*HTTPRequest
	// CallCount tracks how many times Do was invoked.
	CallCount int
}

func (m *mockHTTPClient) Do(req *HTTPRequest) (*HTTPResponse, error) {
	m.CallCount++
	m.Requests = append(m.Requests, req)
	if m.DoFunc != nil {
		return m.DoFunc(req)
	}
	return &HTTPResponse{StatusCode: 200, Body: []byte("{}")}, nil
}

// jsonResponse is a small helper for constructing a successful HTTPResponse
// whose body is the given raw JSON text.
func jsonResponse(status int, body string) *HTTPResponse {
	return &HTTPResponse{StatusCode: status, Body: []byte(body)}
}

// newTestClient builds a Client wired to the given mock transport.
func newTestClient(m *mockHTTPClient) *Client {
	return NewClientWithConfig("test-api-key", "https://unit-test.invalid", m)
}

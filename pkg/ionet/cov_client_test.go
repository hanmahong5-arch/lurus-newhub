package ionet

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// --- NewDefaultHTTPClient / DefaultHTTPClient.Do -------------------------

func TestNewDefaultHTTPClient_SetsTimeout(t *testing.T) {
	c := NewDefaultHTTPClient(7 * time.Second)
	if c == nil || c.client == nil {
		t.Fatal("expected non-nil client with underlying http.Client")
	}
	if c.client.Timeout != 7*time.Second {
		t.Fatalf("expected timeout 7s, got %v", c.client.Timeout)
	}
}

func TestDefaultHTTPClient_Do_Success(t *testing.T) {
	var gotMethod, gotPath, gotHeader, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHeader = r.Header.Get("X-Custom")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		// Set the same header twice to verify only the first value survives.
		w.Header().Add("X-Multi", "first")
		w.Header().Add("X-Multi", "second")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewDefaultHTTPClient(5 * time.Second)
	resp, err := c.Do(&HTTPRequest{
		Method:  http.MethodPost,
		URL:     srv.URL + "/widgets",
		Headers: map[string]string{"X-Custom": "abc"},
		Body:    []byte(`{"a":1}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("server saw method %q, want POST", gotMethod)
	}
	if gotPath != "/widgets" {
		t.Errorf("server saw path %q, want /widgets", gotPath)
	}
	if gotHeader != "abc" {
		t.Errorf("server saw X-Custom %q, want abc", gotHeader)
	}
	if gotBody != `{"a":1}` {
		t.Errorf("server saw body %q, want {\"a\":1}", gotBody)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode = %d, want 201", resp.StatusCode)
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Errorf("Body = %q, want {\"ok\":true}", resp.Body)
	}
	// Only the first value of a multi-valued header must be kept.
	if resp.Headers["X-Multi"] != "first" {
		t.Errorf("Headers[X-Multi] = %q, want first (only first value retained)", resp.Headers["X-Multi"])
	}
}

func TestDefaultHTTPClient_Do_InvalidMethod(t *testing.T) {
	c := NewDefaultHTTPClient(5 * time.Second)
	_, err := c.Do(&HTTPRequest{Method: "IN VALID", URL: "http://example.invalid/x"})
	if err == nil {
		t.Fatal("expected error for invalid HTTP method")
	}
	if !strings.Contains(err.Error(), "failed to create HTTP request") {
		t.Errorf("error = %v, want wrapping 'failed to create HTTP request'", err)
	}
}

func TestDefaultHTTPClient_Do_TransportFailure(t *testing.T) {
	// Connect to a closed local port: this is loopback-only (no real
	// external network access) and reliably fails fast with a dial error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a local port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // now guaranteed nothing is listening on addr

	c := NewDefaultHTTPClient(2 * time.Second)
	_, err = c.Do(&HTTPRequest{Method: http.MethodGet, URL: "http://" + addr + "/x"})
	if err == nil {
		t.Fatal("expected transport error connecting to a closed port")
	}
	if !strings.Contains(err.Error(), "HTTP request failed") {
		t.Errorf("error = %v, want wrapping 'HTTP request failed'", err)
	}
}

// errorReadCloser simulates a response body that fails mid-read, exercising
// the "failed to read response body" branch of Do.
type errorReadCloser struct{}

func (errorReadCloser) Read(_ []byte) (int, error) { return 0, errors.New("simulated read failure") }
func (errorReadCloser) Close() error                { return nil }

type erroringRoundTripper struct{}

func (erroringRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       errorReadCloser{},
		Header:     make(http.Header),
	}, nil
}

func TestDefaultHTTPClient_Do_BodyReadFailure(t *testing.T) {
	c := NewDefaultHTTPClient(5 * time.Second)
	c.client.Transport = erroringRoundTripper{}

	_, err := c.Do(&HTTPRequest{Method: http.MethodGet, URL: "http://unit-test.invalid/x"})
	if err == nil {
		t.Fatal("expected error when response body read fails")
	}
	if !strings.Contains(err.Error(), "failed to read response body") {
		t.Errorf("error = %v, want wrapping 'failed to read response body'", err)
	}
}

// --- Constructors ---------------------------------------------------------

func TestNewClient_And_NewEnterpriseClient(t *testing.T) {
	c := NewClient("k1")
	if c.BaseURL != DefaultBaseURL {
		t.Errorf("NewClient BaseURL = %q, want %q", c.BaseURL, DefaultBaseURL)
	}
	if c.APIKey != "k1" {
		t.Errorf("NewClient APIKey = %q, want k1", c.APIKey)
	}
	if _, ok := c.HTTPClient.(*DefaultHTTPClient); !ok {
		t.Errorf("NewClient HTTPClient type = %T, want *DefaultHTTPClient", c.HTTPClient)
	}

	ec := NewEnterpriseClient("k2")
	if ec.BaseURL != DefaultEnterpriseBaseURL {
		t.Errorf("NewEnterpriseClient BaseURL = %q, want %q", ec.BaseURL, DefaultEnterpriseBaseURL)
	}
}

func TestNewClientWithConfig_Defaults(t *testing.T) {
	// Empty baseURL falls back to DefaultBaseURL.
	c := NewClientWithConfig("k", "", nil)
	if c.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want default %q", c.BaseURL, DefaultBaseURL)
	}
	if c.HTTPClient == nil {
		t.Fatal("expected a default HTTPClient when nil is passed")
	}
	if _, ok := c.HTTPClient.(*DefaultHTTPClient); !ok {
		t.Errorf("HTTPClient type = %T, want *DefaultHTTPClient", c.HTTPClient)
	}

	// Custom baseURL and httpClient are preserved untouched.
	mc := &mockHTTPClient{}
	c2 := NewClientWithConfig("k", "https://custom.example/api", mc)
	if c2.BaseURL != "https://custom.example/api" {
		t.Errorf("BaseURL = %q, want custom value preserved", c2.BaseURL)
	}
	if c2.HTTPClient != mc {
		t.Error("expected the exact custom HTTPClient instance to be preserved")
	}
}

// --- makeRequest -----------------------------------------------------------

func TestMakeRequest_MarshalError(t *testing.T) {
	mc := &mockHTTPClient{}
	c := newTestClient(mc)

	// A channel cannot be marshaled to JSON.
	_, err := c.makeRequest(http.MethodPost, "/x", make(chan int))
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if !strings.Contains(err.Error(), "failed to marshal request body") {
		t.Errorf("error = %v, want wrapping 'failed to marshal request body'", err)
	}
	if mc.CallCount != 0 {
		t.Errorf("HTTPClient.Do should not be called when marshaling fails, got %d calls", mc.CallCount)
	}
}

func TestMakeRequest_SetsHeadersAndURL(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `{}`), nil
	}}
	c := newTestClient(mc)
	c.APIKey = "secret-key"

	_, err := c.makeRequest(http.MethodGet, "/deployments", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mc.Requests) != 1 {
		t.Fatalf("expected exactly 1 request, got %d", len(mc.Requests))
	}
	req := mc.Requests[0]
	if req.URL != "https://unit-test.invalid/deployments" {
		t.Errorf("URL = %q, want BaseURL+endpoint concatenation", req.URL)
	}
	if req.Headers["X-API-KEY"] != "secret-key" {
		t.Errorf("X-API-KEY header = %q, want secret-key", req.Headers["X-API-KEY"])
	}
	if req.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type header = %q, want application/json", req.Headers["Content-Type"])
	}
	if req.Body != nil {
		t.Errorf("Body = %v, want nil when body arg is nil", req.Body)
	}
}

func TestMakeRequest_DoError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("boom")
	}}
	c := newTestClient(mc)
	_, err := c.makeRequest(http.MethodGet, "/x", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "request failed") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want wrapping 'request failed' and underlying 'boom'", err)
	}
}

func TestMakeRequest_APIError_WithDetailField(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(422, `{"detail":"resource_private_name is invalid"}`), nil
	}}
	c := newTestClient(mc)
	_, err := c.makeRequest(http.MethodPost, "/deploy", nil)
	if err == nil {
		t.Fatal("expected APIError")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Code != 422 {
		t.Errorf("Code = %d, want 422", apiErr.Code)
	}
	if apiErr.Message != "resource_private_name is invalid" {
		t.Errorf("Message = %q, want the 'detail' field content", apiErr.Message)
	}
	if apiErr.Details != "" {
		t.Errorf("Details = %q, want empty when detail field parsed successfully", apiErr.Details)
	}
}

func TestMakeRequest_APIError_NonJSONBody(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(500, `internal server error, not json`), nil
	}}
	c := newTestClient(mc)
	_, err := c.makeRequest(http.MethodGet, "/x", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Code != 500 {
		t.Errorf("Code = %d, want 500", apiErr.Code)
	}
	if apiErr.Message != "API request failed with status 500" {
		t.Errorf("Message = %q, want generic fallback message", apiErr.Message)
	}
	if apiErr.Details != "internal server error, not json" {
		t.Errorf("Details = %q, want raw body preserved as details", apiErr.Details)
	}
}

func TestMakeRequest_APIError_EmptyBody(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(404, ``), nil
	}}
	c := newTestClient(mc)
	_, err := c.makeRequest(http.MethodGet, "/x", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Message != "API request failed with status 404" {
		t.Errorf("Message = %q, want generic fallback message", apiErr.Message)
	}
	if apiErr.Details != "" {
		t.Errorf("Details = %q, want empty for empty body", apiErr.Details)
	}
}

func TestMakeRequest_APIError_JSONWithoutDetailField(t *testing.T) {
	// Valid JSON, but no "detail" key -> falls back to raw-body-as-details path.
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(400, `{"error":"bad request"}`), nil
	}}
	c := newTestClient(mc)
	_, err := c.makeRequest(http.MethodGet, "/x", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v (%T), want *APIError", err, err)
	}
	if apiErr.Message != "API request failed with status 400" {
		t.Errorf("Message = %q, want fallback message since 'detail' field absent", apiErr.Message)
	}
	if apiErr.Details != `{"error":"bad request"}` {
		t.Errorf("Details = %q, want raw body", apiErr.Details)
	}
}

func TestMakeRequest_Success_PassesThroughResponse(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `{"data":"ok"}`), nil
	}}
	c := newTestClient(mc)
	resp, err := c.makeRequest(http.MethodGet, "/x", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 || string(resp.Body) != `{"data":"ok"}` {
		t.Errorf("response not passed through unchanged: %+v", resp)
	}
}

// --- buildQueryParams --------------------------------------------------

func TestBuildQueryParams(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var nilTimePtr *time.Time
	zeroTime := time.Time{}

	tests := []struct {
		name   string
		params map[string]interface{}
		want   string // "" or "?key=val&..." form checked via contains for multi-key cases
	}{
		{"nil map", nil, ""},
		{"empty map", map[string]interface{}{}, ""},
		{"nil value skipped", map[string]interface{}{"a": nil}, ""},
		{"empty string skipped", map[string]interface{}{"a": ""}, ""},
		{"non-empty string", map[string]interface{}{"a": "hello"}, "?a=hello"},
		{"zero int skipped", map[string]interface{}{"a": 0}, ""},
		{"nonzero int", map[string]interface{}{"a": 42}, "?a=42"},
		{"zero int64 skipped", map[string]interface{}{"a": int64(0)}, ""},
		{"nonzero int64", map[string]interface{}{"a": int64(99)}, "?a=99"},
		{"zero float64 skipped", map[string]interface{}{"a": float64(0)}, ""},
		{"nonzero float64", map[string]interface{}{"a": 1.5}, "?a=1.5"},
		{"bool false still added", map[string]interface{}{"a": false}, "?a=false"},
		{"bool true added", map[string]interface{}{"a": true}, "?a=true"},
		{"zero time.Time skipped", map[string]interface{}{"a": zeroTime}, ""},
		{"nonzero time.Time", map[string]interface{}{"a": now}, "?a=" + urlEscapeRFC3339(now)},
		{"nil *time.Time skipped", map[string]interface{}{"a": nilTimePtr}, ""},
		{"non-nil *time.Time", map[string]interface{}{"a": &now}, "?a=" + urlEscapeRFC3339(now)},
		{"empty []int skipped", map[string]interface{}{"a": []int{}}, ""},
		{"non-empty []int", map[string]interface{}{"a": []int{1, 2}}, "?a=%5B1%2C2%5D"},
		{"empty []string skipped", map[string]interface{}{"a": []string{}}, ""},
		{"non-empty []string", map[string]interface{}{"a": []string{"x"}}, "?a=%5B%22x%22%5D"},
		{"default type uses fmt.Sprint", map[string]interface{}{"a": struct{ X int }{X: 5}}, "?a=%7B5%7D"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildQueryParams(tt.params)
			if got != tt.want {
				t.Errorf("buildQueryParams(%v) = %q, want %q", tt.params, got, tt.want)
			}
		})
	}
}

func TestBuildQueryParams_MultipleKeysSortedAndJoined(t *testing.T) {
	got := buildQueryParams(map[string]interface{}{
		"zeta":  "z",
		"alpha": "a",
	})
	if got != "?alpha=a&zeta=z" {
		t.Errorf("got %q, want deterministic alpha-sorted query string", got)
	}
}

// urlEscapeRFC3339 formats t like url.Values.Encode() would for a single
// RFC3339-formatted value, so tests stay coupled to behavior, not to a
// hand-copied escaped literal.
func urlEscapeRFC3339(t time.Time) string {
	v := t.Format(time.RFC3339)
	return url.QueryEscape(v)
}

package openrouter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// errRoundTripper simulates a transport-level failure (e.g. DNS/connection
// refused) without touching the network — client.Do returns err directly.
type errRoundTripper struct {
	err error
}

func (rt errRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, rt.err
}

// badBodyReader always fails on Read, to exercise the io.ReadAll error path.
type badBodyReader struct{}

func (badBodyReader) Read(_ []byte) (int, error) { return 0, errors.New("simulated read failure") }
func (badBodyReader) Close() error                { return nil }

// badBodyRoundTripper returns a 200 response whose Body errors on Read.
type badBodyRoundTripper struct{}

func (badBodyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       badBodyReader{},
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func TestFetchModels_BuildRequestError(t *testing.T) {
	// A raw control character in the URL makes http.NewRequestWithContext fail
	// at the "build request" stage, before any network I/O is attempted.
	_, err := FetchModels(context.Background(), "http://example.com/\x7f", &http.Client{})
	if err == nil {
		t.Fatal("expected build request error, got nil")
	}
	if !strings.Contains(err.Error(), "build request") {
		t.Fatalf("expected error to mention 'build request', got: %v", err)
	}
}

func TestFetchModels_ClientDoError(t *testing.T) {
	wantErr := errors.New("simulated dial failure")
	client := &http.Client{Transport: errRoundTripper{err: wantErr}}

	_, err := FetchModels(context.Background(), "http://openrouter.invalid", client)
	if err == nil {
		t.Fatal("expected client.Do error, got nil")
	}
	if !strings.Contains(err.Error(), "openrouter fetch") {
		t.Fatalf("expected error to mention 'openrouter fetch', got: %v", err)
	}
	if !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("expected wrapped error %v, got: %v", wantErr, err)
	}
}

func TestFetchModels_ReadBodyError(t *testing.T) {
	client := &http.Client{Transport: badBodyRoundTripper{}}

	_, err := FetchModels(context.Background(), "http://openrouter.invalid", client)
	if err == nil {
		t.Fatal("expected read body error, got nil")
	}
	if !strings.Contains(err.Error(), "read body") {
		t.Fatalf("expected error to mention 'read body', got: %v", err)
	}
}

func TestFetchModels_DecodeBodyError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not-valid-json`))
	}))
	defer server.Close()

	_, err := FetchModels(context.Background(), server.URL, server.Client())
	if err == nil {
		t.Fatal("expected decode body error, got nil")
	}
	if !strings.Contains(err.Error(), "decode body") {
		t.Fatalf("expected error to mention 'decode body', got: %v", err)
	}
}

func TestFetchModels_NilClientUsesDefaultClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("expected Accept header to be set, got %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	// Passing a nil *http.Client exercises the `client == nil` branch, which
	// falls back to http.DefaultClient. The target is our own httptest
	// server (loopback), so this stays hermetic.
	models, err := FetchModels(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatalf("FetchModels with nil client failed: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected empty model list, got %d", len(models))
	}
}

func TestFetchModels_TrimsTrailingSlashFromBaseURL(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	_, err := FetchModels(context.Background(), server.URL+"///", server.Client())
	if err != nil {
		t.Fatalf("FetchModels failed: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("expected path /v1/models, got %q", gotPath)
	}
}

func TestModelArchitectureAndPricingRoundTrip(t *testing.T) {
	// Sanity-check that the struct tags round-trip through JSON as declared,
	// since these types are otherwise only exercised implicitly by FetchModels.
	const fixture = `{"data":[{
		"id":"vendor/model-x","name":"Model X","created":42,
		"description":"desc",
		"context_length":8192,
		"architecture":{"modality":"text->text","input_modalities":["text"],"output_modalities":["text"],"tokenizer":"cl100k"},
		"pricing":{"prompt":"0.001","completion":"0.002","image":"0.003","request":"0.004"}
	}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture))
	}))
	defer server.Close()

	models, err := FetchModels(context.Background(), server.URL, server.Client())
	if err != nil {
		t.Fatalf("FetchModels failed: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	m := models[0]
	if m.ID != "vendor/model-x" || m.Name != "Model X" || m.Created != 42 ||
		m.Description != "desc" || m.ContextLength != 8192 {
		t.Fatalf("unexpected model fields: %+v", m)
	}
	if m.Architecture.Modality != "text->text" || m.Architecture.Tokenizer != "cl100k" ||
		len(m.Architecture.InputModalities) != 1 || m.Architecture.InputModalities[0] != "text" ||
		len(m.Architecture.OutputModalities) != 1 || m.Architecture.OutputModalities[0] != "text" {
		t.Fatalf("unexpected architecture: %+v", m.Architecture)
	}
	if m.Pricing.Prompt != "0.001" || m.Pricing.Completion != "0.002" ||
		m.Pricing.Image != "0.003" || m.Pricing.Request != "0.004" {
		t.Fatalf("unexpected pricing: %+v", m.Pricing)
	}
	if m.IsFree() {
		t.Fatal("expected non-free model given non-zero prices")
	}
}

var _ io.ReadCloser = badBodyReader{}

package fakeupstream

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// Usage is what every successful answer reports, in each wire's own fields.
// CachedTokens is part of PromptTokens on the OpenAI and Gemini wires and
// reported separately (excluded from input_tokens) on the Anthropic wire —
// the same split the real vendors make.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

// DefaultUsage is chosen so that common list prices give whole quota units:
// at DeepSeek's $0.30 in / $1.20 out per 1M tokens and 500,000 quota per $1,
// 1000 + 500 tokens cost exactly 450 units. A suite that wants different
// numbers sets them, it does not depend on these.
var DefaultUsage = Usage{PromptTokens: 1000, CompletionTokens: 500}

// Config is fixed at construction; Usage can also be changed at runtime.
type Config struct {
	// Key, when set, is the only API key accepted (Bearer, x-api-key,
	// x-goog-api-key or ?key=). Empty accepts any key, including none.
	Key string
	// Usage reported by successful answers. Zero value means DefaultUsage.
	Usage Usage
	// Reply is the assistant text. Empty means "fake reply".
	Reply string
	// FaultOnly answers fault-mode models only and rejects everything else
	// with 400 — the contract of the UAT fault simulator, which must never
	// look like a working vendor by accident.
	FaultOnly bool
}

// Record is one request as the upstream saw it.
type Record struct {
	Time   time.Time `json:"time"`
	Method string    `json:"method"`
	Path   string    `json:"path"`
	Wire   string    `json:"wire"`
	Model  string    `json:"model"`
	Stream bool      `json:"stream"`
	Fault  string    `json:"fault,omitempty"`
	Status int       `json:"status"`
}

// Wire names used in Record.Wire.
const (
	WireOpenAIChat      = "openai_chat"
	WireOpenAIResponses = "openai_responses"
	WireAnthropic       = "anthropic"
	WireGemini          = "gemini"
	WireSystemOne       = "systemone"
)

// Server is the fake vendor. The zero value is not usable; call New.
type Server struct {
	cfg Config

	mu      sync.Mutex
	usage   Usage
	records []Record
	queued  []string // one-shot faults, consumed in order
}

// New builds a server. It does not listen; mount Handler() or use NewTest.
func New(cfg Config) *Server {
	if cfg.Usage == (Usage{}) {
		cfg.Usage = DefaultUsage
	}
	if cfg.Reply == "" {
		cfg.Reply = "fake reply"
	}
	return &Server{cfg: cfg, usage: cfg.Usage}
}

// TB is the part of testing.TB that NewTest needs. Declared here so that this
// package — which the production fault simulator imports — does not link the
// testing package into the server binary.
type TB interface {
	Helper()
	Cleanup(func())
}

// NewTest starts the server on a loopback port for the duration of a test and
// returns it with its base URL (no trailing slash, no /v1).
func NewTest(t TB, cfg Config) (*Server, string) {
	t.Helper()
	s := New(cfg)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts.URL
}

// Usage returns the usage currently reported.
func (s *Server) Usage() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage
}

// SetUsage changes the usage reported from the next request on.
func (s *Server) SetUsage(u Usage) {
	s.mu.Lock()
	s.usage = u
	s.mu.Unlock()
}

// QueueFault makes the next n vendor requests fail with mode, whatever model
// they ask for. It is how a test breaks a request for a real model name.
func (s *Server) QueueFault(mode string, n int) error {
	if !IsFaultMode(mode) {
		return errors.New("unknown fault mode " + mode)
	}
	s.mu.Lock()
	for i := 0; i < n; i++ {
		s.queued = append(s.queued, mode)
	}
	s.mu.Unlock()
	return nil
}

// Requests returns a copy of every vendor request recorded so far. Control
// requests (/_fake/*) are not recorded.
func (s *Server) Requests() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records...)
}

// Reset forgets recorded requests and queued faults; usage is kept.
func (s *Server) Reset() {
	s.mu.Lock()
	s.records = nil
	s.queued = nil
	s.mu.Unlock()
}

func (s *Server) takeQueued() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queued) == 0 {
		return ""
	}
	m := s.queued[0]
	s.queued = s.queued[1:]
	return m
}

func (s *Server) record(r Record) {
	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()
}

// Handler serves the vendor routes and the /_fake control routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/chat/completions", s.vendor(WireOpenAIChat, s.serveOpenAIChat))
	mux.HandleFunc("POST /chat/completions", s.vendor(WireOpenAIChat, s.serveOpenAIChat))
	mux.HandleFunc("POST /v1/responses", s.vendor(WireOpenAIResponses, s.serveResponses))
	mux.HandleFunc("POST /responses", s.vendor(WireOpenAIResponses, s.serveResponses))
	// DeepSeek and Moonshot serve the Anthropic wire under /anthropic.
	mux.HandleFunc("POST /v1/messages", s.vendor(WireAnthropic, s.serveAnthropic))
	mux.HandleFunc("POST /anthropic/v1/messages", s.vendor(WireAnthropic, s.serveAnthropic))
	mux.HandleFunc("POST /v1beta/models/{call}", s.vendor(WireGemini, s.serveGemini))
	mux.HandleFunc("POST /v1/models/{call}", s.vendor(WireGemini, s.serveGemini))
	mux.HandleFunc("POST /v1/systemone", s.vendor(WireSystemOne, s.serveSystemOne))
	mux.HandleFunc("GET /v1/models", s.serveModels)

	mux.HandleFunc("GET /_fake/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /_fake/requests", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"requests": s.Requests()})
	})
	mux.HandleFunc("DELETE /_fake/requests", func(w http.ResponseWriter, _ *http.Request) {
		s.Reset()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /_fake/usage", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.Usage())
	})
	mux.HandleFunc("POST /_fake/usage", func(w http.ResponseWriter, r *http.Request) {
		var u Usage
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil || u.PromptTokens < 0 || u.CompletionTokens < 0 || u.CachedTokens < 0 || u.CachedTokens > u.PromptTokens {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be a Usage with non-negative counts and cached_tokens <= prompt_tokens"})
			return
		}
		s.SetUsage(u)
		writeJSON(w, http.StatusOK, u)
	})
	mux.HandleFunc("POST /_fake/faults", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Mode  string `json:"mode"`
			Count int    `json:"count"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Count <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be {mode, count>0}"})
			return
		}
		if err := s.QueueFault(req.Mode, req.Count); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "modes": FaultModes})
			return
		}
		writeJSON(w, http.StatusOK, req)
	})
	return mux
}

// call is what a vendor handler receives once the request has been parsed,
// authenticated and checked for faults.
type call struct {
	w      http.ResponseWriter
	r      *http.Request
	wire   string
	model  string
	stream bool
	body   map[string]any
	usage  Usage
	params FaultParams
}

// vendor wraps a wire handler with the steps every vendor route shares:
// parse, authenticate, pick a fault, record.
func (s *Server) vendor(wire string, serve func(*call)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		rec := Record{Time: time.Now(), Method: r.Method, Path: r.URL.Path, Wire: wire}
		defer func() {
			rec.Status = sw.status
			s.record(rec)
		}()

		raw, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		body := map[string]any{}
		// A body that is not a JSON object is answered like any other request;
		// the relay under test decides what it sends, not this fake.
		_ = json.Unmarshal(raw, &body)

		c := &call{w: sw, r: r, wire: wire, body: body, usage: s.Usage(), params: FaultParamsFromQuery(r)}
		c.model, _ = body["model"].(string)
		c.stream, _ = body["stream"].(bool)
		if wire == WireGemini {
			c.model, c.stream = parseGeminiCall(r.PathValue("call"))
		}
		rec.Model, rec.Stream = c.model, c.stream

		if !s.authorized(r) {
			rec.Fault = "auth"
			writeWireError(sw, wire, http.StatusUnauthorized, "authentication_error", "invalid api key")
			return
		}

		mode := s.takeQueued()
		if mode == "" && IsFaultMode(c.model) {
			mode = c.model
		}
		if q := r.URL.Query().Get("mode"); mode == "" && q != "" {
			mode = q
		}
		if mode != "" {
			rec.Fault = mode
			if !IsFaultMode(mode) {
				writeWireError(sw, wire, http.StatusBadRequest, "invalid_request_error", unknownModeMessage(mode))
				return
			}
			WriteFault(sw, r, wire, mode, c.params)
			return
		}
		if s.cfg.FaultOnly {
			writeWireError(sw, wire, http.StatusBadRequest, "invalid_request_error", unknownModeMessage(c.model))
			return
		}
		serve(c)
	}
}

func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.Key == "" {
		return true
	}
	got := r.Header.Get("x-api-key")
	if got == "" {
		got = r.Header.Get("x-goog-api-key")
	}
	if got == "" {
		got = r.URL.Query().Get("key")
	}
	if got == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			got = strings.TrimPrefix(a, "Bearer ")
		}
	}
	// X-Faultsim-Token is the UAT simulator's own header.
	if got == "" {
		got = r.Header.Get("X-Faultsim-Token")
	}
	return got == s.cfg.Key
}

func (s *Server) serveModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []any{}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// statusWriter remembers the status for the request record and passes
// flushing and hijacking through — the fault modes need both.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

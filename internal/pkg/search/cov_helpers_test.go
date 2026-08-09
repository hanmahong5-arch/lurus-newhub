package search

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meilisearch/meilisearch-go"
)

// recordedReq captures one HTTP request the fake Meilisearch server received,
// so tests can assert the client built the request (method/path/body) that a
// given business-level call is supposed to produce.
type recordedReq struct {
	Method string
	Path   string
	Body   []byte
}

// fakeMeili is a minimal in-process stand-in for a Meilisearch server. It lets
// tests drive the exact HTTP-level responses (success / upstream error /
// malformed body) that the search package's calls will observe, with zero
// real network access (httptest.Server binds to loopback only).
type fakeMeili struct {
	mu   sync.Mutex
	reqs []recordedReq
	// handler, if set, is consulted first for every request; returning true
	// means the request was fully handled (response already written) and the
	// generic default should be skipped.
	handler func(w http.ResponseWriter, r *http.Request) bool
}

func (fm *fakeMeili) requests() []recordedReq {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	out := make([]recordedReq, len(fm.reqs))
	copy(out, fm.reqs)
	return out
}

// lastBody returns the body of the most recently received request. It must
// be used instead of re-reading r.Body from inside a handler override,
// because the outer wrapper already drained r.Body before invoking handler
// (to populate reqs) — reading it again would just see EOF.
func (fm *fakeMeili) lastBody() []byte {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	if len(fm.reqs) == 0 {
		return nil
	}
	return fm.reqs[len(fm.reqs)-1].Body
}

func (fm *fakeMeili) setHandler(h func(w http.ResponseWriter, r *http.Request) bool) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fm.handler = h
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func defaultTaskInfo(indexUID string) map[string]interface{} {
	return map[string]interface{}{
		"taskUid":    1,
		"indexUid":   indexUID,
		"status":     "enqueued",
		"type":       "documentAdditionOrUpdate",
		"enqueuedAt": time.Now().UTC().Format(time.RFC3339),
	}
}

// defaultResponse answers every Meilisearch REST call this package can issue
// with a generically successful response, mirroring the real server's status
// code conventions (202+TaskInfo for async writes, 200 for reads).
func (fm *fakeMeili) defaultResponse(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/health":
		writeJSON(w, http.StatusOK, map[string]string{"status": "available"})
	case p == "/version":
		writeJSON(w, http.StatusOK, map[string]string{"pkgVersion": "1.10.0", "commitSha": "deadbeef", "commitDate": "2026-01-01T00:00:00Z"})
	case p == "/stats":
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"databaseSize":     int64(1024),
			"usedDatabaseSize": int64(512),
			"indexes":          map[string]interface{}{},
		})
	case strings.HasSuffix(p, "/search"):
		writeJSON(w, http.StatusOK, map[string]interface{}{"hits": []interface{}{}, "estimatedTotalHits": 0})
	case strings.HasPrefix(p, "/indexes/") && strings.HasSuffix(p, "/stats"):
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"numberOfDocuments": int64(7),
			"isIndexing":        false,
			"fieldDistribution": map[string]int64{"content": 7},
		})
	default:
		// Covers create/delete index, settings updates, and document
		// add/delete — Meilisearch answers all of those asynchronously with
		// 202 Accepted + a TaskInfo body.
		idx := ""
		if parts := strings.Split(strings.TrimPrefix(p, "/indexes/"), "/"); len(parts) > 0 {
			idx = parts[0]
		}
		writeJSON(w, http.StatusAccepted, defaultTaskInfo(idx))
	}
}

// newFakeMeiliServer starts a loopback-only HTTP server standing in for
// Meilisearch, returning it plus a handle to record/override responses.
func newFakeMeiliServer(t *testing.T) (*httptest.Server, *fakeMeili) {
	t.Helper()
	fm := &fakeMeili{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fm.mu.Lock()
		fm.reqs = append(fm.reqs, recordedReq{Method: r.Method, Path: r.URL.Path, Body: body})
		h := fm.handler
		fm.mu.Unlock()

		if h != nil && h(w, r) {
			return
		}
		fm.defaultResponse(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, fm
}

// globalsSnapshot captures every package-level mutable configuration variable
// so tests can freely flip them and restore the previous state afterwards.
// The package intentionally uses package-level globals rather than a struct,
// so tests in this package must not run in parallel with each other.
type globalsSnapshot struct {
	client          meilisearch.ServiceManager
	enabled         bool
	host            string
	apiKey          string
	syncEnabled     bool
	syncBatchSize   int
	syncInterval    int
	maxResults      int
	searchTimeout   int64
	debug           bool
	workerCount     int
	retryCount      int
	retryDelay      int
	autoCreateIndex bool
	indexPrefix     string
}

func snapshotGlobals() globalsSnapshot {
	return globalsSnapshot{
		client: Client, enabled: Enabled, host: Host, apiKey: APIKey,
		syncEnabled: SyncEnabled, syncBatchSize: SyncBatchSize, syncInterval: SyncInterval,
		maxResults: MaxResults, searchTimeout: SearchTimeout, debug: Debug,
		workerCount: WorkerCount, retryCount: RetryCount, retryDelay: RetryDelay,
		autoCreateIndex: AutoCreateIndex, indexPrefix: IndexPrefix,
	}
}

func (s globalsSnapshot) restore() {
	Client = s.client
	Enabled = s.enabled
	Host = s.host
	APIKey = s.apiKey
	SyncEnabled = s.syncEnabled
	SyncBatchSize = s.syncBatchSize
	SyncInterval = s.syncInterval
	MaxResults = s.maxResults
	SearchTimeout = s.searchTimeout
	Debug = s.debug
	WorkerCount = s.workerCount
	RetryCount = s.retryCount
	RetryDelay = s.retryDelay
	AutoCreateIndex = s.autoCreateIndex
	IndexPrefix = s.indexPrefix
}

// withFakeClient points the package's global Client at a local fake
// Meilisearch server and marks the integration enabled, restoring the
// previous global state at test cleanup.
func withFakeClient(t *testing.T) (*httptest.Server, *fakeMeili) {
	t.Helper()
	snap := snapshotGlobals()
	t.Cleanup(snap.restore)

	srv, fm := newFakeMeiliServer(t)
	Client = meilisearch.New(srv.URL, meilisearch.WithAPIKey("test-key"))
	Enabled = true
	SyncBatchSize = 1000
	IndexPrefix = ""
	return srv, fm
}

// withDisabled marks the integration disabled with a nil client, restoring
// the previous global state at test cleanup.
func withDisabled(t *testing.T) {
	t.Helper()
	snap := snapshotGlobals()
	t.Cleanup(snap.restore)
	Client = nil
	Enabled = false
}

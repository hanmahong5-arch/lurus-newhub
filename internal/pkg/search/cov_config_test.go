package search

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/meilisearch/meilisearch-go"
)

// setEnv sets an env var for the duration of the test and restores whatever
// was there before (including "unset") on cleanup.
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	_ = os.Unsetenv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		}
	})
}

func TestGetEnv(t *testing.T) {
	t.Run("returns value when set", func(t *testing.T) {
		setEnv(t, "SEARCH_TEST_GETENV", "custom")
		if got := getEnv("SEARCH_TEST_GETENV", "default"); got != "custom" {
			t.Fatalf("getEnv() = %q, want %q", got, "custom")
		}
	})
	t.Run("falls back when unset", func(t *testing.T) {
		unsetEnv(t, "SEARCH_TEST_GETENV_MISSING")
		if got := getEnv("SEARCH_TEST_GETENV_MISSING", "default"); got != "default" {
			t.Fatalf("getEnv() = %q, want %q", got, "default")
		}
	})
	t.Run("falls back when set to empty string", func(t *testing.T) {
		setEnv(t, "SEARCH_TEST_GETENV_EMPTY", "")
		if got := getEnv("SEARCH_TEST_GETENV_EMPTY", "default"); got != "default" {
			t.Fatalf("getEnv() = %q, want %q (empty string must not override default)", got, "default")
		}
	})
}

func TestGetIntEnv(t *testing.T) {
	cases := []struct {
		name    string
		set     bool
		value   string
		def     int
		want    int
	}{
		{"valid positive", true, "42", 7, 42},
		{"valid negative", true, "-5", 7, -5},
		{"valid zero", true, "0", 7, 0},
		{"unset falls back", false, "", 7, 7},
		{"non-numeric falls back", true, "not-a-number", 7, 7},
		{"float-looking falls back", true, "3.14", 7, 7},
		{"empty string falls back", true, "", 7, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "SEARCH_TEST_INTENV_" + tc.name
			if tc.set {
				setEnv(t, key, tc.value)
			} else {
				unsetEnv(t, key)
			}
			if got := getIntEnv(key, tc.def); got != tc.want {
				t.Fatalf("getIntEnv(%q) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

func TestGetBoolEnv(t *testing.T) {
	cases := []struct {
		name  string
		set   bool
		value string
		def   bool
		want  bool
	}{
		{"true", true, "true", false, true},
		{"false", true, "false", true, false},
		{"1", true, "1", false, true},
		{"0", true, "0", true, false},
		{"unset falls back true", false, "", true, true},
		{"unset falls back false", false, "", false, false},
		{"garbage falls back", true, "not-a-bool", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "SEARCH_TEST_BOOLENV_" + tc.name
			if tc.set {
				setEnv(t, key, tc.value)
			} else {
				unsetEnv(t, key)
			}
			if got := getBoolEnv(key, tc.def); got != tc.want {
				t.Fatalf("getBoolEnv(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"empty", "", "***"},
		{"exactly 8 chars stays masked", "12345678", "***"},
		{"9 chars reveals prefix and suffix", "123456789", "1234...6789"},
		{"typical 32-char key", "sk-abcdefghijklmnopqrstuvwxyz012", "sk-a...z012"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := maskAPIKey(tc.key)
			if got != tc.want {
				t.Fatalf("maskAPIKey(%q) = %q, want %q", tc.key, got, tc.want)
			}
			// The mask must never leak the raw secret verbatim for keys long
			// enough to be split.
			if len(tc.key) > 8 && strings.Contains(got, tc.key) {
				t.Fatalf("maskAPIKey(%q) leaked the raw key in %q", tc.key, got)
			}
		})
	}
}

func TestGetIndexName(t *testing.T) {
	snap := snapshotGlobals()
	t.Cleanup(snap.restore)

	IndexPrefix = ""
	if got := GetIndexName("logs"); got != "logs" {
		t.Fatalf("GetIndexName() with empty prefix = %q, want %q", got, "logs")
	}

	IndexPrefix = "tenantA"
	if got := GetIndexName("logs"); got != "tenantA_logs" {
		t.Fatalf("GetIndexName() with prefix = %q, want %q", got, "tenantA_logs")
	}
}

func TestIsEnabled(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		client  bool // whether Client is non-nil
		want    bool
	}{
		{"disabled, no client", false, false, false},
		{"disabled, has client", false, true, false},
		{"enabled, no client", true, false, false},
		{"enabled, has client", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := snapshotGlobals()
			t.Cleanup(snap.restore)

			Enabled = tc.enabled
			if tc.client {
				srv, _ := newFakeMeiliServer(t)
				Client = newTestClient(srv.URL)
			} else {
				Client = nil
			}
			if got := IsEnabled(); got != tc.want {
				t.Fatalf("IsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsHealthy(t *testing.T) {
	t.Run("not enabled short-circuits without any HTTP call", func(t *testing.T) {
		withDisabled(t)
		if IsHealthy() {
			t.Fatal("IsHealthy() = true when integration disabled, want false")
		}
	})

	t.Run("enabled and server reports available", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if !IsHealthy() {
			t.Fatal("IsHealthy() = false with healthy server, want true")
		}
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Path != "/health" {
			t.Fatalf("expected exactly one GET /health call, got %+v", reqs)
		}
	})

	t.Run("enabled but server reports a non-available status", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/health" {
				return false
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "unavailable"})
			return true
		})
		if IsHealthy() {
			t.Fatal("IsHealthy() = true when server status != available, want false")
		}
	})

	t.Run("enabled but server errors", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/health" {
				return false
			}
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		if IsHealthy() {
			t.Fatal("IsHealthy() = true when server errors, want false")
		}
	})
}

func TestGetStats(t *testing.T) {
	t.Run("not enabled returns error, no request", func(t *testing.T) {
		withDisabled(t)
		stats, err := GetStats()
		if err == nil {
			t.Fatal("GetStats() error = nil, want error when disabled")
		}
		if stats != nil {
			t.Fatalf("GetStats() stats = %v, want nil", stats)
		}
	})

	t.Run("enabled aggregates database size and per-index stats", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/stats" {
				return false
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"databaseSize":     int64(2048),
				"usedDatabaseSize": int64(1000),
				"indexes": map[string]interface{}{
					"logs": map[string]interface{}{"numberOfDocuments": 3, "isIndexing": true},
				},
			})
			return true
		})

		stats, err := GetStats()
		if err != nil {
			t.Fatalf("GetStats() error = %v, want nil", err)
		}
		if stats["database_size"] != int64(2048) {
			t.Fatalf("GetStats()[database_size] = %v, want 2048", stats["database_size"])
		}
		if stats["indexes"] != 1 {
			t.Fatalf("GetStats()[indexes] = %v, want 1 (one index in map)", stats["indexes"])
		}
		indexStats, ok := stats["index_stats"].(map[string]interface{})
		if !ok {
			t.Fatalf("GetStats()[index_stats] not a map: %T", stats["index_stats"])
		}
		logsStats, ok := indexStats["logs"].(map[string]interface{})
		if !ok {
			t.Fatalf("GetStats()[index_stats][logs] not a map: %T", indexStats["logs"])
		}
		if logsStats["documents"] != int64(3) {
			t.Fatalf("GetStats()[index_stats][logs][documents] = %v, want 3", logsStats["documents"])
		}
		if logsStats["indexing"] != true {
			t.Fatalf("GetStats()[index_stats][logs][indexing] = %v, want true", logsStats["indexing"])
		}
	})

	t.Run("enabled but upstream errors", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/stats" {
				return false
			}
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		if _, err := GetStats(); err == nil {
			t.Fatal("GetStats() error = nil, want error from upstream failure")
		}
	})
}

// FINDING: internal/pkg/search/config.go:246-263 RetryWithBackoff loops
// `for i := 0; i < RetryCount; i++`. If RetryCount is 0 — which is exactly
// what the package var's zero value is before InitMeilisearch/loadConfig has
// ever run, or what MEILISEARCH_RETRY_COUNT=0 gives an operator trying to
// say "no retries, just try once" — the loop body never executes at all, so
// `fn` (the actual index/search operation) is never attempted. RetryWithBackoff
// then falls through to `fmt.Errorf("failed after %d retries: %w", RetryCount, err)`
// with `err` still nil, producing a malformed, misleading message
// ("failed after 0 retries: %!w(<nil>)") instead of either running the
// operation once or returning a clear configuration error.
// Expected: RetryCount<=0 should behave like "try exactly once", or fail
// fast with an explicit configuration error — not silently skip the
// operation while returning a garbled wrapped-nil error message.
// Actual: zero attempts, and an error string containing the fmt.Errorf verb
// artifact "%!w(<nil>)".
func TestRetryWithBackoff_ZeroRetryCountNeverCallsFnAndReturnsGarbledError(t *testing.T) {
	snap := snapshotGlobals()
	t.Cleanup(snap.restore)
	RetryCount = 0

	calls := 0
	err := RetryWithBackoff(func() error {
		calls++
		return fmt.Errorf("should never even be attempted")
	})
	if calls != 0 {
		t.Fatalf("fn called %d times with RetryCount=0, want 0 (documents the bug: the operation is silently skipped)", calls)
	}
	if err == nil {
		t.Fatal("RetryWithBackoff() with RetryCount=0 error = nil, want a (malformed) error")
	}
	if !strings.Contains(err.Error(), "%!w(<nil>)") {
		t.Fatalf("RetryWithBackoff() with RetryCount=0 error = %q, want it to contain the fmt %%w-of-nil artifact documenting the bug", err.Error())
	}
}

func TestRetryWithBackoff(t *testing.T) {
	t.Run("succeeds on first attempt without extra calls", func(t *testing.T) {
		snap := snapshotGlobals()
		t.Cleanup(snap.restore)
		RetryCount = 3
		RetryDelay = 1
		Debug = false

		calls := 0
		err := RetryWithBackoff(func() error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("RetryWithBackoff() error = %v, want nil", err)
		}
		if calls != 1 {
			t.Fatalf("fn called %d times, want 1 (should not retry after success)", calls)
		}
	})

	t.Run("recovers after transient failures", func(t *testing.T) {
		snap := snapshotGlobals()
		t.Cleanup(snap.restore)
		RetryCount = 5
		RetryDelay = 1

		calls := 0
		err := RetryWithBackoff(func() error {
			calls++
			if calls < 3 {
				return fmt.Errorf("transient failure #%d", calls)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("RetryWithBackoff() error = %v, want nil after eventual success", err)
		}
		if calls != 3 {
			t.Fatalf("fn called %d times, want exactly 3 (stop retrying once it succeeds)", calls)
		}
	})

	t.Run("exhausts retries and wraps the final error", func(t *testing.T) {
		snap := snapshotGlobals()
		t.Cleanup(snap.restore)
		RetryCount = 3
		RetryDelay = 1

		calls := 0
		err := RetryWithBackoff(func() error {
			calls++
			return fmt.Errorf("permanent failure")
		})
		if err == nil {
			t.Fatal("RetryWithBackoff() error = nil, want error after exhausting retries")
		}
		if calls != RetryCount {
			t.Fatalf("fn called %d times, want exactly RetryCount=%d", calls, RetryCount)
		}
		if !strings.Contains(err.Error(), "failed after 3 retries") {
			t.Fatalf("RetryWithBackoff() error = %q, want it to mention retry exhaustion", err.Error())
		}
		if !strings.Contains(err.Error(), "permanent failure") {
			t.Fatalf("RetryWithBackoff() error = %q, want it to wrap the underlying cause", err.Error())
		}
	})
}

func TestInitMeilisearch(t *testing.T) {
	// InitMeilisearch calls loadConfig() itself, which re-reads every
	// MEILISEARCH_* env var and overwrites the package globals — so these
	// tests must drive behavior through the environment, not by presetting
	// the globals directly.
	envKeys := []string{
		"MEILISEARCH_ENABLED", "MEILISEARCH_HOST", "MEILISEARCH_API_KEY",
		"MEILISEARCH_SYNC_ENABLED", "MEILISEARCH_SYNC_BATCH_SIZE", "MEILISEARCH_SYNC_INTERVAL",
		"MEILISEARCH_MAX_SEARCH_RESULTS", "MEILISEARCH_SEARCH_TIMEOUT", "MEILISEARCH_DEBUG",
		"MEILISEARCH_WORKER_COUNT", "MEILISEARCH_RETRY_COUNT", "MEILISEARCH_RETRY_DELAY",
		"MEILISEARCH_AUTO_CREATE_INDEX", "MEILISEARCH_INDEX_PREFIX",
	}
	resetEnv := func(t *testing.T) {
		for _, k := range envKeys {
			unsetEnv(t, k)
		}
	}
	restoreGlobalsAfter := func(t *testing.T) {
		snap := snapshotGlobals()
		t.Cleanup(snap.restore)
	}

	t.Run("disabled by default performs no network call", func(t *testing.T) {
		restoreGlobalsAfter(t)
		resetEnv(t)

		if err := InitMeilisearch(); err != nil {
			t.Fatalf("InitMeilisearch() error = %v, want nil when disabled", err)
		}
		if Enabled {
			t.Fatal("Enabled = true after loadConfig with no env vars, want false (documented default)")
		}
		if IsEnabled() {
			t.Fatal("IsEnabled() = true, want false when MEILISEARCH_ENABLED unset")
		}
	})

	t.Run("enabled without API key fails validation before any connection attempt", func(t *testing.T) {
		restoreGlobalsAfter(t)
		resetEnv(t)
		setEnv(t, "MEILISEARCH_ENABLED", "true")
		setEnv(t, "MEILISEARCH_HOST", "http://127.0.0.1:1")
		// MEILISEARCH_API_KEY intentionally left unset.

		err := InitMeilisearch()
		if err == nil {
			t.Fatal("InitMeilisearch() error = nil, want error for missing API key")
		}
		if !strings.Contains(err.Error(), "MEILISEARCH_API_KEY") {
			t.Fatalf("InitMeilisearch() error = %q, want it to name the missing API key", err.Error())
		}
	})

	t.Run("enabled with unreachable health check surfaces a connection error", func(t *testing.T) {
		restoreGlobalsAfter(t)
		resetEnv(t)
		srv, fm := newFakeMeiliServer(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/health" {
				return false
			}
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		setEnv(t, "MEILISEARCH_ENABLED", "true")
		setEnv(t, "MEILISEARCH_HOST", srv.URL)
		setEnv(t, "MEILISEARCH_API_KEY", "test-key")

		err := InitMeilisearch()
		if err == nil {
			t.Fatal("InitMeilisearch() error = nil, want error when health check fails")
		}
		if !strings.Contains(err.Error(), "failed to connect") {
			t.Fatalf("InitMeilisearch() error = %q, want it to mention connection failure", err.Error())
		}
	})

	t.Run("enabled, healthy, version lookup failing is only a warning not a fatal error", func(t *testing.T) {
		restoreGlobalsAfter(t)
		resetEnv(t)
		srv, fm := newFakeMeiliServer(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/version" {
				return false
			}
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		setEnv(t, "MEILISEARCH_ENABLED", "true")
		setEnv(t, "MEILISEARCH_HOST", srv.URL)
		setEnv(t, "MEILISEARCH_API_KEY", "test-key")
		setEnv(t, "MEILISEARCH_AUTO_CREATE_INDEX", "false")

		if err := InitMeilisearch(); err != nil {
			t.Fatalf("InitMeilisearch() error = %v, want nil (version lookup failure is non-fatal)", err)
		}
		if !IsEnabled() {
			t.Fatal("IsEnabled() = false after successful InitMeilisearch, want true")
		}
	})

	t.Run("auto create index true provisions all three indexes", func(t *testing.T) {
		restoreGlobalsAfter(t)
		resetEnv(t)
		srv, fm := newFakeMeiliServer(t)
		setEnv(t, "MEILISEARCH_ENABLED", "true")
		setEnv(t, "MEILISEARCH_HOST", srv.URL)
		setEnv(t, "MEILISEARCH_API_KEY", "test-key")
		setEnv(t, "MEILISEARCH_AUTO_CREATE_INDEX", "true")

		if err := InitMeilisearch(); err != nil {
			t.Fatalf("InitMeilisearch() error = %v, want nil", err)
		}

		createCalls := 0
		for _, r := range fm.requests() {
			if r.Method == http.MethodPost && r.Path == "/indexes" {
				createCalls++
			}
		}
		if createCalls != 3 {
			t.Fatalf("saw %d POST /indexes calls, want 3 (logs, users, channels)", createCalls)
		}
	})

	t.Run("auto create index true propagates a settings failure", func(t *testing.T) {
		restoreGlobalsAfter(t)
		resetEnv(t)
		srv, fm := newFakeMeiliServer(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/settings/searchable-attributes") {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		setEnv(t, "MEILISEARCH_ENABLED", "true")
		setEnv(t, "MEILISEARCH_HOST", srv.URL)
		setEnv(t, "MEILISEARCH_API_KEY", "test-key")
		setEnv(t, "MEILISEARCH_AUTO_CREATE_INDEX", "true")

		err := InitMeilisearch()
		if err == nil {
			t.Fatal("InitMeilisearch() error = nil, want error when index settings update fails")
		}
		if !strings.Contains(err.Error(), "failed to initialize indexes") {
			t.Fatalf("InitMeilisearch() error = %q, want it to wrap the indexing failure", err.Error())
		}
	})
}

// newTestClient builds a Meilisearch client pointed at a local test server;
// factored out so config tests can construct a "has a client" state without
// duplicating the client wiring used by withFakeClient.
func newTestClient(url string) meilisearch.ServiceManager {
	return meilisearch.New(url, meilisearch.WithAPIKey("test-key"))
}

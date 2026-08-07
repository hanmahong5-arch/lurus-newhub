package search

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/meilisearch/meilisearch-go"
)

func sampleLog() *Log {
	return &Log{
		Id:               101,
		CreatedAt:        1700000000,
		Type:             2,
		UserId:           55,
		Username:         "alice",
		TokenId:          9,
		TokenName:        "primary-token",
		ModelName:        "gpt-test",
		Content:          "hello world",
		Quota:            1234,
		PromptTokens:     10,
		CompletionTokens: 20,
		UseTime:          3,
		IsStream:         true,
		ChannelId:        7,
		ChannelName:      "channel-a",
		Group:            "default",
		Ip:               "10.0.0.1",
		Other:            "raw-extra-json",
		ChannelType:      4,
		RelayMode:        1,
		UpstreamModel:    "upstream-gpt",
		TotalLatencyMs:   250,
	}
}

func TestConvertLogToDocument(t *testing.T) {
	log := sampleLog()
	doc := ConvertLogToDocument(log)

	if doc.ID != log.Id || doc.Username != log.Username || doc.Content != log.Content {
		t.Fatalf("ConvertLogToDocument core fields mismatch: %+v vs %+v", doc, log)
	}
	if doc.ChannelType != log.ChannelType || doc.RelayMode != log.RelayMode ||
		doc.UpstreamModel != log.UpstreamModel || doc.TotalLatencyMs != log.TotalLatencyMs {
		t.Fatalf("ConvertLogToDocument governance fields mismatch: doc=%+v log=%+v", doc, log)
	}
	if doc.IsStream != true {
		t.Fatalf("ConvertLogToDocument.IsStream = %v, want true", doc.IsStream)
	}
}

// FINDING: internal/pkg/search/logs_index.go:99-120 ConvertDocumentToLog drops
// the "governance" fields (ChannelType, RelayMode, UpstreamModel,
// TotalLatencyMs) and Other even though both LogDocument and Log carry them.
// Repro: round-trip a document produced by ConvertLogToDocument back through
// ConvertDocumentToLog (as SearchLogs does for every search hit) — the
// returned Log always reports ChannelType=0, RelayMode=0, UpstreamModel="",
// TotalLatencyMs=0 regardless of what was indexed/searched.
// Expected: SearchLogs results should preserve those fields like every other
// field on the document. Actual: they are silently zeroed on every search.
func TestConvertDocumentToLog_DropsGovernanceFields(t *testing.T) {
	original := sampleLog()
	doc := ConvertLogToDocument(original)
	roundTripped := ConvertDocumentToLog(doc)

	// Fields that DO survive the round trip.
	if roundTripped.Id != original.Id || roundTripped.Username != original.Username ||
		roundTripped.Content != original.Content || roundTripped.ChannelName != original.ChannelName {
		t.Fatalf("expected core fields preserved, got %+v from %+v", roundTripped, original)
	}

	// Fields the current implementation loses (see FINDING above). This
	// assertion documents the actual (buggy) behavior: it must go red the
	// moment someone starts copying these fields without also fixing the
	// callers that may depend on the current zero-value behavior.
	if roundTripped.ChannelType != 0 || roundTripped.RelayMode != 0 ||
		roundTripped.UpstreamModel != "" || roundTripped.TotalLatencyMs != 0 {
		t.Fatalf("ConvertDocumentToLog unexpectedly preserved governance fields (%+v); "+
			"update the FINDING comment if this was intentionally fixed", roundTripped)
	}
}

func TestEscapeFilterString(t *testing.T) {
	bs := "\\" // one literal backslash
	qt := "\"" // one literal double quote

	cases := []struct {
		name  string
		in    string
		want  string
	}{
		{"no special chars", "plain-value", "plain-value"},
		{"single quote char", "a" + qt + "b", "a" + bs + qt + "b"},
		{"single backslash char", "a" + bs + "b", "a" + bs + bs + "b"},
		{"backslash then quote", "a" + bs + qt + "b", "a" + bs + bs + bs + qt + "b"},
		{"injection-style attempt", `x" OR "1"="1`, `x\" OR \"1\"=\"1`},
		{"empty string", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeFilterString(tc.in)
			if got != tc.want {
				t.Fatalf("escapeFilterString(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// The escaped output must never contain a bare (unescaped) quote,
			// since that is exactly the injection primitive being defended
			// against when the value is interpolated into `field = "<value>"`.
			for i := 0; i < len(got); i++ {
				if got[i] == '"' {
					if i == 0 || got[i-1] != '\\' {
						t.Fatalf("escapeFilterString(%q) = %q has an unescaped quote at byte %d", tc.in, got, i)
					}
				}
			}
		})
	}
}

func TestBuildLogsFilter(t *testing.T) {
	cases := []struct {
		name   string
		params SearchLogsParams
		want   string
	}{
		{"all zero values produces no filter", SearchLogsParams{}, ""},
		{"type only", SearchLogsParams{Type: 3}, "type = 3"},
		{"type zero is excluded", SearchLogsParams{Type: 0, Username: "bob"}, `username = "bob"`},
		{"negative type is excluded", SearchLogsParams{Type: -1}, ""},
		{"start timestamp only", SearchLogsParams{StartTimestamp: 100}, "created_at >= 100"},
		{"end timestamp only", SearchLogsParams{EndTimestamp: 200}, "created_at <= 200"},
		{"time range", SearchLogsParams{StartTimestamp: 100, EndTimestamp: 200},
			"created_at >= 100 AND created_at <= 200"},
		{"channel id zero excluded", SearchLogsParams{ChannelID: 0}, ""},
		{"channel id negative excluded", SearchLogsParams{ChannelID: -5}, ""},
		{"channel id positive", SearchLogsParams{ChannelID: 42}, "channel_id = 42"},
		{"group filter", SearchLogsParams{Group: "team-a"}, `group = "team-a"`},
		{"username with quote is escaped", SearchLogsParams{Username: `o"brien`},
			`username = "o\"brien"`},
		{"every filter combined preserves declaration order", SearchLogsParams{
			Type: 1, StartTimestamp: 10, EndTimestamp: 20,
			Username: "alice", TokenName: "tok", ModelName: "gpt-test",
			ChannelID: 3, Group: "grp",
		}, `type = 1 AND created_at >= 10 AND created_at <= 20 AND username = "alice" AND token_name = "tok" AND model_name = "gpt-test" AND channel_id = 3 AND group = "grp"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildLogsFilter(tc.params); got != tc.want {
				t.Fatalf("buildLogsFilter(%+v) = %q, want %q", tc.params, got, tc.want)
			}
		})
	}
}

func TestGetIntFromMap(t *testing.T) {
	m := map[string]interface{}{
		"f":   float64(42),
		"i64": int64(43),
		"i":   int(44),
		"str": "not-a-number",
	}
	cases := []struct {
		key  string
		want int64
	}{
		{"f", 42}, {"i64", 43}, {"i", 44}, {"str", 0}, {"missing", 0},
	}
	for _, tc := range cases {
		if got := getIntFromMap(m, tc.key); got != tc.want {
			t.Fatalf("getIntFromMap(%q) = %d, want %d", tc.key, got, tc.want)
		}
	}
	if got := getIntFromMap(nil, "f"); got != 0 {
		t.Fatalf("getIntFromMap(nil map) = %d, want 0", got)
	}
}

func TestGetStringFromMap(t *testing.T) {
	m := map[string]interface{}{"s": "value", "n": 5}
	if got := getStringFromMap(m, "s"); got != "value" {
		t.Fatalf("getStringFromMap(s) = %q, want %q", got, "value")
	}
	if got := getStringFromMap(m, "n"); got != "" {
		t.Fatalf("getStringFromMap(n) with wrong type = %q, want empty", got)
	}
	if got := getStringFromMap(m, "missing"); got != "" {
		t.Fatalf("getStringFromMap(missing) = %q, want empty", got)
	}
}

func TestGetBoolFromMap(t *testing.T) {
	m := map[string]interface{}{"b": true, "s": "true"}
	if got := getBoolFromMap(m, "b"); got != true {
		t.Fatalf("getBoolFromMap(b) = %v, want true", got)
	}
	if got := getBoolFromMap(m, "s"); got != false {
		t.Fatalf("getBoolFromMap(s) with string type = %v, want false", got)
	}
	if got := getBoolFromMap(m, "missing"); got != false {
		t.Fatalf("getBoolFromMap(missing) = %v, want false", got)
	}
}

func TestIndexLog(t *testing.T) {
	t.Run("disabled is a silent no-op", func(t *testing.T) {
		withDisabled(t)
		if err := IndexLog(sampleLog()); err != nil {
			t.Fatalf("IndexLog() error = %v, want nil when disabled", err)
		}
	})

	t.Run("enabled indexes a single document", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := IndexLog(sampleLog()); err != nil {
			t.Fatalf("IndexLog() error = %v, want nil", err)
		}
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Method != http.MethodPost || reqs[0].Path != "/indexes/logs/documents" {
			t.Fatalf("expected a single POST /indexes/logs/documents, got %+v", reqs)
		}
		if !strings.Contains(string(reqs[0].Body), `"id":101`) {
			t.Fatalf("request body %s does not carry the log id", reqs[0].Body)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusBadRequest)
			return true
		})
		err := IndexLog(sampleLog())
		if err == nil {
			t.Fatal("IndexLog() error = nil, want error on upstream failure")
		}
		if !strings.Contains(err.Error(), "failed to index log") {
			t.Fatalf("IndexLog() error = %q, want it to mention 'failed to index log'", err.Error())
		}
	})
}

func TestIndexLogsBatch(t *testing.T) {
	t.Run("disabled or empty input is a no-op", func(t *testing.T) {
		withDisabled(t)
		if err := IndexLogsBatch([]*Log{sampleLog()}); err != nil {
			t.Fatalf("IndexLogsBatch() disabled error = %v, want nil", err)
		}

		_, fm := withFakeClient(t)
		if err := IndexLogsBatch(nil); err != nil {
			t.Fatalf("IndexLogsBatch(nil) error = %v, want nil", err)
		}
		if len(fm.requests()) != 0 {
			t.Fatalf("IndexLogsBatch(nil) issued %d HTTP requests, want 0", len(fm.requests()))
		}
	})

	t.Run("splits into multiple requests honoring SyncBatchSize", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncBatchSize = 2 // force 5 logs into 3 batches: 2,2,1

		logs := make([]*Log, 5)
		for i := range logs {
			l := sampleLog()
			l.Id = i + 1
			logs[i] = l
		}
		if err := IndexLogsBatch(logs); err != nil {
			t.Fatalf("IndexLogsBatch() error = %v, want nil", err)
		}
		reqs := fm.requests()
		if len(reqs) != 3 {
			t.Fatalf("issued %d document-add requests, want 3 batches for 5 logs at batch size 2", len(reqs))
		}
	})

	t.Run("stops and reports error on the failing batch", func(t *testing.T) {
		_, fm := withFakeClient(t)
		SyncBatchSize = 2
		calls := 0
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/indexes/logs/documents" {
				return false
			}
			calls++
			if calls == 2 {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})

		logs := make([]*Log, 5)
		for i := range logs {
			l := sampleLog()
			l.Id = i + 1
			logs[i] = l
		}
		err := IndexLogsBatch(logs)
		if err == nil {
			t.Fatal("IndexLogsBatch() error = nil, want error when a batch fails")
		}
		if !strings.Contains(err.Error(), "failed to index log batch") {
			t.Fatalf("IndexLogsBatch() error = %q, want batch-failure message", err.Error())
		}
		if calls != 2 {
			t.Fatalf("expected exactly 2 batch attempts before stopping, got %d", calls)
		}
	})
}

func TestSearchLogs(t *testing.T) {
	t.Run("disabled returns an explicit error", func(t *testing.T) {
		withDisabled(t)
		logs, total, err := SearchLogs(SearchLogsParams{Keyword: "x"})
		if err == nil {
			t.Fatal("SearchLogs() error = nil, want error when disabled")
		}
		if logs != nil || total != 0 {
			t.Fatalf("SearchLogs() disabled = (%v, %d), want (nil, 0)", logs, total)
		}
	})

	t.Run("defaults page<1 to 1 and pageSize<1 to 10, offset math is correct", func(t *testing.T) {
		_, fm := withFakeClient(t)
		var capturedOffset, capturedLimit int64 = -1, -1
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/search") {
				return false
			}
			var req meilisearch.SearchRequest
			_ = json.Unmarshal(fm.lastBody(), &req)
			capturedOffset = req.Offset
			capturedLimit = req.Limit
			writeJSON(w, http.StatusOK, map[string]interface{}{"hits": []interface{}{}, "estimatedTotalHits": 0})
			return true
		})

		_, _, err := SearchLogs(SearchLogsParams{Page: 0, PageSize: 0})
		if err != nil {
			t.Fatalf("SearchLogs() error = %v, want nil", err)
		}
		if capturedLimit != 10 {
			t.Fatalf("captured Limit = %d, want default 10 for PageSize<1", capturedLimit)
		}
		if capturedOffset != 0 {
			t.Fatalf("captured Offset = %d, want 0 for defaulted page 1", capturedOffset)
		}
	})

	t.Run("page 3 with pageSize 20 computes offset 40", func(t *testing.T) {
		_, fm := withFakeClient(t)
		var capturedOffset int64 = -1
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/search") {
				return false
			}
			var req meilisearch.SearchRequest
			_ = json.Unmarshal(fm.lastBody(), &req)
			capturedOffset = req.Offset
			writeJSON(w, http.StatusOK, map[string]interface{}{"hits": []interface{}{}, "estimatedTotalHits": 0})
			return true
		})
		if _, _, err := SearchLogs(SearchLogsParams{Page: 3, PageSize: 20}); err != nil {
			t.Fatalf("SearchLogs() error = %v, want nil", err)
		}
		if capturedOffset != 40 {
			t.Fatalf("captured Offset = %d, want 40 ((3-1)*20)", capturedOffset)
		}
	})

	t.Run("decodes hits and prefers TotalHits over EstimatedTotalHits when set", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/search") {
				return false
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"hits": []map[string]interface{}{
					{"id": 5, "username": "carol", "content": "found me", "quota": 99},
				},
				"estimatedTotalHits": 1000,
				"totalHits":          7,
			})
			return true
		})
		logs, total, err := SearchLogs(SearchLogsParams{Keyword: "found"})
		if err != nil {
			t.Fatalf("SearchLogs() error = %v, want nil", err)
		}
		if total != 7 {
			t.Fatalf("total = %d, want 7 (TotalHits overrides EstimatedTotalHits)", total)
		}
		if len(logs) != 1 || logs[0].Id != 5 || logs[0].Username != "carol" {
			t.Fatalf("logs = %+v, want a single decoded hit for carol", logs)
		}
	})

	t.Run("falls back to EstimatedTotalHits when TotalHits is zero", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/search") {
				return false
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"hits":               []interface{}{},
				"estimatedTotalHits": 55,
			})
			return true
		})
		_, total, err := SearchLogs(SearchLogsParams{})
		if err != nil {
			t.Fatalf("SearchLogs() error = %v, want nil", err)
		}
		if total != 55 {
			t.Fatalf("total = %d, want 55 (EstimatedTotalHits used when TotalHits unset)", total)
		}
	})

	t.Run("malformed hit is skipped instead of aborting the whole page", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/search") {
				return false
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"hits": []map[string]interface{}{
					{"id": "not-a-number"}, // wrong type for LogDocument.ID (int) — DecodeInto must error
					{"id": 8, "username": "dave"},
				},
				"estimatedTotalHits": 2,
			})
			return true
		})
		logs, _, err := SearchLogs(SearchLogsParams{})
		if err != nil {
			t.Fatalf("SearchLogs() error = %v, want nil (malformed hit should be skipped, not fatal)", err)
		}
		if len(logs) != 1 || logs[0].Id != 8 {
			t.Fatalf("logs = %+v, want only the well-formed hit for dave (id 8)", logs)
		}
	})

	t.Run("upstream search failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/search") {
				return false
			}
			w.WriteHeader(http.StatusBadRequest)
			return true
		})
		_, _, err := SearchLogs(SearchLogsParams{Keyword: strings.Repeat("q", 5000)})
		if err == nil {
			t.Fatal("SearchLogs() error = nil, want error on upstream failure")
		}
		if !strings.Contains(err.Error(), "failed to search logs") {
			t.Fatalf("SearchLogs() error = %q, want it to mention 'failed to search logs'", err.Error())
		}
	})
}

func TestDeleteLogsByIDs(t *testing.T) {
	t.Run("disabled or empty ids is a no-op", func(t *testing.T) {
		withDisabled(t)
		if err := DeleteLogsByIDs([]int{1, 2}); err != nil {
			t.Fatalf("DeleteLogsByIDs() disabled error = %v, want nil", err)
		}
		_, fm := withFakeClient(t)
		if err := DeleteLogsByIDs(nil); err != nil {
			t.Fatalf("DeleteLogsByIDs(nil) error = %v, want nil", err)
		}
		if len(fm.requests()) != 0 {
			t.Fatalf("DeleteLogsByIDs(nil) issued requests: %+v, want none", fm.requests())
		}
	})

	t.Run("deletes by id list", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := DeleteLogsByIDs([]int{1, 2, 3}); err != nil {
			t.Fatalf("DeleteLogsByIDs() error = %v, want nil", err)
		}
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Path != "/indexes/logs/documents/delete-batch" {
			t.Fatalf("expected one delete-batch call, got %+v", reqs)
		}
		if !strings.Contains(string(reqs[0].Body), `"1"`) || !strings.Contains(string(reqs[0].Body), `"3"`) {
			t.Fatalf("delete-batch body %s missing expected string ids", reqs[0].Body)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		if err := DeleteLogsByIDs([]int{1}); err == nil {
			t.Fatal("DeleteLogsByIDs() error = nil, want error on upstream failure")
		}
	})
}

func TestClearLogsIndex(t *testing.T) {
	t.Run("disabled returns an explicit error", func(t *testing.T) {
		withDisabled(t)
		if err := ClearLogsIndex(); err == nil {
			t.Fatal("ClearLogsIndex() error = nil, want error when disabled")
		}
	})

	t.Run("enabled clears the whole index", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := ClearLogsIndex(); err != nil {
			t.Fatalf("ClearLogsIndex() error = %v, want nil", err)
		}
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Method != http.MethodDelete || reqs[0].Path != "/indexes/logs/documents" {
			t.Fatalf("expected one DELETE /indexes/logs/documents, got %+v", reqs)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		if err := ClearLogsIndex(); err == nil {
			t.Fatal("ClearLogsIndex() error = nil, want error on upstream failure")
		}
	})
}

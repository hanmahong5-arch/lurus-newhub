package search

import (
	"net/http"
	"strings"
	"testing"
)

func TestConvertChannelToDocument(t *testing.T) {
	t.Run("nil optional pointers leave zero values", func(t *testing.T) {
		ch := &Channel{
			Id: 1, Type: 3, Name: "chan-a", Models: "gpt-4,gpt-3.5",
			Group: "default", Status: 1, Balance: 12.5, TestTime: 100,
			BaseURL: nil, Tag: nil, Priority: nil,
		}
		doc := ConvertChannelToDocument(ch)
		if doc.ID != 1 || doc.Name != "chan-a" || doc.Balance != 12.5 {
			t.Fatalf("ConvertChannelToDocument core fields mismatch: %+v", doc)
		}
		if doc.BaseURL != "" || doc.Tag != "" || doc.Priority != 0 {
			t.Fatalf("ConvertChannelToDocument() with nil pointers = %+v, want empty/zero optional fields", doc)
		}
	})

	t.Run("non-nil optional pointers are dereferenced", func(t *testing.T) {
		url := "https://api.example.com"
		tag := "prod"
		priority := int64(42)
		ch := &Channel{
			Id: 2, Name: "chan-b", BaseURL: &url, Tag: &tag, Priority: &priority,
		}
		doc := ConvertChannelToDocument(ch)
		if doc.BaseURL != url || doc.Tag != tag || doc.Priority != priority {
			t.Fatalf("ConvertChannelToDocument() = %+v, want BaseURL=%q Tag=%q Priority=%d", doc, url, tag, priority)
		}
	})
}

func TestIndexChannel(t *testing.T) {
	t.Run("disabled is a silent no-op", func(t *testing.T) {
		withDisabled(t)
		if err := IndexChannel(&Channel{Id: 1, Name: "x"}); err != nil {
			t.Fatalf("IndexChannel() error = %v, want nil when disabled", err)
		}
	})

	t.Run("enabled indexes the channel document", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := IndexChannel(&Channel{Id: 3, Name: "chan-c"}); err != nil {
			t.Fatalf("IndexChannel() error = %v, want nil", err)
		}
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Method != http.MethodPost || reqs[0].Path != "/indexes/channels/documents" {
			t.Fatalf("expected one POST /indexes/channels/documents, got %+v", reqs)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		err := IndexChannel(&Channel{Id: 3, Name: "chan-c"})
		if err == nil {
			t.Fatal("IndexChannel() error = nil, want error on upstream failure")
		}
		if !strings.Contains(err.Error(), "failed to index channel") {
			t.Fatalf("IndexChannel() error = %q, want it to mention 'failed to index channel'", err.Error())
		}
	})
}

func TestSearchChannels(t *testing.T) {
	t.Run("disabled returns an explicit error", func(t *testing.T) {
		withDisabled(t)
		results, total, err := SearchChannels("q", "", 0, 1, 10)
		if err == nil {
			t.Fatal("SearchChannels() error = nil, want error when disabled")
		}
		if results != nil || total != 0 {
			t.Fatalf("SearchChannels() disabled = (%v, %d), want (nil, 0)", results, total)
		}
	})

	t.Run("no filters when group and status are unset", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if _, _, err := SearchChannels("", "", 0, 1, 10); err != nil {
			t.Fatalf("SearchChannels() error = %v, want nil", err)
		}
		body := string(fm.lastBody())
		if strings.Contains(body, "filter") {
			t.Fatalf("search body %s should omit 'filter' entirely when no filters apply", body)
		}
	})

	t.Run("builds group+status filter", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if _, _, err := SearchChannels("q", "grp\"1", 3, 1, 10); err != nil {
			t.Fatalf("SearchChannels() error = %v, want nil", err)
		}
		body := string(fm.lastBody())
		if !strings.Contains(body, `group = \"grp\\\"1\"`) {
			t.Fatalf("search body %s missing escaped group filter", body)
		}
		if !strings.Contains(body, "status = 3") {
			t.Fatalf("search body %s missing status filter", body)
		}
	})

	t.Run("decodes results and reports total", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/search") {
				return false
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"hits":               []map[string]interface{}{{"id": float64(9), "name": "chan-z"}},
				"estimatedTotalHits": 1,
			})
			return true
		})
		results, total, err := SearchChannels("chan", "", 0, 1, 10)
		if err != nil {
			t.Fatalf("SearchChannels() error = %v, want nil", err)
		}
		if total != 1 || len(results) != 1 || results[0]["name"] != "chan-z" {
			t.Fatalf("SearchChannels() = (%v, %d), want single chan-z hit", results, total)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		_, _, err := SearchChannels("x", "", 0, 1, 10)
		if err == nil {
			t.Fatal("SearchChannels() error = nil, want error on upstream failure")
		}
		if !strings.Contains(err.Error(), "failed to search channels") {
			t.Fatalf("SearchChannels() error = %q, want it to mention 'failed to search channels'", err.Error())
		}
	})
}

func TestDeleteChannelByID(t *testing.T) {
	t.Run("disabled is a silent no-op", func(t *testing.T) {
		withDisabled(t)
		if err := DeleteChannelByID(4); err != nil {
			t.Fatalf("DeleteChannelByID() error = %v, want nil when disabled", err)
		}
	})

	t.Run("enabled deletes the document by id", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := DeleteChannelByID(4); err != nil {
			t.Fatalf("DeleteChannelByID() error = %v, want nil", err)
		}
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Method != http.MethodDelete || reqs[0].Path != "/indexes/channels/documents/4" {
			t.Fatalf("expected DELETE /indexes/channels/documents/4, got %+v", reqs)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		if err := DeleteChannelByID(4); err == nil {
			t.Fatal("DeleteChannelByID() error = nil, want error on upstream failure")
		}
	})
}

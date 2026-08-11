package search

import (
	"net/http"
	"strings"
	"testing"
)

func TestInitializeIndexes(t *testing.T) {
	t.Run("disabled returns an explicit error before any HTTP call", func(t *testing.T) {
		withDisabled(t)
		if err := InitializeIndexes(); err == nil {
			t.Fatal("InitializeIndexes() error = nil, want error when disabled")
		}
	})

	t.Run("enabled provisions logs, users and channels indexes in order", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := InitializeIndexes(); err != nil {
			t.Fatalf("InitializeIndexes() error = %v, want nil", err)
		}
		var createdOrder []string
		for _, r := range fm.requests() {
			if r.Method == http.MethodPost && r.Path == "/indexes" {
				createdOrder = append(createdOrder, string(r.Body))
			}
		}
		if len(createdOrder) != 3 {
			t.Fatalf("saw %d index creations, want 3", len(createdOrder))
		}
		if !strings.Contains(createdOrder[0], `"logs"`) ||
			!strings.Contains(createdOrder[1], `"users"`) ||
			!strings.Contains(createdOrder[2], `"channels"`) {
			t.Fatalf("index creation order = %v, want logs, users, channels", createdOrder)
		}
	})

	t.Run("logs index settings failure short-circuits before users/channels", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasPrefix(r.URL.Path, "/indexes/logs/settings/") {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		err := InitializeIndexes()
		if err == nil {
			t.Fatal("InitializeIndexes() error = nil, want error when logs index settings fail")
		}
		if !strings.Contains(err.Error(), "failed to initialize logs index") {
			t.Fatalf("InitializeIndexes() error = %q, want it to name the logs index", err.Error())
		}
		for _, r := range fm.requests() {
			if r.Path == "/indexes/users/documents" {
				t.Fatal("users index work happened after logs index failed; expected short-circuit")
			}
		}
	})

	t.Run("users index settings failure is wrapped and named", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasPrefix(r.URL.Path, "/indexes/users/settings/") {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		err := InitializeIndexes()
		if err == nil {
			t.Fatal("InitializeIndexes() error = nil, want error when users index settings fail")
		}
		if !strings.Contains(err.Error(), "failed to initialize users index") {
			t.Fatalf("InitializeIndexes() error = %q, want it to name the users index", err.Error())
		}
	})

	t.Run("channels index settings failure is wrapped and named", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasPrefix(r.URL.Path, "/indexes/channels/settings/") {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		err := InitializeIndexes()
		if err == nil {
			t.Fatal("InitializeIndexes() error = nil, want error when channels index settings fail")
		}
		if !strings.Contains(err.Error(), "failed to initialize channels index") {
			t.Fatalf("InitializeIndexes() error = %q, want it to name the channels index", err.Error())
		}
	})

	t.Run("logs index faceting failure is surfaced", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/indexes/logs/settings/faceting" {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		err := InitializeIndexes()
		if err == nil {
			t.Fatal("InitializeIndexes() error = nil, want error when logs faceting update fails")
		}
		if !strings.Contains(err.Error(), "failed to update faceting settings") {
			t.Fatalf("InitializeIndexes() error = %q, want it to mention faceting", err.Error())
		}
	})

	t.Run("logs index pagination failure is surfaced", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/indexes/logs/settings/pagination" {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		err := InitializeIndexes()
		if err == nil {
			t.Fatal("InitializeIndexes() error = nil, want error when logs pagination update fails")
		}
		if !strings.Contains(err.Error(), "failed to update pagination settings") {
			t.Fatalf("InitializeIndexes() error = %q, want it to mention pagination", err.Error())
		}
	})

	t.Run("logs index ranking rules failure is surfaced", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/indexes/logs/settings/ranking-rules" {
				w.WriteHeader(http.StatusInternalServerError)
				return true
			}
			return false
		})
		err := InitializeIndexes()
		if err == nil {
			t.Fatal("InitializeIndexes() error = nil, want error when logs ranking rules update fails")
		}
		if !strings.Contains(err.Error(), "failed to update ranking rules") {
			t.Fatalf("InitializeIndexes() error = %q, want it to mention ranking rules", err.Error())
		}
	})

	t.Run("index prefix propagates into every created index name", func(t *testing.T) {
		_, fm := withFakeClient(t)
		IndexPrefix = "tenantX"
		if err := InitializeIndexes(); err != nil {
			t.Fatalf("InitializeIndexes() error = %v, want nil", err)
		}
		var names []string
		for _, r := range fm.requests() {
			if r.Method == http.MethodPost && r.Path == "/indexes" {
				names = append(names, string(r.Body))
			}
		}
		for _, n := range names {
			if !strings.Contains(n, "tenantX_") {
				t.Fatalf("index create body %s missing tenant prefix", n)
			}
		}
	})
}

func TestGetIndexInfo(t *testing.T) {
	t.Run("disabled returns an explicit error", func(t *testing.T) {
		withDisabled(t)
		info, err := GetIndexInfo(IndexLogs)
		if err == nil {
			t.Fatal("GetIndexInfo() error = nil, want error when disabled")
		}
		if info != nil {
			t.Fatalf("GetIndexInfo() info = %v, want nil", info)
		}
	})

	t.Run("enabled returns document count and indexing flag", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/indexes/logs/stats" {
				return false
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"numberOfDocuments": int64(12),
				"isIndexing":        true,
				"fieldDistribution": map[string]int64{"content": 12},
			})
			return true
		})
		info, err := GetIndexInfo(IndexLogs)
		if err != nil {
			t.Fatalf("GetIndexInfo() error = %v, want nil", err)
		}
		if info["name"] != "logs" || info["documents"] != int64(12) || info["indexing"] != true {
			t.Fatalf("GetIndexInfo() = %+v, want name=logs documents=12 indexing=true", info)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusNotFound)
			return true
		})
		if _, err := GetIndexInfo(IndexLogs); err == nil {
			t.Fatal("GetIndexInfo() error = nil, want error on upstream 404")
		}
	})
}

func TestDeleteIndex(t *testing.T) {
	t.Run("disabled returns an explicit error", func(t *testing.T) {
		withDisabled(t)
		if err := DeleteIndex(IndexUsers); err == nil {
			t.Fatal("DeleteIndex() error = nil, want error when disabled")
		}
	})

	t.Run("enabled deletes the named index", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := DeleteIndex(IndexUsers); err != nil {
			t.Fatalf("DeleteIndex() error = %v, want nil", err)
		}
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Method != http.MethodDelete || reqs[0].Path != "/indexes/users" {
			t.Fatalf("expected DELETE /indexes/users, got %+v", reqs)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		if err := DeleteIndex(IndexUsers); err == nil {
			t.Fatal("DeleteIndex() error = nil, want error on upstream failure")
		}
	})
}

func TestRebuildIndex(t *testing.T) {
	t.Run("disabled returns an explicit error", func(t *testing.T) {
		withDisabled(t)
		if err := RebuildIndex(IndexLogs); err == nil {
			t.Fatal("RebuildIndex() error = nil, want error when disabled")
		}
	})

	t.Run("unknown index name is rejected even though delete already ran", func(t *testing.T) {
		_, fm := withFakeClient(t)
		err := RebuildIndex("not-a-real-index")
		if err == nil {
			t.Fatal("RebuildIndex() error = nil, want error for unknown index name")
		}
		if !strings.Contains(err.Error(), "unknown index name") {
			t.Fatalf("RebuildIndex() error = %q, want it to mention the unknown name", err.Error())
		}
		deleteSeen := false
		for _, r := range fm.requests() {
			if r.Method == http.MethodDelete && r.Path == "/indexes/not-a-real-index" {
				deleteSeen = true
			}
		}
		if !deleteSeen {
			t.Fatal("RebuildIndex() did not attempt to delete the stale index before validating the name")
		}
	})

	t.Run("rebuilds each known index type", func(t *testing.T) {
		for _, idx := range []string{IndexLogs, IndexUsers, IndexChannels} {
			t.Run(idx, func(t *testing.T) {
				_, fm := withFakeClient(t)
				if err := RebuildIndex(idx); err != nil {
					t.Fatalf("RebuildIndex(%q) error = %v, want nil", idx, err)
				}
				deleteSeen, createSeen := false, false
				for _, r := range fm.requests() {
					if r.Method == http.MethodDelete && r.Path == "/indexes/"+idx {
						deleteSeen = true
					}
					if r.Method == http.MethodPost && r.Path == "/indexes" {
						createSeen = true
					}
				}
				if !deleteSeen || !createSeen {
					t.Fatalf("RebuildIndex(%q) requests = %+v, want a delete then a create", idx, fm.requests())
				}
			})
		}
	})

	t.Run("delete failure for a nonexistent index is tolerated (ignored) before recreation", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Method == http.MethodDelete && r.URL.Path == "/indexes/logs" {
				w.WriteHeader(http.StatusNotFound)
				return true
			}
			return false
		})
		if err := RebuildIndex(IndexLogs); err != nil {
			t.Fatalf("RebuildIndex() error = %v, want nil (delete-not-found must not abort rebuild)", err)
		}
	})
}

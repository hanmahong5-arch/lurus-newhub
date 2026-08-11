package search

import (
	"net/http"
	"strings"
	"testing"
)

func sampleUser() *User {
	return &User{
		Id:          77,
		Username:    "bob",
		Email:       "bob@example.com",
		DisplayName: "Bob B",
		Role:        1,
		Status:      1,
		Quota:       500,
		UsedQuota:   25,
		Group:       "default",
	}
}

func TestConvertUserToDocument(t *testing.T) {
	u := sampleUser()
	doc := ConvertUserToDocument(u)
	if doc.ID != u.Id || doc.Username != u.Username || doc.Email != u.Email ||
		doc.DisplayName != u.DisplayName || doc.Role != u.Role || doc.Status != u.Status ||
		doc.Quota != u.Quota || doc.UsedQuota != u.UsedQuota || doc.Group != u.Group {
		t.Fatalf("ConvertUserToDocument mismatch: doc=%+v user=%+v", doc, u)
	}
	if doc.CreatedTime != 0 {
		t.Fatalf("ConvertUserToDocument.CreatedTime = %d, want 0 (User model has no created time source)", doc.CreatedTime)
	}
}

func TestIndexUser(t *testing.T) {
	t.Run("disabled is a silent no-op", func(t *testing.T) {
		withDisabled(t)
		if err := IndexUser(sampleUser()); err != nil {
			t.Fatalf("IndexUser() error = %v, want nil when disabled", err)
		}
	})

	t.Run("enabled indexes the user document", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := IndexUser(sampleUser()); err != nil {
			t.Fatalf("IndexUser() error = %v, want nil", err)
		}
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Method != http.MethodPost || reqs[0].Path != "/indexes/users/documents" {
			t.Fatalf("expected one POST /indexes/users/documents, got %+v", reqs)
		}
		if !strings.Contains(string(reqs[0].Body), `"bob@example.com"`) {
			t.Fatalf("request body %s missing expected email", reqs[0].Body)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		err := IndexUser(sampleUser())
		if err == nil {
			t.Fatal("IndexUser() error = nil, want error on upstream failure")
		}
		if !strings.Contains(err.Error(), "failed to index user") {
			t.Fatalf("IndexUser() error = %q, want it to mention 'failed to index user'", err.Error())
		}
	})
}

func TestSearchUsers(t *testing.T) {
	t.Run("disabled returns an explicit error", func(t *testing.T) {
		withDisabled(t)
		results, total, err := SearchUsers("q", "", 0, 1, 10)
		if err == nil {
			t.Fatal("SearchUsers() error = nil, want error when disabled")
		}
		if results != nil || total != 0 {
			t.Fatalf("SearchUsers() disabled = (%v, %d), want (nil, 0)", results, total)
		}
	})

	t.Run("builds combined group+status filter and paginates", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/search") {
				return false
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"hits":               []map[string]interface{}{{"id": float64(1), "username": "eve"}},
				"estimatedTotalHits": 1,
			})
			return true
		})
		results, total, err := SearchUsers("eve", "team-x", 2, 2, 5)
		if err != nil {
			t.Fatalf("SearchUsers() error = %v, want nil", err)
		}
		if total != 1 || len(results) != 1 || results[0]["username"] != "eve" {
			t.Fatalf("SearchUsers() = (%v, %d), want a single eve hit", results, total)
		}
		body := string(fm.lastBody())
		if !strings.Contains(body, `group = \"team-x\"`) || !strings.Contains(body, "status = 2") {
			t.Fatalf("search filter body %s missing expected group/status clauses", body)
		}
		if !strings.Contains(body, `"offset":5`) || !strings.Contains(body, `"limit":5`) {
			t.Fatalf("search body %s missing expected offset/limit for page 2 size 5", body)
		}
	})

	t.Run("negative page and pageSize default safely", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if _, _, err := SearchUsers("", "", 0, -3, -1); err != nil {
			t.Fatalf("SearchUsers() error = %v, want nil", err)
		}
		body := string(fm.lastBody())
		// Offset 0 is the JSON zero value and Meilisearch's SearchRequest
		// omits it entirely (omitempty) — its *absence* is what proves page
		// defaulted to 1; Limit is always emitted so we assert it directly.
		if strings.Contains(body, `"offset"`) {
			t.Fatalf("search body %s carries an explicit offset, want it omitted for page 1", body)
		}
		if !strings.Contains(body, `"limit":10`) {
			t.Fatalf("search body %s want limit 10 default for pageSize<1", body)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		_, _, err := SearchUsers("x", "", 0, 1, 10)
		if err == nil {
			t.Fatal("SearchUsers() error = nil, want error on upstream failure")
		}
		if !strings.Contains(err.Error(), "failed to search users") {
			t.Fatalf("SearchUsers() error = %q, want it to mention 'failed to search users'", err.Error())
		}
	})
}

func TestDeleteUserByID(t *testing.T) {
	t.Run("disabled is a silent no-op", func(t *testing.T) {
		withDisabled(t)
		if err := DeleteUserByID(9); err != nil {
			t.Fatalf("DeleteUserByID() error = %v, want nil when disabled", err)
		}
	})

	t.Run("enabled deletes the document by id", func(t *testing.T) {
		_, fm := withFakeClient(t)
		if err := DeleteUserByID(9); err != nil {
			t.Fatalf("DeleteUserByID() error = %v, want nil", err)
		}
		reqs := fm.requests()
		if len(reqs) != 1 || reqs[0].Method != http.MethodDelete || reqs[0].Path != "/indexes/users/documents/9" {
			t.Fatalf("expected DELETE /indexes/users/documents/9, got %+v", reqs)
		}
	})

	t.Run("upstream failure is wrapped", func(t *testing.T) {
		_, fm := withFakeClient(t)
		fm.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		if err := DeleteUserByID(9); err == nil {
			t.Fatal("DeleteUserByID() error = nil, want error on upstream failure")
		}
	})
}

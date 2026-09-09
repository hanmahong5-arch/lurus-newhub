package handler

// guide_session_id_lock_test.go — L6 doc lock: doc/product-integration-guide.md's
// X-Session-Id row used to claim the raw value "不落库,不回显" (never stored,
// never echoed back) — false since L2-REQUEST-IDENTITY: the value IS written
// to the log row's Other["session_id"] (governance.go), classified
// TierPublic (classification.go), and returned by GET /v1/generation
// (v1_generation.go). A sibling product reading the old wording would
// conclude session_id is safe for anything (it never leaves the gateway) and
// put personal data in it — the opposite of the truth. This is a static
// grep over the doc file, not a handler test: it exists in this package
// because the guide documents this package's endpoints.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuideSessionIdLock(t *testing.T) {
	// internal/adapter/handler is three levels below the repo root.
	path := filepath.Join("..", "..", "..", "doc", "product-integration-guide.md")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := string(src)

	// Anchor on the markdown table row's lead-in specifically (not a bare
	// "X-Session-Id" substring), so a prose mention elsewhere in §E (e.g.
	// listing it among the CORS-allowed inbound headers) can't be mistaken
	// for the row this lock is about.
	idx := strings.Index(doc, "| `X-Session-Id`")
	if idx < 0 {
		t.Fatal("doc/product-integration-guide.md has no X-Session-Id table row")
	}
	// The row is a single markdown table line; take a window around the
	// first mention wide enough to hold the whole row without also
	// swallowing neighbouring rows.
	end := idx + 1200
	if end > len(doc) {
		end = len(doc)
	}
	row := doc[idx:end]
	if nl := strings.Index(row, "\n"); nl >= 0 {
		row = row[:nl]
	}

	if !strings.Contains(row, "session_id") {
		t.Errorf("X-Session-Id row does not mention the session_id log field: %s", row)
	}
	if !strings.Contains(row, "/v1/generation") {
		t.Errorf("X-Session-Id row does not mention GET /v1/generation: %s", row)
	}
	if strings.Contains(row, "不落库") {
		t.Errorf("X-Session-Id row still claims the raw value is never stored (不落库) — false, it is written to the log row's session_id field: %s", row)
	}
	if strings.Contains(row, "不回显") {
		t.Errorf("X-Session-Id row still claims the raw value is never echoed back (不回显) — false, GET /v1/generation returns it: %s", row)
	}
}

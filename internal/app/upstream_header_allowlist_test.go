package app

// upstream_header_allowlist_test.go — cycle-14 L6 (PB): a successful relay
// call was handing the customer the upstream vendor's own response headers
// (measured on a live instance: Server, Eo-Cache-Status, Eo-Log-Uuid,
// X-Ds-Trace-Id, Strict-Transport-Security). That names the vendor we routed
// to, leaks the vendor CDN's log identifier, and — in the HSTS case — applies
// an upstream transport-security policy to OUR domain.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestIOCopyBytesGracefully_StripsUpstreamVendorHeaders is the PB oracle: the
// headers an upstream edge really sent must not reach the client, while the
// ones the client needs to parse the body must.
func TestIOCopyBytesGracefully_StripsUpstreamVendorHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	src := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			// Observed on a live relay response, byte for byte.
			"Server":                    {"openresty"},
			"Eo-Cache-Status":           {"MISS"},
			"Eo-Log-Uuid":               {"1234567890123456789"},
			"X-Ds-Trace-Id":             {"abcdef0123456789"},
			"Strict-Transport-Security": {"max-age=31536000; includeSubDomains; preload"},
			// Must survive.
			"Content-Type": {"application/json; charset=utf-8"},
		},
	}
	IOCopyBytesGracefully(c, src, []byte(`{"ok":true}`))

	for _, leak := range []string{"Server", "Eo-Cache-Status", "Eo-Log-Uuid", "X-Ds-Trace-Id", "Strict-Transport-Security"} {
		if got := w.Header().Get(leak); got != "" {
			t.Errorf("%s = %q reached the client — upstream vendor headers must be stripped to the explicit allow-list", leak, got)
		}
	}
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the upstream's — the client cannot parse the body without it", got)
	}
	if got := w.Header().Get("Content-Length"); got != "11" {
		t.Errorf("Content-Length = %q, want 11 (recomputed from the payload)", got)
	}
}

// TestIOCopyBytesGracefully_AllowListedBodyHeadersSurvive covers the rest of
// UpstreamHeadersForwarded in one pass, so shrinking the allow-list cannot
// pass unnoticed.
func TestIOCopyBytesGracefully_AllowListedBodyHeadersSurvive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	src := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Content-Type":        {"audio/mpeg"},
			"Content-Disposition": {`attachment; filename="speech.mp3"`},
			"Content-Encoding":    {"gzip"},
			"Retry-After":         {"30"},
		},
	}
	IOCopyBytesGracefully(c, src, []byte("bytes"))

	for name, want := range map[string]string{
		"Content-Type":        "audio/mpeg",
		"Content-Disposition": `attachment; filename="speech.mp3"`,
		"Content-Encoding":    "gzip",
		"Retry-After":         "30",
	} {
		if got := w.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestUpstreamHeaderLists_DenyListStaysDisjointFromAllowList keeps the
// deny-list meaningful now that the filter is an allow-list: the five names
// in UpstreamHeadersNotForwarded (which provider's
// TestUpstreamRequestIdHeaders_AllSkippedFromClientResponse asserts its own
// capture list is a subset of) must never be added to
// UpstreamHeadersForwarded.
func TestUpstreamHeaderLists_DenyListStaysDisjointFromAllowList(t *testing.T) {
	if len(UpstreamHeadersNotForwarded) == 0 || len(UpstreamHeadersForwarded) == 0 {
		t.Fatal("one of the two lists is empty — the comparison would be vacuous")
	}
	for name := range UpstreamHeadersNotForwarded {
		if UpstreamHeadersForwarded[name] {
			t.Errorf("%q is in both UpstreamHeadersForwarded and UpstreamHeadersNotForwarded — the allow-list would forward a header the deny-list exists to stop", name)
		}
	}
	// http.Header canonicalises names; a non-canonical key in either map
	// silently never matches src.Header.
	for _, m := range []map[string]bool{UpstreamHeadersForwarded, UpstreamHeadersNotForwarded} {
		for name := range m {
			if canon := http.CanonicalHeaderKey(name); canon != name {
				t.Errorf("%q is not in canonical form (%q) — it can never match a key in src.Header", name, canon)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Inventory gate for the OTHER upstream-header copy sites.
//
// IOCopyBytesGracefully is the main relay path, but it is not the only place
// that copies an upstream response's headers onto our writer. The sites below
// are the ones the scan below finds today; the
// four outside this file still forward EVERYTHING the vendor sent, and each
// is in a package this lane does not own (hand-off #4). The gate exists so a
// NEW copy site cannot be added without someone deciding which posture it
// takes — and so this list cannot quietly go stale.
//
// BLIND SPOT: it is a TEXT scan for one idiom — a
//
//	for <k>, <v> := range <something>.Header {
//
// line, followed within twelve lines by a line that writes a header through
// one of the four spellings this repo actually uses (Writer.Header().Set,
// Writer.Header().Add, Writer.Header()[...] =, or gin's c.Header) — in
// non-test .go files under internal/. It therefore cannot see: a copy done
// through a helper function or maps.Copy; a copy whose write is more than
// twelve lines below the loop; a copy of header values pulled out one at a
// time by name; or anything outside internal/. It can also over-report: an
// unrelated header write twelve lines under an unrelated Header loop counts
// as a site, which costs an entry in the list and nothing else. It proves
// nothing about whether a listed site is CORRECT — only that the set of
// sites matching this idiom is the set someone last looked at.
// ---------------------------------------------------------------------------

// knownUpstreamHeaderCopySites maps a repo-relative path to why it is here.
var knownUpstreamHeaderCopySites = map[string]string{
	"internal/app/http.go":                           "IOCopyBytesGracefully — filtered through UpstreamHeadersForwarded (this lane)",
	"internal/adapter/provider/openai/audio.go":      "text-to-speech passthrough — still forwards everything (hand-off #4)",
	"internal/adapter/provider/task/suno/adaptor.go": "async task submit — still forwards everything (hand-off #4)",
	"internal/adapter/provider/minimax/tts.go":       "text-to-speech passthrough — still forwards everything (hand-off #4)",
	"internal/adapter/handler/video_proxy.go":        "video asset proxy — still forwards everything (hand-off #4)",
}

var headerCopyLoop = regexp.MustCompile(`for\s+\w+\s*,\s*\w+\s*:=\s*range\s+\w+\.Header\s*\{`)

// headerWriteSpellings are the ways this repo writes a response header; a
// scan that knew only the first one missed three of the five sites.
var headerWriteSpellings = []string{
	"Writer.Header().Set(",
	"Writer.Header().Add(",
	"Writer.Header()[",
	"c.Header(",
}

func lineWritesAHeader(line string) bool {
	if strings.HasPrefix(strings.TrimSpace(line), "//") {
		return false
	}
	for _, spelling := range headerWriteSpellings {
		if strings.Contains(line, spelling) {
			return true
		}
	}
	return false
}

func TestUpstreamHeaderCopySites_AreAllAccountedFor(t *testing.T) {
	root := filepath.Join("..", "..") // internal/app -> repo root
	found := map[string]bool{}
	filesScanned := 0

	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		filesScanned++
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			// A commented-out copy loop is not a copy site. (Two of them
			// exist in this repo; counting comments as code is exactly how a
			// previous gate in this repo reported a clean tree.)
			if strings.HasPrefix(strings.TrimSpace(line), "//") || !headerCopyLoop.MatchString(line) {
				continue
			}
			for j := i + 1; j < len(lines) && j <= i+12; j++ {
				if strings.Contains(lines[j], "func ") {
					break // a new declaration: out of this loop body
				}
				if lineWritesAHeader(lines[j]) {
					rel, relErr := filepath.Rel(root, path)
					if relErr != nil {
						return relErr
					}
					found[filepath.ToSlash(rel)] = true
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if filesScanned < 100 {
		t.Fatalf("scanned only %d files under internal/ — the walk is broken, not the tree clean", filesScanned)
	}
	if len(found) == 0 {
		t.Fatal("the scan matched zero header-copy sites, including this package's own — the pattern is broken")
	}

	for site := range found {
		if _, known := knownUpstreamHeaderCopySites[site]; !known {
			t.Errorf("%s copies upstream response headers onto our writer and is not in knownUpstreamHeaderCopySites: decide its posture (strip to an allow-list, or record here why forwarding everything is right for it) before shipping it", site)
		}
	}
	for site := range knownUpstreamHeaderCopySites {
		if !found[site] {
			t.Errorf("knownUpstreamHeaderCopySites lists %s but the scan no longer finds a copy loop there — remove the entry (or fix the scan) so this inventory cannot go stale", site)
		}
	}
	// Self-check on the comment skip: both of these files contain a
	// COMMENTED-OUT copy loop and nothing else. If the scan ever counted
	// comments as code (a gate in this repo once counted comments as
	// consumers and reported a clean tree), they would show up here.
	for _, commentedOut := range []string{"internal/app/midjourney.go", "internal/app/relay/mjproxy_handler.go"} {
		if found[commentedOut] {
			t.Errorf("%s was counted as a copy site, but its copy loop is commented out — the scan is counting comments as code", commentedOut)
		}
	}
	t.Logf("upstream header copy sites: %d found across %d scanned files", len(found), filesScanned)
}

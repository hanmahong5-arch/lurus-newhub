package app

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/stretchr/testify/assert"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name     string
		v1       string
		v2       string
		expected int
	}{
		{
			name:     "equal_versions",
			v1:       "1.0.0",
			v2:       "1.0.0",
			expected: 0,
		},
		{
			name:     "v1_less_than_v2",
			v1:       "1.0.0",
			v2:       "1.1.0",
			expected: -1,
		},
		{
			name:     "v1_greater_than_v2",
			v1:       "2.0.0",
			v2:       "1.9.0",
			expected: 1,
		},
		{
			name:     "patch_version_comparison",
			v1:       "1.0.1",
			v2:       "1.0.0",
			expected: 1,
		},
		{
			name:     "major_version_difference",
			v1:       "2.0.0",
			v2:       "1.99.99",
			expected: 1,
		},
		{
			name:     "with_v_prefix",
			v1:       "v1.0.0",
			v2:       "v1.1.0",
			expected: -1,
		},
		{
			name:     "mixed_prefix",
			v1:       "1.0.0",
			v2:       "v1.0.0",
			expected: 0,
		},
		{
			name:     "critical_bug_10_vs_9",
			v1:       "1.10.0",
			v2:       "1.9.0",
			expected: 1, // FIXED: Was -1 with string comparison
		},
		{
			name:     "double_digit_major",
			v1:       "10.0.0",
			v2:       "9.0.0",
			expected: 1, // FIXED: Was -1 with string comparison
		},
		{
			name:     "prerelease_alpha",
			v1:       "1.0.0-alpha",
			v2:       "1.0.0",
			expected: -1,
		},
		{
			name:     "prerelease_beta_vs_alpha",
			v1:       "1.0.0-beta",
			v2:       "1.0.0-alpha",
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareVersions(tt.v1, tt.v2)
			assert.Equal(t, tt.expected, result,
				"compareVersions(%s, %s) = %d, expected %d",
				tt.v1, tt.v2, result, tt.expected)
		})
	}
}

func TestCompareVersions_NonSemver(t *testing.T) {
	// Test fallback for non-semver formats
	tests := []struct {
		name     string
		v1       string
		v2       string
		expected int
	}{
		{
			name:     "equal_non_semver",
			v1:       "latest",
			v2:       "latest",
			expected: 0,
		},
		{
			name:     "different_non_semver",
			v1:       "beta",
			v2:       "alpha",
			expected: 1, // String comparison fallback
		},
		{
			name:     "different_non_semver_less",
			v1:       "alpha",
			v2:       "beta",
			expected: -1, // String comparison fallback (v1 < v2)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareVersions(tt.v1, tt.v2)
			assert.Equal(t, tt.expected, result,
				"compareVersions(%s, %s) = %d, expected %d",
				tt.v1, tt.v2, result, tt.expected)
		})
	}
}

// TestHandleDownload_StoresMaskedIP is the cycle-13 L5 oracle for
// download_logs minimization: the row HandleDownload's async goroutine
// writes must carry repo.MaskIP's coarsened form of the caller's address,
// never the raw value, and the User-Agent must be capped at
// maxDownloadLogFieldBytes. newReleaseSvc (release_service_extra_test.go)
// does not migrate entity.DownloadLog — only Release/ReleaseArtifact — so
// this test migrates it itself before exercising the real HandleDownload
// path (same DB, same goroutine, matching TestHandleDownload_IncrementsCount
// in final_extra_test.go).
func TestHandleDownload_StoresMaskedIP(t *testing.T) {
	db := setupServiceTestDB(t)
	svc := newReleaseSvc(t, db)
	if err := db.AutoMigrate(&entity.DownloadLog{}); err != nil {
		t.Fatalf("migrate download_logs: %v", err)
	}

	rel := entity.Release{ProductId: "switch", Version: "1.0.0", Title: "R", IsPublished: true}
	if err := db.Create(&rel).Error; err != nil {
		t.Fatalf("seed release: %v", err)
	}
	art := entity.ReleaseArtifact{
		ReleaseId:      rel.Id,
		Platform:       "linux",
		Arch:           "amd64",
		Filename:       "switch-linux-amd64",
		FileSize:       10,
		StoragePath:    "tools/switch/1.0.0/switch-linux-amd64",
		ChecksumSha256: "deadbeef",
	}
	if err := db.Create(&art).Error; err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	remoteAddr := "203.0.113.42"
	oversizedUA := strings.Repeat("A", maxDownloadLogFieldBytes+100) // exceeds the cap
	// Referer is capped by the same helper and needs its own oversized
	// input: with a short one here, deleting the Referer truncation left
	// every test in this package green (cycle-13 L5 repair, D-L5-2).
	oversizedReferer := "https://example.com/ref?q=" + strings.Repeat("b", maxDownloadLogFieldBytes)
	if err := svc.HandleDownload(context.Background(), art.Id, remoteAddr, oversizedUA, oversizedReferer); err != nil {
		t.Fatalf("HandleDownload: %v", err)
	}
	// Let the async download-log goroutine finish before reading.
	time.Sleep(50 * time.Millisecond)

	var got entity.DownloadLog
	if err := db.Where("artifact_id = ?", art.Id).First(&got).Error; err != nil {
		t.Fatalf("read download log: %v", err)
	}
	if got.IpAddress == remoteAddr {
		t.Errorf("download_logs.ip_address = %q, want masked, not the raw RemoteAddr", got.IpAddress)
	}
	if want := repo.MaskIP(remoteAddr); got.IpAddress != want {
		t.Errorf("download_logs.ip_address = %q, want %q (repo.MaskIP result)", got.IpAddress, want)
	}
	if len(got.UserAgent) > maxDownloadLogFieldBytes {
		t.Errorf("download_logs.user_agent = %d bytes, want <= %d", len(got.UserAgent), maxDownloadLogFieldBytes)
	}
	if len(got.Referer) > maxDownloadLogFieldBytes {
		t.Errorf("download_logs.referer = %d bytes, want <= %d", len(got.Referer), maxDownloadLogFieldBytes)
	}
	if !strings.HasPrefix(oversizedReferer, got.Referer) {
		t.Errorf("download_logs.referer is not a prefix of the header sent: %q", got.Referer)
	}
}

// TestHandleDownload_UnparseableIPStoresSentinel is the cycle-13 L5 repair
// oracle (D-L5-4) for the address repo.MaskIP cannot parse: it returns ""
// for those, and download_logs.ip_address is an `inet` column
// (entity.DownloadLog), which rejects the empty string — the row would be
// lost inside the fire-and-forget goroutine whose error nobody reads.
// "0.0.0.0" is a value inet accepts and no client can present as its own
// address.
func TestHandleDownload_UnparseableIPStoresSentinel(t *testing.T) {
	db := setupServiceTestDB(t)
	svc := newReleaseSvc(t, db)
	if err := db.AutoMigrate(&entity.DownloadLog{}); err != nil {
		t.Fatalf("migrate download_logs: %v", err)
	}

	rel := entity.Release{ProductId: "switch", Version: "2.0.0", Title: "R2", IsPublished: true}
	if err := db.Create(&rel).Error; err != nil {
		t.Fatalf("seed release: %v", err)
	}
	art := entity.ReleaseArtifact{
		ReleaseId:      rel.Id,
		Platform:       "linux",
		Arch:           "amd64",
		Filename:       "switch-linux-amd64",
		FileSize:       10,
		StoragePath:    "tools/switch/2.0.0/switch-linux-amd64",
		ChecksumSha256: "deadbeef2",
	}
	if err := db.Create(&art).Error; err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	if err := svc.HandleDownload(context.Background(), art.Id, "fe80::1%eth0", "curl/8", ""); err != nil {
		t.Fatalf("HandleDownload: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	var got entity.DownloadLog
	if err := db.Where("artifact_id = ?", art.Id).First(&got).Error; err != nil {
		t.Fatalf("read download log: %v", err)
	}
	if got.IpAddress != downloadLogUnknownIP {
		t.Errorf("download_logs.ip_address = %q for an address MaskIP cannot parse, want %q (an inet column rejects the empty string)", got.IpAddress, downloadLogUnknownIP)
	}
}

// TestDownloadLogMaskedIP pins the two branches of the helper
// HandleDownload writes through (cycle-13 L5 repair, D-L5-4): a parseable
// address is coarsened by repo.MaskIP, anything else becomes the
// inet-acceptable sentinel rather than "".
func TestDownloadLogMaskedIP(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ipv4_is_masked_to_24", "203.0.113.42", "203.0.113.0"},
		{"ipv6_is_masked_to_48", "2001:db8:1234:5678::1", "2001:db8:1234::"},
		{"zone_scoped_ipv6_is_unparseable", "fe80::1%eth0", downloadLogUnknownIP},
		{"garbage_is_unparseable", "not-an-ip", downloadLogUnknownIP},
		{"empty_is_unparseable", "", downloadLogUnknownIP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := downloadLogMaskedIP(tc.in); got != tc.want {
				t.Errorf("downloadLogMaskedIP(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestTruncateDownloadLogField is the cycle-13 L5 repair oracle (D-L5-2)
// for the half of the download_logs minimization the first round left
// unexercised: the cap itself AND the UTF-8 boundary the helper's doc
// comment claims. The oversized ASCII case alone would stay green under a
// naive s[:maxDownloadLogFieldBytes]; the multi-byte case is what makes
// that mutation red (512 is not a multiple of 3, so a byte slice lands
// mid-rune).
func TestTruncateDownloadLogField(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"short_ascii_is_unchanged", "Mozilla/5.0"},
		{"empty_is_unchanged", ""},
		{"exactly_at_the_cap", strings.Repeat("A", maxDownloadLogFieldBytes)},
		{"oversized_ascii", strings.Repeat("A", maxDownloadLogFieldBytes+100)},
		{"oversized_three_byte_runes", strings.Repeat("字", 300)},         // 900 bytes
		{"oversized_four_byte_runes", strings.Repeat("\U0001F600", 200)}, // 800 bytes
		{"oversized_mixed", strings.Repeat("a字", 300)},                   // 1200 bytes
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateDownloadLogField(tc.in)
			if len(got) > maxDownloadLogFieldBytes {
				t.Errorf("len = %d bytes, want <= %d", len(got), maxDownloadLogFieldBytes)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8 (%q…): PostgreSQL rejects it on insert", got[:min(len(got), 24)])
			}
			if !strings.HasPrefix(tc.in, got) {
				t.Errorf("result is not a prefix of the input")
			}
			if len(tc.in) <= maxDownloadLogFieldBytes && got != tc.in {
				t.Errorf("input within the cap was altered: %d bytes in, %d out", len(tc.in), len(got))
			}
			// A rune-aware truncation stops at most one rune (<= 4 bytes)
			// short of the cap; anything further means the loop is dropping
			// content it had room for.
			if len(tc.in) > maxDownloadLogFieldBytes && len(got) < maxDownloadLogFieldBytes-3 {
				t.Errorf("truncated to %d bytes, want within 3 bytes of the %d cap", len(got), maxDownloadLogFieldBytes)
			}
		})
	}
}

package app

import (
	"context"
	"strings"
	"testing"
	"time"

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
	if err := svc.HandleDownload(context.Background(), art.Id, remoteAddr, oversizedUA, "https://example.com/ref"); err != nil {
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
}

package app

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/mod/semver"
)

// ReleaseService handles business logic for releases and tool downloads.
type ReleaseService struct {
	repo        *repo.ReleaseRepository
	minioClient *minio.Client
	minioBucket string
	// minioPresignClient uses the public endpoint so presigned URLs have the
	// externally reachable host in the signature. Traefik preserves Host on
	// ingress, so MinIO signature verification succeeds. Falls back to
	// minioClient when nil (internal URL only, suitable for in-cluster use).
	minioPresignClient *minio.Client

	manifestMu    sync.RWMutex
	manifestCache *toolManifestSnapshot
}

// toolManifestSnapshot holds a cached tool manifest with expiry.
type toolManifestSnapshot struct {
	response  ToolManifestResponse
	expiresAt time.Time
}

const manifestCacheTTL = 5 * time.Minute

// --- Tool manifest types (match existing Switch client contract) ---

// ToolManifestResponse is the top-level manifest API response.
type ToolManifestResponse struct {
	GeneratedAt string                       `json:"generated_at"`
	Tools       map[string]ToolManifestEntry `json:"tools"`
}

// ToolManifestEntry describes a single tool in the download manifest.
type ToolManifestEntry struct {
	Type          string                       `json:"type"`
	NpmPackage    string                       `json:"npm_package,omitempty"`
	LatestVersion string                       `json:"latest_version"`
	Platforms     map[string]ToolManifestAsset `json:"platforms,omitempty"`
}

// ToolManifestAsset holds the download URL, checksum and size for one platform.
type ToolManifestAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

// storageObject is a simplified view of an object in MinIO.
type storageObject struct {
	key  string
	size int64
}

// NewReleaseService creates a new release service with optional MinIO integration.
func NewReleaseService(releaseRepo *repo.ReleaseRepository) *ReleaseService {
	cfg := config.Get()
	svc := &ReleaseService{
		repo:        releaseRepo,
		minioBucket: cfg.Storage.MinIOBucket,
	}

	if cfg.Storage.MinIOEndpoint != "" {
		client, err := minio.New(cfg.Storage.MinIOEndpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.Storage.MinIOAccessKey, cfg.Storage.MinIOSecretKey, ""),
			Secure: cfg.Storage.MinIOSecure,
			// Set region explicitly to prevent bucket-location HTTP lookup on each presign call.
			Region: "us-east-1",
		})
		if err != nil {
			slog.Error("failed to initialize MinIO client", "error", err)
		} else {
			svc.minioClient = client
			slog.Info("MinIO client initialized", "endpoint", cfg.Storage.MinIOEndpoint, "bucket", svc.minioBucket)
		}
	}

	// Optional presign client using the public endpoint.
	// Region must be set so minio-go skips the bucket-location HTTP lookup;
	// that lookup would fail because the public DNS may not be resolvable from
	// within the cluster. With a fixed region, PresignedGetObject is purely local.
	if pub := cfg.Storage.MinIOPublicEndpoint; pub != "" && svc.minioClient != nil {
		host := strings.TrimPrefix(strings.TrimPrefix(pub, "https://"), "http://")
		secure := strings.HasPrefix(pub, "https://")
		if pc, err := minio.New(host, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.Storage.MinIOAccessKey, cfg.Storage.MinIOSecretKey, ""),
			Secure: secure,
			Region: "us-east-1",
		}); err != nil {
			slog.Warn("failed to initialize MinIO presign client, presigned URLs will use internal endpoint", "error", err)
		} else {
			svc.minioPresignClient = pc
			slog.Info("MinIO presign client initialized", "public_endpoint", pub)
		}
	}

	return svc
}

// IsStorageConfigured returns true if MinIO is available.
func (s *ReleaseService) IsStorageConfigured() bool {
	return s.minioClient != nil
}

// ------------------------------------------------------------------
// Release DB operations
// ------------------------------------------------------------------

// ListReleasesResponse defines the response structure
type ListReleasesResponse struct {
	Releases []entity.Release `json:"releases"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// ListReleases retrieves releases with pagination
func (s *ReleaseService) ListReleases(ctx context.Context, params repo.ListReleasesParams) (*ListReleasesResponse, error) {
	releases, total, err := s.repo.ListReleases(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	return &ListReleasesResponse{
		Releases: releases,
		Total:    total,
		Page:     params.Page,
		PageSize: params.PageSize,
	}, nil
}

// LatestReleaseResponse defines the response structure for latest release
type LatestReleaseResponse struct {
	Release   *entity.Release `json:"release"`
	HasUpdate bool            `json:"has_update"`
}

// GetLatestRelease retrieves the latest release and checks for updates
func (s *ReleaseService) GetLatestRelease(ctx context.Context, productId string, currentVersion string) (*LatestReleaseResponse, error) {
	release, err := s.repo.GetLatestRelease(ctx, productId)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest release: %w", err)
	}

	if release == nil {
		return &LatestReleaseResponse{
			Release:   nil,
			HasUpdate: false,
		}, nil
	}

	hasUpdate := false
	if currentVersion != "" {
		hasUpdate = compareVersions(currentVersion, release.Version) < 0
	}

	return &LatestReleaseResponse{
		Release:   release,
		HasUpdate: hasUpdate,
	}, nil
}

// GetReleaseByID retrieves a single release
func (s *ReleaseService) GetReleaseByID(ctx context.Context, id int64) (*entity.Release, error) {
	return s.repo.GetReleaseByID(ctx, id)
}

// ------------------------------------------------------------------
// MinIO download operations
// ------------------------------------------------------------------

// GenerateDownloadURL generates a presigned URL for a release artifact.
func (s *ReleaseService) GenerateDownloadURL(ctx context.Context, artifact *entity.ReleaseArtifact) (string, error) {
	if s.minioClient == nil {
		return "", fmt.Errorf("object storage not configured: set MINIO_ENDPOINT environment variable")
	}

	presignedURL, err := s.presignClient().PresignedGetObject(ctx, s.minioBucket, artifact.StoragePath, time.Hour, nil)
	if err != nil {
		return "", fmt.Errorf("generate presigned URL for %s: %w", artifact.StoragePath, err)
	}
	return presignedURL.String(), nil
}

// maxDownloadLogFieldBytes caps how much of a raw User-Agent/Referer header
// download_logs keeps (cycle-13 L5, download_logs minimization). This table
// has no user_id/tenant_id column — a download log row cannot be attributed
// to an erasure subject, so it is not part of the privacy-erasure cascade
// (see the exemption entry in erasure_model_coverage_test.go); minimizing
// what gets written at insert time is the mitigation instead.
const maxDownloadLogFieldBytes = 512

// downloadLogUnknownIP is what download_logs stores when repo.MaskIP cannot
// parse the caller's address (it returns "" for those — zone-scoped IPv6
// such as "fe80::1%eth0", a host name, anything malformed).
// DownloadLog.IpAddress is an `inet` column (entity/release.go), and inet
// rejects the empty string, so writing MaskIP's "" straight through would
// fail the INSERT inside HandleDownload's fire-and-forget goroutine, whose
// error only reaches a log line. The unspecified address is accepted by
// inet and reads as a sentinel rather than as a real caller's address.
const downloadLogUnknownIP = "0.0.0.0"

// downloadLogMaskedIP coarsens an address for download_logs, falling back
// to downloadLogUnknownIP when repo.MaskIP cannot parse it.
func downloadLogMaskedIP(ip string) string {
	if masked := repo.MaskIP(ip); masked != "" {
		return masked
	}
	return downloadLogUnknownIP
}

// truncateDownloadLogField caps s to at most maxDownloadLogFieldBytes UTF-8
// bytes, stopping before a rune that would cross the budget rather than
// slicing mid-rune — PostgreSQL rejects an invalid UTF-8 byte sequence on
// insert into a text/varchar column, so a naive s[:512] risks turning an
// oversized-but-legal header into an insert failure.
func truncateDownloadLogField(s string) string {
	if len(s) <= maxDownloadLogFieldBytes {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if b.Len()+utf8.RuneLen(r) > maxDownloadLogFieldBytes {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// HandleDownload handles download logic: logging and count increment
func (s *ReleaseService) HandleDownload(ctx context.Context, artifactId int64, ipAddress, userAgent, referer string) error {
	go func() { // #nosec G118 — intentional fire-and-forget: download log is best-effort; request context must not propagate.
		logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		log := &entity.DownloadLog{
			ArtifactId: artifactId,
			// IP is coarsened before it is ever persisted, same /24-IPv4 /
			// /48-IPv6 mask repo.MaskIP already applies to the sessions list
			// (cycle-13 L5). CountryCode is derived from the ORIGINAL
			// address — geo lookup is not wired yet either way (see
			// extractCountryFromIP) but the mask is coarser than a country
			// boundary would ever need to be. An address MaskIP cannot
			// parse becomes downloadLogUnknownIP rather than "" — see that
			// constant for why the inet column makes the difference.
			IpAddress:    downloadLogMaskedIP(ipAddress),
			UserAgent:    truncateDownloadLogField(userAgent),
			Referer:      truncateDownloadLogField(referer),
			CountryCode:  extractCountryFromIP(ipAddress),
			Status:       "initiated",
			DownloadedAt: time.Now(),
		}

		if err := s.repo.LogDownload(logCtx, log); err != nil {
			slog.Error("failed to log download", "artifact_id", artifactId, "error", err)
		}
	}()

	if err := s.repo.IncrementDownloadCount(ctx, artifactId); err != nil {
		return fmt.Errorf("failed to increment download count: %w", err)
	}

	return nil
}

// GetChangelog retrieves the changelog for a release
func (s *ReleaseService) GetChangelog(ctx context.Context, releaseId int64) (string, error) {
	return s.repo.GetChangelogByReleaseID(ctx, releaseId)
}

// ------------------------------------------------------------------
// Dynamic tool manifest (MinIO-backed)
// ------------------------------------------------------------------

// npmTools returns the static npm tool entries (not stored in MinIO).
func npmTools() map[string]ToolManifestEntry {
	return map[string]ToolManifestEntry{
		"claude": {
			Type:          "npm",
			NpmPackage:    "@anthropic-ai/claude-code",
			LatestVersion: "1.0.26",
		},
		"codex": {
			Type:          "npm",
			NpmPackage:    "@openai/codex",
			LatestVersion: "0.1.2505161344",
		},
		"gemini": {
			Type:          "npm",
			NpmPackage:    "@google/gemini-cli",
			LatestVersion: "0.1.9",
		},
		"openclaw": {
			Type:          "npm",
			NpmPackage:    "openclaw",
			LatestVersion: "1.0.0",
		},
	}
}

// BuildToolManifest returns a complete tool manifest. Binary tools are
// discovered dynamically from MinIO; npm tools are static. Results are
// cached for manifestCacheTTL.
func (s *ReleaseService) BuildToolManifest(ctx context.Context) (*ToolManifestResponse, error) {
	// Fast path: return cached manifest if still fresh
	s.manifestMu.RLock()
	if snap := s.manifestCache; snap != nil && time.Now().Before(snap.expiresAt) {
		resp := snap.response
		s.manifestMu.RUnlock()
		return &resp, nil
	}
	s.manifestMu.RUnlock()

	// Slow path: rebuild manifest
	tools := npmTools()

	if s.minioClient != nil {
		binaryTools, err := s.discoverBinaryTools(ctx)
		if err != nil {
			slog.Warn("failed to discover binary tools from storage, returning npm-only manifest", "error", err)
		} else {
			for k, v := range binaryTools {
				tools[k] = v
			}
		}
	}

	resp := ToolManifestResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Tools:       tools,
	}

	// Cache the result
	s.manifestMu.Lock()
	s.manifestCache = &toolManifestSnapshot{
		response:  resp,
		expiresAt: time.Now().Add(manifestCacheTTL),
	}
	s.manifestMu.Unlock()

	return &resp, nil
}

// discoverBinaryTools scans MinIO bucket under "tools/" prefix and builds
// manifest entries for each tool, selecting the latest semver version.
//
// Expected MinIO layout:
//
//	tools/{tool_name}/{version}/{tool_name}-{os}-{arch}{ext}
//	tools/{tool_name}/{version}/checksums.sha256
func (s *ReleaseService) discoverBinaryTools(ctx context.Context) (map[string]ToolManifestEntry, error) {
	// toolVersionFiles groups objects: tool → version → list of objects
	toolVersionFiles := make(map[string]map[string][]storageObject)

	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for obj := range s.minioClient.ListObjects(listCtx, s.minioBucket, minio.ListObjectsOptions{
		Prefix:    "tools/",
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("list objects in tools/: %w", obj.Err)
		}

		// Parse: tools/{tool}/{version}/{filename}
		parts := strings.Split(obj.Key, "/")
		if len(parts) < 4 {
			continue
		}
		toolName := parts[1]
		version := parts[2]

		if toolVersionFiles[toolName] == nil {
			toolVersionFiles[toolName] = make(map[string][]storageObject)
		}
		toolVersionFiles[toolName][version] = append(toolVersionFiles[toolName][version], storageObject{
			key:  obj.Key,
			size: obj.Size,
		})
	}

	result := make(map[string]ToolManifestEntry, len(toolVersionFiles))

	for toolName, versions := range toolVersionFiles {
		latestVersion := latestSemverKey(versions)
		if latestVersion == "" {
			continue
		}

		objects := versions[latestVersion]

		// Read checksums if available
		checksums := s.readChecksums(ctx, toolName, latestVersion)

		// Build platform entries
		platforms := make(map[string]ToolManifestAsset)
		for _, obj := range objects {
			filename := path.Base(obj.key)

			// Skip checksum files
			if strings.HasSuffix(filename, ".sha256") || strings.HasSuffix(filename, ".sha256sum") {
				continue
			}

			osName, arch := parsePlatformFromFilename(toolName, filename)
			if osName == "" || arch == "" {
				continue
			}

			presignedURL, err := s.presignClient().PresignedGetObject(ctx, s.minioBucket, obj.key, time.Hour, nil)
			if err != nil {
				slog.Warn("failed to generate presigned URL", "key", obj.key, "error", err)
				continue
			}

			platformKey := osName + "/" + arch
			asset := ToolManifestAsset{
				URL:  presignedURL.String(),
				Size: obj.size,
			}
			if sha, ok := checksums[filename]; ok {
				asset.SHA256 = sha
			}
			platforms[platformKey] = asset
		}

		if len(platforms) == 0 {
			continue
		}

		result[toolName] = ToolManifestEntry{
			Type:          "binary",
			LatestVersion: latestVersion,
			Platforms:     platforms,
		}
	}

	return result, nil
}

// readChecksums reads a checksums.sha256 file from MinIO and returns
// a map of filename → sha256 hex string.
func (s *ReleaseService) readChecksums(ctx context.Context, toolName, version string) map[string]string {
	checksumKey := fmt.Sprintf("tools/%s/%s/checksums.sha256", toolName, version)

	obj, err := s.minioClient.GetObject(ctx, s.minioBucket, checksumKey, minio.GetObjectOptions{})
	if err != nil {
		return nil
	}
	defer obj.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(obj)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Format: "<sha256>  <filename>" or "<sha256> <filename>"
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			sha := fields[0]
			fname := fields[len(fields)-1]
			fname = strings.TrimPrefix(fname, "*")
			result[fname] = sha
		}
	}
	return result
}

// parsePlatformFromFilename extracts OS and arch from a binary filename.
// Expected format: {tool}-{os}-{arch}{ext}
func parsePlatformFromFilename(toolName, filename string) (osName, arch string) {
	name := filename
	for _, ext := range []string{".tar.gz", ".tgz", ".exe", ".zip", ".dmg"} {
		if strings.HasSuffix(name, ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}

	prefix := toolName + "-"
	if !strings.HasPrefix(name, prefix) {
		return "", ""
	}
	remainder := name[len(prefix):]

	parts := strings.SplitN(remainder, "-", 2)
	if len(parts) != 2 {
		return "", ""
	}

	osName = parts[0]
	arch = parts[1]

	validOS := map[string]bool{"windows": true, "linux": true, "darwin": true}
	validArch := map[string]bool{"amd64": true, "arm64": true, "x86_64": true, "aarch64": true}
	if !validOS[osName] || !validArch[arch] {
		return "", ""
	}

	switch arch {
	case "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}

	return osName, arch
}

// latestSemverKey finds the latest semantic version key from a map.
func latestSemverKey(versions map[string][]storageObject) string {
	versionList := make([]string, 0, len(versions))
	for v := range versions {
		versionList = append(versionList, v)
	}
	if len(versionList) == 0 {
		return ""
	}

	sort.Slice(versionList, func(i, j int) bool {
		return compareVersions(versionList[i], versionList[j]) > 0
	})

	return versionList[0]
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

// compareVersions compares two semantic versions.
// Returns -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2.
func compareVersions(v1, v2 string) int {
	if !strings.HasPrefix(v1, "v") {
		v1 = "v" + v1
	}
	if !strings.HasPrefix(v2, "v") {
		v2 = "v" + v2
	}

	if !semver.IsValid(v1) || !semver.IsValid(v2) {
		if v1 == v2 {
			return 0
		}
		if v1 < v2 {
			return -1
		}
		return 1
	}

	return semver.Compare(v1, v2)
}

// presignClient returns the client to use for PresignedGetObject calls.
// If a dedicated presign client with the public endpoint is available, it is
// returned; otherwise the internal minioClient is used.
func (s *ReleaseService) presignClient() *minio.Client {
	if s.minioPresignClient != nil {
		return s.minioPresignClient
	}
	return s.minioClient
}

func extractCountryFromIP(ipAddress string) string {
	ip := net.ParseIP(ipAddress)
	if ip == nil {
		return ""
	}
	return ""
}

package handler

// task_media_guard.go — the shared SSRF-adjacent guard, data: URL inliner
// and body-size cap behind BOTH video_proxy.go's VideoProxy and
// task_artifacts.go's GetTaskArtifactContent (cycle-8 L9, tasks-plugins-02/
// 17/19). Neither handler had any of this before this lane —
// video_proxy.go's only existing protection was the per-task ownership
// check (untouched by this lane, still 403, still locked by
// TestVideoProxy_ForeignUser403 in task_generic_test.go). Building the
// scheme allow-list, self-URL/loop check, data: inliner and copy cap once
// here and calling the shared pieces from both handlers is what
// "generalises video_proxy.go" means for this lane; see video_proxy.go and
// task_media_guard_test.go for how each handler's mutation-locked test
// proves both call the same functions.
//
// General private-IP/domain SSRF policy (fetch_setting, AllowPrivateIp,
// domain/IP allow/deny lists) is a SEPARATE, already-existing concern
// (app.ValidateOutboundURL, used by channel egress and internal/app/
// download.go) and is applied by streamMediaContent below via that same
// function — cycle-9 L4 closed the gap where VideoProxy (video_proxy.go)
// served the same class of URL without ever calling it: both routes now
// call app.ValidateOutboundURL, in addition to sharing the scheme/
// self-URL/size-cap guard below. What IS new in this file is
// the self-URL/loop check, which fetch_setting has no notion of: an
// operator running with allow_private_ip=true (the tests in
// task_artifacts_test.go and video_proxy_test.go set it, to reach an
// httptest.Server on 127.0.0.1 — not every test in this package does; e.g.
// cov_handler-channel_upstream_test.go leaves it false) still must not have
// an artifact URL cause this gateway to fetch from itself.
import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// maxProxiedArtifactBytes bounds how much of a single upstream response this
// gateway streams back, so a hostile or runaway "vendor" URL cannot turn one
// request into an unbounded transfer. 200MiB comfortably covers the sizes
// the compiled task adaptors' generated video/audio actually produce. It is
// a var, not a const, so a test can lower it to exercise the cap without
// transferring hundreds of megabytes (see task_media_guard_test.go /
// video_proxy_test.go); production code never assigns to it.
//
// A KNOWN Content-Length over the cap is rejected with 502 BEFORE any
// response header is written (see streamMediaContent/VideoProxy) — the
// operator ruling on the round-1 acceptance findings: silently truncating
// under a 200 would hand the caller a corrupt file it has no way to detect
// (the same corruption the round-1 report flagged: the cap used to truncate
// while the upstream Content-Length was still forwarded unmodified). An
// UNKNOWN-length stream (no Content-Length, e.g. chunked) cannot be
// rejected up front; it is still stopped at the cap, and that truncation is
// logged and counted (metrics.TaskMediaGuardRejectionsTotal, reason
// "size_cap") so it is at least visible to a scraper.
var maxProxiedArtifactBytes int64 = 200 * 1024 * 1024

// allowedArtifactScheme is the scheme allow-list an artifact URL must pass
// before this gateway will touch it for an outbound fetch: http/https only
// — called directly by both streamMediaContent below and VideoProxy
// (video_proxy.go), the one shared predicate rather than a duplicated
// two-string comparison at each call site (cycle-8 L9 repair: the previous
// version additionally allowed "data" here, which was never actually
// reachable — parseDataURL intercepts a data: URL earlier in both call
// sites, before this function is ever consulted).
func allowedArtifactScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

// isSelfOrLoopURL reports whether u's host is the same host that served the
// inbound request c carries. An artifact URL that points back at this same
// gateway would either waste a round trip proxying its own response or, if
// the target were itself an artifact-content route, recurse — so it is
// refused before any outbound dial, regardless of the general SSRF policy's
// allow_private_ip setting (self-reference is not a "private IP" question:
// production's own host is a public domain).
func isSelfOrLoopURL(c *gin.Context, u *url.URL) bool {
	target := u.Hostname()
	if target == "" {
		return false
	}
	inbound := hostnameOnly(c.Request.Host)
	return inbound != "" && strings.EqualFold(target, inbound)
}

// hostnameOnly strips an optional :port suffix from a Host-header-shaped
// string, tolerating the port-less case net.SplitHostPort rejects.
func hostnameOnly(hostport string) string {
	if hostport == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}

// parseDataURL decodes a data: URL (RFC 2397: data:[<mediatype>][;base64],<data>).
// ok is false for anything that does not parse as that shape, which the
// caller treats the same as "not a data: URL at all".
func parseDataURL(raw string) (mimeType string, data []byte, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(raw, prefix) {
		return "", nil, false
	}
	rest := raw[len(prefix):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", nil, false
	}
	meta := rest[:comma]
	payload := rest[comma+1:]
	isBase64 := strings.HasSuffix(meta, ";base64")
	if isBase64 {
		meta = strings.TrimSuffix(meta, ";base64")
	}
	mimeType = meta
	if mimeType == "" {
		// RFC 2397's default when the media type is omitted.
		mimeType = "text/plain;charset=US-ASCII"
	}
	if isBase64 {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", nil, false
		}
		return mimeType, decoded, true
	}
	// RFC 2397 payloads are percent-encoded octet-by-octet; '+' is a literal
	// plus, not the application/x-www-form-urlencoded "space" escape query
	// strings use. url.PathUnescape decodes %XX without that substitution —
	// url.QueryUnescape (the previous implementation) silently turned every
	// literal '+' in a non-base64 data: payload into a space.
	if decodedStr, err := url.PathUnescape(payload); err == nil {
		return mimeType, []byte(decodedStr), true
	}
	return mimeType, []byte(payload), true
}

// copyCapped is io.Copy bounded by maxProxiedArtifactBytes: it copies up to
// max bytes from src to dst and treats running out of src before the cap
// (the ordinary case) as success rather than an error — io.CopyN's own
// EOF-on-short-source signal.
func copyCapped(dst io.Writer, src io.Reader, max int64) (int64, error) {
	n, err := io.CopyN(dst, src, max)
	if errors.Is(err, io.EOF) {
		return n, nil
	}
	return n, err
}

// streamMediaContent serves GetTaskArtifactContent's response body for one
// already-resolved artifact URL: inline for data:, a guarded GET for
// http(s). It is NOT used by VideoProxy — that handler needs per-channel
// auth headers (Authorization / x-goog-api-key) and a per-channel proxy
// client this function has no channel to look up, so VideoProxy instead
// calls allowedArtifactScheme/isSelfOrLoopURL directly around its own
// request construction and copyCapped for its own response copy (see
// video_proxy.go). This function and VideoProxy therefore share the guard
// and the copy cap, not the whole fetch — which is what the plan's "shared
// helper used by both" asked for.

// setArtifactRouteSecurityHeaders sets the headers that apply to every
// response this route serves on the 200 path, whether the bytes came from a
// data: URL or an upstream fetch: X-Content-Type-Options prevents a
// vendor-supplied payload from being MIME-sniffed into something other than
// its declared/carried Content-Type; Cache-Control and Referrer-Policy are
// specific to this route (not VideoProxy — see its own doc comment) because
// TokenAuth also accepts the bearer token as a `?key=` query parameter
// (middleware/auth.go), which makes this a browser-navigable URL: a
// vendor-supplied text/html artefact must not be cacheable or leak this
// URL (and the token embedded in it) via the Referer header of whatever it
// links to.
func setArtifactRouteSecurityHeaders(c *gin.Context) {
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	c.Writer.Header().Set("Referrer-Policy", "no-referrer")
}

func respondArtifactRejected(c *gin.Context, route, reason, message string) {
	metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(route, reason).Inc()
	c.JSON(http.StatusBadGateway, gin.H{
		"error": gin.H{"type": "server_error", "message": message, "code": string(types.ErrorCodeArtifactRequestRejected)},
	})
}

func streamMediaContent(c *gin.Context, rawURL string) {
	const route = "artifact_content"

	if mimeType, data, ok := parseDataURL(rawURL); ok {
		setArtifactRouteSecurityHeaders(c)
		c.Data(http.StatusOK, mimeType, data)
		return
	}

	u, err := url.Parse(rawURL)
	if err != nil || !allowedArtifactScheme(u.Scheme) {
		respondArtifactRejected(c, route, "scheme", "Artifact URL is not fetchable")
		return
	}
	if isSelfOrLoopURL(c, u) {
		respondArtifactRejected(c, route, "self_url", "Refused to proxy a self-referential artifact URL")
		return
	}
	if err := app.ValidateOutboundURL(rawURL); err != nil {
		respondArtifactRejected(c, route, "egress_check", "Artifact URL failed the egress check")
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"type": "server_error", "message": "Failed to build artifact request"},
		})
		return
	}
	resp, err := app.GetHttpClient().Do(req)
	if err != nil {
		metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(route, "upstream_error").Inc()
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"type": "server_error", "message": "Failed to fetch artifact content", "code": string(types.ErrorCodeArtifactUpstreamError)},
		})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(route, "upstream_error").Inc()
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"type": "server_error", "message": "Upstream artifact fetch failed", "code": string(types.ErrorCodeArtifactUpstreamError)},
		})
		return
	}

	// A KNOWN oversized Content-Length is rejected before any header is
	// written to the client — see maxProxiedArtifactBytes' doc comment for
	// why silent truncation under a 200 is not an option.
	if resp.ContentLength > maxProxiedArtifactBytes {
		respondArtifactRejected(c, route, "size_cap", "Artifact exceeds the proxy size limit")
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Writer.Header().Set("Content-Type", contentType)
	setArtifactRouteSecurityHeaders(c)
	c.Writer.WriteHeader(http.StatusOK)
	n, copyErr := copyCapped(c.Writer, resp.Body, maxProxiedArtifactBytes)
	if copyErr != nil {
		logger.LogError(c.Request.Context(), "GetTaskArtifactContent: stream copy: "+copyErr.Error())
	}
	if resp.ContentLength < 0 && n >= maxProxiedArtifactBytes {
		metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(route, "size_cap").Inc()
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"GetTaskArtifactContent: unknown-length artifact stream truncated at %d bytes", maxProxiedArtifactBytes))
	}
}

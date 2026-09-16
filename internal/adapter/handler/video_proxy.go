package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
)

// redactVideoURLForLog reduces raw to scheme://host/path before it reaches an
// error log, dropping BOTH the query/fragment and any userinfo component.
//
// Two credential shapes have to go. url.URL.Redacted() removes neither: it
// masks only a userinfo *password* and keeps the query string, while a Gemini
// video URL carries its API key AS a query parameter (video_proxy_gemini.go's
// ensureAPIKey appends "?key=<apiKey>"). A plain trim at the first '?'/'#'
// removes the query but keeps "user:password@" in the authority. This builds
// the safe form from the parsed URL instead, and falls back to the trim only
// when raw does not parse — in that case there is no authority to isolate, and
// cutting at '?'/'#' still removes the query-parameter shape a credential takes.
//
// Callers: every videoURL log sink in this file. TestRedactVideoURLForLog
// covers the query, fragment, userinfo, both-at-once and unparseable cases, so
// replacing this body with `return raw` fails the build.
func redactVideoURLForLog(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		u.RawFragment = ""
		return u.String()
	}
	if idx := strings.IndexAny(raw, "?#"); idx >= 0 {
		return raw[:idx]
	}
	return raw
}

// videoProxyMetricsRoute is the "route" label VideoProxy's rejections/
// truncations carry on metrics.TaskMediaGuardRejectionsTotal, distinguishing
// this legacy relay route from the generic artifact-content route
// (task_media_guard.go's streamMediaContent uses "artifact_content").
const videoProxyMetricsRoute = "video_proxy"

func VideoProxy(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "task_id is required",
				"type":    "invalid_request_error",
			},
		})
		return
	}

	task, exists, err := repo.GetByOnlyTaskId(taskID)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to query task %s: %s", taskID, err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Failed to query task",
				"type":    "server_error",
			},
		})
		return
	}
	if !exists || task == nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to get task %s: %v", taskID, err))
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"message": "Task not found",
				"type":    "invalid_request_error",
			},
		})
		return
	}

	// Ownership check — only the task owner (or an admin/root) may proxy the
	// resulting video. Without this, any authenticated tenant could fetch any
	// other tenant's generated video by guessing or scraping task_id.
	// Ported from 2b-svc-newapi commit da3cb48f (2026-03-31).
	requesterID := c.GetInt("id")
	requesterRole := c.GetInt("role")
	isPrivileged := requesterRole >= common.RoleAdminUser
	if !isPrivileged && task.UserId != requesterID {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			`{"event":"video_proxy_forbidden","task_id":"%s","task_owner":%d,"requester":%d}`,
			taskID, task.UserId, requesterID,
		))
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": "access denied",
				"type":    "forbidden",
			},
		})
		return
	}

	if task.Status != repo.TaskStatusSuccess {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Task is not completed yet, current status: %s", task.Status),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	channel, err := repo.CacheGetChannel(task.ChannelId)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to get task %s: not found", taskID))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Failed to retrieve channel information",
				"type":    "server_error",
			},
		})
		return
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	var videoURL string
	proxy := channel.GetSetting().Proxy
	client, err := app.GetHttpClientWithProxy(proxy)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create proxy client for task %s: %s", taskID, err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Failed to create proxy client",
				"type":    "server_error",
			},
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "", nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create request: %s", err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Failed to create proxy request",
				"type":    "server_error",
			},
		})
		return
	}

	switch channel.Type {
	case constant.ChannelTypeGemini:
		apiKey := task.PrivateData.Key
		if apiKey == "" {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Missing stored API key for Gemini task %s", taskID))
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"message": "API key not stored for task",
					"type":    "server_error",
				},
			})
			return
		}

		videoURL, err = getGeminiVideoURL(channel, task, apiKey)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve Gemini video URL for task %s: %s", taskID, err.Error()))
			c.JSON(http.StatusBadGateway, gin.H{
				"error": gin.H{
					"message": "Failed to resolve Gemini video URL",
					"type":    "server_error",
				},
			})
			return
		}
		req.Header.Set("x-goog-api-key", apiKey)
	case constant.ChannelTypeOpenAI, constant.ChannelTypeSora:
		videoURL = fmt.Sprintf("%s/v1/videos/%s/content", baseURL, task.TaskID)
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	default:
		// Video URL is directly in task.FailReason
		videoURL = task.FailReason
	}

	req.URL, err = url.Parse(videoURL)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to parse URL %s: %s", redactVideoURLForLog(videoURL), err.Error()))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Failed to create proxy request",
				"type":    "server_error",
			},
		})
		return
	}

	// Cycle-8 L9: the same self-URL/loop guard task_media_guard.go's
	// streamMediaContent applies to the generic artifact-content route, now
	// also gating this handler's resolved videoURL before it is dialed.
	// This is new protection — video_proxy.go had neither check before this
	// lane — not a port of pre-existing behaviour; the ownership check above
	// and its 403 are what stays byte-identical (see
	// TestVideoProxy_ForeignUser403 in task_generic_test.go).
	//
	// allowedArtifactScheme (task_media_guard.go), the same http/https-only
	// predicate streamMediaContent uses, is observably a no-op for THIS
	// handler: Go's http.Client already refuses to dial a non-http(s)
	// scheme with no network I/O, landing in the same client.Do err!=nil
	// branch below — which is why this branch keeps the OLD "Failed to
	// fetch video content" message (the operator ruling's byte-identical
	// requirement for "the previously-502 case") instead of a distinct
	// message: there is no behaviour left to distinguish. It is kept
	// anyway as defense-in-depth against a future change to the
	// client/transport, and its rejection is still counted.
	if !allowedArtifactScheme(req.URL.Scheme) {
		metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(videoProxyMetricsRoute, "scheme").Inc()
		logger.LogError(c.Request.Context(), fmt.Sprintf("Refusing unfetchable video URL scheme for task %s: %s", taskID, redactVideoURLForLog(videoURL)))
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"message": "Failed to fetch video content",
				"type":    "server_error",
			},
		})
		return
	}
	if isSelfOrLoopURL(c, req.URL) {
		metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(videoProxyMetricsRoute, "self_url").Inc()
		logger.LogError(c.Request.Context(), fmt.Sprintf("Refusing self-referential video URL for task %s: %s", taskID, redactVideoURLForLog(videoURL)))
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"message": "Refused to proxy a self-referential video URL",
				"type":    "server_error",
			},
		})
		return
	}

	// Cycle-9 L4: the general private-IP/domain SSRF policy
	// (app.ValidateOutboundURL — fetch_setting's AllowPrivateIp/domain/IP
	// allow-deny lists) already gated channel egress (channel.go,
	// v2_channel_actions.go) and the artefact-content route
	// (task_media_guard.go's streamMediaContent) before this lane; this
	// handler served the same class of vendor-supplied URL with only the
	// scheme/self-URL checks above and no call to it at all. Closing that
	// gap is this lane's whole point (see task_media_guard.go and
	// metrics.go's corrected doc comments). respondArtifactRejected is the
	// artefact route's own refusal function, reused here rather than
	// duplicated so the response shape (type/message/code) cannot drift
	// between the two routes that serve the same class of URL — only the
	// route label passed to it differs, which is what keeps the metric
	// able to tell the two routes apart.
	//
	// Known operational constraint (operator ruling, round-1 acceptance):
	// app.ValidateOutboundURL resolves the target host itself via
	// net.LookupIP (internal/pkg/common/ssrf_protection.go, reached from
	// ssrf_guard.go via common.ValidateURLWithFetchSetting), on the pod's
	// own network path, and
	// fails CLOSED when that resolution errors — regardless of the
	// per-channel proxy this handler otherwise dials through (client,
	// built above from channel.GetSetting().Proxy). VideoProxy is the
	// only one of the two guarded task-media routes that supports a
	// per-channel proxy, i.e. the case where the pod deliberately cannot
	// resolve/reach the vendor directly. A channel configured with a
	// proxy specifically because its videoURL host is NOT resolvable from
	// the pod will now get a 502 here instead of a working proxied fetch.
	// This was deliberately kept (not special-cased around the proxy
	// setting) rather than softened: the sibling artefact route has no
	// proxy option at all and still gets this same fail-closed DNS check
	// from the same function call, so exempting VideoProxy's proxied case
	// would be the one place the two routes' egress posture diverges — see
	// TestVideoProxy_RefusesUnresolvableDomainWithProxiedChannel below and
	// the corresponding row in doc/product-integration-guide.md.
	if err := app.ValidateOutboundURL(videoURL); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Refusing video URL blocked by egress policy for task %s: %s: %s", taskID, redactVideoURLForLog(videoURL), err.Error()))
		respondArtifactRejected(c, videoProxyMetricsRoute, "egress_check", egressCheckRejectionMessage)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(videoProxyMetricsRoute, "upstream_error").Inc()
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to fetch video from %s: %s", redactVideoURLForLog(videoURL), err.Error()))
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"message": "Failed to fetch video content",
				"type":    "server_error",
			},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(videoProxyMetricsRoute, "upstream_error").Inc()
		logger.LogError(c.Request.Context(), fmt.Sprintf("Upstream returned status %d for %s", resp.StatusCode, redactVideoURLForLog(videoURL)))
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Upstream service returned status %d", resp.StatusCode),
				"type":    "server_error",
			},
		})
		return
	}

	// A KNOWN oversized Content-Length is rejected before any header is
	// written to the client, same as the generic artifact-content route —
	// see maxProxiedArtifactBytes' doc comment (task_media_guard.go) for why
	// silently truncating under a 200 (this handler's previous behaviour)
	// is not an option: the client would receive a corrupt file under a
	// Content-Length header that still claims the full size.
	if resp.ContentLength > maxProxiedArtifactBytes {
		metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(videoProxyMetricsRoute, "size_cap").Inc()
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video content exceeds proxy size limit for task %s: content-length=%d", taskID, resp.ContentLength))
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"message": "Video content exceeds the proxy size limit",
				"type":    "server_error",
			},
		})
		return
	}

	for key, values := range resp.Header {
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}

	c.Writer.Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.WriteHeader(resp.StatusCode)
	// copyCapped (task_media_guard.go): same body-size cap the generic
	// artifact-content route uses, so neither proxy can turn one request
	// into an unbounded transfer.
	n, err := copyCapped(c.Writer, resp.Body, maxProxiedArtifactBytes)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to stream video content: %s", err.Error()))
	}
	if resp.ContentLength < 0 && n >= maxProxiedArtifactBytes {
		metrics.TaskMediaGuardRejectionsTotal.WithLabelValues(videoProxyMetricsRoute, "size_cap").Inc()
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"VideoProxy: unknown-length video stream truncated at %d bytes for task %s", maxProxiedArtifactBytes, taskID))
	}
}

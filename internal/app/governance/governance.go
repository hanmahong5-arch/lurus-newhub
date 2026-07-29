package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"

	"github.com/gin-gonic/gin"
)

const (
	// Context key for storing precomputed request fingerprint.
	ctxKeyFingerprint = "governance_fingerprint"

	// Maximum bytes of request body used for fingerprint computation.
	fingerprintBodyLimit = 4096

	// Log detail levels for content strategy.
	LogDetailNone = "none" // Skip logging entirely.
	LogDetailFull = "full" // Reserved for future use (e.g. extended metadata). Does NOT store raw prompts.
)

// EnrichContext computes the request fingerprint and stores it in the gin context.
// Call this once per request after relayInfo is created and the request body is cached.
// NOTE: Only called in the main Relay() path. Midjourney and Task relay paths
// do not call EnrichContext — fingerprint will be empty string for those requests.
func EnrichContext(c *gin.Context, tokenID int, modelName string) {
	body, err := common.GetRequestBody(c)
	if err != nil || len(body) == 0 {
		return
	}
	fp := ComputeFingerprint(tokenID, modelName, body)
	c.Set(ctxKeyFingerprint, fp)
}

// EnrichLogParams fills governance fields on RecordConsumeLogParams using data
// from the gin context and RelayInfo. Call this immediately before RecordConsumeLog.
func EnrichLogParams(c *gin.Context, info *relaycommon.RelayInfo, params *entity.RecordConsumeLogParams) {
	if info == nil || params == nil {
		return
	}

	// Channel type: prefer ChannelMeta, fall back to gin context.
	if info.ChannelMeta != nil {
		params.ChannelType = info.ChannelMeta.ChannelType
	} else {
		params.ChannelType = c.GetInt("channel_type")
	}

	params.RelayMode = info.RelayMode

	// Request fingerprint from context (set by EnrichContext).
	if fp, exists := c.Get(ctxKeyFingerprint); exists {
		if s, ok := fp.(string); ok {
			params.RequestFingerprint = s
		}
	}

	// Upstream model: the actual model name sent to the provider.
	if info.ChannelMeta != nil && info.ChannelMeta.UpstreamModelName != "" {
		params.UpstreamModel = info.ChannelMeta.UpstreamModelName
	} else {
		params.UpstreamModel = info.OriginModelName
	}

	// Total latency from request start to now.
	params.TotalLatencyMs = int(time.Since(info.StartTime).Milliseconds())

	// Cost attribution (migration 029): stamp the token's project onto the log
	// row. This function is THE chokepoint — every RecordConsumeLog call site
	// in the repo (app/quota.go, relay/compatible_handler.go,
	// relay/relay_task.go, relay/mjproxy_handler.go) calls it first — so the
	// attribution is wired in one place rather than six.
	//
	// RelayInfo is the primary source because settlement has no gin.Context;
	// the context read is the fallback for relay paths that assemble a
	// RelayInfo without genBaseRelayInfo. 0 means unassigned either way, and
	// unassigned is a legitimate first-class value — never "fix" it to
	// something non-zero here or the per-project figures stop summing to the
	// tenant total.
	params.ProjectId = info.ProjectId
	if params.ProjectId == 0 && c != nil {
		params.ProjectId = common.GetContextKeyInt(c, constant.ContextKeyProjectId)
	}

	// Apply log detail level from user setting.
	logLevel := info.UserSetting.LogDetailLevel
	params.LogDetailLevel = logLevel

	// Enrich the Other map with data flow metadata.
	if params.Other == nil {
		params.Other = make(map[string]interface{})
	}
	params.Other["data_flow_source"] = params.TokenName
	params.Other["data_flow_dest"] = constant.GetChannelTypeName(params.ChannelType)
	// Workstream 0: cross-product attribution. Resolved on the relay path; paths
	// that don't set it (MJ/Task/WS) fall back to the default product id so the
	// field is always present and queryable.
	sourceProduct := info.SourceProduct
	if sourceProduct == "" {
		sourceProduct = ratio_setting.DefaultSourceProduct
	}
	params.Other["source_product"] = sourceProduct
	// NOTE: client_ip is NOT written here — it is controlled by the user's
	// RecordIpLog setting and handled in RecordConsumeLog / RecordErrorLog.
}

// ComputeFingerprint produces a 16-hex-char (64-bit) SHA-256 digest of
// tokenID + modelName + first 4096 bytes of the request body.
// The fingerprint is NOT a security hash — it serves as a deduplication /
// anomaly detection key in governance queries.
func ComputeFingerprint(tokenID int, modelName string, requestBody []byte) string {
	h := sha256.New()
	// Length-prefixed fields prevent boundary confusion.
	fmt.Fprintf(h, "%d|%s|", tokenID, modelName)
	if len(requestBody) > fingerprintBodyLimit {
		h.Write(requestBody[:fingerprintBodyLimit])
	} else {
		h.Write(requestBody)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

package handler

// GET /v1/generation — resolve a single relayed request to its cost,
// provider and usage using nothing but the id the caller already has (an
// inbound X-Request-Id it sent, or the one middleware.RequestId minted and
// echoed back on the original response). Mirrors OpenRouter's
// /api/v1/generation name and purpose so an OpenRouter-SDK-shaped integrator
// needs no new vocabulary to reconcile spend — documented in
// doc/product-integration-guide.md §F (key-holder endpoints).
//
// Route: GET /v1/generation (dashboard.go's TokenAuth group). Read-only, no
// flag: the underlying data (the caller's own log row) was already
// queryable one row at a time via /logs; this just adds the by-request-id
// lookup a caller holding only a key (no console access) can use.

import (
	"net/http"
	"regexp"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// generationIdPattern matches the same charset/length RequestId() accepts on
// an inbound X-Request-Id (see middleware/request-id.go), plus the shorter
// ids GetTimeString+GetRandomString mints — both are alphanumeric with
// [._-] and within 8..36 characters (the same bounds the inbound header
// accepts), so one pattern covers both without querying twice.
var generationIdPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,36}$`)

type generationUsageView struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	// CachedTokens is omitted (not zero-valued) when the row carries no
	// cache_tokens key at all — a non-Anthropic-cache request should not
	// look like a cache request that hit for exactly 0 tokens.
	CachedTokens *int `json:"cached_tokens,omitempty"`
}

type generationView struct {
	Id            string              `json:"id"`
	RequestId     string              `json:"request_id"`
	Model         string              `json:"model"`
	UpstreamModel string              `json:"upstream_model,omitempty"`
	ProviderName  string              `json:"provider_name"`
	Quota         int                 `json:"quota"`
	TotalCost     float64             `json:"total_cost"`
	Usage         generationUsageView `json:"usage"`
	LatencyMs     int                 `json:"latency_ms"`
	FirstTokenMs  int                 `json:"first_token_ms,omitempty"`
	Streamed      bool                `json:"streamed"`
	SourceProduct string              `json:"source_product,omitempty"`
	ProjectId     int                 `json:"project_id,omitempty"`
	SessionId     string              `json:"session_id,omitempty"`
	CreatedAt     int64               `json:"created_at"`
}

// GetGeneration handles GET /v1/generation?id=<request_id>.
func GetGeneration(c *gin.Context) {
	id := c.Query("id")
	// A malformed id can never match a stored request_id (RequestId() only
	// ever sets ids that satisfy this same charset), so there is nothing to
	// look up — 404 same as "not found", not 400, matching the "wrong id ==
	// not-yours-or-never-happened" convention GetLogByRequestID enforces.
	if !generationIdPattern.MatchString(id) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "generation not found"})
		return
	}

	row, err := repo.GetLogByRequestID(id, c.GetInt("id"), c.GetString("tenant_id"), c.GetInt("token_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "generation not found"})
		return
	}

	// Read the caller-visible subset of Other, same projection /logs uses —
	// this is what keeps admin_info/channel_id (and any other TierInternal
	// key) out of this response even though the row itself carries them.
	otherMap, _ := common.StrToMap(repo.SanitizeOtherForUser(row.Other))

	var cachedTokens *int
	if v, ok := otherMap["cache_tokens"]; ok {
		if f, ok := v.(float64); ok {
			n := int(f)
			cachedTokens = &n
		}
	}
	var firstTokenMs int
	if v, ok := otherMap["frt"]; ok {
		if f, ok := v.(float64); ok && f > 0 {
			firstTokenMs = int(f)
		}
	}
	sessionId, _ := otherMap["session_id"].(string)
	sourceProduct, _ := otherMap["source_product"].(string)
	requestId, _ := otherMap["request_id"].(string)
	if requestId == "" {
		requestId = id
	}

	totalCost := 0.0
	if common.QuotaPerUnit > 0 {
		totalCost = float64(row.Quota) / common.QuotaPerUnit
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": generationView{
			Id:            id,
			RequestId:     requestId,
			Model:         row.ModelName,
			UpstreamModel: row.UpstreamModel,
			ProviderName:  constant.GetChannelTypeName(row.ChannelType),
			Quota:         row.Quota,
			TotalCost:     totalCost,
			Usage: generationUsageView{
				PromptTokens:     row.PromptTokens,
				CompletionTokens: row.CompletionTokens,
				CachedTokens:     cachedTokens,
			},
			LatencyMs:     row.TotalLatencyMs,
			FirstTokenMs:  firstTokenMs,
			Streamed:      row.IsStream,
			SourceProduct: sourceProduct,
			ProjectId:     row.ProjectId,
			SessionId:     sessionId,
			CreatedAt:     row.CreatedAt,
		},
	})
}

package dto

import (
	"encoding/json"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// OpenAIResponsesCompactionRequest is the documented field subset accepted
// by POST /v1/responses/compact (cycle-8 L6, wire-formats-03). This is an
// ALLOW-LIST, not an exclusion list: only the fields declared below are ever
// forwarded to the vendor (see ToOpenAIResponsesRequest). Every other field
// of dto.OpenAIResponsesRequest — Include, MaxOutputTokens, Metadata, Store,
// Temperature, ToolChoice, TopP, Truncation, User, MaxToolCalls, Prompt and
// Stream — is dropped with no field to bind it to (IsStream below is
// hard-wired false; there is no Stream field). Tools/Reasoning/Text are
// decoded here for client compatibility (Codex-parity request shapes send
// them) but are NOT copied into the converted request by
// ToOpenAIResponsesRequest — matching the upstream reference, which
// documents them as "parsed for client compatibility but intentionally not
// sent upstream". prompt_cache_options is not modelled this cycle (the
// parent type has no such field either); its absence is deliberate, not an
// oversight. Any field outside this struct that a caller sends in the raw
// request body is silently dropped by the JSON decode into this type — see
// ToOpenAIResponsesRequest for why that also holds in pass-through mode.
type OpenAIResponsesCompactionRequest struct {
	Model                string          `json:"model"`
	Input                json.RawMessage `json:"input,omitempty"`
	Instructions         json.RawMessage `json:"instructions,omitempty"`
	PreviousResponseID   string          `json:"previous_response_id,omitempty"`
	Tools                json.RawMessage `json:"tools,omitempty"`
	ParallelToolCalls    json.RawMessage `json:"parallel_tool_calls,omitempty"`
	Reasoning            *Reasoning      `json:"reasoning,omitempty"`
	ServiceTier          string          `json:"service_tier,omitempty"`
	PromptCacheKey       json.RawMessage `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`
	Text                 json.RawMessage `json:"text,omitempty"`
}

// ToOpenAIResponsesRequest converts to the full wire type, populating ONLY
// Model/Input/Instructions/PreviousResponseID/ParallelToolCalls/
// ServiceTier/PromptCacheKey/PromptCacheRetention. Tools, Reasoning and Text
// are deliberately NOT copied (see the struct's doc comment above); every
// other field of OpenAIResponsesRequest stays at its Go zero value
// regardless of what the caller's raw JSON body contained — this struct
// never round-trips the caller's raw bytes, so a smuggled or dropped field
// has nowhere to land, in EITHER the converted path or the compact
// endpoint's own pass-through mode (which marshals the return value of this
// method, not the client's original body — see relay.ResponsesHelper).
func (r *OpenAIResponsesCompactionRequest) ToOpenAIResponsesRequest() *OpenAIResponsesRequest {
	return &OpenAIResponsesRequest{
		Model:                r.Model,
		Input:                r.Input,
		Instructions:         r.Instructions,
		PreviousResponseID:   r.PreviousResponseID,
		ParallelToolCalls:    r.ParallelToolCalls,
		ServiceTier:          r.ServiceTier,
		PromptCacheKey:       r.PromptCacheKey,
		PromptCacheRetention: r.PromptCacheRetention,
	}
}

// GetTokenCountMeta delegates to the full request's implementation (Input +
// Instructions, like the parent) rather than duplicating its input-parsing
// logic — ToOpenAIResponsesRequest carries the only two fields that
// implementation reads out of this subset.
func (r *OpenAIResponsesCompactionRequest) GetTokenCountMeta() *types.TokenCountMeta {
	return r.ToOpenAIResponsesRequest().GetTokenCountMeta()
}

// IsStream is always false: the compact request type has no Stream field to
// bind, so there is nothing a caller could set to make this true.
func (r *OpenAIResponsesCompactionRequest) IsStream(c *gin.Context) bool {
	return false
}

func (r *OpenAIResponsesCompactionRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}

// OpenAIResponsesCompactionResponse is read for two things by
// OaiResponsesCompactHandler — Usage for billing, and Error to detect a
// vendor error carried in-band inside an HTTP 200 (the same shape
// OpenAIResponsesResponse guards against, see its GetOpenAIError) — before
// the response bytes are forwarded to the caller verbatim (not re-marshalled
// from this struct), unlike the full /v1/responses handler which fully
// unmarshals, inspects and re-serves the body.
type OpenAIResponsesCompactionResponse struct {
	Usage *Usage `json:"usage,omitempty"`
	Error any    `json:"error,omitempty"`
}

// GetOpenAIError 从动态错误类型中提取OpenAIError结构
func (o *OpenAIResponsesCompactionResponse) GetOpenAIError() *types.OpenAIError {
	return GetOpenAIError(o.Error)
}

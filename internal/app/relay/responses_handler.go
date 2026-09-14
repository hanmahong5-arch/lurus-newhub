package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func ResponsesHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	// POST /v1/responses/compact (cycle-8 L6, wire-formats-03): the subset
	// gate runs BEFORE the PassThroughRequestEnabled branch below can even
	// see a request — decode into the compact DTO, refuse an unsupported
	// channel with zero upstream calls, then derive a LOCAL
	// *dto.OpenAIResponsesRequest carrying only the documented subset. This
	// must not assign back into info.Request: handler.Relay builds one
	// RelayInfo per call and reuses the same pointer across every failover
	// attempt in its retry loop, so mutating the shared field here would
	// make attempt 2 see the already-converted type and fail the assertion
	// below — turning a retryable transient upstream failure into a
	// customer-visible 400 (repair-round finding A-F1;
	// TestResponsesHelper_SameRelayInfoRetriesAfterMutation covers this).
	isCompact := info.RelayMode == relayconstant.RelayModeResponsesCompact
	var responsesReq *dto.OpenAIResponsesRequest
	if isCompact {
		compactReq, ok := info.Request.(*dto.OpenAIResponsesCompactionRequest)
		if !ok {
			return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.OpenAIResponsesCompactionRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if !common.SupportsResponsesCompact(info.ChannelType) {
			return types.NewErrorWithStatusCode(fmt.Errorf("channel type %d does not support /v1/responses/compact", info.ChannelType), types.ErrorCodeResponsesCompactUnsupported, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		responsesReq = compactReq.ToOpenAIResponsesRequest()
	} else {
		var ok bool
		responsesReq, ok = info.Request.(*dto.OpenAIResponsesRequest)
		if !ok {
			return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.OpenAIResponsesRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
	}

	request, err := common.DeepCopy(responsesReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to GeneralOpenAIRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	var requestBody io.Reader
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		if isCompact {
			// The compact endpoint's pass-through mode still forwards only
			// the RE-ENCODED subset (`request`, converted above), never the
			// caller's raw body — PassThroughRequestEnabled exists to skip
			// OUR request conversion, not to bypass the subset gate.
			jsonData, err := common.Marshal(request)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
			// Pass-through must still honour the channel's disabled-field
			// policy (repair-round finding B-F2): a channel with
			// AllowServiceTier=false (RemoveDisabledFields' default) strips
			// service_tier on the converted path, and skipping that here
			// would let a caller reach the vendor's premium tier on a
			// channel whose settings forbid it while newhub prices the call
			// at the standard ratio.
			jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
			requestBody = bytes.NewBuffer(jsonData)
		} else {
			body, err := common.GetRequestBody(c)
			if err != nil {
				return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
			}
			requestBody = bytes.NewBuffer(body)
		}
	} else {
		convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		jsonData, err := common.Marshal(convertedRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// remove disabled fields for OpenAI Responses API
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// apply param override
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverride(jsonData, info.ParamOverride, relaycommon.BuildParamOverrideContext(info))
			if err != nil {
				return types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
			}
		}

		if common.DebugEnabled {
			println("requestBody: ", string(jsonData))
		}
		requestBody = bytes.NewBuffer(jsonData)
	}

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		httpResp = resp.(*http.Response)

		if httpResp.StatusCode != http.StatusOK {
			newAPIError = app.RelayErrorHandler(c.Request.Context(), httpResp, false)
			// reset status code 重置状态码
			app.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		// reset status code 重置状态码
		app.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
		app.PostAudioConsumeQuota(c, info, usage.(*dto.Usage), "")
	} else {
		postConsumeQuota(c, info, usage.(*dto.Usage))
	}

	// Response registry insert (cycle-8 L7, tasks-plugins-12): AFTER
	// postConsumeQuota, plain POST /v1/responses — the compact endpoint
	// (isCompact) is excluded here rather than relying on
	// maybeUpsertResponseRegistry's own channel-type gate, because
	// OaiResponsesCompactHandler does not call c.Set("responses_id", ...)
	// today (that call lives in OaiResponsesHandler and
	// OaiResponsesStreamHandler — openai/relay_responses.go:49 and :172), so
	// a compact call would not find an id to register anyway. Best-effort:
	// see maybeUpsertResponseRegistry's doc comment for why a failure here
	// must not propagate.
	if !isCompact {
		maybeUpsertResponseRegistry(c, info, responsesReq)
	}
	return nil
}

// maybeUpsertResponseRegistry writes response_registry row for a successful
// POST /v1/responses, unless the caller opted out (store:false), the
// channel type does not support the Responses API's stateful surface
// (common.SupportsResponsesStateful's allow-list — a SEPARATE predicate from
// the /v1/responses/compact gate above, cycle-8 L7 repair round finding
// B-F6: the two questions are not the same, even though both happen to be
// OpenAI-only this cycle), the channel is multi-key (see below), or no id
// was ever stashed into the gin context (an id-less vendor body, or a
// response shape neither OaiResponsesHandler nor OaiResponsesStreamHandler
// stashed — see their own doc comments).
//
// Multi-key skip (cycle-8 L7 repair round, finding B-F3): the row pins
// channel_id but not the key index a multi-key channel selected for THIS
// request (info.ChannelMultiKeyIndex) — GetNextEnabledKey picks per request
// (random or polling), so a later GET/DELETE re-selecting through
// SetupContextForSelectedChannel would run with a DIFFERENT key than the one
// that minted the id, and the vendor would answer its own 404 for an
// account that never created the response — the exact failure this lane
// exists to prevent, one level down. Migration 036 (this cycle, frozen) has
// no key-index column, so the interim fix is to not write a row at all for
// a multi-key channel; the key-index column is a cycle-9 follow-up (see
// doc/product-integration-guide.md's /v1/responses/:response_id row).
//
// This runs on the billed hot path and MUST NEVER fail the response it
// belongs to: a write error is logged and counted
// (metrics.ResponseRegistryErrorsTotal), never returned. The caller already
// received (and was billed for) a valid response; losing the ability to
// later GET/DELETE it by id is a degraded feature, not a failed request.
func maybeUpsertResponseRegistry(c *gin.Context, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) {
	// store defaults to true on the OpenAI wire when omitted; only an
	// explicit literal `false` opts out — see dto.OpenAIResponsesRequest.Store
	// (json.RawMessage, not a bool, to preserve "absent" vs "false").
	if string(req.Store) == "false" {
		return
	}
	if !common.SupportsResponsesStateful(info.ChannelType) {
		return
	}
	if info.ChannelIsMultiKey {
		return
	}
	idVal, exists := c.Get("responses_id")
	if !exists {
		return
	}
	id, ok := idVal.(string)
	if !ok || id == "" {
		return
	}

	// Read the tenant through the SAME accessor the read side
	// (handler.RelayResponsesRetrieve/Delete) uses, so the two can never
	// silently drift onto different context keys (cycle-8 L7 repair round,
	// finding B-F8). Falls back to the raw "tenant_id" key only when the
	// full tenant-context middleware chain did not run (unit tests that
	// hand-seed "tenant_id" without TokenAuth) — in production both keys
	// are populated from the same tokenTenantId at auth time (auth.go), so
	// this fallback is never exercised on the live path.
	tenantId := c.GetString("tenant_id")
	if tenantCtx, tenantErr := middleware.GetTenantContext(c); tenantErr == nil && tenantCtx != nil {
		tenantId = tenantCtx.TenantID
	}

	now := common.GetTimestamp()
	row := &entity.ResponseRegistry{
		ResponseId:    id,
		TenantId:      tenantId,
		UserId:        info.UserId,
		TokenId:       info.TokenId,
		ChannelId:     info.ChannelId,
		UpstreamModel: info.UpstreamModelName,
		CreatedAt:     now,
		ExpiresAt:     now + repo.ResponseRegistryTTLSeconds(),
	}
	if err := repo.UpsertResponseRegistry(row); err != nil {
		metrics.RecordResponseRegistryError()
		logger.LogError(c, "response registry upsert failed: "+err.Error())
	}
}

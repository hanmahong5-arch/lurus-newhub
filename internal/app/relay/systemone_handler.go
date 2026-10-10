package relay

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// SystemOneHelper relays POST /v1/systemone. It mirrors RerankHelper with two
// deliberate differences: a non-200 upstream response goes to the adaptor's
// SystemOneError (which decides caller fault vs channel fault, so a bad
// upstream key is never surfaced as the caller's 401), and billing is whatever
// usage DoResponse reports (input tokens only; output is always free).
func SystemOneHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	soReq, ok := info.Request.(*dto.SystemOneRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.SystemOneRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(soReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to SystemOneRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	baseAdaptor := GetAdaptor(info.ApiType)
	if baseAdaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor, ok := baseAdaptor.(provider.SystemOneAdaptor)
	if !ok {
		return types.NewError(fmt.Errorf("api type %d does not serve system one", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	baseAdaptor.Init(info)

	// No pass-through branch (unlike RerankHelper): the raw caller body would
	// skip model mapping, and a self-hosted server silently auto-routes any
	// name it does not know (laya-english would never pin its checkpoint). The
	// converted body loses nothing a caller is allowed to send.
	convertedRequest, err := adaptor.ConvertSystemOneRequest(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverride(jsonData, info.ParamOverride, relaycommon.BuildParamOverrideContext(info))
		if err != nil {
			return types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
		}
	}
	requestBody := bytes.NewBuffer(jsonData)

	resp, err := baseAdaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	httpResp, _ := resp.(*http.Response)
	if httpResp != nil && httpResp.StatusCode != http.StatusOK {
		newAPIError = adaptor.SystemOneError(c, info, httpResp)
		_ = httpResp.Body.Close() // idempotent; the classifier may already have drained it
		if newAPIError == nil {
			// A classifier that declines to classify must not turn an upstream
			// failure into a billed success.
			newAPIError = types.NewOpenAIError(fmt.Errorf("system one upstream returned status %d", httpResp.StatusCode), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)
		}
		app.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	usage, newAPIError := baseAdaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		// A contract-violating answer still cost the upstream its inference:
		// when the adaptor reports a valid usage next to the error, settle it.
		// The pre-consume is consumed by that settlement, so clear the local
		// hold before the caller's failure path would hand it back a second
		// time. The platform pre-auth id is cleared by the settlement itself
		// (app.settleOrPark / abandonPreAuth are its only two exits).
		if billed, _ := usage.(*dto.Usage); billed != nil && billed.TotalTokens > 0 {
			postConsumeQuota(c, info, billed)
			info.FinalPreConsumedQuota = 0
		}
		app.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}
	billed, _ := usage.(*dto.Usage)
	if billed == nil {
		billed = &dto.Usage{}
	}
	postConsumeQuota(c, info, billed)
	return nil
}

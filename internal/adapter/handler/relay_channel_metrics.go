package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// recordChannelAttempt feeds the per-channel traffic series once per upstream
// attempt of the retry loop. Failures that are not an upstream exchange
// (caller quota, request preparation, routing, persistence - see
// types.RelayErrorType) are not the channel's doing and are not recorded.
func recordChannelAttempt(channelID int, apiErr *types.NewAPIError) {
	if apiErr == nil {
		metrics.RecordChannelAttempt(channelID, http.StatusOK, false, false, false)
		return
	}
	switch types.RelayErrorType(apiErr) {
	case "internal", "insufficient_quota":
		return
	}
	status := apiErr.StatusCode
	timeout := status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout || status == 524
	network := apiErr.GetErrorCode() == types.ErrorCodeDoRequestFailed
	if network {
		status = 0 // no HTTP answer was received
	}
	metrics.RecordChannelAttempt(channelID, status, true, timeout, network)
}

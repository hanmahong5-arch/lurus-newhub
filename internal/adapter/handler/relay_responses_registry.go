package handler

// relay_responses_registry.go — GET/DELETE /v1/responses/:response_id
// (cycle-8 L7, tasks-plugins-12): the stateful half of the OpenAI Responses
// API. A POST /v1/responses with store != false is pinned to the channel
// that produced it in response_registry (relay.ResponsesHelper's insert
// hook, internal/app/relay/responses_handler.go); these two routes look
// that row up and route straight back to the SAME channel + key —
// middleware.SetupContextForSelectedChannel, not weighted Distribute()
// selection, because a vendor-minted response id is meaningless to any
// other channel.
//
// Ownership is same-tenant AND same-user (O7, cycle-8 plan §8 L4/L7
// amendment). These four lookup/ownership outcomes — absent id, another
// user's row, another tenant's row, a missing/disabled/no-longer-eligible
// channel — answer the same 404 response_not_found gateway envelope; this
// route never answers 403 for any of them, so a caller cannot distinguish
// "not yours" from "does not exist" by status code or body shape. Upstream
// and transport failures (SetupContextForSelectedChannel's own error,
// GenRelayInfo failure, no adaptor, DoRequest failure, a non-*http.Response
// or a body-read failure) answer their own 5xx, not this 404 — see each
// branch below (cycle-8 L7 repair round, finding A-F8/B-F13). And once the
// row is found and the channel is reachable, a vendor-side 404 (the vendor
// itself has no record of the id) is forwarded byte-for-byte instead: it
// carries the vendor's own body, not this gateway envelope, and has no
// error.code — see the last branch below (finding B-F11).

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// RelayResponsesRetrieve serves GET /v1/responses/:response_id.
func RelayResponsesRetrieve(c *gin.Context) {
	relayResponsesRegistry(c, relayconstant.RelayModeResponsesRetrieve)
}

// RelayResponsesDelete serves DELETE /v1/responses/:response_id.
func RelayResponsesDelete(c *gin.Context) {
	relayResponsesRegistry(c, relayconstant.RelayModeResponsesDelete)
}

// respondResponseNotFound writes the 404 envelope used for this file's
// lookup/ownership outcomes — absent id, another user's row, another
// tenant's row, a missing/disabled/no-longer-eligible channel — same
// status, same OpenAI-wire shape, same error code regardless of which of
// those applies, so they are byte-identical on the wire. Upstream and
// transport failures (GenRelayInfo, no adaptor, DoRequest, a non-
// *http.Response, a body-read failure) answer their own 5xx instead, not
// this function — see each branch below.
func respondResponseNotFound(c *gin.Context) {
	apiErr := types.NewErrorWithStatusCode(fmt.Errorf("response not found"), types.ErrorCodeResponseNotFound, http.StatusNotFound, types.ErrOptionWithSkipRetry())
	c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
}

func relayResponsesRegistry(c *gin.Context, relayMode int) {
	responseId := c.Param("response_id")

	row, err := repo.GetResponseRegistry(responseId)
	if err != nil {
		// Absent id: nothing to deny, nothing to audit — the same shape a
		// stranger sees. Ownership mismatches below are the audited case.
		respondResponseNotFound(c)
		return
	}

	requesterUserId := c.GetInt("id")
	tenantCtx, tenantErr := middleware.GetTenantContext(c)
	if tenantErr != nil || row.TenantId != tenantCtx.TenantID || row.UserId != requesterUserId {
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorToken, requesterUserId,
			governance.ActionResponseDenied, governance.ResourceResponse, 0,
			fmt.Sprintf(`{"response_id":%q,"requester_user_id":%d}`, responseId, requesterUserId)))
		respondResponseNotFound(c)
		return
	}

	channel, err := repo.GetChannelById(row.ChannelId, true)
	if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled {
		respondResponseNotFound(c)
		return
	}
	// Re-check the channel TYPE at read time, not just its status (cycle-8
	// L7 repair round, finding A-F6): the insert hook only ever writes a row
	// for a channel type in common.SupportsResponsesStateful's allow-list
	// (relay.ResponsesHelper), but nothing stops an operator retyping the
	// channel (e.g. to Azure) AFTER the row was written. Without this check,
	// SetupContextForSelectedChannel below would still succeed (it only
	// validates status/model): this handler never assigns the relayMode
	// parameter onto info.RelayMode (see the InitChannelMeta comment
	// below), so genBaseRelayInfo's Path2RelayMode leaves info.RelayMode ==
	// RelayModeResponses for a "/v1/responses/<id>" path — and the OpenAI
	// adaptor's GetRequestURL DOES have a RelayModeResponses case on Azure
	// (openai/adaptor.go), which would build the Azure response-CREATION
	// URL ("/openai/v1/responses?api-version=..."), silently dropping the
	// response_id — not the generic per-model deployment URL other relay
	// modes fall through to. Answer the same 404 instead, with zero
	// upstream calls.
	if !common.SupportsResponsesStateful(channel.Type) {
		respondResponseNotFound(c)
		return
	}

	if apiErr := middleware.SetupContextForSelectedChannel(c, channel, row.UpstreamModel); apiErr != nil {
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}

	info, genErr := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAIResponses, &dto.OpenAIResponsesRequest{Model: row.UpstreamModel}, nil)
	if genErr != nil {
		apiErr := types.NewErrorWithStatusCode(genErr, types.ErrorCodeGenRelayInfoFailed, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	// InitChannelMeta is mandatory (every relay entry point calls it) — it
	// resolves ApiType/ApiKey/ChannelBaseUrl from the context keys
	// SetupContextForSelectedChannel just set, which adaptor.DoRequest below
	// depends on. relayMode (the function parameter) is NOT assigned onto
	// info.RelayMode: DoResponse (which switches on info.RelayMode) is
	// never called here — this handler bypasses it and reads the raw
	// *http.Response itself (see below). The OpenAI adaptor's
	// GetRequestURL also switches on info.RelayMode, but only inside its
	// Azure branch, which the earlier SupportsResponsesStateful gate above
	// keeps unreachable here (that map is OpenAI-only today — see its own
	// doc comment for what would happen if it weren't). So on the channel
	// types this handler can actually reach, leaving relayMode unassigned
	// is unobservable (cycle-8 L7 repair round, finding A-F6).
	info.InitChannelMeta(c)

	adaptor := relay.GetAdaptor(info.ApiType)
	if adaptor == nil {
		apiErr := types.NewErrorWithStatusCode(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	adaptor.Init(info)

	// No body: GET/DELETE carry none, and this surface does no request
	// conversion — DoApiRequest builds the outbound *http.Request from
	// c.Request.Method (already GET/DELETE, since that's how this handler
	// is routed) and info.RequestURLPath (the generic "/v1/responses/:id"
	// path genBaseRelayInfo captured from c.Request.URL).
	resp, doErr := adaptor.DoRequest(c, info, nil)
	if doErr != nil {
		apiErr := types.NewOpenAIError(doErr, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil {
		apiErr := types.NewErrorWithStatusCode(fmt.Errorf("upstream returned no response"), types.ErrorCodeBadResponse, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	defer func() { _ = httpResp.Body.Close() }()

	// Byte-passthrough: no usage parsing, no pre/post-consume — GET/DELETE
	// on an already-billed resource write no quota row.
	contentType := httpResp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	if strings.Contains(contentType, "text/event-stream") {
		// Streaming retrieval (cycle-8 L7 repair round, finding B-F5):
		// GET /v1/responses/:response_id?stream=true is the documented
		// resume path for a background response. Buffering the whole
		// upstream body with io.ReadAll before writing anything (the
		// branch below) would hold a still-in-progress vendor stream until
		// it closes — potentially minutes — and then deliver it as one
		// blob. Copy incrementally instead, flushing after every chunk
		// (helper.FlushWriter — the same flush primitive
		// helper.StreamScannerHandler's own write path uses).
		c.Writer.Header().Set("Content-Type", contentType)
		c.Writer.WriteHeader(httpResp.StatusCode)
		buf := make([]byte, 4096)
		for {
			n, readErr := httpResp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
					break
				}
				_ = helper.FlushWriter(c)
			}
			if readErr != nil {
				break
			}
		}
	} else {
		body, readErr := io.ReadAll(httpResp.Body)
		if readErr != nil {
			apiErr := types.NewOpenAIError(readErr, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
			c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
			return
		}
		c.Data(httpResp.StatusCode, contentType, body)
	}

	action := governance.ActionResponseRetrieved
	if relayMode == relayconstant.RelayModeResponsesDelete {
		action = governance.ActionResponseDeleted
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorToken, requesterUserId,
		action, governance.ResourceResponse, 0,
		fmt.Sprintf(`{"response_id":%q,"channel_id":%d,"upstream_status":%d}`, responseId, row.ChannelId, httpResp.StatusCode)))

	if relayMode == relayconstant.RelayModeResponsesDelete && httpResp.StatusCode >= 200 && httpResp.StatusCode < 300 {
		if delErr := repo.DeleteResponseRegistry(responseId); delErr != nil {
			// The vendor already confirmed deletion; a local cleanup failure
			// only leaves a stale registry row (harmless — the next GET/DELETE
			// through it just returns the vendor's own now-404) rather than
			// something this response should fail over.
			common.SysError("delete response registry row after vendor 2xx: " + delErr.Error())
		}
	}
}

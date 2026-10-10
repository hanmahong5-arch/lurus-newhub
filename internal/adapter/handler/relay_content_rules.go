package handler

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// contentFormatFor maps a relay format to the wire format the content rules
// understand. ok=false for formats the rules do not cover (embeddings, audio,
// images, realtime, tasks): those pass untouched.
func contentFormatFor(f types.RelayFormat) (contentpolicy.Format, bool) {
	switch f {
	case types.RelayFormatOpenAI:
		return contentpolicy.FormatOpenAIChat, true
	case types.RelayFormatClaude:
		return contentpolicy.FormatClaude, true
	case types.RelayFormatGemini:
		return contentpolicy.FormatGemini, true
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompact:
		return contentpolicy.FormatResponses, true
	}
	return "", false
}

// applyContentRules applies the caller's tenant content rules (platform rules
// first) to the request body, before any parsing, conversion or forwarding.
//
// Why it works on the cached raw body: every path to the upstream - the typed
// conversion path and the pass-through path alike - starts from
// common.GetRequestBody. Rewriting that one buffer means a pass-through
// channel forwards the masked bytes; masking only the parsed struct would let
// pass-through send the caller's original body (the "pass-through restores
// the redaction" defect fixed upstream in axonhub PR 2153).
//
// A reject rule in enforce mode returns a 400 content_rejected error naming
// the rule id and never the matched text. Observe mode only counts and
// audits. A rule-store failure fails open (logged): the relay must not go
// down because the rules table is unreadable, and the failure is loud in the
// system log.
func applyContentRules(c *gin.Context, relayFormat types.RelayFormat) *types.NewAPIError {
	format, ok := contentFormatFor(relayFormat)
	if !ok {
		return nil
	}
	rs, err := repo.ContentRulesetForTenant(c.GetString("tenant_id"))
	if err != nil {
		common.SysError("content rules unavailable, request forwarded unfiltered: " + err.Error())
		return nil //nolint:nilerr // by design: a rules outage must not turn into a relay outage; the forward is logged above
	}
	if rs.Len() == 0 {
		return nil
	}
	body, err := common.GetRequestBody(c)
	if err != nil || len(body) == 0 {
		return nil //nolint:nilerr // the normal parse path reports the malformed body with the right wire error
	}
	res := rs.Apply(format, body)
	contentpolicy.Report(res)
	if len(res.Hits) > 0 {
		recordContentRuleHit(c, res)
	}
	if res.Rejected != nil {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("request rejected by content rule %d", res.Rejected.RuleId),
			types.ErrorCodeContentRejected, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if res.Changed {
		c.Set(common.KeyRequestBody, res.Body)
		c.Request.Body = io.NopCloser(bytes.NewReader(res.Body))
		c.Request.ContentLength = int64(len(res.Body))
	}
	return nil
}

// recordContentRuleHit writes one audit event per request that matched any
// rule: rule ids, kinds, modes and counts only - never the matched content.
func recordContentRuleHit(c *gin.Context, res contentpolicy.Result) {
	var b strings.Builder
	b.WriteString(`{"rules":[`)
	for i, h := range res.Hits {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"rule_id":%d,"kind":%q,"mode":%q,"count":%d}`, h.RuleId, h.Kind, h.Mode, h.Count)
	}
	b.WriteString(`],"rejected":`)
	if res.Rejected != nil {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteByte('}')
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorToken, c.GetInt("id"),
		governance.ActionContentRuleHit, governance.ResourceContentRule, 0, b.String()))
}

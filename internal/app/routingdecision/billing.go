package routingdecision

import (
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	pkgcommon "github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// EvalQuota prices an evaluation at the evaluator model's own price for the
// caller's group: per-call price when one is configured, otherwise input
// tokens x model ratio x group ratio (System One bills the input side only,
// matching how the relay path settles the same model). An unpriced model
// costs nothing here - the evaluation is a platform-side step the tenant opted
// into, and refusing the routed request because the evaluator has no price
// would turn a billing gap into an outage. The row is still written (quota 0),
// so the gap is visible.
func EvalQuota(model, usingGroup, userGroup string, inputTokens int) int {
	groupRatio := ratio_setting.GetGroupRatio(usingGroup)
	if r, ok := ratio_setting.GetGroupGroupRatio(userGroup, usingGroup); ok {
		groupRatio = r
	}
	gr := decimal.NewFromFloat(groupRatio)
	if price, ok := ratio_setting.GetModelPrice(model, false); ok {
		return int(decimal.NewFromFloat(price).Mul(decimal.NewFromFloat(pkgcommon.QuotaPerUnit)).Mul(gr).Round(0).IntPart())
	}
	ratio, ok, _ := ratio_setting.GetModelRatio(model)
	if !ok || inputTokens <= 0 {
		return 0
	}
	q := int(decimal.NewFromInt(int64(inputTokens)).Mul(decimal.NewFromFloat(ratio)).Mul(gr).Round(0).IntPart())
	if q <= 0 && ratio > 0 && groupRatio > 0 {
		q = 1 // a chargeable call never rounds down to free
	}
	return q
}

// Settle books an evaluation as its own consume row (model = evaluator model,
// other.routing_eval = true, the request's own request_id) and debits the
// caller. Failures are logged and flagged on the row, never returned: the
// routed request has already been decided and must proceed.
//
// Tests replace it through the exported variable to observe the call without a
// database.
var Settle = settleEval

func settleEval(c *gin.Context, model string, out *EvalOutput, latency time.Duration) {
	if out == nil {
		return
	}
	info := evalRelayInfo(c, model, out, latency)

	quota := EvalQuota(model, info.UsingGroup, info.UserGroup, out.InputTokens)
	other := map[string]interface{}{
		"routing_eval": true,
		"routing":      "evaluation",
	}
	if quota != 0 {
		if err := app.SettleConsume(c, info, quota, 0, "routing_eval"); err != nil {
			app.FlagSettlementOutcome(other, err)
		}
	}
	params := repo.RecordConsumeLogParams{
		ChannelId:        out.ChannelID,
		PromptTokens:     out.InputTokens,
		CompletionTokens: out.OutputTokens,
		ModelName:        model,
		TokenName:        c.GetString("token_name"),
		Quota:            quota,
		Content:          fmt.Sprintf("decision routing evaluation (%dms)", latency.Milliseconds()),
		TokenId:          info.TokenId,
		UseTimeSeconds:   int(latency.Seconds()),
		Group:            info.UsingGroup,
		Other:            other,
	}
	governance.EnrichLogParams(c, info, &params)
	repo.RecordConsumeLog(c, info.UserId, params)
}

// evalRelayInfo is the RelayInfo the evaluation settles under: the caller's
// identity from the gin context (user, token, project, product attribution
// and the platform wallet account), the evaluator model as the model, and
// the evaluator's channel. IdentityAccountID is what routes SettleConsume to
// the platform wallet for unified-billing tenants - without it the evaluation
// would only ever hit the local ledger.
func evalRelayInfo(c *gin.Context, model string, out *EvalOutput, latency time.Duration) *common.RelayInfo {
	var identityAccountID int64
	if v, exists := c.Get("identity_account_id"); exists {
		identityAccountID, _ = v.(int64)
	}
	info := &common.RelayInfo{
		IdentityAccountID: identityAccountID,
		SourceProduct: ratio_setting.ResolveSourceProductWithDefault(c.GetHeader(ratio_setting.SourceProductHeader),
			pkgcommon.GetContextKeyString(c, constant.ContextKeyTokenSourceProduct)),
		UserId:          pkgcommon.GetContextKeyInt(c, constant.ContextKeyUserId),
		UsingGroup:      pkgcommon.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		UserGroup:       pkgcommon.GetContextKeyString(c, constant.ContextKeyUserGroup),
		TokenId:         pkgcommon.GetContextKeyInt(c, constant.ContextKeyTokenId),
		TokenKey:        pkgcommon.GetContextKeyString(c, constant.ContextKeyTokenKey),
		TokenUnlimited:  pkgcommon.GetContextKeyBool(c, constant.ContextKeyTokenUnlimited),
		TokenGroup:      pkgcommon.GetContextKeyString(c, constant.ContextKeyTokenGroup),
		ProjectId:       pkgcommon.GetContextKeyInt(c, constant.ContextKeyProjectId),
		EmployeeRef:     pkgcommon.GetContextKeyString(c, constant.ContextKeyEmployeeRef),
		OriginModelName: model,
		StartTime:       time.Now().Add(-latency),
		ChannelMeta: &common.ChannelMeta{
			ChannelType:       out.ChannelType,
			ChannelId:         out.ChannelID,
			UpstreamModelName: out.UpstreamModel,
		},
	}
	if info.UsingGroup == "" {
		info.UsingGroup = out.Group
	}
	return info
}

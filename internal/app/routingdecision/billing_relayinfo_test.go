package routingdecision

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// The evaluation settles under the caller's own identity: the platform wallet
// account (what routes SettleConsume to the wallet for unified-billing
// tenants), the token / project attribution, the evaluator as the model and
// the evaluator's channel. Dropping the account id would silently move every
// evaluation to the local ledger only.
func TestEvalRelayInfo_CarriesCallerIdentityAndWalletAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set("identity_account_id", int64(66021))
	c.Set(string(constant.ContextKeyUserId), 7)
	c.Set(string(constant.ContextKeyTokenId), 9)
	c.Set(string(constant.ContextKeyUsingGroup), "vip")
	out := &EvalOutput{ChannelID: 3, ChannelType: 58, UpstreamModel: "evaluator-a", Group: "default"}

	info := evalRelayInfo(c, "evaluator-a", out, 40*time.Millisecond)

	if info.IdentityAccountID != 66021 {
		t.Fatalf("IdentityAccountID = %d, want 66021 (the wallet account from the gin context)", info.IdentityAccountID)
	}
	if info.UserId != 7 || info.TokenId != 9 || info.UsingGroup != "vip" {
		t.Fatalf("caller identity not carried: user=%d token=%d group=%q", info.UserId, info.TokenId, info.UsingGroup)
	}
	if info.OriginModelName != "evaluator-a" || info.ChannelMeta == nil || info.ChannelId != 3 {
		t.Fatalf("evaluator model/channel not carried: %+v", info)
	}
	// No group on the context -> the evaluator channel's group.
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	if g := evalRelayInfo(c2, "evaluator-a", out, 0).UsingGroup; g != "default" {
		t.Fatalf("UsingGroup fallback = %q, want the evaluator channel group", g)
	}
}

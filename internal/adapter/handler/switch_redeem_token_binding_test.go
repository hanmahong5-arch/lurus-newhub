package handler

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ============================================================================
// 业务事故防线：一张卡换出来的网关 token 必须「只值这张卡的钱」
//
// 白标客户端拿激活码换到的是一把可以直接打 LLM 的网关 token。这把 token 发错
// 属性有两种当场变成钱的事故：
//
//   - 事故一（无限额度）：token 发成 unlimited_quota=true / remain_quota=0 不设限
//     ⇒ 客户买的是 25 万额度的卡，拿到手却是一把不封顶的钥匙，一台机器就能把
//     经销商的整个预付池刷穿，而且刷的每一分钱都是我们先垫给上游的。
//   - 事故二（落到 default 租户）：token 的 tenant_id 不是发卡经销商而是 "default"
//     ⇒ 中转层的额度池闸门按 token 的租户找池子，找不到池子就当「不限量」放行
//     (PoolBalanceCheck 的 ErrPoolNotFound 分支)，于是这条流量既不扣经销商的池，
//     也不进任何人的账 —— 完全免费且不计费，对账时凭空少一块。
//
// 兑换成功本身已有测试覆盖；这里专门盯发出去的那把 token 的属性，因为它出错时
// 兑换流程一切正常、日志全绿，只有月底账单能看出来。
// ============================================================================

// TestSwitchRedeem_IssuedTokenIsTenantBoundAndQuotaCapped 兑一张卡，然后把
// 发出去的那把 token 从库里捞出来逐项核对。
func TestSwitchRedeem_IssuedTokenIsTenantBoundAndQuotaCapped(t *testing.T) {
	ctx, r := setupSwitchRedeemRouter(t)
	defer ctx.Cleanup()

	const cardQuota = 250000
	key := common.GetRandomString(32)
	seedSwitchRedemption(t, ctx, key, common.RedemptionCodeStatusEnabled, 0, cardQuota)

	w := postSwitchRedeem(t, r, map[string]string{
		"code":        key,
		"fingerprint": "fp-token-binding-0001",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("redeem: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	env := parseEnvelope(t, w)
	if success, _ := env["success"].(bool); !success {
		t.Fatalf("redeem 未成功，无法核对发出的 token: %s", w.Body.String())
	}
	data, ok := env["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data field: %s", w.Body.String())
	}
	tokenKey, _ := data["user_token"].(string)
	if tokenKey == "" {
		t.Fatalf("兑换成功却没有回 user_token: %s", w.Body.String())
	}

	var issued repo.Token
	if err := repo.WithoutTenantIsolation(ctx.DB).Where("`key` = ?", tokenKey).First(&issued).Error; err != nil {
		t.Fatalf("回给客户端的 token 在库里不存在(%v) —— 客户端拿着一把服务端不认的钥匙", err)
	}

	// 事故一：额度不封顶。
	if issued.UnlimitedQuota {
		t.Errorf("卡面 %d 额度换出了一把不限额度的 token —— 一台机器可以把经销商预付池刷穿", cardQuota)
	}
	if issued.RemainQuota != cardQuota {
		t.Errorf("token 额度 = %d，卡面 = %d —— 客户拿到的额度与他买的不一致", issued.RemainQuota, cardQuota)
	}

	// 事故二：token 落到 default 租户 ⇒ 额度池闸门找不到池子会按「不限量」放行，
	// 这条流量就变成既不扣池也不计费的免费流量。
	if issued.TenantId != ctx.TenantID {
		t.Errorf("token 落在租户 %q，应属于发卡经销商 %q —— 中转层会按错误的租户找额度池，"+
			"流量既不扣经销商的池也不进任何人的账", issued.TenantId, ctx.TenantID)
	}

	// 兑换出来的匿名账号也必须落在同一个经销商下，否则后续这台设备的用量归错人。
	var endUser repo.User
	if err := repo.WithoutTenantIsolation(ctx.DB).First(&endUser, issued.UserId).Error; err != nil {
		t.Fatalf("token 指向的用户不存在: %v", err)
	}
	if endUser.TenantId != ctx.TenantID {
		t.Errorf("匿名终端用户落在租户 %q，应为 %q —— 这台设备后续的用量会归错经销商",
			endUser.TenantId, ctx.TenantID)
	}

	// token 必须是启用状态，否则客户兑成功了却打不通，客服无从解释。
	if issued.Status != common.TokenStatusEnabled {
		t.Errorf("发出的 token 状态 = %d，不是启用态 —— 客户兑换成功但一用就报错", issued.Status)
	}
}

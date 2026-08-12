package handler

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ============================================================================
// 业务事故防线：一张激活卡的完整生命周期
//
// 发卡是真金白银：经销商 A 花钱向我们买了一批激活码发给他自己的客户。
// 这个文件盯的是「别人误刷/恶意刷 A 的卡」之后卡还能不能用：
//
//   - 事故 A：B 租户的登录用户拿到 A 的卡号（截图、群里转发、扫号）去兑换，
//     兑成功了 ⇒ A 掏钱买的额度进了 B 的客户账上，A 的客户拿着卡兑不了，
//     只能找 A 索赔。
//   - 事故 B（更隐蔽）：兑换被正确拒绝了，但拒绝的那次已经把卡标成「已使用」
//     ⇒ 卡没进任何人账上就作废了。A 的客户拿着一张永远兑不了的卡，客服查
//     系统显示「已使用」，谁都说不清额度去哪了。这种「拒绝时顺手烧卡」只有
//     跑完「拒绝 → 正主再兑一次」这条链才看得出来。
//   - 事故 C：正主兑完之后卡还能再兑一次 ⇒ 一张卡刷出两份额度。
// ============================================================================

// TestRedeemCard_ForeignTenantAttemptRejectedAndCardStaysUsable walks
// 误刷 → 拒绝 → 正主兑换 → 二次兑换 四步，全部走真实的 HTTP 端点
// (POST /api/v2/:tenant/redeem) 和真实的 repo.Redeem 事务。
func TestRedeemCard_ForeignTenantAttemptRejectedAndCardStaysUsable(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// 经销商 A（= ctx.TenantID）花钱买的一张卡。
	card := SeedV2Redemption(t, ctx, ctx.AdminUser.Id)

	// 另一个租户 B 的登录用户 —— 他拿到了 A 的卡号。
	const foreignTenantID = "tenant-foreign-reseller"
	intruder := &repo.User{
		Username:    "foreign-buyer",
		DisplayName: "Foreign Tenant Buyer",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "foreign-buyer@test.local",
		TenantId:    foreignTenantID,
		Group:       "default",
		Quota:       0,
	}
	if err := ctx.DB.Create(intruder).Error; err != nil {
		t.Fatalf("seed foreign-tenant user: %v", err)
	}

	// --- 事故 A：B 的用户兑 A 的卡，必须被拒 ---
	w := V2Request(ctx.Router, http.MethodPost, "/api/v2/test-tenant/redeem",
		map[string]string{"code": card.Key},
		map[string]string{
			"X-Test-Tenant-ID": foreignTenantID,
			"X-Test-User-ID":   strconv.Itoa(intruder.Id),
		})
	if w.Code == http.StatusOK {
		t.Fatalf("跨租户兑换成功了 —— 经销商 A 掏钱买的额度进了 B 的客户账上: body=%s", w.Body.String())
	}
	// 拒绝的理由必须是租户归属，而不是「格式错」「找不到」这类顺带挡住的原因：
	// 后者只要有人改一下校验顺序，越权就重新放开而测试仍然是绿的。
	if !strings.Contains(w.Body.String(), "不属于当前租户") {
		t.Fatalf("被拒了但不是因为租户归属，越权判定可能根本没跑到: status=%d body=%s",
			w.Code, w.Body.String())
	}

	var reloadedIntruder repo.User
	if err := repo.WithoutTenantIsolation(ctx.DB).First(&reloadedIntruder, intruder.Id).Error; err != nil {
		t.Fatalf("reload foreign user: %v", err)
	}
	if reloadedIntruder.Quota != 0 {
		t.Errorf("跨租户兑换被拒但额度还是进账了 %d —— 拒绝只是表面文章", reloadedIntruder.Quota)
	}

	// --- 事故 B：被拒的那一次不许把卡烧掉 ---
	var afterAttempt repo.Redemption
	if err := repo.WithoutTenantIsolation(ctx.DB).Where("`key` = ?", card.Key).First(&afterAttempt).Error; err != nil {
		t.Fatalf("reload card after rejected attempt: %v", err)
	}
	if afterAttempt.Status != common.RedemptionCodeStatusEnabled {
		t.Fatalf("一次被拒的兑换把卡烧了(status=%d) —— A 的客户拿到一张永远兑不了的卡，"+
			"而系统显示「已使用」，额度去向对不上账", afterAttempt.Status)
	}
	if afterAttempt.UsedUserId != 0 || afterAttempt.RedeemedTime != 0 {
		t.Errorf("被拒的兑换在卡上留下了使用痕迹: used_user_id=%d redeemed_time=%d —— 后续对账会把这张卡算成已核销",
			afterAttempt.UsedUserId, afterAttempt.RedeemedTime)
	}

	// --- 正主兑换：必须成功，且额度不多不少正好一份 ---
	ownerQuotaBefore := ctx.NormalUser.Quota
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/redeem",
		map[string]string{"code": card.Key}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("正主兑换自己经销商的卡失败了: status=%d body=%s", w.Code, w.Body.String())
	}
	resp := AssertV2Success(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("redeem 成功但没有 data: %s", w.Body.String())
	}
	if added := int(data["quota_added"].(float64)); added != card.Quota {
		t.Errorf("到账额度与卡面不符: 卡面 %d，实际到账 %d", card.Quota, added)
	}

	var owner repo.User
	if err := repo.WithoutTenantIsolation(ctx.DB).First(&owner, ctx.NormalUser.Id).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if want := ownerQuotaBefore + card.Quota; owner.Quota != want {
		t.Errorf("正主账上额度 = %d，应为 %d（原有 %d + 卡面 %d）",
			owner.Quota, want, ownerQuotaBefore, card.Quota)
	}

	// --- 事故 C：兑过的卡不许再兑第二次 ---
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/redeem",
		map[string]string{"code": card.Key}, nil)
	if w.Code == http.StatusOK {
		t.Fatalf("同一张卡兑了两次 —— 一张卡刷出两份额度: body=%s", w.Body.String())
	}
	if err := repo.WithoutTenantIsolation(ctx.DB).First(&owner, ctx.NormalUser.Id).Error; err != nil {
		t.Fatalf("reload owner after second redeem: %v", err)
	}
	if want := ownerQuotaBefore + card.Quota; owner.Quota != want {
		t.Errorf("第二次兑换虽然报错但额度又加了一遍: %d，应仍为 %d", owner.Quota, want)
	}
}

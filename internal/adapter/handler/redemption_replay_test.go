package handler

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestUpdateRedemption_UsedCodeCannotBeReset guards the redemption-replay
// vulnerability: a tenant admin must not be able to reset an already-used code
// back to a redeemable status and mint unlimited quota via Redeem().
func TestUpdateRedemption_UsedCodeCannotBeReset(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	used := &repo.Redemption{
		UserId:      ctx.AdminUser.Id,
		TenantId:    ctx.TenantID,
		Key:         common.GetRandomString(32),
		Name:        "spent",
		Quota:       500,
		Status:      common.RedemptionCodeStatusUsed,
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(used).Error; err != nil {
		t.Fatalf("seed used redemption: %v", err)
	}

	// Tenant admin tries to flip the used code back to Enabled to replay it.
	body := repo.Redemption{Id: used.Id, Status: common.RedemptionCodeStatusEnabled}
	c, w := v1Ctx(http.MethodPut, "/?status_only=1", body, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateRedemption(c)

	if v1Body(t, w)["success"] != false {
		t.Fatalf("resetting a used code must fail, body=%s", w.Body.String())
	}
	var got repo.Redemption
	ctx.DB.Where("id = ?", used.Id).First(&got)
	if got.Status != common.RedemptionCodeStatusUsed {
		t.Errorf("used code status must remain Used(%d), got %d", common.RedemptionCodeStatusUsed, got.Status)
	}
}

// TestUpdateRedemption_EnabledToDisabledAllowed confirms the guard does not
// break the legitimate enable/disable status transitions on an unused code.
func TestUpdateRedemption_EnabledToDisabledAllowed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := seedV1Redemption(t, ctx, ctx.TenantID) // Enabled
	body := repo.Redemption{Id: r.Id, Status: common.RedemptionCodeStatusDisabled}
	c, w := v1Ctx(http.MethodPut, "/?status_only=1", body, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateRedemption(c)

	if v1Body(t, w)["success"] != true {
		t.Fatalf("disabling an unused code must succeed, body=%s", w.Body.String())
	}
	var got repo.Redemption
	ctx.DB.Where("id = ?", r.Id).First(&got)
	if got.Status != common.RedemptionCodeStatusDisabled {
		t.Errorf("status must be Disabled(%d), got %d", common.RedemptionCodeStatusDisabled, got.Status)
	}
}

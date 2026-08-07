package repo

// cov_repo_channel_tenant_ops_test.go — tenant-scoped channel bulk operations
// (Enable/Disable/Edit/Delete "ByTagAndTenant"). These exist specifically so a
// per-tenant admin's bulk action can never reach across tenant boundaries even
// when two tenants happen to reuse the same tag string — the tests below prove
// that boundary holds for both the channels table AND the (tenant-id-less)
// abilities table, which is scoped indirectly via channel_id.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// repoSeedTaggedChannel creates one channel + one matching ability row for the
// given tenant/tag, returning the channel id.
func repoSeedTaggedChannel(t *testing.T, tenantID, tag string, status int, priority int64, weight uint, abilityEnabled bool) int {
	t.Helper()
	ch := &Channel{
		TenantId:    tenantID,
		Type:        1,
		Key:         "sk-" + common.GetUUID(),
		Status:      status,
		Name:        "chan-" + tenantID + "-" + tag,
		Tag:         common.GetPointer(tag),
		Priority:    common.GetPointer(priority),
		Weight:      common.GetPointer(weight),
		Models:      "gpt-4o",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	ability := &Ability{
		Group:     "default",
		Model:     "gpt-4o",
		ChannelId: ch.Id,
		Enabled:   abilityEnabled,
		Priority:  common.GetPointer(priority),
		Weight:    weight,
		Tag:       common.GetPointer(tag),
	}
	if err := DB.Create(ability).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	return ch.Id
}

func repoChannelStatus(t *testing.T, id int) int {
	t.Helper()
	var ch Channel
	if err := DB.First(&ch, "id = ?", id).Error; err != nil {
		t.Fatalf("load channel %d: %v", id, err)
	}
	return ch.Status
}

func repoAbilityEnabled(t *testing.T, channelID int) bool {
	t.Helper()
	var a Ability
	if err := DB.First(&a, "channel_id = ?", channelID).Error; err != nil {
		t.Fatalf("load ability for channel %d: %v", channelID, err)
	}
	return a.Enabled
}

func TestEnableDisableChannelByTagAndTenant_ScopedToOwnTenant(t *testing.T) {
	SetupTestDB(t)

	tenantA := repoSeedTaggedChannel(t, "tenant-a", "shared", common.ChannelStatusManuallyDisabled, 1, 1, false)
	tenantB := repoSeedTaggedChannel(t, "tenant-b", "shared", common.ChannelStatusManuallyDisabled, 1, 1, false)

	if err := EnableChannelByTagAndTenant("tenant-a", "shared"); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if got := repoChannelStatus(t, tenantA); got != common.ChannelStatusEnabled {
		t.Errorf("tenant-a channel status=%d want enabled(%d)", got, common.ChannelStatusEnabled)
	}
	if !repoAbilityEnabled(t, tenantA) {
		t.Error("tenant-a ability must be enabled after EnableChannelByTagAndTenant")
	}
	// Cross-tenant isolation: tenant-b's same-tag channel must be untouched.
	if got := repoChannelStatus(t, tenantB); got != common.ChannelStatusManuallyDisabled {
		t.Errorf("tenant-b channel leaked: status=%d want still disabled(%d)", got, common.ChannelStatusManuallyDisabled)
	}
	if repoAbilityEnabled(t, tenantB) {
		t.Error("tenant-b ability leaked: must still be disabled")
	}

	// Now disable tenant-a only; tenant-b (never enabled) stays disabled either way,
	// but we flip tenant-b to enabled first so a leak would be observable.
	if err := DB.Model(&Channel{}).Where("id = ?", tenantB).Update("status", common.ChannelStatusEnabled).Error; err != nil {
		t.Fatalf("prep tenant-b enabled: %v", err)
	}
	if err := DB.Model(&Ability{}).Where("channel_id = ?", tenantB).Update("enabled", true).Error; err != nil {
		t.Fatalf("prep tenant-b ability enabled: %v", err)
	}

	if err := DisableChannelByTagAndTenant("tenant-a", "shared"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := repoChannelStatus(t, tenantA); got != common.ChannelStatusManuallyDisabled {
		t.Errorf("tenant-a channel status=%d want disabled(%d)", got, common.ChannelStatusManuallyDisabled)
	}
	if repoAbilityEnabled(t, tenantA) {
		t.Error("tenant-a ability must be disabled after DisableChannelByTagAndTenant")
	}
	if got := repoChannelStatus(t, tenantB); got != common.ChannelStatusEnabled {
		t.Errorf("tenant-b channel leaked on disable: status=%d want still enabled(%d)", got, common.ChannelStatusEnabled)
	}
	if !repoAbilityEnabled(t, tenantB) {
		t.Error("tenant-b ability leaked on disable: must still be enabled")
	}
}

func TestEnableChannelByTagAndTenant_NoMatchIsNoopNotError(t *testing.T) {
	SetupTestDB(t)
	repoSeedTaggedChannel(t, "tenant-a", "real-tag", common.ChannelStatusManuallyDisabled, 1, 1, false)

	// Right tenant, wrong tag.
	if err := EnableChannelByTagAndTenant("tenant-a", "no-such-tag"); err != nil {
		t.Fatalf("want nil error for no-match, got %v", err)
	}
	// Right tag, wrong tenant.
	if err := EnableChannelByTagAndTenant("tenant-x", "real-tag"); err != nil {
		t.Fatalf("want nil error for wrong tenant, got %v", err)
	}
}

func TestEditChannelByTagAndTenant_ScopedUpdateAndAbilitySync(t *testing.T) {
	SetupTestDB(t)

	tenantAID := repoSeedTaggedChannel(t, "tenant-a", "t1", common.ChannelStatusEnabled, 1, 1, true)
	tenantBID := repoSeedTaggedChannel(t, "tenant-b", "t1", common.ChannelStatusEnabled, 1, 1, true)

	newTag := "t2"
	newPriority := int64(5)
	newWeight := uint(9)
	// models/group left nil so the edit takes the "sync abilities in place"
	// branch (UpdateAbilityByChannelIds), not the recreate-abilities branch.
	err := EditChannelByTagAndTenant("tenant-a", "t1", &newTag, nil, nil, nil, &newPriority, &newWeight, nil, nil)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}

	var chA Channel
	if err := DB.First(&chA, "id = ?", tenantAID).Error; err != nil {
		t.Fatalf("load tenant-a channel: %v", err)
	}
	if chA.Tag == nil || *chA.Tag != "t2" {
		t.Errorf("tenant-a channel tag=%v want t2", chA.Tag)
	}
	if chA.Priority == nil || *chA.Priority != 5 {
		t.Errorf("tenant-a channel priority=%v want 5", chA.Priority)
	}
	if chA.Weight == nil || *chA.Weight != 9 {
		t.Errorf("tenant-a channel weight=%v want 9", chA.Weight)
	}

	var abA Ability
	if err := DB.First(&abA, "channel_id = ?", tenantAID).Error; err != nil {
		t.Fatalf("load tenant-a ability: %v", err)
	}
	if abA.Tag == nil || *abA.Tag != "t2" {
		t.Errorf("tenant-a ability tag=%v want t2", abA.Tag)
	}
	if abA.Priority == nil || *abA.Priority != 5 {
		t.Errorf("tenant-a ability priority=%v want 5", abA.Priority)
	}
	if abA.Weight != 9 {
		t.Errorf("tenant-a ability weight=%d want 9", abA.Weight)
	}

	// Tenant-b's identically-tagged channel/ability must be completely untouched.
	var chB Channel
	if err := DB.First(&chB, "id = ?", tenantBID).Error; err != nil {
		t.Fatalf("load tenant-b channel: %v", err)
	}
	if chB.Tag == nil || *chB.Tag != "t1" {
		t.Errorf("tenant-b channel leaked: tag=%v want still t1", chB.Tag)
	}
	if chB.Priority == nil || *chB.Priority != 1 {
		t.Errorf("tenant-b channel leaked: priority=%v want still 1", chB.Priority)
	}

	var abB Ability
	if err := DB.First(&abB, "channel_id = ?", tenantBID).Error; err != nil {
		t.Fatalf("load tenant-b ability: %v", err)
	}
	if abB.Tag == nil || *abB.Tag != "t1" {
		t.Errorf("tenant-b ability leaked: tag=%v want still t1", abB.Tag)
	}
	if abB.Priority == nil || *abB.Priority != 1 {
		t.Errorf("tenant-b ability leaked: priority=%v want still 1", abB.Priority)
	}
}

func TestEditChannelByTagAndTenant_NoMatchIsNoop(t *testing.T) {
	SetupTestDB(t)
	repoSeedTaggedChannel(t, "tenant-a", "t1", common.ChannelStatusEnabled, 1, 1, true)

	newTag := "should-not-apply"
	if err := EditChannelByTagAndTenant("tenant-a", "missing-tag", &newTag, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("want nil error for no-match tag, got %v", err)
	}
	if err := EditChannelByTagAndTenant("tenant-nope", "t1", &newTag, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("want nil error for wrong tenant, got %v", err)
	}
}

func TestDeleteDisabledChannelByTenant_ScopedToOwnTenant(t *testing.T) {
	SetupTestDB(t)

	// tenant-a: one manually-disabled, one auto-disabled, one enabled.
	aDisabled1 := repoSeedTaggedChannel(t, "tenant-a", "d1", common.ChannelStatusManuallyDisabled, 1, 1, false)
	aDisabled2 := repoSeedTaggedChannel(t, "tenant-a", "d2", common.ChannelStatusAutoDisabled, 1, 1, false)
	aEnabled := repoSeedTaggedChannel(t, "tenant-a", "d3", common.ChannelStatusEnabled, 1, 1, true)
	// tenant-b: also disabled, must survive tenant-a's prune.
	bDisabled := repoSeedTaggedChannel(t, "tenant-b", "d1", common.ChannelStatusManuallyDisabled, 1, 1, false)

	deleted, err := DeleteDisabledChannelByTenant("tenant-a")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("want 2 channels deleted, got %d", deleted)
	}

	var remainingA []Channel
	DB.Where("tenant_id = ?", "tenant-a").Find(&remainingA)
	if len(remainingA) != 1 || remainingA[0].Id != aEnabled {
		t.Fatalf("want only the enabled tenant-a channel to survive, got %+v", remainingA)
	}
	for _, id := range []int{aDisabled1, aDisabled2} {
		var cnt int64
		DB.Model(&Channel{}).Where("id = ?", id).Count(&cnt)
		if cnt != 0 {
			t.Errorf("channel %d should have been deleted", id)
		}
	}

	var bCount int64
	DB.Model(&Channel{}).Where("id = ?", bDisabled).Count(&bCount)
	if bCount != 1 {
		t.Fatal("tenant-b's disabled channel must survive tenant-a's prune (cross-tenant isolation)")
	}
}

func TestUpdateAbilityByChannelIds_EmptyIDsIsNoop(t *testing.T) {
	SetupTestDB(t)
	newTag := "x"
	if err := UpdateAbilityByChannelIds(nil, &newTag, nil, nil); err != nil {
		t.Fatalf("want nil error for empty ids, got %v", err)
	}
	if err := UpdateAbilityByChannelIds([]int{}, &newTag, nil, nil); err != nil {
		t.Fatalf("want nil error for empty (non-nil) ids slice, got %v", err)
	}
}

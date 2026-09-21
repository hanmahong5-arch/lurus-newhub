package app

import (
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// TestPreConsumeTokenQuota_WireMessageIsASCII_UnderEveryDisplayType is the
// boundary oracle the cycle-13 language gate could not be.
//
// What happened: that gate scans string literals under middleware/repo/relay
// and was green all cycle, and the cycle claimed "the 402 for an exhausted key
// is ASCII on all three wires". A live UAT probe on 2026-09-21 then pulled
// this off the wire from a real relay call with a nearly-empty key:
//
//	"token quota is not enough, token remain quota: ＄0.000002, need quota: ＄0.000136"
//
// The literal in quota.go IS ASCII; the amounts were interpolated by
// logger.FormatQuota, which renders a fullwidth ＄ (U+FF04) on the default
// display type and ¥ under CNY. The middleware 402 the gate's sibling test
// covers is a different message on a different path, so both stayed green.
//
// This test drives the message through the same call the relay makes and
// asserts the whole string is ASCII under every display type an operator can
// configure — including the two that are NOT the default, because "the wire
// does not move with the console's currency" is the actual property.
//
// Mutation that must turn this red: put logger.FormatQuota back in
// PreConsumeTokenQuota's rejection message (quota.go).
func TestPreConsumeTokenQuota_WireMessageIsASCII_UnderEveryDisplayType(t *testing.T) {
	db := setupServiceTestDB(t)

	userID := seedTestUser(t, db, 1_000_000)
	tokenID := seedTenantToken(t, db, userID, "t-ascii-402")
	// A key that cannot pay for the request: remain_quota 2, ask for 500.
	if err := db.Model(&repo.Token{}).Where("id = ?", tokenID).
		Updates(map[string]interface{}{"remain_quota": 2, "unlimited_quota": false}).Error; err != nil {
		t.Fatalf("shrink the token: %v", err)
	}
	tok, err := repo.GetTokenById(tokenID)
	if err != nil || tok == nil {
		t.Fatalf("read seeded token: %v", err)
	}

	gs := operation_setting.GetGeneralSetting()
	original := gs.QuotaDisplayType
	t.Cleanup(func() { gs.QuotaDisplayType = original })

	// "" is the default (USD) arm — the one production runs and the one the
	// live probe caught.
	for _, displayType := range []string{"", operation_setting.QuotaDisplayTypeCNY, operation_setting.QuotaDisplayTypeTokens} {
		label := displayType
		if label == "" {
			label = "default_usd"
		}
		t.Run(label, func(t *testing.T) {
			gs.QuotaDisplayType = displayType

			relayInfo := &relaycommon.RelayInfo{
				UserId:   userID,
				TokenId:  tokenID,
				TokenKey: tok.Key,
			}
			err := PreConsumeTokenQuota(relayInfo, 500)
			if err == nil {
				t.Fatal("a 2-quota key was asked for 500 and PreConsumeTokenQuota allowed it")
			}
			msg := err.Error()
			for i, r := range msg {
				if r > 127 {
					t.Fatalf("the 402 wire message carries a non-ASCII rune %q at index %d under display type %q: %s",
						r, i, displayType, msg)
				}
			}
			// The message must still say the two numbers — an ASCII check
			// alone would pass for a message that dropped them.
			if !strings.Contains(msg, "token remain quota:") || !strings.Contains(msg, "need quota:") {
				t.Fatalf("the message lost its figures: %s", msg)
			}
		})
	}
}

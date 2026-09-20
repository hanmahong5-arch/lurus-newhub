package app

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// TestPostConsumeQuota_NoKeyMeansNoKeyLeg pins the TokenId > 0 guard on the
// per-key leg of PostConsumeQuota (Phase 3): a caller that has no key to
// charge — handler/task_video.go's re-settlement with an unresolved payer
// passes TokenId 0 — must not reach DecreaseTokenQuota(0, "", …), whose
// Redis arm would key a cache entry on the empty string and whose batch arm
// would queue a delta for id 0.
//
// Mutation that must turn this red: drop `&& relayInfo.TokenId > 0` from the
// Phase 3 guard in quota.go — the seam then records a call with id 0.
func TestPostConsumeQuota_NoKeyMeansNoKeyLeg(t *testing.T) {
	_ = setupServiceTestDB(t)

	var calls []int
	prevSeam := decreaseTokenQuotaSeam
	decreaseTokenQuotaSeam = func(id int, key string, quota int) error {
		calls = append(calls, id)
		return nil
	}
	t.Cleanup(func() { decreaseTokenQuotaSeam = prevSeam })

	// UserId 0 keeps the user ledger out of the picture (userLedger=false)
	// and TokenId 0 keeps the pool leg out, so the key leg is the only thing
	// this call could move.
	relayInfo := &relaycommon.RelayInfo{UserId: 0, TokenId: 0, TokenKey: ""}
	if err := PostConsumeQuota(relayInfo, 25, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota with no key: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("the per-key debit ran for token id(s) %v; with TokenId 0 there is no key ledger to move", calls)
	}

	// Positive control: with a key id the leg runs — so a green above is
	// not the seam being bypassed altogether.
	if err := PostConsumeQuota(&relaycommon.RelayInfo{UserId: 0, TokenId: 7, TokenKey: "sk-guard"}, 25, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota with a key: %v", err)
	}
	if len(calls) != 1 || calls[0] != 7 {
		t.Fatalf("expected exactly one per-key debit for token 7, got %v", calls)
	}
}

package governance

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// The CNY value of a row's quota is frozen at the rate current when the row
// is recorded (logs.priced_cny4, migration 042), through this hook — the one
// path every RecordConsumeLog call site passes — and from params.Quota
// alone, so credit-pool and local-quota rows (no wallet charge on RelayInfo)
// get it too. Exact amount: QuotaPerUnit=500_000, 73_000 quota = $0.146,
// at 7.3 = CNY 1.0658 = 10_658 units.
func TestEnrichLogParams_FreezesThePricedCNY(t *testing.T) {
	prevRate := operation_setting.USDExchangeRate
	t.Cleanup(func() { operation_setting.USDExchangeRate = prevRate })
	operation_setting.USDExchangeRate = 7.3

	c := newTestContext()
	info := &relaycommon.RelayInfo{StartTime: time.Now()}
	params := &entity.RecordConsumeLogParams{Quota: 73_000, Other: make(map[string]interface{})}
	EnrichLogParams(c, info, params)
	if params.PricedCNY4 != 10_658 {
		t.Errorf("PricedCNY4 = %d, want 10658", params.PricedCNY4)
	}

	// A later rate edit does not touch what was already stamped.
	operation_setting.USDExchangeRate = 6.5
	if params.PricedCNY4 != 10_658 {
		t.Errorf("PricedCNY4 after rate edit = %d, want 10658", params.PricedCNY4)
	}

	// Refund / zero rows carry no price: 0 keeps meaning "nothing to price",
	// the same 0 a pre-042 row has, so readers need only one fallback rule.
	zero := &entity.RecordConsumeLogParams{Quota: 0, Other: make(map[string]interface{})}
	EnrichLogParams(c, info, zero)
	if zero.PricedCNY4 != 0 {
		t.Errorf("PricedCNY4 for quota 0 = %d, want 0", zero.PricedCNY4)
	}
}

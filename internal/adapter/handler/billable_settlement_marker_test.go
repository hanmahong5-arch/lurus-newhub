package handler

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
)

// TestBillablePredicate_ExcludesRowFlaggedByTheRealSettlementWriter pins the
// second half of the billing exclusion (cycle 13 L2): the settlement-failed
// marker is written by app.FlagSettlementOutcome and read by
// repo.BillableConsumePredicate, and the two must agree byte for byte. The
// channel-test marker already has this round trip in
// TestProbeChannel_RowIsUnbilled; this test gives the settlement marker the
// same real-writer oracle instead of a hand-typed JSON fragment.
//
// Mutation that must go red: change the key or value FlagSettlementOutcome
// writes (or the substring the predicate matches) so the two drift apart.
func TestBillablePredicate_ExcludesRowFlaggedByTheRealSettlementWriter(t *testing.T) {
	setupContextTierChannelTestDB(t) // seeds user id 1 + the Log table

	other := map[string]interface{}{"model_ratio": 1.0}
	app.FlagSettlementOutcome(other, errors.New("settlement failed on purpose"))

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	repo.RecordConsumeLog(c, 1, repo.RecordConsumeLogParams{
		ChannelId:        1,
		PromptTokens:     10,
		CompletionTokens: 5,
		ModelName:        "billing-settlement-marker-model",
		TokenName:        "billing-settlement-marker-token",
		Quota:            500,
		Content:          "settlement marker round trip",
		Other:            other,
	})

	var total int64
	if err := repo.LOG_DB.Model(&repo.Log{}).Where("user_id = ? AND model_name = ?", 1, "billing-settlement-marker-model").Count(&total).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if total != 1 {
		t.Fatalf("consume rows written = %d, want exactly 1 (the fixture must record consume logs for this oracle to mean anything)", total)
	}

	var billable int64
	if err := repo.BillableConsumePredicate(repo.LOG_DB.Model(&repo.Log{})).
		Where("user_id = ? AND model_name = ?", 1, "billing-settlement-marker-model").
		Count(&billable).Error; err != nil {
		t.Fatalf("count billable rows: %v", err)
	}
	if billable != 0 {
		t.Fatalf("billable rows = %d, want 0: the row flagged by app.FlagSettlementOutcome must be excluded by repo.BillableConsumePredicate", billable)
	}
}

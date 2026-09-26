package repo

import (
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// TestRecordConsumeLog_PersistsTheWalletCharge: the recorded charge must
// reach the row through the real writer, not only the params struct.
func TestRecordConsumeLog_PersistsTheWalletCharge(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	prevConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = prevConsume })
	user := seedUser(t, "charged-cny4", "charged-cny4@test.local", common.RoleCommonUser, common.UserStatusEnabled, "")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	RecordConsumeLog(c, user.Id, RecordConsumeLogParams{
		ModelName: "m", Quota: 90_000, ChargedCNY4: 12_345,
		Other: map[string]interface{}{},
	})

	var row Log
	if err := LOG_DB.Where("user_id = ? AND type = ?", user.Id, LogTypeConsume).First(&row).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if row.ChargedCNY4 != 12_345 {
		t.Fatalf("charged_cny4 = %d, want 12345", row.ChargedCNY4)
	}
}

package repo

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func keyIdxTestCtx(channelID int, multi bool, idx int) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyChannelId, channelID)
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, multi)
	if multi {
		common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, idx)
	}
	return c
}

func loadOnlyLog(t *testing.T, logType int) Log {
	t.Helper()
	var lg Log
	if err := LOG_DB.Where("type = ?", logType).Order("id desc").First(&lg).Error; err != nil {
		t.Fatalf("log not written: %v", err)
	}
	return lg
}

func TestChannelKeyIdx_ConsumeRow(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	prev := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	defer func() { common.LogConsumeEnabled = prev }()
	u := seedUser(t, "ki-con", "ki-con@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")

	cases := []struct {
		name    string
		multi   bool
		idx     int
		channel int
		want    int64
	}{
		{"single key channel", false, 0, 7, -1},
		{"multi key, key 0 survives GORM zero-value default", true, 0, 7, 0},
		{"multi key, key 3", true, 3, 7, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := keyIdxTestCtx(tc.channel, tc.multi, tc.idx)
			RecordConsumeLog(c, u.Id, RecordConsumeLogParams{ModelName: "model-a", Quota: 5, ChannelId: tc.channel})
			lg := loadOnlyLog(t, LogTypeConsume)
			if lg.ChannelKeyIdx == nil || *lg.ChannelKeyIdx != tc.want {
				t.Fatalf("channel_key_idx = %v, want %d", lg.ChannelKeyIdx, tc.want)
			}
		})
	}
}

func TestChannelKeyIdx_ErrorRow(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	u := seedUser(t, "ki-err", "ki-err@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")

	// Failed attempt on key 2 of channel 9: recorded.
	RecordErrorLog(keyIdxTestCtx(9, true, 2), u.Id, 9, "model-a", "k", "upstream 500", 0, 1, false, "default", nil)
	if lg := loadOnlyLog(t, LogTypeError); lg.ChannelKeyIdx == nil || *lg.ChannelKeyIdx != 2 {
		t.Fatalf("error row channel_key_idx = %v, want 2", lg.ChannelKeyIdx)
	}

	// Failed attempt on key 0: still 0, not the -1 default.
	RecordErrorLog(keyIdxTestCtx(9, true, 0), u.Id, 9, "model-a", "k", "upstream 500", 0, 1, false, "default", nil)
	if lg := loadOnlyLog(t, LogTypeError); lg.ChannelKeyIdx == nil || *lg.ChannelKeyIdx != 0 {
		t.Fatalf("error row channel_key_idx = %v, want 0", lg.ChannelKeyIdx)
	}

	// Context already moved on to another channel: a row about channel 9 must
	// not inherit that channel's key index.
	RecordErrorLog(keyIdxTestCtx(10, true, 4), u.Id, 9, "model-a", "k", "upstream 500", 0, 1, false, "default", nil)
	if lg := loadOnlyLog(t, LogTypeError); lg.ChannelKeyIdx == nil || *lg.ChannelKeyIdx != -1 {
		t.Fatalf("mismatched-channel error row channel_key_idx = %v, want -1", lg.ChannelKeyIdx)
	}
}

func TestChannelKeyIdx_UnstampedRowKeepsDefault(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	// A writer that never learned about keys (nil pointer) gets the DB default.
	if err := LOG_DB.Create(&Log{UserId: 1, Type: LogTypeConsume, CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatal(err)
	}
	var idx int64
	if err := LOG_DB.Raw("SELECT channel_key_idx FROM logs LIMIT 1").Scan(&idx).Error; err != nil {
		t.Fatal(err)
	}
	if idx != -1 {
		t.Fatalf("default channel_key_idx = %d, want -1", idx)
	}
}

func seedUsageLog(t *testing.T, ch int, key int64, typ int, created int64, prompt, completion, quota int, priced int64) {
	t.Helper()
	k := key
	row := &Log{UserId: 1, Type: typ, CreatedAt: created, ChannelId: ch, ChannelKeyIdx: &k,
		PromptTokens: prompt, CompletionTokens: completion, Quota: quota, PricedCNY4: priced, ModelName: "model-a"}
	if err := LOG_DB.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestAggregateChannelUsage(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	now := common.GetTimestamp()
	// channel 1: key 0 two consumes + one error, key 1 one consume, plus an
	// old row outside the window.
	seedUsageLog(t, 1, 0, LogTypeConsume, now-10, 100, 50, 10, 300)
	seedUsageLog(t, 1, 0, LogTypeConsume, now-20, 10, 5, 1, 30)
	seedUsageLog(t, 1, 0, LogTypeError, now-30, 0, 0, 0, 0)
	seedUsageLog(t, 1, 1, LogTypeConsume, now-40, 7, 3, 2, 60)
	seedUsageLog(t, 1, 1, LogTypeConsume, now-100000, 999, 999, 999, 999)
	// channel 2 is a different channel; channel 3 not requested.
	seedUsageLog(t, 2, -1, LogTypeConsume, now-10, 1, 1, 1, 5)
	seedUsageLog(t, 3, -1, LogTypeConsume, now-10, 1, 1, 1, 5)
	// a topup row on channel 1 must not count.
	seedUsageLog(t, 1, 0, LogTypeTopup, now-10, 1000, 1000, 1000, 1000)

	rows, err := AggregateChannelUsage([]int{1, 2}, now-3600, true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[[2]int64]ChannelUsageRow{}
	for _, r := range rows {
		got[[2]int64{int64(r.ChannelId), r.KeyIdx}] = r
	}
	k0 := got[[2]int64{1, 0}]
	if k0.Requests != 3 || k0.Errors != 1 || k0.PromptTokens != 110 || k0.CompletionTokens != 55 || k0.Quota != 11 || k0.CostCNY4 != 330 {
		t.Errorf("channel 1 key 0 = %+v", k0)
	}
	k1 := got[[2]int64{1, 1}]
	if k1.Requests != 1 || k1.Errors != 0 || k1.CostCNY4 != 60 {
		t.Errorf("channel 1 key 1 = %+v (old row must be excluded)", k1)
	}
	if _, ok := got[[2]int64{3, -1}]; ok {
		t.Error("channel 3 was not requested but appears")
	}

	// Per-channel variant collapses keys.
	rows, err = AggregateChannelUsage([]int{1}, now-3600, false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows[0].Requests != 4 || rows[0].Errors != 1 || rows[0].CostCNY4 != 390 || rows[0].KeyIdx != -1 {
		t.Errorf("channel 1 total = %+v", rows[0])
	}

	// Empty id list is a no-op, not an "IN ()" syntax error.
	if rows, err := AggregateChannelUsage(nil, now-3600, false); err != nil || rows != nil {
		t.Errorf("empty ids: rows=%v err=%v", rows, err)
	}
}

func TestAggregateChannelUsage_UnpricedRowsFallBackToQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	now := common.GetTimestamp()
	seedUsageLog(t, 5, -1, LogTypeConsume, now-10, 1, 1, 500000, 0) // priced=0 (pre-042 row)
	rows, err := AggregateChannelUsage([]int{5}, now-3600, false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows[0].CostCNY4 <= 0 {
		t.Errorf("unpriced row must be priced from quota, got cost %d", rows[0].CostCNY4)
	}
}

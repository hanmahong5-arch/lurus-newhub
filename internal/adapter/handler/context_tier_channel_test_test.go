/*
Copyright (C) 2025 LurusTech

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var contextTierChannelTestDBCounter int

// setupContextTierChannelTestDB is a hermetic SQLite harness for testChannel
// (channel-test.go), the unexported billing path the legacy GET
// /api/channel/test/:id route (handler.TestChannel) drives — distinct from
// the V2 TestChannelV2 route, which does not call helper.ModelPriceHelper or
// repo.RecordConsumeLog at all. testChannel hardcodes user id 1
// (repo.GetUserCache(1)), so that id must be seeded.
func setupContextTierChannelTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	contextTierChannelTestDBCounter++
	dsn := fmt.Sprintf("file:ctxtierchanneltest%d?mode=memory&cache=shared", contextTierChannelTestDBCounter)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Channel{}, &repo.Log{}, &repo.Option{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	prevLogEnabled := common.LogConsumeEnabled
	t.Cleanup(func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		common.LogConsumeEnabled = prevLogEnabled
	})
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.LogConsumeEnabled = true

	if err := db.Create(&repo.User{Id: 1, Username: "ctx-tier-channel-test-user", Group: "default", Status: common.UserStatusEnabled}).Error; err != nil {
		t.Fatalf("seed user 1: %v", err)
	}
}

// TestChannelTest_ResettlesContextTierAgainstActualPromptTokens drives the
// real testChannel function (channel-test.go, the legacy GET
// /api/channel/test/:id route's billing path) through a tiered model and
// asserts the consume-log row it writes carries the SETTLED tier's ratio,
// not the pre-consume estimate's. helper.ModelPriceHelper is called there
// with promptTokens=0 (line ~228), so an unresettled write always lands on
// the base tier — cycle-8 plan §8 L5 A-F4/A-F5, "it writes a real
// consume-log row from a stale price".
func TestChannelTest_ResettlesContextTierAgainstActualPromptTokens(t *testing.T) {
	setupContextTierChannelTestDB(t)
	allowLoopbackEgress(t)
	app.InitHttpClient()

	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	prevRatio := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(prevRatio) })
	const model = "ctx-tier-channel-test-model"
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"` + model + `":1.0}`); err != nil {
		t.Fatalf("seed model ratio: %v", err)
	}
	// Above the 3000-token threshold, the tier's model_ratio is 2.0; the
	// promptTokens=0 pre-consume estimate (channel-test.go:228) always picks
	// the base (0-threshold) tier, ratio 1.0 — an unresettled write bills at
	// 1.0 regardless of the actual usage.
	if err := ratio_setting.UpdateContextLengthTiersByJSONString(
		`{"` + model + `":[{"threshold_tokens":0,"model_ratio":1.0},{"threshold_tokens":3000,"model_ratio":2.0}]}`,
	); err != nil {
		t.Fatalf("seed context tiers: %v", err)
	}

	// Upstream reports 4000 prompt tokens — above the threshold.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":4000,"completion_tokens":1,"total_tokens":4001}}`))
	}))
	defer upstream.Close()

	channel := &repo.Channel{
		Type:    1, // OpenAI
		Status:  common.ChannelStatusEnabled,
		Name:    "ctx-tier-channel-test-channel",
		Key:     "sk-ctx-tier-channel-test",
		Models:  model,
		Group:   "default",
		BaseURL: func() *string { u := upstream.URL; return &u }(),
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	result := testChannel(channel, model, "")
	if result.localErr != nil {
		t.Fatalf("testChannel localErr: %v", result.localErr)
	}
	if result.newAPIError != nil {
		t.Fatalf("testChannel newAPIError: %v", result.newAPIError.Error())
	}

	var logRow repo.Log
	if err := repo.DB.Where("channel_id = ?", channel.Id).Order("id desc").First(&logRow).Error; err != nil {
		t.Fatalf("query consume log row: %v", err)
	}
	var other map[string]interface{}
	if err := json.Unmarshal([]byte(logRow.Other), &other); err != nil {
		t.Fatalf("parse log row other JSON: %v — raw: %s", err, logRow.Other)
	}
	if mr, _ := other["model_ratio"].(float64); mr != 2.0 {
		t.Errorf("log row other.model_ratio = %v, want 2.0 (resettled against the actual 4000 prompt tokens, not the promptTokens=0 pre-consume estimate's base tier)", other["model_ratio"])
	}
	// channel-test.go's !UsePrice formula: quota = round(promptTokens +
	// completionTokens*completionRatio) * modelRatio. At the unresettled base
	// tier (ratio 1.0) this would be ~4001; at the resettled tier (2.0) it is
	// roughly double — the >4500 threshold cleanly distinguishes the two.
	if logRow.Quota <= 4500 {
		t.Errorf("log row quota = %d, want > 4500 (billed at the resettled tier, not the base tier)", logRow.Quota)
	}
}

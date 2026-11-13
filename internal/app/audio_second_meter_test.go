package app

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestPostAudioConsumeQuota_AudioSecondSettlement(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 100_000_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000_000, false)

	prevAudio := ratio_setting.AudioSecondPrice2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateAudioSecondPriceByJSONString(prevAudio) })
	model := "tts-audio-sec"
	if err := ratio_setting.UpdateAudioSecondPriceByJSONString(`{"` + model + `":0.002}`); err != nil {
		t.Fatal(err)
	}

	hookPostConsumeQuotaFn(t, func(*relaycommon.RelayInfo, int, int, bool) error {
		return nil
	})

	c := createTestGinContext()
	c.Set("token_name", "audio-sec")
	usage := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 33,
		TotalTokens:      43,
		PromptTokensDetails: dto.InputTokenDetails{
			TextTokens: 10,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			AudioTokens: 33,
		},
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:                userId,
		TokenId:               tokenId,
		TokenKey:              key,
		OriginModelName:       model,
		RelayMode:             relayconstant.RelayModeAudioSpeech,
		AudioDurationSeconds:  2.1, // ceil → 3
		FinalPreConsumedQuota: 0,
		StartTime:             time.Now(),
		ChannelMeta:           &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	PostAudioConsumeQuota(c, relayInfo, usage, "")

	var row repo.Log
	if err := db.Where("user_id = ? AND model_name = ?", userId, model).
		Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("load log: %v", err)
	}
	if row.UsageUnit != "audio_second" || row.UsageQuantity != 3 {
		t.Fatalf("metering = %q/%d, want audio_second/3", row.UsageUnit, row.UsageQuantity)
	}
	wantQuota := int(0.002 * 3 * common.QuotaPerUnit)
	if row.Quota != wantQuota {
		t.Fatalf("quota = %d, want %d", row.Quota, wantQuota)
	}
	if row.UsageSource != "upstream" {
		t.Fatalf("usage_source = %q, want upstream", row.UsageSource)
	}
}

func TestPostAudioConsumeQuota_AudioSecondAbsentKeepsTokenPath(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 100_000_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000_000, false)

	prevAudio := ratio_setting.AudioSecondPrice2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateAudioSecondPriceByJSONString(prevAudio) })
	_ = ratio_setting.UpdateAudioSecondPriceByJSONString(`{}`)

	hookPostConsumeQuotaFn(t, func(*relaycommon.RelayInfo, int, int, bool) error {
		return nil
	})

	c := createTestGinContext()
	c.Set("token_name", "audio-token")
	const model = "tts-token-path"
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 40,
		TotalTokens:      140,
		PromptTokensDetails: dto.InputTokenDetails{
			TextTokens:  60,
			AudioTokens: 40,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			TextTokens:  30,
			AudioTokens: 10,
		},
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 1.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	PostAudioConsumeQuota(c, relayInfo, usage, "")

	var row repo.Log
	if err := db.Where("user_id = ? AND model_name = ?", userId, model).
		Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("load log: %v", err)
	}
	if row.UsageUnit != "token" {
		t.Fatalf("usage_unit = %q, want token when AudioSecondPrice absent", row.UsageUnit)
	}
}

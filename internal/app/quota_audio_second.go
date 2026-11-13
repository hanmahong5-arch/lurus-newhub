package app

import (
	"fmt"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// audioSecondSettle is the TokenHub 0.10 audio-second metering branch of
// PostAudioConsumeQuota. When AudioSecondPrice is configured for the model,
// quota is price × ceil(seconds) × group × QuotaPerUnit and the log row uses
// usage_unit=audio_second. Absent price leaves the caller on the token path.
type audioSecondSettle struct {
	active      bool
	quota       int
	modelPrice  float64
	usePrice    bool
	usageUnit   string
	usageQty    int64
	usageSource string
	logContent  string
}

func settleAudioSecond(
	ctx *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	legacyAudioTokens int,
	groupRatio float64,
) audioSecondSettle {
	asp, ok := helper.AudioSecondPriced(relayInfo)
	if !ok {
		return audioSecondSettle{}
	}
	out := audioSecondSettle{
		active:    true,
		usageUnit: "audio_second",
		usePrice:  true,
	}
	secs, estimated, haveSecs := helper.AudioBillableSeconds(relayInfo, legacyAudioTokens)
	if !haveSecs {
		out.usageSource = "unreported"
		logger.LogError(ctx, fmt.Sprintf(
			"audio_second priced but duration unknown, userId %d, model %s",
			relayInfo.UserId, relayInfo.OriginModelName))
		out.logContent = fmt.Sprintf("音频秒计价未计量（时长未知），分组倍率 %.2f", groupRatio)
		return out
	}
	out.usageQty = secs
	if estimated {
		out.usageSource = "estimated"
	} else {
		out.usageSource = "upstream"
	}
	out.quota = int(decimal.NewFromFloat(asp).
		Mul(decimal.NewFromInt(secs)).
		Mul(decimal.NewFromFloat(groupRatio)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Round(0).IntPart())
	out.modelPrice = asp * float64(secs)
	out.logContent = fmt.Sprintf("音频秒计价 %.6f × %d 秒，分组倍率 %.2f", asp, secs, groupRatio)
	return out
}

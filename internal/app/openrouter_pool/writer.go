package openrouter_pool

import (
	"fmt"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// markMultiKeyCooldownFn is the repo seam for the multi-key write, so the
// guards and routing here can be tested without a database.
// The second result is the channel-level recovery deadline when the write left
// no usable key (see repo.MarkMultiKeyCooldownReport), else 0.
var markMultiKeyCooldownFn = func(id int, key string, until int64, reason string) (bool, int64) {
	return repo.MarkMultiKeyCooldownReport(id, key, until, reason, false)
}

// markMultiKeyCooldownKeepFn is the same write for auto-ban-off channels: the
// key is parked but the channel status is left alone.
var markMultiKeyCooldownKeepFn = func(id int, key string, until int64, reason string) (bool, int64) {
	return repo.MarkMultiKeyCooldownReport(id, key, until, reason, true)
}

// channelIsMultiKeyFn resolves the channel's real key mode. It falls back to
// the ChannelError flag when the channel cannot be loaded (no database, or the
// channel vanished), so hermetic callers keep their declared mode.
var channelIsMultiKeyFn = func(ce types.ChannelError) bool {
	if repo.DB == nil && !common.MemoryCacheEnabled {
		return ce.IsMultiKey
	}
	ch, err := repo.CacheGetChannel(ce.ChannelId)
	if err != nil || ch == nil {
		return ce.IsMultiKey
	}
	return ch.ChannelInfo.IsMultiKey
}

// MaybeMarkCooldown is the relay-side hook for rate-limited keys. Called from
// processChannelError; it applies to every channel type (the package name is
// historical) and is a no-op unless the upstream returned 429.
//
// Balance-exhausted errors (anything app.ShouldDisableChannel flags, e.g. an
// insufficient_quota body on a 429) are NOT rate limits: they go down the
// existing disable path and never get a cooldown, which would re-enable a key
// that is out of money after 30s.
//
//   - multi-key channel: the used key is disabled with a deadline; the reaper
//     re-enables it.
//   - single-key channel: the channel is put on a cooldown (Redis chan_cd:{id}:0,
//     process-local fallback) that selection skips until the deadline passes.
//
// All other error paths (401/billing/etc.) keep their existing AutoBan behavior.
func MaybeMarkCooldown(channelErr types.ChannelError, apiErr *types.NewAPIError) {
	if apiErr == nil {
		return
	}
	if apiErr.StatusCode != 429 {
		return
	}
	// A 429 caused by the request itself (too many tokens for the model) is
	// not a supply-side limit: cooling the channel would starve healthy traffic.
	if app.IsRequestCaused429(apiErr) {
		return
	}
	if isBalanceExhausted(apiErr) || app.ShouldDisableChannel(channelErr.ChannelType, apiErr) {
		return
	}
	// channelErr.IsMultiKey is not trustworthy on the first attempt (the
	// request has not recorded the channel's key mode yet), so the real mode
	// comes from the channel cache.
	isMulti := channelIsMultiKeyFn(channelErr)
	if isMulti && channelErr.UsingKey == "" {
		return
	}

	// The body heuristics (24h "per day" keywords, embedded reset metadata) are
	// OpenRouter free-tier conventions; for any other upstream only the headers
	// are trusted, so an unrelated 429 text cannot park a channel for a day.
	var body []byte
	if channelErr.ChannelType == constant.ChannelTypeOpenRouter {
		body = []byte(apiErr.UpstreamBodyHint)
	}
	until := ParseCooldownUntil(apiErr.UpstreamHeader, body, time.Now(), channelErr.ChannelType)
	if until <= 0 {
		return
	}
	if !isMulti {
		app.MarkChannelCooldown(channelErr.ChannelId, 0, until)
		common.SysLog("channel cooldown: marked channel " +
			strconv.Itoa(channelErr.ChannelId) +
			" until " + time.Unix(until, 0).UTC().Format(time.RFC3339))
		return
	}
	// OpenRouter keeps its historical behaviour (the last parked key flips the
	// channel to auto-disabled whatever auto_ban says). Other channel types
	// the operator excluded from auto-ban keep their status.
	mark := markMultiKeyCooldownFn
	if channelErr.ChannelType != constant.ChannelTypeOpenRouter && !channelErr.AutoBan {
		mark = markMultiKeyCooldownKeepFn
	}
	ok, allParkedUntil := mark(channelErr.ChannelId, channelErr.UsingKey, until, "rate limit (auto cooldown)")
	if !ok {
		return
	}
	if allParkedUntil > 0 {
		// Every key is parked. Record it as a channel-level cooldown so
		// selection skips the channel (its status may be untouched, and the
		// cached routing index lags) and fails over to a healthy one.
		app.MarkChannelCooldown(channelErr.ChannelId, 0, allParkedUntil)
	}
	common.SysLog("channel pool: marked key cooldown on channel " +
		strconv.Itoa(channelErr.ChannelId) +
		" until " + time.Unix(until, 0).UTC().Format(time.RFC3339))
}

// isBalanceExhausted recognises out-of-money 429s by their own code/type, so
// the decision does not depend on the automatic-disable switch that
// app.ShouldDisableChannel honours: with the switch off such an error must
// still never be written as a short cooldown.
func isBalanceExhausted(apiErr *types.NewAPIError) bool {
	oaiErr := apiErr.ToOpenAIError()
	for _, v := range []string{oaiErr.Type, fmt.Sprint(oaiErr.Code)} {
		switch v {
		case "insufficient_quota", "insufficient_user_quota", "token_quota_exhausted", "billing_not_active", "Arrearage":
			return true
		}
	}
	return false
}

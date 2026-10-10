package repo

// option_parse.go - the typed option-value parsers and the retired-key
// warning, moved verbatim out of option.go (pure move, cycle 22) so that
// file stays under its source-size ceiling.

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// optionInt parses an integer option value, and on failure keeps previous,
// reports the key and returns an error for the caller to propagate. Zeroing a
// setting because its stored string did not parse is how a blank admin field
// used to switch a feature off silently; see metrics.OptionParseRejectedTotal.
func optionInt(key, value string, previous int) (int, error) {
	parsed, parseErr := strconv.Atoi(value)
	if parseErr != nil {
		reportOptionParseFailure(key, optionKindInteger)
		return previous, optionKindError(key, optionKindInteger)
	}
	return parsed, nil
}

// optionFloat is optionInt for float options (the money-adjacent ones:
// QuotaPerUnit, Price, USDExchangeRate, ChannelDisableThreshold,
// ModelFallbackMarkup).
func optionFloat(key, value string, previous float64) (float64, error) {
	parsed, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil {
		reportOptionParseFailure(key, optionKindNumber)
		return previous, optionKindError(key, optionKindNumber)
	}
	return parsed, nil
}

// reportOptionParseFailure counts one rejected value and logs the key together
// with the type the value had to be. It is handed a kind, not the parse error,
// because strconv's and encoding/json's messages quote the input they were
// given and this dispatch carries the SMTP password and the OAuth client
// secret.
func reportOptionParseFailure(key string, kind optionValueKind) {
	metrics.RecordOptionParseRejected(key)
	common.SysError(fmt.Sprintf("option %s rejected: value is not a valid %s; the previous value is kept", key, kind))
}

// reportOptionRangeFailure is reportOptionParseFailure's counterpart for a
// value that parsed but landed outside positiveRangeOptionKinds' range. It
// reuses the same rejected-option counter: both are "an admin-submitted
// value for this key did not reach the table", just for a different reason.
// internal/pkg/metrics is not owned by this cycle's L2 lane (cycle13 §2), so
// this does not add a new series.
func reportOptionRangeFailure(key string) {
	metrics.RecordOptionParseRejected(key)
	common.SysError(fmt.Sprintf("option %s rejected: value must be greater than 0 and less than %g; the previous value is kept", key, optionPositiveRangeMax))
}

// retiredOptionKeys maps a hierarchical key that must no longer be written to
// the canonical key that replaced it. The two group-ratio entries reached the
// ratio maps through the config manager's reflect writer, bypassing both
// ratio_setting's mutexes and CheckGroupRatio's validation; see the comment on
// ratio_setting.GroupRatioSetting.
var retiredOptionKeys = map[string]string{
	"group_ratio_setting.group_ratio":       "GroupRatio",
	"group_ratio_setting.group_group_ratio": "GroupGroupRatio",
}

// retiredOptionWarned remembers the retired keys this process has already
// complained about.
var retiredOptionWarned sync.Map

// warnRetiredOptionOnce logs a retired key the first time this process meets
// it and stays silent afterwards. A row an operator has not deleted is read by
// every replica on every SyncOptions tick, so the alternative is a line per
// key per tick per replica for as long as the row exists.
func warnRetiredOptionOnce(key, canonical string) {
	if _, alreadyWarned := retiredOptionWarned.LoadOrStore(key, struct{}{}); alreadyWarned {
		return
	}
	common.SysLog(fmt.Sprintf(
		"option %s is retired and is being ignored; %s is the key that applies. Delete the stale row from the options table to silence this.",
		key, canonical))
}

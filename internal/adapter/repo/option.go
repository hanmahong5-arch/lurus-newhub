package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/config"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Option = entity.Option

func AllOption() ([]*Option, error) {
	var options []*Option
	var err error
	err = DB.Find(&options).Error
	return options, err
}

func InitOptionMap() {
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)

	// 添加原有的系统配置
	common.OptionMap["FileUploadPermission"] = strconv.Itoa(common.FileUploadPermission)
	common.OptionMap["FileDownloadPermission"] = strconv.Itoa(common.FileDownloadPermission)
	common.OptionMap["ImageUploadPermission"] = strconv.Itoa(common.ImageUploadPermission)
	common.OptionMap["ImageDownloadPermission"] = strconv.Itoa(common.ImageDownloadPermission)
	common.OptionMap["PasswordLoginEnabled"] = strconv.FormatBool(common.PasswordLoginEnabled)
	common.OptionMap["PasswordRegisterEnabled"] = strconv.FormatBool(common.PasswordRegisterEnabled)
	common.OptionMap["EmailVerificationEnabled"] = strconv.FormatBool(common.EmailVerificationEnabled)
	common.OptionMap["GitHubOAuthEnabled"] = strconv.FormatBool(common.GitHubOAuthEnabled)
	common.OptionMap["LinuxDOOAuthEnabled"] = strconv.FormatBool(common.LinuxDOOAuthEnabled)
	common.OptionMap["TelegramOAuthEnabled"] = strconv.FormatBool(common.TelegramOAuthEnabled)
	common.OptionMap["WeChatAuthEnabled"] = strconv.FormatBool(common.WeChatAuthEnabled)
	common.OptionMap["TurnstileCheckEnabled"] = strconv.FormatBool(common.TurnstileCheckEnabled)
	common.OptionMap["RegisterEnabled"] = strconv.FormatBool(common.RegisterEnabled)
	common.OptionMap["AutomaticDisableChannelEnabled"] = strconv.FormatBool(common.AutomaticDisableChannelEnabled)
	common.OptionMap["AutomaticEnableChannelEnabled"] = strconv.FormatBool(common.AutomaticEnableChannelEnabled)
	common.OptionMap["LogConsumeEnabled"] = strconv.FormatBool(common.LogConsumeEnabled)
	common.OptionMap["DisplayInCurrencyEnabled"] = strconv.FormatBool(common.DisplayInCurrencyEnabled)
	common.OptionMap["DisplayTokenStatEnabled"] = strconv.FormatBool(common.DisplayTokenStatEnabled)
	common.OptionMap["DrawingEnabled"] = strconv.FormatBool(common.DrawingEnabled)
	common.OptionMap["TaskEnabled"] = strconv.FormatBool(common.TaskEnabled)
	common.OptionMap["DataExportEnabled"] = strconv.FormatBool(common.DataExportEnabled)
	common.OptionMap["ChannelDisableThreshold"] = strconv.FormatFloat(common.ChannelDisableThreshold, 'f', -1, 64)
	common.OptionMap["EmailDomainRestrictionEnabled"] = strconv.FormatBool(common.EmailDomainRestrictionEnabled)
	common.OptionMap["EmailAliasRestrictionEnabled"] = strconv.FormatBool(common.EmailAliasRestrictionEnabled)
	common.OptionMap["EmailDomainWhitelist"] = strings.Join(common.EmailDomainWhitelist, ",")
	common.OptionMap["SMTPServer"] = ""
	common.OptionMap["SMTPFrom"] = ""
	common.OptionMap["SMTPPort"] = strconv.Itoa(common.SMTPPort)
	common.OptionMap["SMTPAccount"] = ""
	common.OptionMap["SMTPToken"] = ""
	common.OptionMap["SMTPSSLEnabled"] = strconv.FormatBool(common.SMTPSSLEnabled)
	common.OptionMap["Notice"] = ""
	common.OptionMap["About"] = ""
	common.OptionMap["HomePageContent"] = ""
	common.OptionMap["Footer"] = common.Footer
	common.OptionMap["SystemName"] = common.SystemName
	common.OptionMap["Logo"] = common.Logo
	common.OptionMap["ServerAddress"] = ""
	common.OptionMap["WorkerUrl"] = system_setting.WorkerUrl
	common.OptionMap["WorkerValidKey"] = system_setting.WorkerValidKey
	common.OptionMap["WorkerAllowHttpImageRequestEnabled"] = strconv.FormatBool(system_setting.WorkerAllowHttpImageRequestEnabled)
	common.OptionMap["Price"] = strconv.FormatFloat(operation_setting.Price, 'f', -1, 64)
	common.OptionMap["USDExchangeRate"] = strconv.FormatFloat(operation_setting.USDExchangeRate, 'f', -1, 64)
	common.OptionMap["TopupGroupRatio"] = common.TopupGroupRatio2JSONString()
	common.OptionMap["Chats"] = setting.Chats2JsonString()
	common.OptionMap["AutoGroups"] = setting.AutoGroups2JsonString()
	common.OptionMap["DefaultUseAutoGroup"] = strconv.FormatBool(setting.DefaultUseAutoGroup)
	common.OptionMap["GitHubClientId"] = ""
	common.OptionMap["GitHubClientSecret"] = ""
	common.OptionMap["TelegramBotToken"] = ""
	common.OptionMap["TelegramBotName"] = ""
	common.OptionMap["WeChatServerAddress"] = ""
	common.OptionMap["WeChatServerToken"] = ""
	common.OptionMap["WeChatAccountQRCodeImageURL"] = ""
	common.OptionMap["TurnstileSiteKey"] = ""
	common.OptionMap["TurnstileSecretKey"] = ""
	common.OptionMap["QuotaForNewUser"] = strconv.Itoa(common.QuotaForNewUser)
	common.OptionMap["QuotaForInviter"] = strconv.Itoa(common.QuotaForInviter)
	common.OptionMap["QuotaForInvitee"] = strconv.Itoa(common.QuotaForInvitee)
	common.OptionMap["QuotaRemindThreshold"] = strconv.Itoa(common.QuotaRemindThreshold)
	common.OptionMap["PreConsumedQuota"] = strconv.Itoa(common.PreConsumedQuota)
	common.OptionMap["ModelRequestRateLimitCount"] = strconv.Itoa(setting.ModelRequestRateLimitCount)
	common.OptionMap["ModelRequestRateLimitDurationMinutes"] = strconv.Itoa(setting.ModelRequestRateLimitDurationMinutes)
	common.OptionMap["ModelRequestRateLimitSuccessCount"] = strconv.Itoa(setting.ModelRequestRateLimitSuccessCount)
	common.OptionMap["ModelRequestRateLimitGroup"] = setting.ModelRequestRateLimitGroup2JSONString()
	common.OptionMap["ModelRatio"] = ratio_setting.ModelRatio2JSONString()
	common.OptionMap["ModelPrice"] = ratio_setting.ModelPrice2JSONString()
	common.OptionMap["CacheRatio"] = ratio_setting.CacheRatio2JSONString()
	common.OptionMap["ContextLengthTiers"] = ratio_setting.ContextLengthTiers2JSONString()
	common.OptionMap["GroupRatio"] = ratio_setting.GroupRatio2JSONString()
	common.OptionMap["GroupGroupRatio"] = ratio_setting.GroupGroupRatio2JSONString()
	common.OptionMap["UserUsableGroups"] = setting.UserUsableGroups2JSONString()
	common.OptionMap["CompletionRatio"] = ratio_setting.CompletionRatio2JSONString()
	common.OptionMap["ImageRatio"] = ratio_setting.ImageRatio2JSONString()
	common.OptionMap["AudioRatio"] = ratio_setting.AudioRatio2JSONString()
	common.OptionMap["AudioCompletionRatio"] = ratio_setting.AudioCompletionRatio2JSONString()
	common.OptionMap["TopUpLink"] = common.TopUpLink
	//common.OptionMap["ChatLink"] = common.ChatLink
	//common.OptionMap["ChatLink2"] = common.ChatLink2
	common.OptionMap["QuotaPerUnit"] = strconv.FormatFloat(common.QuotaPerUnit, 'f', -1, 64)
	common.OptionMap["RetryTimes"] = strconv.Itoa(common.RetryTimes)
	common.OptionMap["DataExportInterval"] = strconv.Itoa(common.DataExportInterval)
	common.OptionMap["DataExportDefaultTime"] = common.DataExportDefaultTime
	common.OptionMap["DefaultCollapseSidebar"] = strconv.FormatBool(common.DefaultCollapseSidebar)
	common.OptionMap["MjNotifyEnabled"] = strconv.FormatBool(setting.MjNotifyEnabled)
	common.OptionMap["MjAccountFilterEnabled"] = strconv.FormatBool(setting.MjAccountFilterEnabled)
	common.OptionMap["MjModeClearEnabled"] = strconv.FormatBool(setting.MjModeClearEnabled)
	common.OptionMap["MjForwardUrlEnabled"] = strconv.FormatBool(setting.MjForwardUrlEnabled)
	common.OptionMap["MjActionCheckSuccessEnabled"] = strconv.FormatBool(setting.MjActionCheckSuccessEnabled)
	common.OptionMap["CheckSensitiveEnabled"] = strconv.FormatBool(setting.CheckSensitiveEnabled)
	common.OptionMap["DemoSiteEnabled"] = strconv.FormatBool(operation_setting.DemoSiteEnabled)
	common.OptionMap["SelfUseModeEnabled"] = strconv.FormatBool(operation_setting.SelfUseModeEnabled)
	common.OptionMap["ModelFallbackMarkup"] = strconv.FormatFloat(operation_setting.ModelFallbackMarkup, 'f', -1, 64)
	common.OptionMap["ModelRequestRateLimitEnabled"] = strconv.FormatBool(setting.ModelRequestRateLimitEnabled)
	common.OptionMap["CheckSensitiveOnPromptEnabled"] = strconv.FormatBool(setting.CheckSensitiveOnPromptEnabled)
	common.OptionMap["StopOnSensitiveEnabled"] = strconv.FormatBool(setting.StopOnSensitiveEnabled)
	common.OptionMap["SensitiveWords"] = setting.SensitiveWordsToString()
	common.OptionMap["StreamCacheQueueLength"] = strconv.Itoa(setting.StreamCacheQueueLength)
	common.OptionMap["AutomaticDisableKeywords"] = operation_setting.AutomaticDisableKeywordsToString()
	common.OptionMap["ExposeRatioEnabled"] = strconv.FormatBool(ratio_setting.IsExposeRatioEnabled())

	// SMS Configuration
	common.OptionMap["SMSEnabled"] = strconv.FormatBool(common.SMSEnabled)
	common.OptionMap["SMSAccessKeyId"] = common.SMSAccessKeyId
	common.OptionMap["SMSAccessKeySecret"] = common.SMSAccessKeySecret
	common.OptionMap["SMSSignName"] = common.SMSSignName
	common.OptionMap["SMSRegionId"] = common.SMSRegionId
	common.OptionMap["SMSTemplateLogin"] = ""
	common.OptionMap["SMSTemplateRegister"] = ""
	common.OptionMap["SMSTemplateReset"] = ""
	common.OptionMap["SMSTemplateBind"] = ""
	common.OptionMap["SMSTemplateDefault"] = ""

	// Phone Verification Configuration
	common.OptionMap["PhoneVerificationMode"] = common.PhoneVerificationMode
	common.OptionMap["PhoneRequiredForLogin"] = strconv.FormatBool(common.PhoneRequiredForLogin)
	common.OptionMap["PhoneRequiredForPasswordReset"] = strconv.FormatBool(common.PhoneRequiredForPasswordReset)
	common.OptionMap["PhoneRequiredFor2FAChange"] = strconv.FormatBool(common.PhoneRequiredFor2FAChange)
	common.OptionMap["PhoneRequiredForPayment"] = strconv.FormatBool(common.PhoneRequiredForPayment)
	common.OptionMap["PhoneRequiredForWithdrawal"] = strconv.FormatBool(common.PhoneRequiredForWithdrawal)
	common.OptionMap["PhoneRequiredForPhoneBind"] = strconv.FormatBool(common.PhoneRequiredForPhoneBind)
	common.OptionMap["PhoneRequiredForAccountDelete"] = strconv.FormatBool(common.PhoneRequiredForAccountDelete)
	common.OptionMap["PhoneRequiredForTokenGenerate"] = strconv.FormatBool(common.PhoneRequiredForTokenGenerate)
	common.OptionMap["PhoneRequiredForOAuthBind"] = strconv.FormatBool(common.PhoneRequiredForOAuthBind)

	// Registration Configuration
	common.OptionMap["RegistrationMode"] = common.RegistrationMode
	common.OptionMap["SMSAutoRegister"] = strconv.FormatBool(common.SMSAutoRegister)
	common.OptionMap["InviteCodeRequired"] = strconv.FormatBool(common.InviteCodeRequired)

	// Security Configuration
	common.OptionMap["SensitiveActionRequirePassword"] = strconv.FormatBool(common.SensitiveActionRequirePassword)
	common.OptionMap["SensitiveActionRequire2FA"] = strconv.FormatBool(common.SensitiveActionRequire2FA)
	common.OptionMap["SessionTimeoutMinutes"] = strconv.Itoa(common.SessionTimeoutMinutes)

	// 自动添加所有注册的模型配置
	modelConfigs := config.GlobalConfig.ExportAllConfigs()
	for k, v := range modelConfigs {
		common.OptionMap[k] = v
	}

	common.OptionMapRWMutex.Unlock()
	loadOptionsFromDatabase()
}

func loadOptionsFromDatabase() {
	options, _ := AllOption()
	for _, option := range options {
		err := updateOptionMap(option.Key, option.Value)
		if err != nil {
			if errors.Is(err, errOptionKeyRetired) {
				// Already reported once per process by warnRetiredOptionOnce.
				// This runs on every SyncOptions tick on every replica, so a
				// row an operator has not deleted yet must not produce a log
				// line per tick forever.
				continue
			}
			// The message names the key and the type the value had to be,
			// never the value: this same table holds the SMTP password and the
			// OAuth client secret.
			common.SysLog("failed to update option map: " + err.Error())
		}
	}
}

func SyncOptions(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog("syncing options from database")
		loadOptionsFromDatabase()
	}
}

// SyncOptionsWithContext syncs options with context cancellation support.
func SyncOptionsWithContext(ctx context.Context, frequency int) {
	ticker := time.NewTicker(time.Duration(frequency) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			common.SysLog("options sync stopped")
			return
		case <-ticker.C:
			common.SysLog("syncing options from database")
			loadOptionsFromDatabase()
		}
	}
}

func UpdateOption(key string, value string) error {
	// Parse first, persist second.
	//
	// The other order — which this had — leaves a value the engine refuses in
	// the options table for good: the row says one thing, every replica keeps
	// running the previous value, and the next SyncOptions tick rejects the row
	// again. The operator sees the old number in the console and has no
	// in-product way to find the row that is wrong. A value that cannot be
	// applied is therefore refused before the row is written; the caller gets
	// an error wrapping ErrOptionValueRejected.
	if err := ValidateOptionValue(key, value); err != nil {
		return err
	}

	// Save to database first
	option := Option{
		Key: key,
	}
	// https://gorm.io/docs/update.html#Save-All-Fields
	// 写库失败必须上报：否则调用方以为配置已保存，内存态却在重启/其它副本
	// 同步后静默回滚。因此先确认持久化成功，再更新 OptionMap。
	if err := DB.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
		return err
	}
	option.Value = value
	// Save is a combination function.
	// If save value does not contain primary key, it will execute Create,
	// otherwise it will execute Update (with all fields).
	if err := DB.Save(&option).Error; err != nil {
		return err
	}
	// Update OptionMap
	return updateOptionMap(key, value)
}

// UpdateOptionTx persists a single option row through the given transaction,
// without touching the in-memory OptionMap. Plain UpdateOption cannot be
// composed into a larger transaction: it calls DB (the package-global
// handle) directly, so wrapping calls to it in a repo.DB.Transaction closure
// does not route the write through that transaction at all. Callers that
// need several option rows to commit or roll back together — the pricing
// write's four ratio maps plus the PricingVersion CAS below,
// v2_pricing_write.go — call this once per row inside their own
// repo.DB.Transaction, then apply the in-memory side effects themselves only
// after the transaction commits.
func UpdateOptionTx(tx *gorm.DB, key, value string) error {
	return tx.Save(&entity.Option{Key: key, Value: value}).Error
}

// GetOptionValue reads a single option row directly from the database,
// bypassing the in-memory OptionMap. db may be the package-global DB or an
// open transaction. Used where the cache cannot be trusted for the value —
// after a lost pricing-version CAS race (OptionMap has not been refreshed,
// but the caller needs the value the winning writer just committed) or
// inside a transaction that must read its own not-yet-committed baseline.
func GetOptionValue(db *gorm.DB, key string) (value string, found bool, err error) {
	var opt entity.Option
	err = db.Where("key = ?", key).First(&opt).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return opt.Value, true, nil
}

// GetOptionValueForUpdate reads a single option row within tx with a
// row-level lock (SELECT ... FOR UPDATE via clause.Locking — the same idiom
// as GetChannelForUpdate in channel.go). tx must be an open transaction. On
// PostgreSQL this blocks a concurrent reader of the same key until the
// holder of the lock commits or rolls back, then returns that committed
// value, instead of a plain SELECT's read-committed snapshot that could
// still see the pre-commit value; on SQLite (hermetic test tier) the clause
// is a no-op but the surrounding transaction still serializes writers.
// Used where a caller must evaluate a compare-and-set (or merge a base map)
// against the true latest committed value rather than risk racing a
// concurrent writer of the same row — the pricing-version CAS's no-header
// branch and the four ratio-map baseline reads in v2_pricing_write.go.
func GetOptionValueForUpdate(tx *gorm.DB, key string) (value string, found bool, err error) {
	var opt entity.Option
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("key = ?", key).First(&opt).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return opt.Value, true, nil
}

// CASPricingVersionTx performs the compare-and-swap that guards concurrent
// admin pricing edits: UPDATE options SET value=newVersion WHERE
// key='PricingVersion' AND value=expected. rows-affected must be exactly 1
// for the caller to treat the write as won — 0 means either a racing writer
// won first or the caller's `expected` (typically the client's
// If-Match-Pricing-Version header) is stale.
//
// expected==0 additionally means "not written yet" (currentPricingVersion's
// zero default, v2_pricing_write.go), so a bootstrap row is inserted first —
// ON CONFLICT DO NOTHING, idempotent against a concurrent bootstrap — before
// the CAS UPDATE runs against it. Without this the first pricing write in
// the database's lifetime (the row persists across process restarts) has no
// row to match key='PricingVersion' AND value='0' against, so its CAS UPDATE
// affects 0 rows and loses.
func CASPricingVersionTx(tx *gorm.DB, expected, newVersion int64) (bool, error) {
	if expected == 0 {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&entity.Option{Key: "PricingVersion", Value: "0"}).Error; err != nil {
			return false, err
		}
	}
	res := tx.Model(&entity.Option{}).
		Where("key = ? AND value = ?", "PricingVersion", strconv.FormatInt(expected, 10)).
		Update("value", strconv.FormatInt(newVersion, 10))
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// SetOptionMapValue updates only the in-memory OptionMap for key, issuing no
// database write. Callers that already persisted key through their own
// transaction (e.g. the pricing CAS above) use this to refresh the process
// cache after commit, reusing the same key-dispatch updateOptionMap applies
// to a plain UpdateOption write.
func SetOptionMapValue(key, value string) error {
	return updateOptionMap(key, value)
}

// ErrOptionValueRejected marks an option value that cannot be applied. Every
// error this file produces for a bad value wraps it, so one errors.Is tells
// the admin write path "this is the operator's typo, answer 400 and do not
// persist" rather than "the database is down". ValidateOptionValue refuses,
// BEFORE the row is written: the numeric keys (by kind), the JSON keys (by
// the shape their updater decodes, jsonOptionProbes), the hierarchical
// <config>.<field> keys (by a dry-run decode) and the retired keys. A
// boolean or free-form string key cannot fail to parse.
//
// The message it carries names the key and the type the value had to be. It
// never quotes the value: this dispatch is shared with SMTPToken,
// GitHubClientSecret, TurnstileSecretKey and SMSAccessKeySecret, and the
// message ends up in the system log, in the HTTP response body and — through
// loadOptionsFromDatabase — in the log of every replica on every tick.
var ErrOptionValueRejected = errors.New("option value rejected")

// errOptionKeyRetired is the subclass for a key that is no longer a write
// path at all (retiredOptionKeys). It wraps ErrOptionValueRejected so the
// admin path refuses it like any other bad value, and is recognisable on its
// own so the option-sync tick can stay quiet about a row that is already
// reported.
var errOptionKeyRetired = fmt.Errorf("%w: option key is retired", ErrOptionValueRejected)

// optionValueKind is the operator-facing name of what a stored option string
// has to parse as.
type optionValueKind string

const (
	optionKindInteger optionValueKind = "integer"
	optionKindNumber  optionValueKind = "number"
	optionKindJSON    optionValueKind = "JSON document"
)

// numericOptionKinds is the key -> kind table the admin write path validates
// against before it persists anything. It must list every key updateOptionMap
// hands to optionInt or optionFloat, which is derived from this file's AST and
// checked by TestOptionNumericKindsCoverEveryParsedKey — a key that reaches the
// dispatch without an entry here is accepted into the options table and only
// then refused, which is the divergence this table exists to prevent.
var numericOptionKinds = map[string]optionValueKind{
	"FileUploadPermission":                 optionKindInteger,
	"FileDownloadPermission":               optionKindInteger,
	"ImageUploadPermission":                optionKindInteger,
	"ImageDownloadPermission":              optionKindInteger,
	"SMTPPort":                             optionKindInteger,
	"LinuxDOMinimumTrustLevel":             optionKindInteger,
	"QuotaForNewUser":                      optionKindInteger,
	"QuotaForInviter":                      optionKindInteger,
	"QuotaForInvitee":                      optionKindInteger,
	"QuotaRemindThreshold":                 optionKindInteger,
	"PreConsumedQuota":                     optionKindInteger,
	"ModelRequestRateLimitCount":           optionKindInteger,
	"ModelRequestRateLimitDurationMinutes": optionKindInteger,
	"ModelRequestRateLimitSuccessCount":    optionKindInteger,
	"RetryTimes":                           optionKindInteger,
	"DataExportInterval":                   optionKindInteger,
	"StreamCacheQueueLength":               optionKindInteger,
	"SessionTimeoutMinutes":                optionKindInteger,
	"Price":                                optionKindNumber,
	"USDExchangeRate":                      optionKindNumber,
	"ChannelDisableThreshold":              optionKindNumber,
	"QuotaPerUnit":                         optionKindNumber,
	"ModelFallbackMarkup":                  optionKindNumber,
}

// positiveRangeOptionKinds lists numeric option keys whose parsed value must
// additionally land in (0, optionPositiveRangeMax) — a parse-valid but
// nonsensical write (0, a negative number) for either of these turns every
// relay call's cost computation into a divide-by-zero or a negative price:
// QuotaPerUnit is the quota-to-CNY divisor everywhere in this codebase that
// converts a quota int to a currency amount (v2_billing_invoices.go,
// billing_self.go, /api/status), and USDExchangeRate is the CNY-to-USD
// divisor next to it. There is no business meaning above
// optionPositiveRangeMax either — it exists only to catch a stray extra
// digit, not to express a rate anyone would configure (cycle13 §2:
// "QuotaPerUnit/USDExchangeRate 写入加正数范围守卫"; the pre-consume-period
// freeze, MONEY-2, is out of scope this cycle — see cycle13 §7).
var positiveRangeOptionKinds = map[string]bool{
	"QuotaPerUnit":    true,
	"USDExchangeRate": true,
}

// optionPositiveRangeMax is the exclusive upper bound positiveRangeOptionKinds
// enforces; the lower bound is a strict >0 test, so the constant only needs
// to name the one number.
const optionPositiveRangeMax = 1e9

// jsonOptionKinds lists the keys whose dispatch hands the value to a
// JSON-string updater. Validation for these is json.Valid only: the updaters
// unmarshal into their own shapes and a shape-aware pre-check here would be a
// second copy of those shapes, free to drift and to refuse a value the real
// updater accepts. A malformed document — the typo an operator actually makes
// — is refused before the row is written; a well-formed document of the wrong
// shape is still refused by the updater after the row is written, and the
// handler records that refusal in the audit trail.
// TestOptionJSONKeysCoverEveryJSONUpdater derives the same set from this
// file's AST.
var jsonOptionKinds = map[string]optionValueKind{
	"Chats":                      optionKindJSON,
	"AutoGroups":                 optionKindJSON,
	"TopupGroupRatio":            optionKindJSON,
	"ModelRequestRateLimitGroup": optionKindJSON,
	"ModelRatio":                 optionKindJSON,
	"GroupRatio":                 optionKindJSON,
	"GroupGroupRatio":            optionKindJSON,
	"UserUsableGroups":           optionKindJSON,
	"CompletionRatio":            optionKindJSON,
	"ModelPrice":                 optionKindJSON,
	"CacheRatio":                 optionKindJSON,
	"ContextLengthTiers":         optionKindJSON,
	"ImageRatio":                 optionKindJSON,
	"AudioRatio":                 optionKindJSON,
	"AudioCompletionRatio":       optionKindJSON,
}

// jsonOptionProbes decode a JSON option value into a throwaway of the SAME
// type its updater decodes into (setting.UpdateChatsByJsonString and the
// others named in updateOptionMap), so a well-formed document of the wrong
// shape — {"a":"x"} for a map[string]float64 key — is refused before the row
// is written, instead of being persisted and then refused by an updater that
// had already emptied the live table. The types are copied from each
// updater's make(...); ContextLengthTiers uses the updater's own validator,
// which does not touch the live map. TestOptionJSONProbesCoverEveryJSONKey
// keeps this table equal to jsonOptionKinds, and
// TestOptionJSONProbesRejectWrongShape drives every probe with a wrong-shape
// document.
var jsonOptionProbes = map[string]func(string) error{
	"Chats":                      jsonShape[[]map[string]string],
	"AutoGroups":                 jsonShape[[]string],
	"TopupGroupRatio":            jsonShape[map[string]float64],
	"ModelRequestRateLimitGroup": jsonShape[map[string][2]int],
	"ModelRatio":                 jsonShape[map[string]float64],
	"GroupRatio":                 jsonShape[map[string]float64],
	"GroupGroupRatio":            jsonShape[map[string]map[string]float64],
	"UserUsableGroups":           jsonShape[map[string]string],
	"CompletionRatio":            jsonShape[map[string]float64],
	"ModelPrice":                 jsonShape[map[string]float64],
	"CacheRatio":                 jsonShape[map[string]float64],
	"ContextLengthTiers": func(value string) error {
		_, err := ratio_setting.ValidateContextLengthTiersJSONString(value)
		return err
	},
	"ImageRatio":           jsonShape[map[string]float64],
	"AudioRatio":           jsonShape[map[string]float64],
	"AudioCompletionRatio": jsonShape[map[string]float64],
}

// jsonShape is the generic probe: decode into a throwaway T and report the
// decoder's verdict. It allocates nothing the caller keeps.
func jsonShape[T any](value string) error {
	var probe T
	return json.Unmarshal([]byte(value), &probe)
}

// ValidateOptionValue reports whether value can be applied to key, changing
// nothing at all. UpdateOption calls it before it writes the row.
//
// A key it does not recognise is not an error: most options are free-form
// strings and booleans (`value == "true"`), which cannot fail to parse.
func ValidateOptionValue(key, value string) error {
	if canonical, retired := retiredOptionKeys[key]; retired {
		return fmt.Errorf("%w: %s; write %s instead", errOptionKeyRetired, key, canonical)
	}

	if kind, ok := numericOptionKinds[key]; ok {
		var err error
		var parsedFloat float64
		switch kind {
		case optionKindInteger:
			_, err = strconv.Atoi(value)
		default:
			parsedFloat, err = strconv.ParseFloat(value, 64)
		}
		if err != nil {
			reportOptionParseFailure(key, kind)
			return optionKindError(key, kind)
		}
		if positiveRangeOptionKinds[key] && (parsedFloat <= 0 || parsedFloat >= optionPositiveRangeMax) {
			reportOptionRangeFailure(key)
			return optionRangeError(key)
		}
		return nil
	}

	if kind, ok := jsonOptionKinds[key]; ok {
		probe, known := jsonOptionProbes[key]
		if !known {
			// TestOptionJSONProbesCoverEveryJSONKey keeps the two tables equal;
			// a key that slipped through is refused rather than persisted blind.
			return fmt.Errorf("%w: %s has no shape probe", ErrOptionValueRejected, key)
		}
		if err := probe(value); err != nil {
			reportOptionParseFailure(key, kind)
			return optionKindError(key, kind)
		}
		return nil
	}

	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return nil
	}
	cfg := config.GlobalConfig.Get(parts[0])
	if cfg == nil {
		return nil
	}
	if err := config.ValidateConfigValue(cfg, parts[1], value); err != nil {
		// config's error names the expected type and nothing else
		// (config.parseKindError), so it is safe to log and to return.
		metrics.RecordOptionParseRejected(key)
		common.SysError(fmt.Sprintf("option %s rejected: %v; the previous value is kept", key, err))
		return fmt.Errorf("%w: %s %w", ErrOptionValueRejected, key, err)
	}
	return nil
}

// optionKindError is the single shape of a rejection message.
func optionKindError(key string, kind optionValueKind) error {
	return fmt.Errorf("%w: %s must be a valid %s", ErrOptionValueRejected, key, kind)
}

// optionRangeError is optionKindError's counterpart for positiveRangeOptionKinds:
// the value parsed fine but is out of the range that key must stay in. Like
// optionKindError it never quotes the submitted value — this dispatch is
// shared with secret-bearing keys (see ErrOptionValueRejected's comment).
func optionRangeError(key string) error {
	return fmt.Errorf("%w: %s must be greater than 0 and less than %g", ErrOptionValueRejected, key, optionPositiveRangeMax)
}

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

func updateOptionMap(key string, value string) (err error) {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	// OptionMap mirrors the options table, so it keeps the stored string even
	// when the dispatch below rejects it: the row does exist with that value,
	// and the divergence between it and the running value is what
	// metrics.OptionParseRejectedTotal reports.
	common.OptionMap[key] = value

	// 检查是否是模型配置 - 使用更规范的方式处理
	if handled, configErr := applyHierarchicalOption(key, value); handled {
		return configErr // 已由配置系统处理
	}

	// 处理传统配置项...
	if strings.HasSuffix(key, "Permission") {
		switch key {
		case "FileUploadPermission":
			common.FileUploadPermission, err = optionInt(key, value, common.FileUploadPermission)
		case "FileDownloadPermission":
			common.FileDownloadPermission, err = optionInt(key, value, common.FileDownloadPermission)
		case "ImageUploadPermission":
			common.ImageUploadPermission, err = optionInt(key, value, common.ImageUploadPermission)
		case "ImageDownloadPermission":
			common.ImageDownloadPermission, err = optionInt(key, value, common.ImageDownloadPermission)
		}
		if err != nil {
			return err
		}
	}
	if strings.HasSuffix(key, "Enabled") || key == "DefaultCollapseSidebar" || key == "DefaultUseAutoGroup" {
		boolValue := value == "true"
		switch key {
		case "PasswordRegisterEnabled":
			common.PasswordRegisterEnabled = boolValue
		case "PasswordLoginEnabled":
			common.PasswordLoginEnabled = boolValue
		case "EmailVerificationEnabled":
			common.EmailVerificationEnabled = boolValue
		case "GitHubOAuthEnabled":
			common.GitHubOAuthEnabled = boolValue
		case "LinuxDOOAuthEnabled":
			common.LinuxDOOAuthEnabled = boolValue
		case "WeChatAuthEnabled":
			common.WeChatAuthEnabled = boolValue
		case "TelegramOAuthEnabled":
			common.TelegramOAuthEnabled = boolValue
		case "TurnstileCheckEnabled":
			common.TurnstileCheckEnabled = boolValue
		case "RegisterEnabled":
			common.RegisterEnabled = boolValue
		case "EmailDomainRestrictionEnabled":
			common.EmailDomainRestrictionEnabled = boolValue
		case "EmailAliasRestrictionEnabled":
			common.EmailAliasRestrictionEnabled = boolValue
		case "AutomaticDisableChannelEnabled":
			common.AutomaticDisableChannelEnabled = boolValue
		case "AutomaticEnableChannelEnabled":
			common.AutomaticEnableChannelEnabled = boolValue
		case "LogConsumeEnabled":
			common.LogConsumeEnabled = boolValue
		case "DisplayInCurrencyEnabled":
			// 兼容旧字段：同步到新配置 general_setting.quota_display_type（运行时生效）
			// true -> USD, false -> TOKENS
			newVal := "USD"
			if !boolValue {
				newVal = "TOKENS"
			}
			if cfg := config.GlobalConfig.Get("general_setting"); cfg != nil {
				_ = config.UpdateConfigFromMap(cfg, map[string]string{"quota_display_type": newVal})
			}
		case "DisplayTokenStatEnabled":
			common.DisplayTokenStatEnabled = boolValue
		case "DrawingEnabled":
			common.DrawingEnabled = boolValue
		case "TaskEnabled":
			common.TaskEnabled = boolValue
		case "DataExportEnabled":
			common.DataExportEnabled = boolValue
		case "DefaultCollapseSidebar":
			common.DefaultCollapseSidebar = boolValue
		case "MjNotifyEnabled":
			setting.MjNotifyEnabled = boolValue
		case "MjAccountFilterEnabled":
			setting.MjAccountFilterEnabled = boolValue
		case "MjModeClearEnabled":
			setting.MjModeClearEnabled = boolValue
		case "MjForwardUrlEnabled":
			setting.MjForwardUrlEnabled = boolValue
		case "MjActionCheckSuccessEnabled":
			setting.MjActionCheckSuccessEnabled = boolValue
		case "CheckSensitiveEnabled":
			setting.CheckSensitiveEnabled = boolValue
		case "DemoSiteEnabled":
			operation_setting.DemoSiteEnabled = boolValue
		case "SelfUseModeEnabled":
			operation_setting.SelfUseModeEnabled = boolValue
		case "CheckSensitiveOnPromptEnabled":
			setting.CheckSensitiveOnPromptEnabled = boolValue
		case "ModelRequestRateLimitEnabled":
			setting.ModelRequestRateLimitEnabled = boolValue
		case "StopOnSensitiveEnabled":
			setting.StopOnSensitiveEnabled = boolValue
		case "SMTPSSLEnabled":
			common.SMTPSSLEnabled = boolValue
		case "WorkerAllowHttpImageRequestEnabled":
			system_setting.WorkerAllowHttpImageRequestEnabled = boolValue
		case "DefaultUseAutoGroup":
			setting.DefaultUseAutoGroup = boolValue
		case "ExposeRatioEnabled":
			ratio_setting.SetExposeRatioEnabled(boolValue)
		}
	}
	switch key {
	case "EmailDomainWhitelist":
		common.EmailDomainWhitelist = strings.Split(value, ",")
	case "SMTPServer":
		common.SMTPServer = value
	case "SMTPPort":
		common.SMTPPort, err = optionInt(key, value, common.SMTPPort)
	case "SMTPAccount":
		common.SMTPAccount = value
	case "SMTPFrom":
		common.SMTPFrom = value
	case "SMTPToken":
		common.SMTPToken = value
	case "ServerAddress":
		system_setting.ServerAddress = value
	case "WorkerUrl":
		system_setting.WorkerUrl = value
	case "WorkerValidKey":
		system_setting.WorkerValidKey = value
	case "Chats":
		err = setting.UpdateChatsByJsonString(value)
	case "AutoGroups":
		err = setting.UpdateAutoGroupsByJsonString(value)
	case "Price":
		operation_setting.Price, err = optionFloat(key, value, operation_setting.Price)
	case "USDExchangeRate":
		operation_setting.USDExchangeRate, err = optionFloat(key, value, operation_setting.USDExchangeRate)
	case "TopupGroupRatio":
		err = common.UpdateTopupGroupRatioByJSONString(value)
	case "GitHubClientId":
		common.GitHubClientId = value
	case "GitHubClientSecret":
		common.GitHubClientSecret = value
	case "LinuxDOClientId":
		common.LinuxDOClientId = value
	case "LinuxDOClientSecret":
		common.LinuxDOClientSecret = value
	case "LinuxDOMinimumTrustLevel":
		common.LinuxDOMinimumTrustLevel, err = optionInt(key, value, common.LinuxDOMinimumTrustLevel)
	case "Footer":
		common.Footer = value
	case "SystemName":
		common.SystemName = value
	case "Logo":
		common.Logo = value
	case "WeChatServerAddress":
		common.WeChatServerAddress = value
	case "WeChatServerToken":
		common.WeChatServerToken = value
	case "WeChatAccountQRCodeImageURL":
		common.WeChatAccountQRCodeImageURL = value
	case "TelegramBotToken":
		common.TelegramBotToken = value
	case "TelegramBotName":
		common.TelegramBotName = value
	case "TurnstileSiteKey":
		common.TurnstileSiteKey = value
	case "TurnstileSecretKey":
		common.TurnstileSecretKey = value
	case "QuotaForNewUser":
		common.QuotaForNewUser, err = optionInt(key, value, common.QuotaForNewUser)
	case "QuotaForInviter":
		common.QuotaForInviter, err = optionInt(key, value, common.QuotaForInviter)
	case "QuotaForInvitee":
		common.QuotaForInvitee, err = optionInt(key, value, common.QuotaForInvitee)
	case "QuotaRemindThreshold":
		common.QuotaRemindThreshold, err = optionInt(key, value, common.QuotaRemindThreshold)
	case "PreConsumedQuota":
		common.PreConsumedQuota, err = optionInt(key, value, common.PreConsumedQuota)
	case "ModelRequestRateLimitCount":
		setting.ModelRequestRateLimitCount, err = optionInt(key, value, setting.ModelRequestRateLimitCount)
	case "ModelRequestRateLimitDurationMinutes":
		setting.ModelRequestRateLimitDurationMinutes, err = optionInt(key, value, setting.ModelRequestRateLimitDurationMinutes)
	case "ModelRequestRateLimitSuccessCount":
		setting.ModelRequestRateLimitSuccessCount, err = optionInt(key, value, setting.ModelRequestRateLimitSuccessCount)
	case "ModelRequestRateLimitGroup":
		err = setting.UpdateModelRequestRateLimitGroupByJSONString(value)
	case "RetryTimes":
		common.RetryTimes, err = optionInt(key, value, common.RetryTimes)
	case "DataExportInterval":
		common.DataExportInterval, err = optionInt(key, value, common.DataExportInterval)
	case "DataExportDefaultTime":
		common.DataExportDefaultTime = value
	case "ModelRatio":
		err = ratio_setting.UpdateModelRatioByJSONString(value)
	case "GroupRatio":
		err = ratio_setting.UpdateGroupRatioByJSONString(value)
	case "GroupGroupRatio":
		err = ratio_setting.UpdateGroupGroupRatioByJSONString(value)
	case "UserUsableGroups":
		err = setting.UpdateUserUsableGroupsByJSONString(value)
	case "CompletionRatio":
		err = ratio_setting.UpdateCompletionRatioByJSONString(value)
	case "ModelPrice":
		err = ratio_setting.UpdateModelPriceByJSONString(value)
	case "CacheRatio":
		err = ratio_setting.UpdateCacheRatioByJSONString(value)
	case "ContextLengthTiers":
		err = ratio_setting.UpdateContextLengthTiersByJSONString(value)
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(value)
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(value)
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(value)
	//case "ChatLink":
	//	common.ChatLink = value
	//case "ChatLink2":
	//	common.ChatLink2 = value
	case "ChannelDisableThreshold":
		common.ChannelDisableThreshold, err = optionFloat(key, value, common.ChannelDisableThreshold)
	case "QuotaPerUnit":
		common.QuotaPerUnit, err = optionFloat(key, value, common.QuotaPerUnit)
	case "ModelFallbackMarkup":
		operation_setting.ModelFallbackMarkup, err = optionFloat(key, value, operation_setting.ModelFallbackMarkup)
	case "SensitiveWords":
		setting.SensitiveWordsFromString(value)
	case "AutomaticDisableKeywords":
		operation_setting.AutomaticDisableKeywordsFromString(value)
	case "StreamCacheQueueLength":
		setting.StreamCacheQueueLength, err = optionInt(key, value, setting.StreamCacheQueueLength)
	// SMS Configuration
	case "SMSEnabled":
		common.SMSEnabled = value == "true"
		if common.SMSEnabled {
			// Re-initialize SMS client when enabled
			if initErr := common.InitSmsClient(); initErr != nil {
				common.SysLog("Failed to initialize SMS client: " + initErr.Error())
			}
		}
	case "SMSAccessKeyId":
		common.SMSAccessKeyId = value
	case "SMSAccessKeySecret":
		common.SMSAccessKeySecret = value
	case "SMSSignName":
		common.SMSSignName = value
	case "SMSRegionId":
		common.SMSRegionId = value
		if common.SMSRegionId == "" {
			common.SMSRegionId = "cn-hangzhou"
		}

	// Phone Verification Configuration
	case "PhoneVerificationMode":
		// Validate mode value
		switch value {
		case common.PhoneVerificationDisabled,
			common.PhoneVerificationOptional,
			common.PhoneVerificationRequiredLogin,
			common.PhoneVerificationRequiredSensitive:
			common.PhoneVerificationMode = value
		default:
			common.PhoneVerificationMode = common.PhoneVerificationOptional
		}
	case "PhoneRequiredForLogin":
		common.PhoneRequiredForLogin = value == "true"
	case "PhoneRequiredForPasswordReset":
		common.PhoneRequiredForPasswordReset = value == "true"
	case "PhoneRequiredFor2FAChange":
		common.PhoneRequiredFor2FAChange = value == "true"
	case "PhoneRequiredForPayment":
		common.PhoneRequiredForPayment = value == "true"
	case "PhoneRequiredForWithdrawal":
		common.PhoneRequiredForWithdrawal = value == "true"
	case "PhoneRequiredForPhoneBind":
		common.PhoneRequiredForPhoneBind = value == "true"
	case "PhoneRequiredForAccountDelete":
		common.PhoneRequiredForAccountDelete = value == "true"
	case "PhoneRequiredForTokenGenerate":
		common.PhoneRequiredForTokenGenerate = value == "true"
	case "PhoneRequiredForOAuthBind":
		common.PhoneRequiredForOAuthBind = value == "true"

	// Registration Configuration
	case "RegistrationMode":
		// Validate mode value
		switch value {
		case common.RegistrationModeOpen,
			common.RegistrationModeInviteOnly,
			common.RegistrationModeOAuthOnly,
			common.RegistrationModePhoneVerified,
			common.RegistrationModeClosed:
			common.RegistrationMode = value
		default:
			common.RegistrationMode = common.RegistrationModeOpen
		}
	case "SMSAutoRegister":
		common.SMSAutoRegister = value == "true"
	case "InviteCodeRequired":
		common.InviteCodeRequired = value == "true"

	// Security Configuration
	case "SensitiveActionRequirePassword":
		common.SensitiveActionRequirePassword = value == "true"
	case "SensitiveActionRequire2FA":
		common.SensitiveActionRequire2FA = value == "true"
	case "SessionTimeoutMinutes":
		common.SessionTimeoutMinutes, err = optionInt(key, value, common.SessionTimeoutMinutes)
		if common.SessionTimeoutMinutes <= 0 {
			common.SessionTimeoutMinutes = 10080 // Default to 7 days
		}
	}
	return err
}

// handleConfigUpdate is the boolean-only view of applyHierarchicalOption —
// "does this key belong to a registered hierarchical config" — kept under its
// original name and shape because this package's tests call it that way
// (the three TestHandleConfigUpdate_* cases in sqlite_repo_extra9_test.go).
// Production code calls applyHierarchicalOption so a rejected value is
// reported rather than dropped.
func handleConfigUpdate(key, value string) bool {
	handled, _ := applyHierarchicalOption(key, value)
	return handled
}

// applyHierarchicalOption 处理分层配置更新，返回是否已处理以及处理结果。
//
// The error half is the point: this used to discard whatever
// UpdateConfigFromMap returned, so a malformed JSON value for a hierarchical
// key (gemini.safety_settings, fetch_setting.domain_list, claude.
// model_headers_settings …) was accepted by the admin API, stored in the
// options table, and then quietly not applied on any replica.
func applyHierarchicalOption(key, value string) (bool, error) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return false, nil // 不是分层配置
	}

	if canonical, retired := retiredOptionKeys[key]; retired {
		warnRetiredOptionOnce(key, canonical)
		return true, fmt.Errorf("%w: %s; write %s instead", errOptionKeyRetired, key, canonical)
	}

	configName := parts[0]
	configKey := parts[1]

	// 获取配置对象
	cfg := config.GlobalConfig.Get(configName)
	if cfg == nil {
		return false, nil // 未注册的配置
	}

	// 更新配置
	configMap := map[string]string{
		configKey: value,
	}
	if updateErr := config.UpdateConfigFromMapStrict(cfg, configMap); updateErr != nil {
		// updateErr names the field and the type it had to be, nothing else
		// (config.parseKindError), so it is safe to log and to return.
		metrics.RecordOptionParseRejected(key)
		common.SysError(fmt.Sprintf("option %s rejected: %v; the previous value is kept", key, updateErr))
		return true, fmt.Errorf("%w: %s %w", ErrOptionValueRejected, key, updateErr)
	}

	return true, nil // 已处理
}

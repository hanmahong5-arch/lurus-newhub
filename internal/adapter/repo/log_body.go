package repo

// log_body.go - the opt-in prompt/response archive (migration 052).
//
// The logs table has never stored a prompt or a completion. This file adds an
// archive for tenants that explicitly consent, behind a gate in which EVERY
// condition must hold and any single "no" writes nothing. The write is async
// and best-effort: billing and the log row are already settled when it runs,
// so a failure only increments metrics.LogBodyWriteFailedTotal.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type LogBody = entity.LogBody

// LogBodyFieldMaxBytes caps each stored text field (request body, response
// text). Longer content is cut on a rune boundary and the row is flagged
// truncated.
const LogBodyFieldMaxBytes = 64 * 1024

// logBodyDefaultRetentionDays is LOG_BODY_RETENTION_DAYS when unset.
const logBodyDefaultRetentionDays = 30

// ErrLogBodyNotFound is returned for a missing, expired or foreign-tenant
// body alike, so a caller cannot probe another tenant's request ids.
var ErrLogBodyNotFound = errors.New("log body not found")

// LogBodyRetentionDays resolves LOG_BODY_RETENTION_DAYS per write, so an
// operator change applies without a restart. Non-positive values fall back to
// the default: a body must always expire.
func LogBodyRetentionDays() int {
	if d := common.GetEnvOrDefault("LOG_BODY_RETENTION_DAYS", logBodyDefaultRetentionDays); d > 0 {
		return d
	}
	return logBodyDefaultRetentionDays
}

// LogBodyArchiveInput is everything the archive gate looks at, resolved by the
// caller. Splitting the decision from the context reads keeps the matrix
// testable one condition at a time.
type LogBodyArchiveInput struct {
	// TenantConsent is tenants.sedimentation_consent.
	TenantConsent bool
	// Retention is the effective (platform -> tenant -> token, strictest
	// wins) content retention.
	Retention contentpolicy.RetentionMode
	// ChannelZeroRetention: the serving channel declares data_collection=deny.
	ChannelZeroRetention bool
	// RequestDeniesCollection: the caller sent provider.data_collection=deny.
	RequestDeniesCollection bool
	// LogDetailLevel is the user's setting; "none" is a veto.
	LogDetailLevel string
	// FormatCovered: the wire format is one the content rules inspect, so the
	// body about to be stored really is the rule-processed one.
	FormatCovered bool
}

// ShouldArchiveLogBody is the single gate. All of: tenant consented, effective
// retention is full, channel not zero-retention, caller did not deny
// collection, user did not set log detail "none", and the request format is
// covered by the content rules. LogDetailLevel "full" is NOT required - the
// tenant policy decides - but "none" always vetoes.
func ShouldArchiveLogBody(in LogBodyArchiveInput) bool {
	return in.TenantConsent &&
		in.Retention == contentpolicy.RetentionFull &&
		!in.ChannelZeroRetention &&
		!in.RequestDeniesCollection &&
		in.LogDetailLevel != "none" &&
		in.FormatCovered
}

// LogBodyFormatCovered mirrors handler.contentFormatFor: archiving a body the
// content rules never looked at would store text the tenant's mask rules were
// meant to scrub. A parity test in the handler package keeps the two equal.
func LogBodyFormatCovered(f types.RelayFormat) bool {
	switch f {
	case types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini,
		types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompact,
		types.RelayFormatSystemOne:
		return true
	}
	return false
}

// ---- tenant consent ---------------------------------------------------

type consentEntry struct {
	on  bool
	exp time.Time
}

var consentCache sync.Map // tenantID -> consentEntry

// InvalidateSedimentationConsentCache drops the cached consent on this
// replica; others converge within policyCacheTTL.
func InvalidateSedimentationConsentCache() { consentCache = sync.Map{} }

// GetTenantSedimentationConsent reads the stored flag (uncached).
func GetTenantSedimentationConsent(tenantID string) (bool, error) {
	var on bool
	err := DB.Model(&Tenant{}).Where("id = ?", tenantID).Select("sedimentation_consent").Scan(&on).Error
	return on, err
}

// SetTenantSedimentationConsent stores the flag.
func SetTenantSedimentationConsent(tenantID string, on bool) error {
	res := DB.Model(&Tenant{}).Where("id = ?", tenantID).Update("sedimentation_consent", on)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	InvalidateSedimentationConsentCache()
	return nil
}

// tenantConsents is the hot-path read. Unlike retention (which keeps the last
// decision on a DB blip because it TIGHTENS), consent LOOSENS, so a failed
// lookup answers false: no consent known = no write.
func tenantConsents(tenantID string) bool {
	if tenantID == "" {
		return false
	}
	now := rulesetNow()
	if v, ok := consentCache.Load(tenantID); ok {
		if e := v.(consentEntry); now.Before(e.exp) {
			return e.on
		}
	}
	on, err := GetTenantSedimentationConsent(tenantID)
	if err != nil {
		consentCache.Delete(tenantID)
		return false
	}
	consentCache.Store(tenantID, consentEntry{on: on, exp: now.Add(policyCacheTTL)})
	return on
}

// ---- capture ----------------------------------------------------------

// truncateUTF8 cuts s to at most max bytes without splitting a rune and
// reports whether it cut.
func truncateUTF8(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// sanitizeBodyText makes s storable in a PG text column: invalid UTF-8 and NUL
// both make the INSERT fail, which would be counted as a lost body.
func sanitizeBodyText(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.ReplaceAll(s, "\x00", "")
}

// cachedRequestBody returns the request body the relay already holds, WITHOUT
// reading the connection (common.GetRequestBody would, on a path that never
// cached). That cached buffer is the one the content rules rewrote.
func cachedRequestBody(c *gin.Context) []byte {
	if v, ok := c.Get(common.KeyRequestBody); ok {
		if b, ok := v.([]byte); ok {
			return b
		}
	}
	return nil
}

func relayFormatOf(c *gin.Context) types.RelayFormat {
	v, ok := c.Get(string(constant.ContextKeyRelayFormat))
	if !ok {
		return ""
	}
	switch f := v.(type) {
	case types.RelayFormat:
		return f
	case string:
		return types.RelayFormat(f)
	}
	return ""
}

// logBodyInput resolves the gate inputs for a persisted consume row.
func logBodyInput(c *gin.Context, l *Log, logDetailLevel string) LogBodyArchiveInput {
	setting, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	filter, _ := common.GetContextKeyType[dto.ProviderFilter](c, constant.ContextKeyProviderFilter)
	return LogBodyArchiveInput{
		TenantConsent:           tenantConsents(l.TenantId),
		Retention:               EffectiveContentRetention(l.TenantId, l.TokenId),
		ChannelZeroRetention:    strings.EqualFold(strings.TrimSpace(setting.DataCollection), dto.DataCollectionDeny),
		RequestDeniesCollection: filter.DataCollection == dto.DataCollectionDeny,
		LogDetailLevel:          logDetailLevel,
		FormatCovered:           LogBodyFormatCovered(relayFormatOf(c)),
	}
}

// buildLogBody assembles the row from the request context, or nil when there
// is nothing addressable or nothing to store.
func buildLogBody(c *gin.Context, l *Log, requestID string, now time.Time) *LogBody {
	if requestID == "" || len(requestID) > 64 {
		return nil
	}
	var reqBody string
	if !strings.HasPrefix(strings.ToLower(c.ContentType()), "multipart/") {
		reqBody = string(cachedRequestBody(c))
	}
	respText, captured := "", false
	if v, ok := c.Get(string(constant.ContextKeyResponseText)); ok {
		if s, ok := v.(string); ok {
			respText, captured = s, true
		}
	}
	if reqBody == "" && respText == "" {
		return nil
	}
	reqBody, t1 := truncateUTF8(reqBody, LogBodyFieldMaxBytes)
	respText, t2 := truncateUTF8(respText, LogBodyFieldMaxBytes)
	return &LogBody{
		RequestId:        requestID,
		TenantId:         l.TenantId,
		UserId:           int64(l.UserId),
		TokenId:          int64(l.TokenId),
		Model:            l.ModelName,
		CreatedAt:        now.Unix(),
		ExpiresAt:        now.AddDate(0, 0, LogBodyRetentionDays()).Unix(),
		RequestBody:      sanitizeBodyText(reqBody),
		ResponseText:     sanitizeBodyText(respText),
		ResponseCaptured: captured,
		Truncated:        t1 || t2,
	}
}

// archiveLogBody is called by RecordConsumeLog after the log row is stored.
// Everything that touches the gin context runs synchronously (the context is
// recycled once the handler returns); only the INSERT is deferred.
func archiveLogBody(c *gin.Context, l *Log, requestID, logDetailLevel string) {
	if c == nil || l == nil {
		return
	}
	if !ShouldArchiveLogBody(logBodyInput(c, l, logDetailLevel)) {
		return
	}
	row := buildLogBody(c, l, requestID, time.Now())
	if row == nil {
		return
	}
	AsyncGo(func() {
		if err := DB.Create(row).Error; err != nil {
			metrics.LogBodyWriteFailedTotal.Inc()
			common.SysError("failed to archive log body for tenant " + row.TenantId + ": " + err.Error())
		}
	})
}

// ---- reads and sweep --------------------------------------------------

// GetLogBody loads an archived body confined to the tenant; an expired row is
// treated as absent even before the sweep removes it.
func GetLogBody(tenantID, requestID string) (*LogBody, error) {
	if tenantID == "" {
		return nil, ErrLogBodyNotFound
	}
	return getLogBody(tenantID, requestID, true)
}

// GetLogBodyAnyTenant is the platform-admin read: not confined to a tenant.
func GetLogBodyAnyTenant(requestID string) (*LogBody, error) {
	return getLogBody("", requestID, false)
}

func getLogBody(tenantID, requestID string, confine bool) (*LogBody, error) {
	if requestID == "" {
		return nil, ErrLogBodyNotFound
	}
	q := DB.Where("request_id = ? AND expires_at > ?", requestID, common.GetTimestamp())
	if confine {
		q = q.Where("tenant_id = ?", tenantID)
	}
	var row LogBody
	err := q.Order("id desc").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrLogBodyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// DeleteExpiredLogBodies hard-deletes rows whose expires_at is at or before
// now, in batches (id-subquery shape, see DeleteLogsBefore for why), stopping
// after maxBatches so a backlog drains over later passes.
func DeleteExpiredLogBodies(ctx context.Context, now int64, batch, maxBatches int) (int64, error) {
	if batch <= 0 {
		batch = 500
	}
	if maxBatches <= 0 {
		maxBatches = logRetentionMaxBatches
	}
	var total int64
	for i := 0; i < maxBatches; i++ {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		idQuery := DB.Model(&LogBody{}).Select("id").Where("expires_at <= ?", now).Order("id").Limit(batch)
		res := DB.Where("id IN (?)", idQuery).Delete(&LogBody{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if res.RowsAffected < int64(batch) {
			break
		}
	}
	return total, nil
}

// PurgeTenantLogBodies hard-deletes every archived body of one tenant, in
// batches. It is the synchronous half of consent withdrawal: the tenant was
// promised that turning the switch off removes what was kept, not merely that
// nothing new is added. Stops after maxBatches like the sweep does; whatever
// is left is picked up by DeleteUnconsentedLogBodies on the next pass.
func PurgeTenantLogBodies(ctx context.Context, tenantID string, batch, maxBatches int) (int64, error) {
	if tenantID == "" {
		return 0, nil
	}
	return deleteLogBodiesWhere(ctx, batch, maxBatches, "tenant_id = ?", tenantID)
}

// DeleteUnconsentedLogBodies hard-deletes bodies whose tenant does not
// currently consent - the sweep's backstop for a withdrawal whose synchronous
// purge failed halfway, for writes still in flight on another replica during
// the consent cache window, and for rows of a tenant that no longer exists
// (fail closed: unknown tenant = no consent).
func DeleteUnconsentedLogBodies(ctx context.Context, batch, maxBatches int) (int64, error) {
	consenting := DB.Model(&Tenant{}).Select("id").Where("sedimentation_consent = ?", true)
	return deleteLogBodiesWhere(ctx, batch, maxBatches, "tenant_id NOT IN (?)", consenting)
}

func deleteLogBodiesWhere(ctx context.Context, batch, maxBatches int, query string, args ...interface{}) (int64, error) {
	if batch <= 0 {
		batch = 500
	}
	if maxBatches <= 0 {
		maxBatches = logRetentionMaxBatches
	}
	var total int64
	for i := 0; i < maxBatches; i++ {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		idQuery := DB.Model(&LogBody{}).Select("id").Where(query, args...).Order("id").Limit(batch)
		res := DB.WithContext(ctx).Where("id IN (?)", idQuery).Delete(&LogBody{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if res.RowsAffected < int64(batch) {
			break
		}
	}
	return total, nil
}

// CountExpiredLogBodies is the backlog behind DeleteExpiredLogBodies.
func CountExpiredLogBodies(ctx context.Context, now int64) (int64, error) {
	var n int64
	err := DB.WithContext(ctx).Model(&LogBody{}).Where("expires_at <= ?", now).Count(&n).Error
	return n, err
}

// HardDeleteLogBodiesBatch hard-deletes one batch of an erasing user's archived
// bodies (PIPL erasure cascade, lifecycle.executeErasure's content step).
// Prompts and completions are the most personal data the gateway ever holds,
// so unlike logs (pseudonymized) the rows go entirely. Returns the deleted
// ids; empty means done.
func HardDeleteLogBodiesBatch(ctx context.Context, userID int, batchSize int) ([]int64, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	var ids []int64
	if err := DB.WithContext(ctx).Model(&LogBody{}).Where("user_id = ?", userID).
		Order("id ASC").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := DB.WithContext(ctx).Where("id IN ?", ids).Delete(&LogBody{}).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

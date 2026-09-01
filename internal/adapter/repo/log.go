package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/search"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

// Type aliases pointing to entity package
type Log = entity.Log
type RecordConsumeLogParams = entity.RecordConsumeLogParams
type LogQueryParams = entity.LogQueryParams
type Stat = entity.Stat

// Re-export log type constants from entity
const (
	LogTypeUnknown = entity.LogTypeUnknown
	LogTypeTopup   = entity.LogTypeTopup
	LogTypeConsume = entity.LogTypeConsume
	LogTypeManage  = entity.LogTypeManage
	LogTypeSystem  = entity.LogTypeSystem
	LogTypeError   = entity.LogTypeError
	LogTypeRefund  = entity.LogTypeRefund
)

// ============================================================================
// Tenant scoping — structural enforcement for the logs table
// ============================================================================
//
// The logs table is deliberately NOT covered by TenantPlugin auto-scoping
// (see hasTenantIDColumn in tenant_plugin.go):
//   - the plugin only engages on tenant-bound context handles (WithTenantID /
//     GetTenantDB), which no log call site uses, so listing "logs" there would
//     add zero read protection;
//   - its beforeCreate callback errors when the tenant context is missing,
//     which would break every bare-handle log write (RecordLog /
//     RecordConsumeLog / RecordErrorLog — the relay hot path);
//   - the plugin is registered on DB only (repo/main.go), while LOG_DB may be
//     a separate database (LOG_SQL_DSN) that never has the plugin at all.
//
// Isolation for logs is therefore enforced HERE, structurally: every exported
// query function in this file is either principal-scoped (filtered by user_id
// / token_id / token key — globally unique principals that cannot cross
// tenants) or requires an explicit TenantScope argument. Forgetting the
// tenant decision is a compile error, not a silent cross-tenant leak.

// TenantScope is the mandatory tenant decision for cross-user log queries.
// Construct it with ForTenant or AllTenantsForAdmin — the zero value is
// fail-closed (matches no rows).
type TenantScope struct {
	tenantID   string
	allTenants bool
}

// ForTenant scopes a log query to a single tenant. An empty tenantID matches
// no rows (every log row carries a non-empty tenant_id, default 'default'),
// so a missing tenant fails closed instead of leaking all tenants.
func ForTenant(tenantID string) TenantScope {
	return TenantScope{tenantID: tenantID}
}

// AllTenantsForAdmin deliberately spans every tenant. Reserve it for
// platform-admin surfaces (v1 admin console, root analytics) and system
// tasks (retention cleanup) — never for caller-facing views.
func AllTenantsForAdmin() TenantScope {
	return TenantScope{allTenants: true}
}

// apply adds the scope's tenant filter to a logs query.
func (s TenantScope) apply(tx *gorm.DB) *gorm.DB {
	if s.allTenants {
		return tx
	}
	return tx.Where("tenant_id = ?", s.tenantID)
}

// resolveLogTenantID picks the tenant to stamp on a log row being written:
// the gin context's tenant_id when present (v1 session auth and the v2
// tenant-slug middleware both set it), otherwise the owning user's tenant.
// The fallback matters on the plain /v1 relay path: TokenAuth injects no
// tenant context, so before this fallback every such row was silently
// stamped 'default' even when the token belonged to another tenant —
// polluting the default tenant's log views and hiding the rows from the
// owning tenant's. System rows (userId 0) keep the 'default' stamp.
func resolveLogTenantID(ginTenantID string, userId int) string {
	if ginTenantID != "" {
		return ginTenantID
	}
	if userId > 0 {
		if uc, err := GetUserCache(userId); err == nil && uc.TenantId != "" {
			return uc.TenantId
		}
	}
	return "default"
}

func formatUserLogs(logs []*Log) {
	for i := range logs {
		logs[i].ChannelName = ""
		// Strip Internal-tier governance struct fields (not visible to regular users).
		logs[i].RequestFingerprint = ""
		logs[i].UpstreamModel = ""
		logs[i].Other = SanitizeOtherForUser(logs[i].Other)
		logs[i].Id = logs[i].Id % 1024
	}
}

// SanitizeOtherForUser returns a log row's Other JSON with every TierInternal
// key stripped (governance classification) — the projection a non-admin caller
// may see. Both the v1 self-log formatting above and the v2 log projection go
// through here so there is exactly one strip list; an empty or unparseable
// payload comes back as "" so callers can omit the field.
func SanitizeOtherForUser(other string) string {
	otherMap, _ := common.StrToMap(other)
	if otherMap == nil {
		return ""
	}
	for _, key := range internalOtherKeys {
		delete(otherMap, key)
	}
	return common.MapToJsonStr(otherMap)
}

// internalOtherKeys lists Other map keys classified as TierInternal that must
// be stripped before returning logs to non-admin users.
var internalOtherKeys = []string{
	"admin_info",
	"model_ratio",
	"group_ratio",
	"completion_ratio",
	"cache_ratio",
	"cache_creation_ratio",
	"model_price",
	"user_group_ratio",
	// "frt" is deliberately NOT here: time to first token is the caller's own
	// request timing, classified TierPublic alongside total_latency_ms. See
	// governance/classification.go — and TestInternalOtherKeys_NoPublicField
	// keeps the two lists from drifting apart again.
	"is_model_mapped",
	"upstream_model_name",
	"web_search_price",
	"web_search_call_count",
	"file_search_price",
	"file_search_call_count",
	"image_ratio",
	"audio_ratio",
	"audio_completion_ratio",
	"audio_input_price",
	"image_generation_call_price",
	"data_flow_source",
	"data_flow_dest",
}

// GetLogByKey returns the history of the ONE token identified by its
// plaintext key, and only when that token belongs to the calling principal.
//
// The ownership predicate lives here rather than in the handler on purpose:
// the key is caller-supplied, so the endpoint's own auth (TokenAuth) proves
// only that SOMEBODY is authenticated, not that the queried key is theirs.
// Enforcing in the repo means a future second call site cannot reopen the
// hole by forgetting the check — passing a caller identity is a compile-time
// obligation, the same discipline TenantScope imposes on the cross-user
// queries above.
//
// Fail-closed rules:
//   - callerUserID <= 0 is denied outright. Provisioned tokens carry
//     UserId = 0, so `target.UserId == callerUserID` would be 0 == 0 for
//     EVERY provisioned token — an equality that is not an identity. Denying
//     is the simpler half of the choice: such callers lose an endpoint they
//     have no console for, instead of gaining each other's spend history.
//   - an empty callerTenantID matches nothing, matching ForTenant's
//     fail-closed convention.
//   - an unknown key and a foreign key return the IDENTICAL shape (both go
//     through deny), so the endpoint is not an existence oracle for other
//     people's keys. Only a genuine infrastructure error is surfaced.
//
// The token lookup is Unscoped because a caller who revoked (soft-deleted)
// their own key must still be able to read that key's history.
func GetLogByKey(key string, callerUserID int, callerTenantID string) (logs []*Log, err error) {
	separateLogDB := os.Getenv("LOG_SQL_DSN") != ""
	// deny reproduces, byte for byte, what an unknown key returned before this
	// check existed: an empty page on the join path, not-found on the
	// separate-log-DB path.
	deny := func() ([]*Log, error) {
		if separateLogDB {
			return nil, gorm.ErrRecordNotFound
		}
		return []*Log{}, nil
	}
	if callerUserID <= 0 || callerTenantID == "" {
		return deny()
	}
	var tk Token
	if err = DB.Unscoped().Model(&Token{}).Where(logKeyCol+"=?", strings.TrimPrefix(key, "sk-")).First(&tk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return deny()
		}
		return nil, err
	}
	tokenTenant := tk.TenantId
	if tokenTenant == "" {
		// Rows predating the tenant column default land in the default tenant.
		tokenTenant = "default"
	}
	if tk.UserId != callerUserID || tokenTenant != callerTenantID {
		return deny()
	}
	err = LOG_DB.Model(&Log{}).Where("token_id=?", tk.Id).Find(&logs).Error
	formatUserLogs(logs)
	return logs, err
}

// recordLogTx writes an audit log within a DB transaction when LOG_DB == DB (single-database setup).
// When LOG_DB is a separate database, falls back to LOG_DB (best-effort, no cross-DB transaction).
func recordLogTx(tx *gorm.DB, userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	l := &Log{
		UserId:    userId,
		TenantId:  resolveLogTenantID("", userId),
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	db := LOG_DB
	if LOG_DB == DB {
		db = tx
	}
	if err := db.Create(l).Error; err != nil {
		common.SysLog("failed to record log in transaction: " + err.Error())
	} else {
		search.SyncLogAsync(convertLogToSearchLog(l))
	}
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		TenantId:  resolveLogTenantID("", userId),
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	} else {
		// Async sync to Meilisearch
		// 异步同步到 Meilisearch
		search.SyncLogAsync(convertLogToSearchLog(log))
	}
}

// RecordLogWithTenant writes an audit log with explicit tenant_id.
func RecordLogWithTenant(userId int, tenantID string, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		TenantId:  tenantID,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	} else {
		search.SyncLogAsync(convertLogToSearchLog(log))
	}
}

// convertLogToSearchLog converts model.Log to search.Log
// 将 model.Log 转换为 search.Log
func convertLogToSearchLog(log *Log) *search.Log {
	return &search.Log{
		Id:               log.Id,
		CreatedAt:        log.CreatedAt,
		Type:             log.Type,
		UserId:           log.UserId,
		Username:         log.Username,
		TokenId:          log.TokenId,
		TokenName:        log.TokenName,
		ModelName:        log.ModelName,
		Content:          log.Content,
		Quota:            log.Quota,
		PromptTokens:     log.PromptTokens,
		CompletionTokens: log.CompletionTokens,
		UseTime:          log.UseTime,
		IsStream:         log.IsStream,
		ChannelId:        log.ChannelId,
		ChannelName:      log.ChannelName,
		Group:            log.Group,
		Ip:               log.Ip,
		Other:            log.Other,
		ChannelType:      log.ChannelType,
		RelayMode:        log.RelayMode,
		UpstreamModel:    log.UpstreamModel,
		TotalLatencyMs:   log.TotalLatencyMs,
		// Keep in sync with the DB row: a field missing here makes the
		// corresponding Meilisearch filter silently return nothing rather
		// than error, so "search logs by project" would look broken with no
		// clue why.
		ProjectId: log.ProjectId,
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other map[string]interface{}) {
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, content))
	username := c.GetString("username")
	otherStr := common.MapToJsonStr(other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	// Extract governance fields from context for error logs.
	channelType := c.GetInt("channel_type")
	tenantId := resolveLogTenantID(c.GetString("tenant_id"), userId)
	log := &Log{
		UserId:           userId,
		TenantId:         tenantId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		Other:       otherStr,
		ChannelType: channelType,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	} else {
		// Async sync to Meilisearch
		// 异步同步到 Meilisearch
		search.SyncLogAsync(convertLogToSearchLog(log))
	}
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	// P1-3: emit usage metrics here — the one chokepoint every relay path funnels
	// through — BEFORE the log-config guards, so token/quota series are recorded
	// even when consume-log writes are disabled. Provider is derived from the
	// channel type so RecordTokens' (provider, model) labels are populated.
	metricTenant := resolveLogTenantID(c.GetString("tenant_id"), userId)
	metrics.RecordTokens(constant.GetChannelTypeName(params.ChannelType), params.ModelName,
		params.PromptTokens, params.CompletionTokens)
	if params.Quota > 0 {
		// Counter.Add panics on negatives; refund/zero rows must not be recorded.
		metrics.RecordQuotaConsumed(metricTenant, int64(params.Quota))
	}

	if !common.LogConsumeEnabled {
		return
	}
	// Content strategy: skip log record if user opted out via LogDetailLevel="none".
	// NOTE: This only skips the log write — quota deduction and billing settlement
	// have already occurred before RecordConsumeLog is called. Financial records
	// remain intact; only the consume log entry is omitted.
	if params.LogDetailLevel == "none" {
		return
	}
	logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	username := c.GetString("username")
	otherStr := common.MapToJsonStr(params.Other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		TenantId:         metricTenant,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeConsume,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		Other:              otherStr,
		ChannelType:        params.ChannelType,
		RelayMode:          params.RelayMode,
		RequestFingerprint: params.RequestFingerprint,
		UpstreamModel:      params.UpstreamModel,
		TotalLatencyMs:     params.TotalLatencyMs,
		// Cost attribution (migration 029), filled by
		// governance.EnrichLogParams. 0 = unassigned. This value can never be
		// backfilled: whatever is written here is the row's attribution
		// forever.
		ProjectId: params.ProjectId,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	} else {
		// Async sync to Meilisearch
		// 异步同步到 Meilisearch
		search.SyncLogAsync(convertLogToSearchLog(log))
	}
	if common.DataExportEnabled {
		gopool.Go(func() {
			LogQuotaData(userId, username, params.ModelName, params.Quota, common.GetTimestamp(), params.PromptTokens+params.CompletionTokens)
		})
	}
}

// GetAllLogs lists logs across users. scope is the explicit tenant decision:
// the v1 platform-admin console passes AllTenantsForAdmin(); tenant-facing
// callers must pass ForTenant.
func GetAllLogs(scope TenantScope, logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, group string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = scope.apply(LOG_DB)
	} else {
		tx = scope.apply(LOG_DB.Where("logs.type = ?", logType))
	}

	if modelName != "" {
		tx = tx.Where("logs.model_name like ?", modelName)
	}
	if username != "" {
		tx = tx.Where("logs.username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order("logs.id desc").Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
			return logs, total, err
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return logs, total, err
}

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int, group string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB.Where("logs.user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("logs.user_id = ? and logs.type = ?", userId, logType)
	}

	if modelName != "" {
		tx = tx.Where("logs.model_name like ?", modelName)
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order("logs.id desc").Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}

	formatUserLogs(logs)
	return logs, total, err
}

// logKeywordCondition matches keyword against the integer `type` column only
// when it parses as a number: on PostgreSQL, binding an arbitrary string
// against an integer column raises 22P02 ("invalid input syntax for type
// integer") and aborts the whole query, so non-numeric keywords must only hit
// the content LIKE arm.
func logKeywordCondition(tx *gorm.DB, keyword string) *gorm.DB {
	if logType, parseErr := strconv.Atoi(keyword); parseErr == nil {
		return tx.Where("type = ? or content LIKE ?", logType, keyword+"%")
	}
	return tx.Where("content LIKE ?", keyword+"%")
}

// SearchAllLogs searches logs across users. scope is the explicit tenant
// decision (see GetAllLogs).
func SearchAllLogs(scope TenantScope, keyword string) (logs []*Log, err error) {
	err = logKeywordCondition(scope.apply(LOG_DB), keyword).Order("id desc").Limit(common.MaxRecentItems).Find(&logs).Error
	return logs, err
}

func SearchUserLogs(userId int, keyword string) (logs []*Log, err error) {
	err = logKeywordCondition(LOG_DB.Where("user_id = ?", userId), keyword).Order("id desc").Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs)
	return logs, err
}

// SumUsedQuota aggregates quota/rpm/tpm across users. scope is the explicit
// tenant decision — the username filter alone is NOT an isolation boundary
// (usernames are not guaranteed unique across tenants).
func SumUsedQuota(scope TenantScope, logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string) (stat Stat) {
	tx := scope.apply(LOG_DB.Table("logs")).Select("sum(quota) quota")

	// 为rpm和tpm创建单独的查询
	rpmTpmQuery := scope.apply(LOG_DB.Table("logs")).Select("count(*) rpm, sum(prompt_tokens) + sum(completion_tokens) tpm")

	if username != "" {
		tx = tx.Where("username = ?", username)
		rpmTpmQuery = rpmTpmQuery.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
		rpmTpmQuery = rpmTpmQuery.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name like ?", modelName)
		rpmTpmQuery = rpmTpmQuery.Where("model_name like ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
		rpmTpmQuery = rpmTpmQuery.Where("channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
		rpmTpmQuery = rpmTpmQuery.Where(logGroupCol+" = ?", group)
	}

	tx = tx.Where("type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("type = ?", LogTypeConsume)

	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 执行查询
	// Scan rpm/tpm into a separate struct: GORM's Scan resets the whole
	// destination struct per call, so scanning both queries into `stat`
	// zeroed Quota on the second scan (quota was always returned as 0).
	tx.Scan(&stat)
	var rate Stat
	rpmTpmQuery.Scan(&rate)
	stat.Rpm = rate.Rpm
	stat.Tpm = rate.Tpm

	return stat
}

// SumUsedToken aggregates token counts across users. scope is the explicit
// tenant decision (see SumUsedQuota).
func SumUsedToken(scope TenantScope, logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := scope.apply(LOG_DB.Table("logs")).Select("ifnull(sum(prompt_tokens),0) + ifnull(sum(completion_tokens),0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

// DeleteOldLog batch-deletes logs older than targetTimestamp. scope is the
// explicit tenant decision: retention cleanup (platform-admin) passes
// AllTenantsForAdmin(); a tenant-facing purge must pass ForTenant.
func DeleteOldLog(ctx context.Context, scope TenantScope, targetTimestamp int64, limit int) (int64, error) {
	var total int64 = 0

	for {
		if nil != ctx.Err() {
			return total, ctx.Err()
		}

		result := scope.apply(LOG_DB.Where("created_at < ?", targetTimestamp)).Limit(limit).Delete(&Log{})
		if nil != result.Error {
			return total, result.Error
		}

		total += result.RowsAffected

		if result.RowsAffected < int64(limit) {
			break
		}
	}

	return total, nil
}

// ============================================================================
// V2 API Log Query Functions with Tenant Support
// ============================================================================

// GetUserLogsWithParams retrieves logs for a user with tenant isolation.
// scope replaces the former params.TenantID soft filter (an empty TenantID
// silently meant "all tenants" — fail-open); params.TenantID is now ignored.
func GetUserLogsWithParams(scope TenantScope, params *LogQueryParams) (logs []*Log, total int64, err error) {
	tx := scope.apply(LOG_DB.Model(&Log{}))

	// Apply user filter
	if params.UserID > 0 {
		tx = tx.Where("user_id = ?", params.UserID)
	}

	// Apply type filter
	if params.LogType > 0 {
		tx = tx.Where("type = ?", params.LogType)
	}

	// Apply model name filter
	if params.ModelName != "" {
		tx = tx.Where("model_name = ?", params.ModelName)
	}

	// Apply time range filters
	if params.StartTime > 0 {
		tx = tx.Where("created_at >= ?", params.StartTime)
	}
	if params.EndTime > 0 {
		tx = tx.Where("created_at <= ?", params.EndTime)
	}

	// Apply token name filter
	if params.TokenName != "" {
		tx = tx.Where("token_name = ?", params.TokenName)
	}

	// Cursor filter for live-tail: only rows strictly newer than the given id.
	// id is indexed and monotonic, so this is a cheap "give me what's new" probe.
	if params.AfterID > 0 {
		tx = tx.Where("id > ?", params.AfterID)
	}

	// Cost-attribution filter (migration 029). > 0 only: 0 means "no filter".
	// NOTE: logs.project_id carries no index by design — see the field comment
	// in entity/log.go. This filter always runs alongside the tenant/user
	// clauses, which are indexed, so it narrows an already-bounded scan.
	if params.ProjectID > 0 {
		tx = tx.Where("project_id = ?", params.ProjectID)
	}

	// Count total matching records
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// Apply pagination and fetch results
	err = tx.Order("created_at DESC").Offset(params.Offset).Limit(params.Limit).Find(&logs).Error
	return logs, total, err
}

// GetTenantLogsWithParams retrieves all logs for a tenant (tenant-admin view).
// scope replaces the former params.TenantID soft filter (an empty TenantID
// silently meant "all tenants" — fail-open); params.TenantID is now ignored.
func GetTenantLogsWithParams(scope TenantScope, params *LogQueryParams) (logs []*Log, total int64, err error) {
	tx := scope.apply(LOG_DB.Model(&Log{}))

	// Apply type filter
	if params.LogType > 0 {
		tx = tx.Where("type = ?", params.LogType)
	}

	// Apply model name filter
	if params.ModelName != "" {
		tx = tx.Where("model_name = ?", params.ModelName)
	}

	// Apply time range filters
	if params.StartTime > 0 {
		tx = tx.Where("created_at >= ?", params.StartTime)
	}
	if params.EndTime > 0 {
		tx = tx.Where("created_at <= ?", params.EndTime)
	}

	// Apply token name filter
	if params.TokenName != "" {
		tx = tx.Where("token_name = ?", params.TokenName)
	}

	// Apply username filter
	if params.Username != "" {
		tx = tx.Where("username = ?", params.Username)
	}

	// Cost-attribution filter (migration 029). > 0 only: 0 means "no filter".
	if params.ProjectID > 0 {
		tx = tx.Where("project_id = ?", params.ProjectID)
	}

	// Count total matching records
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// Apply pagination and fetch results
	err = tx.Order("created_at DESC").Offset(params.Offset).Limit(params.Limit).Find(&logs).Error
	return logs, total, err
}

// GetUserLogsInternal returns paginated logs for a user (internal API, no tenant filter).
func GetUserLogsInternal(userID, offset, limit int) (logs []*Log, total int64, err error) {
	tx := LOG_DB.Model(&Log{}).Where("user_id = ?", userID)
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order("created_at DESC").Offset(offset).Limit(limit).Find(&logs).Error
	return logs, total, err
}

// GetTokenLogsInternal returns paginated logs filtered by token ID (internal API).
func GetTokenLogsInternal(tokenID, offset, limit int) (logs []*Log, total int64, err error) {
	tx := LOG_DB.Model(&Log{}).Where("token_id = ?", tokenID)
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order("created_at DESC").Offset(offset).Limit(limit).Find(&logs).Error
	return logs, total, err
}

// LogStatEntry holds aggregated log statistics.
type LogStatEntry struct {
	Key        string `json:"key"`
	Count      int64  `json:"count"`
	TotalQuota int64  `json:"total_quota"`
}

// GetUserLogStatByPeriod returns consume-usage stats filtered by time period
// and grouped by model. created_at is a unix-epoch bigint (see
// GetUserLogStatInternal below); the caller passes a time.Time, so this
// function is responsible for the .Unix() conversion — binding time.Time
// directly into the Where clause makes PostgreSQL reject the query with
// 22P02 (invalid_text_representation). Only LogTypeConsume rows are
// included: topup/manage rows are written with an EMPTY model_name
// (RecordLog / RecordLogWithTenant, log.go:199-220/223-, build Log{}
// without setting Quota at all, so those rows are Quota==0, not "huge" —
// their actual problem is that grouping by model_name would add a spurious
// empty-key group to the result), and LogTypeError rows carry a real
// ModelName but were never billed, so including them would inflate that
// model's count with requests that cost nothing.
func GetUserLogStatByPeriod(userID int, since time.Time) ([]LogStatEntry, error) {
	var results []LogStatEntry
	err := LOG_DB.Model(&Log{}).
		Select("model_name as key, COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota").
		Where("user_id = ? AND type = ? AND created_at >= ?", userID, LogTypeConsume, since.Unix()).
		Group("model_name").
		Order("total_quota DESC").
		Find(&results).Error
	return results, err
}

// GetUserLogStatInternal returns aggregated usage stats (by model or day).
func GetUserLogStatInternal(userID int, groupBy string) ([]LogStatEntry, error) {
	var results []LogStatEntry
	var selectExpr, groupExpr string
	switch groupBy {
	case "day":
		// created_at is a unix-epoch bigint: PG has no DATE(bigint), so it
		// needs the TO_TIMESTAMP conversion. The SQLite arm exists only for
		// the hermetic unit-test tier (same convention as v2_log_cluster.go).
		var dayExpr string
		if common.UsingPostgreSQL {
			dayExpr = "TO_CHAR(TO_TIMESTAMP(created_at), 'YYYY-MM-DD')"
		} else {
			dayExpr = "strftime('%Y-%m-%d', datetime(created_at, 'unixepoch'))"
		}
		selectExpr = dayExpr + " as key, COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota"
		groupExpr = dayExpr
	default:
		selectExpr = "model_name as key, COUNT(*) as count, COALESCE(SUM(quota), 0) as total_quota"
		groupExpr = "model_name"
	}
	err := LOG_DB.Model(&Log{}).
		Select(selectExpr).
		Where("user_id = ?", userID).
		Group(groupExpr).
		Order("total_quota DESC").
		Find(&results).Error
	return results, err
}

package repo

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/search"

	"gorm.io/gorm"
)

// log_attribution.go — the enterprise attribution query filter (migration 045),
// plus convertLogToSearchLog moved out of log.go unchanged (source-size ratchet).

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
		ProjectId:   log.ProjectId,
		EmployeeRef: log.EmployeeRef,
	}
}

// ApplyLogAttributionFilters narrows a logs query by the enterprise
// attribution columns (migration 045). projectIDs == nil leaves the query
// alone; a non-nil EMPTY slice matches nothing (fail closed — a department
// lead without a project must not see every row). employeeRef is an exact
// match; empty = no filter. The caller supplies the tenant clause (via
// TenantScope); both filters are served by the indexes of migration 047.
func ApplyLogAttributionFilters(tx *gorm.DB, projectIDs []int, employeeRef string) *gorm.DB {
	if projectIDs != nil {
		if len(projectIDs) == 0 {
			tx = tx.Where("1 = 0")
		} else {
			tx = tx.Where("project_id IN ?", projectIDs)
		}
	}
	if employeeRef != "" {
		tx = tx.Where("employee_ref = ?", employeeRef)
	}
	return tx
}

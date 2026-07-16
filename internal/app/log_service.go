package app

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/search"
)

// LogSearchParams represents the unified parameters for log search operations.
type LogSearchParams struct {
	Keyword        string
	UserId         int
	Username       string
	LogType        int
	StartTimestamp int64
	EndTimestamp    int64
	ModelName      string
	TokenName      string
	ChannelID      int
	Group          string
	Page           int
	PageSize       int
}

// LogSearchResult holds the results of a log search.
type LogSearchResult struct {
	Items interface{}
	Total int64
	Page  int
}

// SearchLogs performs a log search using Meilisearch with DB fallback.
// Works for both admin-level (all logs) and user-level searches.
func SearchLogs(params LogSearchParams) (*LogSearchResult, error) {
	// Try Meilisearch first if enabled
	if search.IsEnabled() {
		page := params.Page
		if page == 0 {
			page = 1
		}
		pageSize := params.PageSize
		if pageSize == 0 {
			pageSize = 10
		}

		searchParams := search.SearchLogsParams{
			Keyword:        params.Keyword,
			Type:           params.LogType,
			StartTimestamp: params.StartTimestamp,
			EndTimestamp:   params.EndTimestamp,
			Username:       params.Username,
			TokenName:      params.TokenName,
			ModelName:      params.ModelName,
			ChannelID:      params.ChannelID,
			Group:          params.Group,
			Page:           page,
			PageSize:       pageSize,
		}

		logs, total, err := search.SearchLogs(searchParams)
		if err == nil {
			return &LogSearchResult{
				Items: logs,
				Total: total,
				Page:  page,
			}, nil
		}

		// Log error but fall back to database
		common.SysLog("Meilisearch search failed, falling back to database: " + err.Error())
	}

	// Fallback to database search
	if params.UserId > 0 {
		logs, err := repo.SearchUserLogs(params.UserId, params.Keyword)
		if err != nil {
			return nil, err
		}
		return &LogSearchResult{Items: logs, Total: int64(len(logs))}, nil
	}

	// Only the v1 platform-admin search path reaches here (the user path
	// returned above): deliberately cross-tenant.
	logs, err := repo.SearchAllLogs(repo.AllTenantsForAdmin(), params.Keyword)
	if err != nil {
		return nil, err
	}
	return &LogSearchResult{Items: logs, Total: int64(len(logs))}, nil
}

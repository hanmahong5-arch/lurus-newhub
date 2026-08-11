package search

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/meilisearch/meilisearch-go"
)

// LogDocument represents a log document in Meilisearch
// 表示 Meilisearch 中的日志文档
type LogDocument struct {
	ID               int    `json:"id"`
	CreatedAt        int64  `json:"created_at"`
	Type             int    `json:"type"`
	UserID           int    `json:"user_id"`
	Username         string `json:"username"`
	TokenID          int    `json:"token_id"`
	TokenName        string `json:"token_name"`
	ModelName        string `json:"model_name"`
	Content          string `json:"content"`
	Quota            int    `json:"quota"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	UseTime          int    `json:"use_time"`
	IsStream         bool   `json:"is_stream"`
	ChannelID        int    `json:"channel_id"`
	ChannelName      string `json:"channel_name"`
	Group            string `json:"group"`
	IP               string `json:"ip"`
	// Governance fields (Public/Internal tier — safe for search index)
	ChannelType    int    `json:"channel_type"`
	RelayMode      int    `json:"relay_mode"`
	UpstreamModel  string `json:"upstream_model,omitempty"`
	TotalLatencyMs int    `json:"total_latency_ms"`
	// Cost-attribution project (migration 029); 0 = unassigned. Registered as
	// a filterable attribute in config.go so per-project log search works.
	ProjectID int `json:"project_id"`
}

// Log represents the log model (to avoid circular import with model package)
// 表示日志模型(避免与 model 包的循环导入)
type Log struct {
	Id               int
	CreatedAt        int64
	Type             int
	UserId           int
	Username         string
	TokenId          int
	TokenName        string
	ModelName        string
	Content          string
	Quota            int
	PromptTokens     int
	CompletionTokens int
	UseTime          int
	IsStream         bool
	ChannelId        int
	ChannelName      string
	Group            string
	Ip               string
	Other            string
	ChannelType      int
	RelayMode        int
	UpstreamModel    string
	TotalLatencyMs   int
	ProjectId        int
}

// ConvertLogToDocument converts a Log to LogDocument
// 将 Log 转换为 LogDocument
func ConvertLogToDocument(log *Log) *LogDocument {
	return &LogDocument{
		ID:               log.Id,
		CreatedAt:        log.CreatedAt,
		Type:             log.Type,
		UserID:           log.UserId,
		Username:         log.Username,
		TokenID:          log.TokenId,
		TokenName:        log.TokenName,
		ModelName:        log.ModelName,
		Content:          log.Content,
		Quota:            log.Quota,
		PromptTokens:     log.PromptTokens,
		CompletionTokens: log.CompletionTokens,
		UseTime:          log.UseTime,
		IsStream:         log.IsStream,
		ChannelID:        log.ChannelId,
		ChannelName:      log.ChannelName,
		Group:            log.Group,
		IP:               log.Ip,
		ChannelType:      log.ChannelType,
		RelayMode:        log.RelayMode,
		UpstreamModel:    log.UpstreamModel,
		TotalLatencyMs:   log.TotalLatencyMs,
		ProjectID:        log.ProjectId,
	}
}

// ConvertDocumentToLog converts LogDocument back to Log
// 将 LogDocument 转换回 Log
func ConvertDocumentToLog(doc *LogDocument) *Log {
	return &Log{
		Id:               doc.ID,
		CreatedAt:        doc.CreatedAt,
		Type:             doc.Type,
		UserId:           doc.UserID,
		Username:         doc.Username,
		TokenId:          doc.TokenID,
		TokenName:        doc.TokenName,
		ModelName:        doc.ModelName,
		Content:          doc.Content,
		Quota:            doc.Quota,
		PromptTokens:     doc.PromptTokens,
		CompletionTokens: doc.CompletionTokens,
		UseTime:          doc.UseTime,
		IsStream:         doc.IsStream,
		ChannelId:        doc.ChannelID,
		ChannelName:      doc.ChannelName,
		Group:            doc.Group,
		Ip:               doc.IP,
		ProjectId:        doc.ProjectID,
		ChannelType:      doc.ChannelType,
		RelayMode:        doc.RelayMode,
		UpstreamModel:    doc.UpstreamModel,
		TotalLatencyMs:   doc.TotalLatencyMs,
	}
}

// IndexLog indexes a single log entry
// 索引单个日志条目
func IndexLog(log *Log) error {
	if !IsEnabled() {
		return nil // Silently skip if not enabled
	}

	doc := ConvertLogToDocument(log)
	indexName := GetIndexName(IndexLogs)
	index := Client.Index(indexName)

	_, err := index.AddDocuments([]LogDocument{*doc}, nil)
	if err != nil {
		if Debug {
			common.SysLog(fmt.Sprintf("Failed to index log %d: %v", log.Id, err))
		}
		return fmt.Errorf("failed to index log: %w", err)
	}

	if Debug {
		common.SysLog(fmt.Sprintf("Indexed log %d successfully", log.Id))
	}
	return nil
}

// IndexLogsBatch indexes multiple logs in batch
// 批量索引多个日志
func IndexLogsBatch(logs []*Log) error {
	if !IsEnabled() || len(logs) == 0 {
		return nil
	}

	docs := make([]LogDocument, len(logs))
	for i, log := range logs {
		docs[i] = *ConvertLogToDocument(log)
	}

	indexName := GetIndexName(IndexLogs)
	index := Client.Index(indexName)

	// Split into batches
	// 分批处理
	for i := 0; i < len(docs); i += SyncBatchSize {
		end := i + SyncBatchSize
		if end > len(docs) {
			end = len(docs)
		}

		batch := docs[i:end]
		_, err := index.AddDocuments(batch, nil)
		if err != nil {
			if Debug {
				common.SysLog(fmt.Sprintf("Failed to index log batch %d-%d: %v", i, end, err))
			}
			return fmt.Errorf("failed to index log batch: %w", err)
		}

		if Debug {
			common.SysLog(fmt.Sprintf("Indexed log batch %d-%d (%d documents)", i, end, len(batch)))
		}
	}

	return nil
}

// SearchLogsParams represents search parameters for logs
// 表示日志搜索参数
type SearchLogsParams struct {
	Keyword        string // Search keyword / 搜索关键词
	Type           int    // Log type / 日志类型
	StartTimestamp int64  // Start timestamp / 开始时间戳
	EndTimestamp   int64  // End timestamp / 结束时间戳
	Username       string // Username filter / 用户名过滤
	TokenName      string // Token name filter / 令牌名过滤
	ModelName      string // Model name filter / 模型名过滤
	ChannelID      int    // Channel ID filter / 通道ID过滤
	Group          string // Group filter / 分组过滤
	Page           int    // Page number (1-based) / 页码(从1开始)
	PageSize       int    // Page size / 每页大小
}

// SearchLogs searches logs with filters and pagination
// 使用过滤和分页搜索日志
func SearchLogs(params SearchLogsParams) ([]*Log, int64, error) {
	if !IsEnabled() {
		return nil, 0, fmt.Errorf("Meilisearch is not enabled")
	}

	indexName := GetIndexName(IndexLogs)
	index := Client.Index(indexName)

	// Build filter string
	// 构建过滤字符串
	filters := buildLogsFilter(params)

	// Calculate offset
	// 计算偏移量
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 {
		params.PageSize = 10
	}
	offset := (params.Page - 1) * params.PageSize

	// Build search request
	// 构建搜索请求
	searchReq := &meilisearch.SearchRequest{
		Limit:  int64(params.PageSize),
		Offset: int64(offset),
		Sort:   []string{"created_at:desc"}, // Sort by creation time descending / 按创建时间降序排序
	}

	// Add filter if exists
	// 如果存在则添加过滤器
	if filters != "" {
		searchReq.Filter = filters
	}

	// Execute search
	// 执行搜索
	searchResp, err := index.Search(params.Keyword, searchReq)
	if err != nil {
		if Debug {
			common.SysLog(fmt.Sprintf("Search logs failed: %v", err))
		}
		return nil, 0, fmt.Errorf("failed to search logs: %w", err)
	}

	// Convert results
	// 转换结果
	logs := make([]*Log, 0, len(searchResp.Hits))
	for _, hit := range searchResp.Hits {
		// Parse hit to LogDocument
		// 解析 hit 为 LogDocument
		doc := &LogDocument{}
		if err := hit.DecodeInto(doc); err == nil {
			logs = append(logs, ConvertDocumentToLog(doc))
		}
	}

	total := searchResp.EstimatedTotalHits
	if searchResp.TotalHits > 0 {
		total = searchResp.TotalHits
	}

	if Debug {
		common.SysLog(fmt.Sprintf("Search logs returned %d results (total: %d)", len(logs), total))
	}

	return logs, total, nil
}

// buildLogsFilter builds a Meilisearch filter string from search parameters
// 从搜索参数构建 Meilisearch 过滤字符串
func buildLogsFilter(params SearchLogsParams) string {
	var filters []string

	// Type filter
	// 类型过滤
	if params.Type > 0 {
		filters = append(filters, fmt.Sprintf("type = %d", params.Type))
	}

	// Time range filter
	// 时间范围过滤
	if params.StartTimestamp > 0 {
		filters = append(filters, fmt.Sprintf("created_at >= %d", params.StartTimestamp))
	}
	if params.EndTimestamp > 0 {
		filters = append(filters, fmt.Sprintf("created_at <= %d", params.EndTimestamp))
	}

	// Username filter
	// 用户名过滤
	if params.Username != "" {
		filters = append(filters, fmt.Sprintf("username = \"%s\"", escapeFilterString(params.Username)))
	}

	// Token name filter
	// 令牌名过滤
	if params.TokenName != "" {
		filters = append(filters, fmt.Sprintf("token_name = \"%s\"", escapeFilterString(params.TokenName)))
	}

	// Model name filter
	// 模型名过滤
	if params.ModelName != "" {
		filters = append(filters, fmt.Sprintf("model_name = \"%s\"", escapeFilterString(params.ModelName)))
	}

	// Channel ID filter
	// 通道ID过滤
	if params.ChannelID > 0 {
		filters = append(filters, fmt.Sprintf("channel_id = %d", params.ChannelID))
	}

	// Group filter
	// 分组过滤
	if params.Group != "" {
		filters = append(filters, fmt.Sprintf("group = \"%s\"", escapeFilterString(params.Group)))
	}

	// Join all filters with AND
	// 用 AND 连接所有过滤器
	return strings.Join(filters, " AND ")
}

// DeleteLogsByIDs deletes logs by their IDs
// 通过ID删除日志
func DeleteLogsByIDs(ids []int) error {
	if !IsEnabled() || len(ids) == 0 {
		return nil
	}

	indexName := GetIndexName(IndexLogs)
	index := Client.Index(indexName)

	// Convert IDs to strings
	// 将ID转换为字符串
	docIDs := make([]string, len(ids))
	for i, id := range ids {
		docIDs[i] = strconv.Itoa(id)
	}

	_, err := index.DeleteDocuments(docIDs, nil)
	if err != nil {
		return fmt.Errorf("failed to delete logs: %w", err)
	}

	if Debug {
		common.SysLog(fmt.Sprintf("Deleted %d logs from search index", len(ids)))
	}

	return nil
}

// ClearLogsIndex clears all documents from logs index
// 清空日志索引的所有文档
func ClearLogsIndex() error {
	if !IsEnabled() {
		return fmt.Errorf("Meilisearch is not enabled")
	}

	indexName := GetIndexName(IndexLogs)
	index := Client.Index(indexName)

	_, err := index.DeleteAllDocuments(nil)
	if err != nil {
		return fmt.Errorf("failed to clear logs index: %w", err)
	}

	common.SysLog("Cleared all documents from logs index")
	return nil
}

// Helper functions for map parsing
// map 解析辅助函数

func getIntFromMap(m map[string]interface{}, key string) int64 {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		}
	}
	return 0
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getBoolFromMap(m map[string]interface{}, key string) bool {
	if val, ok := m[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func escapeFilterString(s string) string {
	// Escape quotes and backslashes in filter strings
	// 转义过滤字符串中的引号和反斜杠
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

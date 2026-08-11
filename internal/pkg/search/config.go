package search

import (
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/meilisearch/meilisearch-go"
)

// Index names constants
// 索引名称常量
const (
	IndexLogs     = "logs"
	IndexUsers    = "users"
	IndexChannels = "channels"
	IndexTokens   = "tokens"
)

// InitializeIndexes creates and configures all Meilisearch indexes
// 创建并配置所有 Meilisearch 索引
func InitializeIndexes() error {
	if !IsEnabled() {
		return fmt.Errorf("Meilisearch is not enabled")
	}

	common.SysLog("Initializing Meilisearch indexes...")

	// Initialize logs index
	// 初始化日志索引
	if err := initializeLogsIndex(); err != nil {
		return fmt.Errorf("failed to initialize logs index: %w", err)
	}

	// Initialize users index
	// 初始化用户索引
	if err := initializeUsersIndex(); err != nil {
		return fmt.Errorf("failed to initialize users index: %w", err)
	}

	// Initialize channels index
	// 初始化通道索引
	if err := initializeChannelsIndex(); err != nil {
		return fmt.Errorf("failed to initialize channels index: %w", err)
	}

	common.SysLog("All Meilisearch indexes initialized successfully")
	return nil
}

// initializeLogsIndex creates and configures the logs index
// 创建并配置日志索引
func initializeLogsIndex() error {
	indexName := GetIndexName(IndexLogs)
	common.SysLog(fmt.Sprintf("Initializing logs index: %s", indexName))

	// Create or get index
	// 创建或获取索引
	index := Client.Index(indexName)

	// Create index with primary key if not exists
	// 如果不存在则创建带主键的索引
	_, err := Client.CreateIndex(&meilisearch.IndexConfig{
		Uid:        indexName,
		PrimaryKey: "id",
	})
	if err != nil {
		// Index might already exist, which is fine
		// 索引可能已存在,这是正常的
		if Debug {
			common.SysLog(fmt.Sprintf("Logs index create: %v (might already exist)", err))
		}
	}

	// Configure searchable attributes
	// 配置可搜索属性
	searchableAttributes := []string{
		"content",      // Log content / 日志内容
		"username",     // Username / 用户名
		"token_name",   // Token name / 令牌名称
		"model_name",   // Model name / 模型名称
		"ip",           // IP address / IP 地址
		"channel_name", // Channel name / 通道名称
	}

	_, err = index.UpdateSearchableAttributes(&searchableAttributes)
	if err != nil {
		return fmt.Errorf("failed to update searchable attributes: %w", err)
	}

	// Configure filterable attributes
	// 配置可过滤属性
	filterableAttributesStr := []string{
		"type",        // Log type / 日志类型
		"created_at",  // Creation timestamp / 创建时间戳
		"user_id",     // User ID / 用户ID
		"token_id",    // Token ID / 令牌ID
		"channel_id",  // Channel ID / 通道ID
		"group",       // Group / 分组
		"is_stream",   // Stream flag / 流式标志
		"quota",       // Quota / 额度
		"model_name",  // Model name / 模型名称 (also filterable)
		"username",    // Username / 用户名 (also filterable)
		"project_id",  // Cost-attribution project / 成本归集项目 (migration 029)
	}
	// Convert to []interface{}
	filterableAttributes := make([]interface{}, len(filterableAttributesStr))
	for i, v := range filterableAttributesStr {
		filterableAttributes[i] = v
	}

	_, err = index.UpdateFilterableAttributes(&filterableAttributes)
	if err != nil {
		return fmt.Errorf("failed to update filterable attributes: %w", err)
	}

	// Configure sortable attributes
	// 配置可排序属性
	sortableAttributes := []string{
		"created_at",         // Creation time / 创建时间
		"quota",              // Quota / 额度
		"use_time",           // Use time / 使用时间
		"prompt_tokens",      // Prompt tokens / 输入Token数
		"completion_tokens",  // Completion tokens / 输出Token数
	}

	_, err = index.UpdateSortableAttributes(&sortableAttributes)
	if err != nil {
		return fmt.Errorf("failed to update sortable attributes: %w", err)
	}

	// Configure ranking rules
	// 配置排序规则
	rankingRules := []string{
		"words",       // Number of matching words / 匹配词数量
		"typo",        // Typo tolerance / 拼写容错
		"proximity",   // Word proximity / 词语接近度
		"attribute",   // Attribute ranking / 属性排序
		"sort",        // Custom sort / 自定义排序
		"exactness",   // Exactness of match / 匹配精确度
	}

	_, err = index.UpdateRankingRules(&rankingRules)
	if err != nil {
		return fmt.Errorf("failed to update ranking rules: %w", err)
	}

	// Configure faceting settings
	// 配置分面设置
	faceting := &meilisearch.Faceting{
		MaxValuesPerFacet: 100,
	}
	_, err = index.UpdateFaceting(faceting)
	if err != nil {
		return fmt.Errorf("failed to update faceting settings: %w", err)
	}

	// Configure pagination
	// 配置分页
	pagination := &meilisearch.Pagination{
		MaxTotalHits: 100000,
	}
	_, err = index.UpdatePagination(pagination)
	if err != nil {
		return fmt.Errorf("failed to update pagination settings: %w", err)
	}

	common.SysLog(fmt.Sprintf("Logs index configured: %s", indexName))
	return nil
}

// initializeUsersIndex creates and configures the users index
// 创建并配置用户索引
func initializeUsersIndex() error {
	indexName := GetIndexName(IndexUsers)
	common.SysLog(fmt.Sprintf("Initializing users index: %s", indexName))

	// Create or get index
	// 创建或获取索引
	index := Client.Index(indexName)

	// Create index with primary key
	// 创建带主键的索引
	_, err := Client.CreateIndex(&meilisearch.IndexConfig{
		Uid:        indexName,
		PrimaryKey: "id",
	})
	if err != nil && Debug {
		common.SysLog(fmt.Sprintf("Users index create: %v (might already exist)", err))
	}

	// Configure searchable attributes
	// 配置可搜索属性
	searchableAttributes := []string{
		"username",     // Username / 用户名
		"email",        // Email / 邮箱
		"display_name", // Display name / 显示名称
	}

	_, err = index.UpdateSearchableAttributes(&searchableAttributes)
	if err != nil {
		return fmt.Errorf("failed to update searchable attributes: %w", err)
	}

	// Configure filterable attributes
	// 配置可过滤属性
	filterableAttributesStr := []string{
		"group",       // Group / 分组
		"role",        // Role / 角色
		"status",      // Status / 状态
		"quota",       // Quota / 额度
		"used_quota",  // Used quota / 已用额度
	}
	// Convert to []interface{}
	filterableAttributes := make([]interface{}, len(filterableAttributesStr))
	for i, v := range filterableAttributesStr {
		filterableAttributes[i] = v
	}

	_, err = index.UpdateFilterableAttributes(&filterableAttributes)
	if err != nil {
		return fmt.Errorf("failed to update filterable attributes: %w", err)
	}

	// Configure sortable attributes
	// 配置可排序属性
	sortableAttributes := []string{
		"quota",       // Quota / 额度
		"used_quota",  // Used quota / 已用额度
		"created_time",// Creation time / 创建时间
	}

	_, err = index.UpdateSortableAttributes(&sortableAttributes)
	if err != nil {
		return fmt.Errorf("failed to update sortable attributes: %w", err)
	}

	common.SysLog(fmt.Sprintf("Users index configured: %s", indexName))
	return nil
}

// initializeChannelsIndex creates and configures the channels index
// 创建并配置通道索引
func initializeChannelsIndex() error {
	indexName := GetIndexName(IndexChannels)
	common.SysLog(fmt.Sprintf("Initializing channels index: %s", indexName))

	// Create or get index
	// 创建或获取索引
	index := Client.Index(indexName)

	// Create index with primary key
	// 创建带主键的索引
	_, err := Client.CreateIndex(&meilisearch.IndexConfig{
		Uid:        indexName,
		PrimaryKey: "id",
	})
	if err != nil && Debug {
		common.SysLog(fmt.Sprintf("Channels index create: %v (might already exist)", err))
	}

	// Configure searchable attributes
	// 配置可搜索属性
	searchableAttributes := []string{
		"name",     // Channel name / 通道名称
		"base_url", // Base URL / 基础URL
		"models",   // Model list / 模型列表
		"tag",      // Tag / 标签
	}

	_, err = index.UpdateSearchableAttributes(&searchableAttributes)
	if err != nil {
		return fmt.Errorf("failed to update searchable attributes: %w", err)
	}

	// Configure filterable attributes
	// 配置可过滤属性
	filterableAttributesStr := []string{
		"type",     // Channel type / 通道类型
		"status",   // Status / 状态
		"group",    // Group / 分组
		"priority", // Priority / 优先级
		"models",   // Models / 模型列表 (for filtering by model)
	}
	// Convert to []interface{}
	filterableAttributes := make([]interface{}, len(filterableAttributesStr))
	for i, v := range filterableAttributesStr {
		filterableAttributes[i] = v
	}

	_, err = index.UpdateFilterableAttributes(&filterableAttributes)
	if err != nil {
		return fmt.Errorf("failed to update filterable attributes: %w", err)
	}

	// Configure sortable attributes
	// 配置可排序属性
	sortableAttributes := []string{
		"priority",    // Priority / 优先级
		"balance",     // Balance / 余额
		"test_time",   // Test time / 测试时间
	}

	_, err = index.UpdateSortableAttributes(&sortableAttributes)
	if err != nil {
		return fmt.Errorf("failed to update sortable attributes: %w", err)
	}

	common.SysLog(fmt.Sprintf("Channels index configured: %s", indexName))
	return nil
}

// GetIndexInfo returns information about an index
// 返回索引信息
func GetIndexInfo(indexName string) (map[string]interface{}, error) {
	if !IsEnabled() {
		return nil, fmt.Errorf("Meilisearch is not enabled")
	}

	fullIndexName := GetIndexName(indexName)
	index := Client.Index(fullIndexName)

	stats, err := index.GetStats()
	if err != nil {
		return nil, fmt.Errorf("failed to get index stats: %w", err)
	}

	info := make(map[string]interface{})
	info["name"] = fullIndexName
	info["documents"] = stats.NumberOfDocuments
	info["indexing"] = stats.IsIndexing
	info["field_distribution"] = stats.FieldDistribution

	return info, nil
}

// DeleteIndex deletes an index (use with caution!)
// 删除索引(谨慎使用!)
func DeleteIndex(indexName string) error {
	if !IsEnabled() {
		return fmt.Errorf("Meilisearch is not enabled")
	}

	fullIndexName := GetIndexName(indexName)
	common.SysLog(fmt.Sprintf("Deleting index: %s", fullIndexName))

	_, err := Client.DeleteIndex(fullIndexName)
	if err != nil {
		return fmt.Errorf("failed to delete index: %w", err)
	}

	common.SysLog(fmt.Sprintf("Index deleted: %s", fullIndexName))
	return nil
}

// RebuildIndex deletes and recreates an index (use for troubleshooting)
// 重建索引(用于故障排除)
func RebuildIndex(indexName string) error {
	if !IsEnabled() {
		return fmt.Errorf("Meilisearch is not enabled")
	}

	common.SysLog(fmt.Sprintf("Rebuilding index: %s", indexName))

	// Delete existing index
	// 删除现有索引
	fullIndexName := GetIndexName(indexName)
	_, _ = Client.DeleteIndex(fullIndexName) // Ignore error if index doesn't exist

	// Reinitialize based on index type
	// 根据索引类型重新初始化
	switch indexName {
	case IndexLogs:
		return initializeLogsIndex()
	case IndexUsers:
		return initializeUsersIndex()
	case IndexChannels:
		return initializeChannelsIndex()
	default:
		return fmt.Errorf("unknown index name: %s", indexName)
	}
}

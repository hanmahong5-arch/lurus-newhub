package handler

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// AsyncGo is package handler's own fire-and-forget spawn seam, same
// convention as repo.AsyncGo (internal/adapter/repo/async.go) and
// app.AsyncGo (internal/app/quota.go). SyncAllChannelsNow below is currently
// the sole call site (grep AsyncGo( under this package): its bare `go
// syncAllChannelModels(...)` outlived whichever test's TestMain swapped
// repo.DB to a fresh *gorm.DB and back — the check inside
// syncAllChannelModels ("if repo.DB == nil") passes at call time but the
// package-level var can still be reassigned or closed by a concurrent
// test's t.Cleanup before GetAllChannels actually runs, panicking on a nil
// or already-closed handle (cycle-8 L10 repair, ruling A-F3). Forcing this
// seam inline in a package's TestMain makes every such spawn finish before
// the request handler returns, eliminating the race. Production is NOT
// behaviourally identical to the bare `go` statement it replaces: the
// default value here is gopool.Go, whose worker recovers a panic in the
// spawned func and logs it (gopool@v0.1.3 util/gopool/worker.go's
// run()) instead of letting it crash the process the way an unrecovered
// panic in a bare `go` statement would.
var AsyncGo = gopool.Go

// GetAllModelsMeta 获取模型列表（分页）
func GetAllModelsMeta(c *gin.Context) {

	pageInfo := common.GetPageQuery(c)
	modelsMeta, err := repo.GetAllModels(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 批量填充附加字段，提升列表接口性能
	enrichModels(modelsMeta, modelScopeForCaller(c))
	var total int64
	repo.DB.Model(&repo.Model{}).Count(&total)

	// 统计供应商计数（全部数据，不受分页影响）
	vendorCounts, _ := repo.GetVendorModelCounts()

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(modelsMeta)
	common.ApiSuccess(c, gin.H{
		"items":         modelsMeta,
		"total":         total,
		"page":          pageInfo.GetPage(),
		"page_size":     pageInfo.GetPageSize(),
		"vendor_counts": vendorCounts,
	})
}

// SearchModelsMeta 搜索模型列表
func SearchModelsMeta(c *gin.Context) {

	keyword := c.Query("keyword")
	vendor := c.Query("vendor")
	pageInfo := common.GetPageQuery(c)

	modelsMeta, total, err := repo.SearchModels(keyword, vendor, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 批量填充附加字段，提升列表接口性能
	enrichModels(modelsMeta, modelScopeForCaller(c))
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(modelsMeta)
	common.ApiSuccess(c, pageInfo)
}

// GetModelMeta 根据 ID 获取单条模型信息
func GetModelMeta(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var m repo.Model
	if err := repo.DB.First(&m, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	enrichModels([]*repo.Model{&m}, modelScopeForCaller(c))
	common.ApiSuccess(c, &m)
}

// CreateModelMeta 新建模型
func CreateModelMeta(c *gin.Context) {
	var m repo.Model
	if err := c.ShouldBindJSON(&m); err != nil {
		common.ApiError(c, err)
		return
	}
	if m.ModelName == "" {
		common.ApiErrorMsg(c, "模型名称不能为空")
		return
	}
	// 名称冲突检查
	if dup, err := repo.IsModelNameDuplicated(0, m.ModelName); err != nil {
		common.ApiError(c, err)
		return
	} else if dup {
		common.ApiErrorMsg(c, "模型名称已存在")
		return
	}

	if err := repo.ModelInsert(&m); err != nil {
		common.ApiError(c, err)
		return
	}
	repo.RefreshPricing()
	common.ApiSuccess(c, &m)
}

// UpdateModelMeta 更新模型
func UpdateModelMeta(c *gin.Context) {
	statusOnly := c.Query("status_only") == "true"

	var m repo.Model
	if err := c.ShouldBindJSON(&m); err != nil {
		common.ApiError(c, err)
		return
	}
	if m.Id == 0 {
		common.ApiErrorMsg(c, "缺少模型 ID")
		return
	}

	if statusOnly {
		// 只更新状态，防止误清空其他字段
		if err := repo.DB.Model(&repo.Model{}).Where("id = ?", m.Id).Update("status", m.Status).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	} else {
		// 名称冲突检查
		if dup, err := repo.IsModelNameDuplicated(m.Id, m.ModelName); err != nil {
			common.ApiError(c, err)
			return
		} else if dup {
			common.ApiErrorMsg(c, "模型名称已存在")
			return
		}

		if err := repo.ModelUpdate(&m); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	repo.RefreshPricing()
	common.ApiSuccess(c, &m)
}

// DeleteModelMeta 删除模型
func DeleteModelMeta(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := repo.DB.Delete(&repo.Model{}, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	repo.RefreshPricing()
	common.ApiSuccess(c, nil)
}

// GetModelsPricingInfo returns pricing source info for the models table rows.
//
// A non-root caller does not get the rows that only ANOTHER tenant's channels
// serve: the models table is written by the channel model-sync worker
// (model_sync_worker.go) as well as by the operator, so a tenant's private
// fine-tune id can land in it, and this endpoint used to echo every name back
// to every tenant admin. A row no enabled channel serves at all is platform
// metadata, not a tenant's, and stays — dropping it would empty this page for
// a tenant with no channels of its own.
func GetModelsPricingInfo(c *gin.Context) {
	var models []repo.Model
	if err := repo.DB.Find(&models).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	type pricingInfo struct {
		ModelName string  `json:"model_name"`
		Source    string  `json:"source"`
		Ratio     float64 `json:"ratio"`
		Family    string  `json:"family,omitempty"`
		BaseRatio float64 `json:"base_ratio,omitempty"`
		Markup    float64 `json:"markup,omitempty"`
	}

	scope := modelScopeForCaller(c)
	var visible, routedByAnyone map[string]struct{}
	if !scope.isRoot {
		visible = pricingModelNameSet(scope.catalogue())
		routedByAnyone = pricingModelNameSet(repo.GetPricing())
	}

	result := make([]pricingInfo, 0, len(models))
	for _, m := range models {
		if !scope.isRoot {
			if _, routed := routedByAnyone[m.ModelName]; routed {
				if _, ok := visible[m.ModelName]; !ok {
					continue
				}
			}
		}
		ps := ratio_setting.GetModelPricingSource(m.ModelName)
		result = append(result, pricingInfo{
			ModelName: m.ModelName,
			Source:    ps.Source,
			Ratio:     ps.Ratio,
			Family:    ps.Family,
			BaseRatio: ps.BaseRatio,
			Markup:    ps.Markup,
		})
	}

	common.ApiSuccess(c, result)
}

// pricingModelNameSet indexes a catalogue by model name.
func pricingModelNameSet(catalogue []repo.Pricing) map[string]struct{} {
	set := make(map[string]struct{}, len(catalogue))
	for _, p := range catalogue {
		set[p.ModelName] = struct{}{}
	}
	return set
}

// SyncAllChannelsNow triggers an immediate model sync for all enabled channels.
// Runs asynchronously so the HTTP response returns immediately.
func SyncAllChannelsNow(c *gin.Context) {
	AsyncGo(func() { syncAllChannelModels(context.Background()) })
	common.ApiSuccess(c, gin.H{"message": "channel model sync started"})
}

// modelCatalogueScope is the "which channels may this caller see" decision for
// the model-meta read handlers, taken once per request by
// modelScopeForCaller. For the list pages (/api/models/, /search, /:id) the
// models table itself is the platform's global catalogue and is not narrowed;
// what is narrowed is the per-row data enrichModels attaches from the
// channels/abilities tables — bound_channels (channel names, i.e. customer
// names) and enable_groups (group names configured for one tenant's
// channels). GetModelsPricingInfo, the third handler that takes this scope,
// additionally drops the rows only another tenant's channels serve.
type modelCatalogueScope struct {
	tenantID string
	isRoot   bool
}

// modelScopeForCaller reuses the v1 discovery scope rule (channel.go) so
// /api/models/* and /api/channel/models_enabled answer the same caller from
// the same set of channels — including the blank-tenant fail-closed fallback.
func modelScopeForCaller(c *gin.Context) modelCatalogueScope {
	tenantID, isRoot := tenantScopeForDiscovery(c)
	return modelCatalogueScope{tenantID: tenantID, isRoot: isRoot}
}

// boundChannels answers the channel-name lookup under this scope.
func (s modelCatalogueScope) boundChannels(modelNames []string) map[string][]repo.BoundChannel {
	if s.isRoot {
		m, _ := repo.GetBoundChannelsByModelsMap(modelNames)
		return m
	}
	m, _ := repo.GetBoundChannelsByModelsMapForTenant(modelNames, s.tenantID)
	return m
}

// catalogue answers the pricing catalogue under this scope: the global one
// for root, the shared ∪ own projection for a tenant caller.
func (s modelCatalogueScope) catalogue() []repo.Pricing {
	if s.isRoot {
		return repo.GetPricing()
	}
	return repo.GetPricingForTenant(s.tenantID)
}

// enableGroupsByModel indexes the scoped catalogue's enable_groups by model
// name. nil for root, whose exact-match path keeps calling
// repo.GetModelEnableGroups unchanged.
func (s modelCatalogueScope) enableGroupsByModel() map[string][]string {
	if s.isRoot {
		return nil
	}
	visible := s.catalogue()
	byModel := make(map[string][]string, len(visible))
	for _, p := range visible {
		byModel[p.ModelName] = p.EnableGroup
	}
	return byModel
}

// enrichModels 批量填充附加信息：端点、渠道、分组、计费类型，避免 N+1 查询
func enrichModels(models []*repo.Model, scope modelCatalogueScope) {
	if len(models) == 0 {
		return
	}

	// 1) 拆分精确与规则匹配
	exactNames := make([]string, 0)
	exactIdx := make(map[string][]int) // modelName -> indices in models
	ruleIndices := make([]int, 0)
	for i, m := range models {
		if m == nil {
			continue
		}
		if m.NameRule == repo.NameRuleExact {
			exactNames = append(exactNames, m.ModelName)
			exactIdx[m.ModelName] = append(exactIdx[m.ModelName], i)
		} else {
			ruleIndices = append(ruleIndices, i)
		}
	}

	// 2) 批量查询精确模型的绑定渠道（按调用者可见的渠道集合）
	channelsByModel := scope.boundChannels(exactNames)
	scopedGroups := scope.enableGroupsByModel()

	// 3) 精确模型：端点从缓存、渠道批量映射、分组/计费类型从缓存
	for name, indices := range exactIdx {
		chs := channelsByModel[name]
		for _, idx := range indices {
			mm := models[idx]
			if mm.Endpoints == "" {
				eps := repo.GetModelSupportEndpointTypes(mm.ModelName)
				if b, err := json.Marshal(eps); err == nil {
					mm.Endpoints = string(b)
				}
			}
			mm.BoundChannels = chs
			if scopedGroups == nil {
				mm.EnableGroups = repo.GetModelEnableGroups(mm.ModelName)
			} else if groups, ok := scopedGroups[mm.ModelName]; ok {
				mm.EnableGroups = groups
			} else {
				// Same empty-slice shape repo.GetModelEnableGroups returns for
				// an unknown model, so the JSON stays [] and never null.
				mm.EnableGroups = make([]string, 0)
			}
			mm.QuotaTypes = repo.GetModelQuotaTypes(mm.ModelName)
		}
	}

	if len(ruleIndices) == 0 {
		return
	}

	// 4) 一次性读取定价缓存，内存匹配所有规则模型（同样按调用者可见的渠道集合，
	// 否则一条 prefix 规则会把别的租户的模型名当成自己的 matched_models 回显）
	pricings := scope.catalogue()

	// 为全部规则模型收集匹配名集合、端点并集、分组并集、配额集合
	matchedNamesByIdx := make(map[int][]string)
	endpointSetByIdx := make(map[int]map[constant.EndpointType]struct{})
	groupSetByIdx := make(map[int]map[string]struct{})
	quotaSetByIdx := make(map[int]map[int]struct{})

	for _, p := range pricings {
		for _, idx := range ruleIndices {
			mm := models[idx]
			var matched bool
			switch mm.NameRule {
			case repo.NameRulePrefix:
				matched = strings.HasPrefix(p.ModelName, mm.ModelName)
			case repo.NameRuleSuffix:
				matched = strings.HasSuffix(p.ModelName, mm.ModelName)
			case repo.NameRuleContains:
				matched = strings.Contains(p.ModelName, mm.ModelName)
			}
			if !matched {
				continue
			}
			matchedNamesByIdx[idx] = append(matchedNamesByIdx[idx], p.ModelName)

			es := endpointSetByIdx[idx]
			if es == nil {
				es = make(map[constant.EndpointType]struct{})
				endpointSetByIdx[idx] = es
			}
			for _, et := range p.SupportedEndpointTypes {
				es[et] = struct{}{}
			}

			gs := groupSetByIdx[idx]
			if gs == nil {
				gs = make(map[string]struct{})
				groupSetByIdx[idx] = gs
			}
			for _, g := range p.EnableGroup {
				gs[g] = struct{}{}
			}

			qs := quotaSetByIdx[idx]
			if qs == nil {
				qs = make(map[int]struct{})
				quotaSetByIdx[idx] = qs
			}
			qs[p.QuotaType] = struct{}{}
		}
	}

	// 5) 汇总所有匹配到的模型名称，批量查询一次渠道
	allMatchedSet := make(map[string]struct{})
	for _, names := range matchedNamesByIdx {
		for _, n := range names {
			allMatchedSet[n] = struct{}{}
		}
	}
	allMatched := make([]string, 0, len(allMatchedSet))
	for n := range allMatchedSet {
		allMatched = append(allMatched, n)
	}
	matchedChannelsByModel := scope.boundChannels(allMatched)

	// 6) 回填每个规则模型的并集信息
	for _, idx := range ruleIndices {
		mm := models[idx]

		// 端点并集 -> 序列化
		if es, ok := endpointSetByIdx[idx]; ok && mm.Endpoints == "" {
			eps := make([]constant.EndpointType, 0, len(es))
			for et := range es {
				eps = append(eps, et)
			}
			if b, err := json.Marshal(eps); err == nil {
				mm.Endpoints = string(b)
			}
		}

		// 分组并集
		if gs, ok := groupSetByIdx[idx]; ok {
			groups := make([]string, 0, len(gs))
			for g := range gs {
				groups = append(groups, g)
			}
			mm.EnableGroups = groups
		}

		// 配额类型集合（保持去重并排序）
		if qs, ok := quotaSetByIdx[idx]; ok {
			arr := make([]int, 0, len(qs))
			for k := range qs {
				arr = append(arr, k)
			}
			sort.Ints(arr)
			mm.QuotaTypes = arr
		}

		// 渠道并集
		names := matchedNamesByIdx[idx]
		channelSet := make(map[string]repo.BoundChannel)
		for _, n := range names {
			for _, ch := range matchedChannelsByModel[n] {
				key := ch.Name + "_" + strconv.Itoa(ch.Type)
				channelSet[key] = ch
			}
		}
		if len(channelSet) > 0 {
			chs := make([]repo.BoundChannel, 0, len(channelSet))
			for _, ch := range channelSet {
				chs = append(chs, ch)
			}
			mm.BoundChannels = chs
		}

		// 匹配信息
		mm.MatchedModels = names
		mm.MatchedCount = len(names)
	}
}

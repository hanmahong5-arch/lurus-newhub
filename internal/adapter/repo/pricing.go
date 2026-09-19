package repo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// Pricing is a type alias for entity.Pricing (canonical definition in domain/entity/pricing.go)
type Pricing = entity.Pricing

// PricingVendor is a type alias for entity.PricingVendor (canonical definition in domain/entity/pricing.go)
type PricingVendor = entity.PricingVendor

var (
	pricingMap           []Pricing
	vendorsList          []PricingVendor
	supportedEndpointMap map[string]common.EndpointInfo
	lastGetPricingTime   time.Time
	updatePricingLock    sync.Mutex

	// pricingOwnerGroups[model name][channel tenant id] lists the groups
	// that tenant's channels serve the model in. Built by updatePricing
	// next to pricingMap and read by GetPricingForTenant, which is how one
	// process-wide catalogue can answer per-tenant without a second query.
	// Guarded by updatePricingLock.
	pricingOwnerGroups = make(map[string]map[string][]string)

	// 缓存映射：模型名 -> 启用分组 / 计费类型
	modelEnableGroups     = make(map[string][]string)
	modelQuotaTypeMap     = make(map[string]int)
	modelEnableGroupsLock = sync.RWMutex{}
)

var (
	modelSupportEndpointTypes = make(map[string][]constant.EndpointType)
	modelSupportEndpointsLock = sync.RWMutex{}
)

// InvalidatePricingCache forces the next GetPricing/GetVendors call to
// rebuild the catalogue instead of serving the ~1-minute cache. Called after
// a versioned pricing write commits (handler v2_pricing_write.go,
// pricing_write_legacy.go) so a console refetch right after a save shows the
// ratios just written.
func InvalidatePricingCache() {
	updatePricingLock.Lock()
	defer updatePricingLock.Unlock()
	lastGetPricingTime = time.Time{}
}

// PricingCacheAge reports how long ago the catalogue cache was rebuilt; a
// zero lastGetPricingTime (never built, or invalidated) reads as very old.
// Test seam for the invalidation contract.
func PricingCacheAge() time.Duration {
	updatePricingLock.Lock()
	defer updatePricingLock.Unlock()
	return time.Since(lastGetPricingTime)
}

func GetPricing() []Pricing {
	if time.Since(lastGetPricingTime) > time.Minute*1 || len(pricingMap) == 0 {
		updatePricingLock.Lock()
		defer updatePricingLock.Unlock()
		// Double check after acquiring the lock
		if time.Since(lastGetPricingTime) > time.Minute*1 || len(pricingMap) == 0 {
			modelSupportEndpointsLock.Lock()
			defer modelSupportEndpointsLock.Unlock()
			updatePricing()
		}
	}
	return pricingMap
}

// GetVendors 返回当前定价接口使用到的供应商信息
func GetVendors() []PricingVendor {
	if time.Since(lastGetPricingTime) > time.Minute*1 || len(pricingMap) == 0 {
		// 保证先刷新一次
		GetPricing()
	}
	return vendorsList
}

func GetModelSupportEndpointTypes(model string) []constant.EndpointType {
	if model == "" {
		return make([]constant.EndpointType, 0)
	}
	modelSupportEndpointsLock.RLock()
	defer modelSupportEndpointsLock.RUnlock()
	if endpoints, ok := modelSupportEndpointTypes[model]; ok {
		return endpoints
	}
	return make([]constant.EndpointType, 0)
}

func updatePricing() {
	//modelRatios := common.GetModelRatios()
	enableAbilities, err := GetAllEnableAbilityWithChannels()
	if err != nil {
		common.SysLog(fmt.Sprintf("GetAllEnableAbilityWithChannels error: %v", err))
		return
	}
	// 预加载模型元数据与供应商一次，避免循环查询
	var allMeta []Model
	_ = DB.Find(&allMeta).Error
	metaMap := make(map[string]*Model)
	prefixList := make([]*Model, 0)
	suffixList := make([]*Model, 0)
	containsList := make([]*Model, 0)
	for i := range allMeta {
		m := &allMeta[i]
		if m.NameRule == NameRuleExact {
			metaMap[m.ModelName] = m
		} else {
			switch m.NameRule {
			case NameRulePrefix:
				prefixList = append(prefixList, m)
			case NameRuleSuffix:
				suffixList = append(suffixList, m)
			case NameRuleContains:
				containsList = append(containsList, m)
			}
		}
	}

	// 将非精确规则模型匹配到 metaMap
	for _, m := range prefixList {
		for _, pricingModel := range enableAbilities {
			if strings.HasPrefix(pricingModel.Model, m.ModelName) {
				if _, exists := metaMap[pricingModel.Model]; !exists {
					metaMap[pricingModel.Model] = m
				}
			}
		}
	}
	for _, m := range suffixList {
		for _, pricingModel := range enableAbilities {
			if strings.HasSuffix(pricingModel.Model, m.ModelName) {
				if _, exists := metaMap[pricingModel.Model]; !exists {
					metaMap[pricingModel.Model] = m
				}
			}
		}
	}
	for _, m := range containsList {
		for _, pricingModel := range enableAbilities {
			if strings.Contains(pricingModel.Model, m.ModelName) {
				if _, exists := metaMap[pricingModel.Model]; !exists {
					metaMap[pricingModel.Model] = m
				}
			}
		}
	}

	// 预加载供应商
	var vendors []Vendor
	_ = DB.Find(&vendors).Error
	vendorMap := make(map[int]*Vendor)
	for i := range vendors {
		vendorMap[vendors[i].Id] = &vendors[i]
	}

	// 初始化默认供应商映射
	initDefaultVendorMapping(metaMap, vendorMap, enableAbilities)

	// 构建对前端友好的供应商列表
	vendorsList = make([]PricingVendor, 0, len(vendorMap))
	for _, v := range vendorMap {
		vendorsList = append(vendorsList, PricingVendor{
			ID:          v.Id,
			Name:        v.Name,
			Description: v.Description,
			Icon:        v.Icon,
		})
	}

	modelGroupsMap := make(map[string]*types.Set[string])
	// ownerGroups is modelGroupsMap split by the owning channel's tenant, so
	// GetPricingForTenant can rebuild a per-tenant view of both the model
	// list and each model's enable_groups without re-querying.
	ownerGroups := make(map[string]map[string]*types.Set[string])

	for _, ability := range enableAbilities {
		groups, ok := modelGroupsMap[ability.Model]
		if !ok {
			groups = types.NewSet[string]()
			modelGroupsMap[ability.Model] = groups
		}
		groups.Add(ability.Group)

		owners, ok := ownerGroups[ability.Model]
		if !ok {
			owners = make(map[string]*types.Set[string])
			ownerGroups[ability.Model] = owners
		}
		ownerSet, ok := owners[ability.ChannelTenantId]
		if !ok {
			ownerSet = types.NewSet[string]()
			owners[ability.ChannelTenantId] = ownerSet
		}
		ownerSet.Add(ability.Group)
	}

	//这里使用切片而不是Set，因为一个模型可能支持多个端点类型，并且第一个端点是优先使用端点
	modelSupportEndpointsStr := make(map[string][]string)

	// 先根据已有能力填充原生端点
	for _, ability := range enableAbilities {
		endpoints := modelSupportEndpointsStr[ability.Model]
		channelTypes := common.GetEndpointTypesByChannelType(ability.ChannelType, ability.Model)
		for _, channelType := range channelTypes {
			if !common.StringsContains(endpoints, string(channelType)) {
				endpoints = append(endpoints, string(channelType))
			}
		}
		modelSupportEndpointsStr[ability.Model] = endpoints
	}

	// 再补充模型自定义端点
	for modelName, meta := range metaMap {
		if strings.TrimSpace(meta.Endpoints) == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(meta.Endpoints), &raw); err == nil {
			endpoints := modelSupportEndpointsStr[modelName]
			for k := range raw {
				if !common.StringsContains(endpoints, k) {
					endpoints = append(endpoints, k)
				}
			}
			modelSupportEndpointsStr[modelName] = endpoints
		}
	}

	modelSupportEndpointTypes = make(map[string][]constant.EndpointType)
	for model, endpoints := range modelSupportEndpointsStr {
		supportedEndpoints := make([]constant.EndpointType, 0)
		for _, endpointStr := range endpoints {
			endpointType := constant.EndpointType(endpointStr)
			supportedEndpoints = append(supportedEndpoints, endpointType)
		}
		modelSupportEndpointTypes[model] = supportedEndpoints
	}

	// 构建全局 supportedEndpointMap（默认 + 自定义覆盖）
	supportedEndpointMap = make(map[string]common.EndpointInfo)
	// 1. 默认端点
	for _, endpoints := range modelSupportEndpointTypes {
		for _, et := range endpoints {
			if info, ok := common.GetDefaultEndpointInfo(et); ok {
				if _, exists := supportedEndpointMap[string(et)]; !exists {
					supportedEndpointMap[string(et)] = info
				}
			}
		}
	}
	// 2. 自定义端点（models 表）覆盖默认
	for _, meta := range metaMap {
		if strings.TrimSpace(meta.Endpoints) == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(meta.Endpoints), &raw); err == nil {
			for k, v := range raw {
				switch val := v.(type) {
				case string:
					supportedEndpointMap[k] = common.EndpointInfo{Path: val, Method: "POST"}
				case map[string]interface{}:
					ep := common.EndpointInfo{Method: "POST"}
					if p, ok := val["path"].(string); ok {
						ep.Path = p
					}
					if m, ok := val["method"].(string); ok {
						ep.Method = strings.ToUpper(m)
					}
					supportedEndpointMap[k] = ep
				default:
					// ignore unsupported types
				}
			}
		}
	}

	pricingMap = make([]Pricing, 0)
	for model, groups := range modelGroupsMap {
		pricing := Pricing{
			ModelName:              model,
			EnableGroup:            groups.Items(),
			SupportedEndpointTypes: modelSupportEndpointTypes[model],
		}

		// 补充模型元数据（描述、标签、供应商、状态）
		if meta, ok := metaMap[model]; ok {
			// 若模型被禁用(status!=1)，则直接跳过，不返回给前端
			if meta.Status != 1 {
				continue
			}
			pricing.Description = meta.Description
			pricing.Icon = meta.Icon
			pricing.Tags = meta.Tags
			pricing.VendorID = meta.VendorID
		}
		modelPrice, findPrice := ratio_setting.GetModelPrice(model, false)
		if findPrice {
			pricing.ModelPrice = modelPrice
			pricing.QuotaType = 1
		} else {
			modelRatio, _, _ := ratio_setting.GetModelRatio(model)
			pricing.ModelRatio = modelRatio
			pricing.CompletionRatio = ratio_setting.GetCompletionRatio(model)
			pricing.QuotaType = 0
		}
		pricingMap = append(pricingMap, pricing)
	}

	pricingOwnerGroups = make(map[string]map[string][]string, len(ownerGroups))
	for model, owners := range ownerGroups {
		byOwner := make(map[string][]string, len(owners))
		for owner, groups := range owners {
			items := groups.Items()
			sort.Strings(items)
			byOwner[owner] = items
		}
		pricingOwnerGroups[model] = byOwner
	}

	// 刷新缓存映射，供高并发快速查询
	modelEnableGroupsLock.Lock()
	modelEnableGroups = make(map[string][]string)
	modelQuotaTypeMap = make(map[string]int)
	for _, p := range pricingMap {
		modelEnableGroups[p.ModelName] = p.EnableGroup
		modelQuotaTypeMap[p.ModelName] = p.QuotaType
	}
	modelEnableGroupsLock.Unlock()

	lastGetPricingTime = time.Now()
}

// GetSupportedEndpointMap 返回全局端点到路径的映射
func GetSupportedEndpointMap() map[string]common.EndpointInfo {
	return supportedEndpointMap
}

// sharedChannelTenantIDs are the two channels.tenant_id values that mean
// "platform-shared, every tenant routes through it": the literal "default"
// (entity/channel.go's column default) and the empty string (rows written
// before the column existed). Same pair abilityTenantScope and
// channel_cache.go's tenantCanUseChannel use.
var sharedChannelTenantIDs = []string{"default", ""}

// GetPricingForTenant is GetPricing projected onto the channels tenantID can
// route through: the platform-shared ones plus its own. A model no visible
// channel serves is dropped from the catalogue entirely, and each surviving
// row's EnableGroup lists only the groups the visible channels serve it in —
// so a tenant's private model names and private group names stay out of the
// answer given to anyone else, including the anonymous public catalogue
// (tenantID ""), without a second DB round trip.
//
// The row values other than EnableGroup (ratios, endpoints, vendor, metadata)
// are the shared catalogue's and are copied by value; the JSON field set is
// identical to GetPricing's, so this narrows what is listed without changing
// the response shape any consumer parses.
func GetPricingForTenant(tenantID string) []Pricing {
	// Refresh the shared catalogue if it is stale (GetPricing releases the
	// lock before it returns), then take the catalogue and its owner index
	// together under one lock so the two cannot come from different rebuilds.
	// updatePricing replaces both slices/maps wholesale rather than mutating
	// them in place, so the values read here stay valid after the unlock.
	GetPricing()
	updatePricingLock.Lock()
	all := pricingMap
	owners := pricingOwnerGroups
	updatePricingLock.Unlock()

	visible := make([]Pricing, 0, len(all))
	for _, p := range all {
		groups := visiblePricingGroups(owners[p.ModelName], tenantID)
		if len(groups) == 0 {
			continue
		}
		item := p
		item.EnableGroup = groups
		visible = append(visible, item)
	}
	return visible
}

// visiblePricingGroups unions the groups contributed by the platform-shared
// channels with those contributed by tenantID's own channels, sorted so the
// answer does not depend on map iteration order. An empty result means no
// channel this caller can route through serves the model.
func visiblePricingGroups(byOwner map[string][]string, tenantID string) []string {
	if len(byOwner) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(byOwner))
	appendOwner := func(owner string) {
		for _, g := range byOwner[owner] {
			if _, dup := seen[g]; dup {
				continue
			}
			seen[g] = struct{}{}
			out = append(out, g)
		}
	}
	for _, shared := range sharedChannelTenantIDs {
		appendOwner(shared)
	}
	if tenantID != "" {
		appendOwner(tenantID)
	}
	sort.Strings(out)
	return out
}

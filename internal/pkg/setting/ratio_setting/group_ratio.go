package ratio_setting

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/config"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

var groupRatio = map[string]float64{
	"default": 1,
	"vip":     1,
	"svip":    1,
}

var groupRatioMutex sync.RWMutex

var (
	GroupGroupRatio = map[string]map[string]float64{
		"vip": {
			"edit_this": 0.9,
		},
	}
	groupGroupRatioMutex sync.RWMutex
)

var defaultGroupSpecialUsableGroup = map[string]map[string]string{
	"vip": {
		"append_1":   "vip_special_group_1",
		"-:remove_1": "vip_removed_group_1",
	},
}

// GroupRatioSetting is the hierarchical config registered under
// "group_ratio_setting".
//
// It used to also carry `group_ratio` and `group_group_ratio` fields holding
// the very same map objects as the package variables below, which gave the
// group multipliers — a price — a second write path: the config manager's
// reflect writer, reached by PUT /api/option with key
// "group_ratio_setting.group_ratio". That path took none of the mutexes the
// accessors here take and skipped CheckGroupRatio's non-negative validation.
// The canonical keys "GroupRatio" and "GroupGroupRatio" (repo/option.go's
// dispatch, which routes to UpdateGroupRatioByJSONString /
// UpdateGroupGroupRatioByJSONString) are the write path that remains, and
// repo.updateOptionMap rejects the two retired hierarchical spellings listed
// in its retiredOptionKeys.
//
// group_special_usable_group stays: it is the field the console writes
// (web/src/pages/Setting/Ratio/GroupRatioSettings.jsx:197 and
// web/src/components/settings/RatioSetting.jsx:58), and it is a types.RWMap,
// which carries its own lock.
//
// That lock is only worth anything if the POINTER to it stops moving: this
// field is read with no lock at all on the token-auth path
// (app.GetUserUsableGroups, reached per relay request from
// middleware/auth.go), so republishing the pointer on every option-sync tick
// would be a data race on a per-request read. config.applyConfigMap therefore
// unmarshals a new value INTO this pointee instead of replacing the pointer;
// the pointer word is written once, by init below, before any goroutine
// exists. TestGroupSpecialUsableGroup_PointerIsStableAcrossAPublish pins it.
type GroupRatioSetting struct {
	GroupSpecialUsableGroup *types.RWMap[string, map[string]string] `json:"group_special_usable_group"`
}

var groupRatioSetting GroupRatioSetting

func init() {
	groupSpecialUsableGroup := types.NewRWMap[string, map[string]string]()
	groupSpecialUsableGroup.AddAll(defaultGroupSpecialUsableGroup)

	groupRatioSetting = GroupRatioSetting{
		GroupSpecialUsableGroup: groupSpecialUsableGroup,
	}

	config.GlobalConfig.Register("group_ratio_setting", &groupRatioSetting)
}

// GetGroupRatioSetting returns the registered group-ratio configuration.
//
// The nil repair below cannot fire from any production path — init allocates
// the map and the config writer never replaces the pointer — but it is pinned
// by ratio_coverage_test.go, which nils the field and expects the next call to
// restore it, so it is kept and made lock-ordered: the check is taken under
// the configuration read lock and the repair under the write lock (released
// and re-taken, never nested: config.RLock is not reentrant), re-checking so
// two callers that both saw nil publish one map rather than two.
func GetGroupRatioSetting() *GroupRatioSetting {
	config.RLock()
	initialised := groupRatioSetting.GroupSpecialUsableGroup != nil
	config.RUnlock()

	if !initialised {
		repairGroupSpecialUsableGroup()
	}
	return &groupRatioSetting
}

func repairGroupSpecialUsableGroup() {
	config.Lock()
	defer config.Unlock()

	if groupRatioSetting.GroupSpecialUsableGroup != nil {
		return
	}
	repaired := types.NewRWMap[string, map[string]string]()
	repaired.AddAll(defaultGroupSpecialUsableGroup)
	groupRatioSetting.GroupSpecialUsableGroup = repaired
}

func GetGroupRatioCopy() map[string]float64 {
	groupRatioMutex.RLock()
	defer groupRatioMutex.RUnlock()

	groupRatioCopy := make(map[string]float64)
	for k, v := range groupRatio {
		groupRatioCopy[k] = v
	}
	return groupRatioCopy
}

func ContainsGroupRatio(name string) bool {
	groupRatioMutex.RLock()
	defer groupRatioMutex.RUnlock()

	_, ok := groupRatio[name]
	return ok
}

func GroupRatio2JSONString() string {
	groupRatioMutex.RLock()
	defer groupRatioMutex.RUnlock()

	jsonBytes, err := json.Marshal(groupRatio)
	if err != nil {
		common.SysLog("error marshalling model ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateGroupRatioByJSONString(jsonStr string) error {
	// 同 UpdateModelRatioByJSONString：解析失败不得破坏已生效的分组倍率。
	tmp := make(map[string]float64)
	if err := json.Unmarshal([]byte(jsonStr), &tmp); err != nil {
		return err
	}

	groupRatioMutex.Lock()
	defer groupRatioMutex.Unlock()

	groupRatio = tmp
	return nil
}

func GetGroupRatio(name string) float64 {
	groupRatioMutex.RLock()
	defer groupRatioMutex.RUnlock()

	ratio, ok := groupRatio[name]
	if !ok {
		common.SysLog("group ratio not found: " + name)
		return 1
	}
	return ratio
}

func GetGroupGroupRatio(userGroup, usingGroup string) (float64, bool) {
	groupGroupRatioMutex.RLock()
	defer groupGroupRatioMutex.RUnlock()

	gp, ok := GroupGroupRatio[userGroup]
	if !ok {
		return -1, false
	}
	ratio, ok := gp[usingGroup]
	if !ok {
		return -1, false
	}
	return ratio, true
}

func GroupGroupRatio2JSONString() string {
	groupGroupRatioMutex.RLock()
	defer groupGroupRatioMutex.RUnlock()

	jsonBytes, err := json.Marshal(GroupGroupRatio)
	if err != nil {
		common.SysLog("error marshalling group-group ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateGroupGroupRatioByJSONString(jsonStr string) error {
	// 同 UpdateModelRatioByJSONString：解析失败不得破坏已生效的分组间倍率。
	tmp := make(map[string]map[string]float64)
	if err := json.Unmarshal([]byte(jsonStr), &tmp); err != nil {
		return err
	}

	groupGroupRatioMutex.Lock()
	defer groupGroupRatioMutex.Unlock()

	GroupGroupRatio = tmp
	return nil
}

func CheckGroupRatio(jsonStr string) error {
	checkGroupRatio := make(map[string]float64)
	err := json.Unmarshal([]byte(jsonStr), &checkGroupRatio)
	if err != nil {
		return err
	}
	for name, ratio := range checkGroupRatio {
		if ratio < 0 {
			return errors.New("group ratio must be not less than 0: " + name)
		}
	}
	return nil
}

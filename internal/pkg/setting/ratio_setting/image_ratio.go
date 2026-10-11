package ratio_setting

// image_ratio.go - the image ratio map and its accessors, moved verbatim out
// of model_ratio.go (pure move, cycle 22) so that file stays under its
// source-size ceiling.

import (
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

var defaultImageRatio = map[string]float64{
	"gpt-image-1": 2,
}
var imageRatioMap map[string]float64
var imageRatioMapMutex sync.RWMutex

func ImageRatio2JSONString() string {
	imageRatioMapMutex.RLock()
	defer imageRatioMapMutex.RUnlock()
	jsonBytes, err := common.Marshal(imageRatioMap)
	if err != nil {
		common.SysError("error marshalling cache ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateImageRatioByJSONString(jsonStr string) error {
	// 同 UpdateModelRatioByJSONString：解析失败不得破坏已生效的图片倍率。
	tmp := make(map[string]float64)
	if err := common.Unmarshal([]byte(jsonStr), &tmp); err != nil {
		return err
	}
	imageRatioMapMutex.Lock()
	imageRatioMap = tmp
	imageRatioMapMutex.Unlock()
	return nil
}

// GetImageRatio looks up the raw name first so an operator entry keyed by
// the exact model name still wins, then FormatMatchingModelName's
// normalised name so a gizmo or gemini thinking-budget family entry is not
// silently skipped.
func GetImageRatio(name string) (float64, bool) {
	imageRatioMapMutex.RLock()
	defer imageRatioMapMutex.RUnlock()
	if ratio, ok := imageRatioMap[name]; ok {
		return ratio, true
	}
	if ratio, ok := imageRatioMap[FormatMatchingModelName(name)]; ok {
		return ratio, true
	}
	return 1, false // Default to 1 if not found
}

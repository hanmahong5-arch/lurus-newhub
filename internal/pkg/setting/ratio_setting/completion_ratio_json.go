package ratio_setting

import (
	"encoding/json"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func GetCompletionRatioMap() map[string]float64 {
	CompletionRatioMutex.RLock()
	defer CompletionRatioMutex.RUnlock()
	return CompletionRatio
}

func CompletionRatio2JSONString() string {
	CompletionRatioMutex.RLock()
	defer CompletionRatioMutex.RUnlock()

	jsonBytes, err := json.Marshal(CompletionRatio)
	if err != nil {
		common.SysError("error marshalling completion ratio: " + err.Error())
	}
	return string(jsonBytes)
}

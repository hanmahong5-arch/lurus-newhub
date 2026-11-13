package ratio_setting

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// AudioSecondPrice is the per-second price (same USD-like unit as ModelPrice)
// of audio models, keyed by model name. TokenHub 0.10 bills speech /
// transcription by duration rather than by invented tokens; a model listed
// here is settled by ceil(duration_seconds), a model absent from it keeps
// the legacy token / audio-ratio path.
//
// Absent and an explicit 0 mean different things: absent -> fall back to
// tokens; 0 -> deliberately free. Unknown must never silently become free.
var (
	audioSecondPriceMap   = map[string]float64{}
	audioSecondPriceMutex sync.RWMutex
)

// GetAudioSecondPrice reports the configured per-second price for the model.
func GetAudioSecondPrice(name string) (float64, bool) {
	audioSecondPriceMutex.RLock()
	defer audioSecondPriceMutex.RUnlock()
	price, ok := audioSecondPriceMap[FormatMatchingModelName(name)]
	return price, ok
}

func AudioSecondPrice2JSONString() string {
	audioSecondPriceMutex.RLock()
	defer audioSecondPriceMutex.RUnlock()
	b, err := common.Marshal(audioSecondPriceMap)
	if err != nil {
		common.SysError("error marshalling audio second price: " + err.Error())
		return "{}"
	}
	return string(b)
}

// UpdateAudioSecondPriceByJSONString replaces the table. It parses into a
// temporary map first so a malformed payload never clears the live prices;
// negative and non-finite prices are rejected.
func UpdateAudioSecondPriceByJSONString(jsonStr string) error {
	tmp := make(map[string]float64)
	if err := json.Unmarshal([]byte(jsonStr), &tmp); err != nil {
		return err
	}
	for model, price := range tmp {
		if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return fmt.Errorf("audio second price for %q must be a finite number >= 0, got %v", model, price)
		}
	}
	audioSecondPriceMutex.Lock()
	audioSecondPriceMap = tmp
	audioSecondPriceMutex.Unlock()
	InvalidateExposedDataCache()
	return nil
}

// AudioBillableSeconds is ceil(duration) for metering. Sub-second clips bill
// one second; non-positive duration bills zero (caller decides unreported).
func AudioBillableSeconds(duration float64) int64 {
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0
	}
	return int64(math.Ceil(duration))
}

// AudioSecondsFromLegacyTokens reverses the historical TTS estimate
// (ceil(seconds)/60*1000 tokens per minute) when a handler never stored
// duration on RelayInfo. Returns 0 when tokens cannot imply a duration.
func AudioSecondsFromLegacyTokens(audioTokens int) int64 {
	if audioTokens <= 0 {
		return 0
	}
	// tokens ≈ ceil(sec)/60*1000 ⇒ sec ≈ tokens*60/1000
	return int64(math.Ceil(float64(audioTokens) * 60.0 / 1000.0))
}

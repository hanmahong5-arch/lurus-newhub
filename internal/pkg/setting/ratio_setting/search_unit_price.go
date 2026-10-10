package ratio_setting

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// SearchUnitPrice is the per-search-unit price (in the same USD-like unit as
// ModelPrice) of retrieval models, keyed by model name. Rerank vendors bill
// per "search unit" rather than per token; a model listed here is settled by
// units, a model absent from it keeps token-ratio settlement.
//
// Absent and an explicit 0 mean different things on purpose: absent -> fall
// back to the token path (and to the existing "ratio not set" error if that
// has no price either); 0 -> deliberately free. "Unknown price" must never
// silently become "free".
var (
	searchUnitPriceMap   = map[string]float64{}
	searchUnitPriceMutex sync.RWMutex
)

// SearchUnitDocsPerUnit is the Cohere convention: one search unit covers up to
// 100 documents of one query. units = ceil(documents/100) * queries.
const SearchUnitDocsPerUnit = 100

// SearchUnits returns the billable search units for one rerank call.
// Zero documents or zero queries bill zero units.
func SearchUnits(documents, queries int) int64 {
	if documents <= 0 || queries <= 0 {
		return 0
	}
	perQuery := (documents + SearchUnitDocsPerUnit - 1) / SearchUnitDocsPerUnit
	return int64(perQuery) * int64(queries)
}

// GetSearchUnitPrice reports the configured price for the model.
func GetSearchUnitPrice(name string) (float64, bool) {
	searchUnitPriceMutex.RLock()
	defer searchUnitPriceMutex.RUnlock()
	price, ok := searchUnitPriceMap[FormatMatchingModelName(name)]
	return price, ok
}

func SearchUnitPrice2JSONString() string {
	searchUnitPriceMutex.RLock()
	defer searchUnitPriceMutex.RUnlock()
	b, err := common.Marshal(searchUnitPriceMap)
	if err != nil {
		common.SysError("error marshalling search unit price: " + err.Error())
		return "{}"
	}
	return string(b)
}

// UpdateSearchUnitPriceByJSONString replaces the table. It parses into a
// temporary map first so a malformed or invalid payload never clears the live
// prices; negative and non-finite prices are rejected.
func UpdateSearchUnitPriceByJSONString(jsonStr string) error {
	tmp := make(map[string]float64)
	if err := json.Unmarshal([]byte(jsonStr), &tmp); err != nil {
		return err
	}
	for model, price := range tmp {
		if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return fmt.Errorf("search unit price for %q must be a finite number >= 0, got %v", model, price)
		}
	}
	searchUnitPriceMutex.Lock()
	searchUnitPriceMap = tmp
	searchUnitPriceMutex.Unlock()
	InvalidateExposedDataCache()
	return nil
}

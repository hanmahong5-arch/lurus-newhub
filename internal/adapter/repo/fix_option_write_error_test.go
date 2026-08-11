package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// Regression: UpdateOption used to discard the errors returned by
// DB.FirstOrCreate / DB.Save and report only updateOptionMap's result. A failed
// persist therefore looked like success to the caller while the new value took
// effect in memory only — silently reverting on restart or on the next
// loadOptionsFromDatabase sync of another replica.

// fixOptionProbeKey is deliberately not handled by updateOptionMap's switch, so
// the test touches nothing but common.OptionMap.
const fixOptionProbeKey = "FixOptionWriteErrorProbe"

func fixOptionMapValue(key string) (string, bool) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	v, ok := common.OptionMap[key]
	return v, ok
}

func TestFixUpdateOption_DBWriteErrorSurfacesAndLeavesMemoryIntact(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, fixOptionProbeKey)
		common.OptionMapRWMutex.Unlock()
	})

	// Baseline: a healthy write persists and updates the in-memory map.
	if err := UpdateOption(fixOptionProbeKey, "v1"); err != nil {
		t.Fatalf("UpdateOption(healthy) = %v, want nil", err)
	}
	var persisted Option
	if err := DB.First(&persisted, "key = ?", fixOptionProbeKey).Error; err != nil {
		t.Fatalf("baseline row not persisted: %v", err)
	}
	if persisted.Value != "v1" {
		t.Fatalf("persisted value = %q, want v1", persisted.Value)
	}
	if v, ok := fixOptionMapValue(fixOptionProbeKey); !ok || v != "v1" {
		t.Fatalf("OptionMap = %q,%v want v1,true", v, ok)
	}

	// Make persistence fail the way a dropped table / broken connection would.
	if err := DB.Exec("DROP TABLE options").Error; err != nil {
		t.Fatalf("drop options table: %v", err)
	}

	err := UpdateOption(fixOptionProbeKey, "v2")
	if err == nil {
		t.Error("UpdateOption(failing DB) = nil, want the DB write error")
	}
	if v, _ := fixOptionMapValue(fixOptionProbeKey); v != "v1" {
		t.Errorf("OptionMap = %q after a failed write, want the previous value v1", v)
	}
}

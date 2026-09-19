package repo

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
)

// option_race_test.go — the production-path half of the copy-on-write
// contract that internal/pkg/setting/config/config_cow_test.go pins at the
// reflect layer.
//
// The write side here is the real one: repo.SetOptionMapValue is what
// loadOptionsFromDatabase (and therefore the SyncOptions tick, every
// SYNC_FREQUENCY seconds) and the admin PUT /api/option path both funnel
// through for a hierarchical key. The read side is the real one too:
// model_setting.GetGeminiSafetySetting is called per relay request from
// internal/adapter/provider/gemini (grep "GetGeminiSafetySetting" — the
// non-test callers are relay-gemini.go and gemini's request converters).
//
// Before the copy-on-write change, the writer handed &geminiSettings.
// SafetySettings to encoding/json, which writes entries into the live map,
// so this test met the runtime's "concurrent map read and map write" fatal
// error — an unrecoverable crash of the whole process, which in production
// means every replica that runs the same tick at the same time.
func TestOptionHotReload_ConcurrentGeminiSafetyRead(t *testing.T) {
	restoreOptionMapForTest(t)

	original, err := json.Marshal(model_setting.GetGeminiSettings().SafetySettings)
	if err != nil {
		t.Fatalf("marshal current safety settings: %v", err)
	}
	t.Cleanup(func() {
		if err := SetOptionMapValue("gemini.safety_settings", string(original)); err != nil {
			t.Errorf("restore safety settings: %v", err)
		}
	})

	// Two shapes an operator could alternate between. Both carry "default",
	// so a reader must observe one of those two values rather than the zero
	// value of a half-written map.
	shapes := []string{
		`{"default":"OFF","gemini-a":"OFF","gemini-b":"OFF","gemini-c":"OFF","gemini-d":"OFF"}`,
		`{"default":"BLOCK_NONE","gemini-e":"BLOCK_NONE","gemini-f":"BLOCK_NONE"}`,
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := SetOptionMapValue("gemini.safety_settings", shapes[i%len(shapes)]); err != nil {
				t.Errorf("SetOptionMapValue: %v", err)
				return
			}
		}
	}()

	var bad int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			got := model_setting.GetGeminiSafetySetting("default")
			if got != "OFF" && got != "BLOCK_NONE" {
				bad++
				return
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	if bad != 0 {
		t.Fatalf("a reader observed a partially published safety-settings map %d time(s)", bad)
	}
}

// restoreOptionMapForTest makes common.OptionMap usable for the duration of
// one test and restores its CONTENTS afterwards. The map is nil until
// InitOptionMap runs (common/constants.go declares it without an initialiser),
// and updateOptionMap writes into it unconditionally.
//
// The contents, not the reference: other tests in this package call
// InitOptionMap (sqlite_repo_test.go, sqlite_repo_extra4_test.go), so by the
// time these tests run the map usually exists, and capturing the reference
// would "restore" the very same map with every key this test wrote still in
// it — QuotaPerUnit="abc", ChannelDisableThreshold="", GroupRatio={"default":2}
// and so on. Nothing reads those keys today, so the package is green either
// way; with `-shuffle=on` (this cycle's CI) a later test that does read one
// would fail in some orders and not others, which is the worst failure to
// debug. A deep copy costs one map copy per test.
func restoreOptionMapForTest(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	var previous map[string]string
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	} else {
		previous = make(map[string]string, len(common.OptionMap))
		for key, value := range common.OptionMap {
			previous[key] = value
		}
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
}

// TestRestoreOptionMapForTest_RestoresContentsNotJustTheReference is the oracle
// for the helper above. Capturing the reference instead of the contents leaves
// every key the inner test wrote in the live map, which with -shuffle=on turns
// into a failure that depends on test order.
func TestRestoreOptionMapForTest_RestoresContentsNotJustTheReference(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap["restore-probe-existing"] = "kept"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, "restore-probe-existing")
		common.OptionMapRWMutex.Unlock()
	})

	t.Run("inner", func(t *testing.T) {
		restoreOptionMapForTest(t)
		common.OptionMapRWMutex.Lock()
		common.OptionMap["restore-probe-leaked"] = "written by the inner test"
		common.OptionMapRWMutex.Unlock()
	})

	common.OptionMapRWMutex.RLock()
	_, leaked := common.OptionMap["restore-probe-leaked"]
	kept, present := common.OptionMap["restore-probe-existing"]
	common.OptionMapRWMutex.RUnlock()

	if leaked {
		t.Error("a key written under restoreOptionMapForTest survived the cleanup; the helper restores the reference, not the contents")
	}
	if !present || kept != "kept" {
		t.Errorf("restore-probe-existing = %q (present=%v), want the pre-existing value to survive", kept, present)
	}
}

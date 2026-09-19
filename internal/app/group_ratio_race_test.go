package app

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/config"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

// group_ratio_race_test.go — group_special_usable_group is read per relay
// request and must not have its pointer republished under that read.
//
// The read path is middleware/auth.go -> app.GetUserUsableGroups (group.go:13)
// -> ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(group),
// with no lock: the map is a types.RWMap, which locks its own contents, and
// that is the whole protection. The write path is the option-sync tick
// (repo.SyncOptions -> ConfigManager.LoadFromDB, every SYNC_FREQUENCY seconds
// on every replica) once an admin has saved the Ratio settings page, which
// writes this key.
//
// So the pointer to that RWMap has to stop moving. config.applyConfigMap
// unmarshals a new value INTO the published pointee (whose UnmarshalJSON takes
// the pointee's own write lock) instead of publishing a fresh pointer; the
// pointer word is written once, by ratio_setting's init, before any goroutine
// exists. TestGroupSpecialUsableGroup_PointerIsStableAcrossAPublish is the
// oracle for that and fails the moment the pointer is replaced again.

const (
	groupProbeUser = "cfg-probe-group"
	groupProbeAdd  = "cfg-probe-added-group"
)

// seedGroupSpecialUsableGroup restores whatever the process had in the map
// when the test started, so these tests do not leak state into the rest of the
// package (which reads the same map).
func seedGroupSpecialUsableGroup(t *testing.T) {
	t.Helper()
	live := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	if live == nil {
		t.Fatal("group_special_usable_group is nil; ratio_setting.init did not run")
	}
	previous := live.ReadAll()
	t.Cleanup(func() {
		current := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
		current.Clear()
		current.AddAll(previous)
	})
}

func publishGroupSpecialUsableGroup(t *testing.T, jsonValue string) {
	t.Helper()
	if err := config.GlobalConfig.LoadFromDB(map[string]string{
		"group_ratio_setting.group_special_usable_group": jsonValue,
	}); err != nil {
		t.Fatalf("publish group_special_usable_group: %v", err)
	}
}

func TestGroupSpecialUsableGroup_PointerIsStableAcrossAPublish(t *testing.T) {
	seedGroupSpecialUsableGroup(t)

	before := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup

	publishGroupSpecialUsableGroup(t, `{"`+groupProbeUser+`":{"`+groupProbeAdd+`":"published by the option tick"}}`)

	after := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	if after != before {
		t.Fatalf("the option writer replaced the group_special_usable_group pointer (%p -> %p); that word is read with no lock on every token-auth request, so replacing it is a data race on the relay hot path",
			before, after)
	}

	// The publication still has to take effect, or "the pointer did not move"
	// would be satisfied by doing nothing at all.
	entry, ok := after.Get(groupProbeUser)
	if !ok {
		t.Fatalf("group_special_usable_group has no %q entry after the publish; keys = %v", groupProbeUser, after.ReadAll())
	}
	if entry[groupProbeAdd] != "published by the option tick" {
		t.Fatalf("entry = %v, want the published description", entry)
	}

	// And the production read path has to see it.
	groups := GetUserUsableGroups(groupProbeUser)
	if _, ok := groups[groupProbeAdd]; !ok {
		t.Fatalf("GetUserUsableGroups(%q) = %v, want it to contain the published special group %q", groupProbeUser, groups, groupProbeAdd)
	}
}

// TestGroupSpecialUsableGroup_MalformedValueDoesNotEmptyTheLiveMap pins the
// reason the new value is validated into a throwaway instance first:
// types.RWMap.UnmarshalJSON empties itself before it decodes, so handing it a
// malformed document directly publishes an empty map — every user loses their
// special groups until someone saves the page again.
func TestGroupSpecialUsableGroup_MalformedValueDoesNotEmptyTheLiveMap(t *testing.T) {
	seedGroupSpecialUsableGroup(t)

	publishGroupSpecialUsableGroup(t, `{"`+groupProbeUser+`":{"`+groupProbeAdd+`":"survives"}}`)

	cfg := config.GlobalConfig.Get("group_ratio_setting")
	if cfg == nil {
		t.Fatal("group_ratio_setting is not registered with the config manager")
	}
	// A JSON document of the wrong shape: the field is
	// map[string]map[string]string.
	err := config.UpdateConfigFromMapStrict(cfg, map[string]string{
		"group_special_usable_group": `{"` + groupProbeUser + `":"not-an-object"}`,
	})
	if err == nil {
		t.Fatal("a malformed group_special_usable_group was accepted; it must be reported")
	}

	live := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	entry, ok := live.Get(groupProbeUser)
	if !ok || entry[groupProbeAdd] != "survives" {
		t.Fatalf("the rejected value emptied the live map: Get(%q) = %v (present=%v)", groupProbeUser, entry, ok)
	}
}

// TestGroupSpecialUsableGroup_TokenAuthReadPathSeesOnlyPublishedMaps runs the
// production read path against the production writer.
//
// Stated honestly: with the pointer held still, no sequence of interleavings
// can make this assertion fail, so on a machine without the race detector it
// is a smoke test, not the oracle — the oracle for the pointer is
// TestGroupSpecialUsableGroup_PointerIsStableAcrossAPublish above. Its value
// is in CI's -race job, which reports the unsynchronised pointer read/write
// pair directly if the writer ever starts replacing the pointer again.
func TestGroupSpecialUsableGroup_TokenAuthReadPathSeesOnlyPublishedMaps(t *testing.T) {
	seedGroupSpecialUsableGroup(t)

	shapes := []string{
		`{"` + groupProbeUser + `":{"` + groupProbeAdd + `":"A"}}`,
		`{"` + groupProbeUser + `":{"` + groupProbeAdd + `":"B"}}`,
	}
	publishGroupSpecialUsableGroup(t, shapes[0])

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
			if err := config.GlobalConfig.LoadFromDB(map[string]string{
				"group_ratio_setting.group_special_usable_group": shapes[i%len(shapes)],
			}); err != nil {
				t.Errorf("publish group_special_usable_group: %v", err)
				return
			}
		}
	}()

	var unpublished int
	var sample string
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			groups := GetUserUsableGroups(groupProbeUser)
			desc, ok := groups[groupProbeAdd]
			if !ok || (desc != "A" && desc != "B") {
				sample = desc
				unpublished++
				return
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	if unpublished != 0 {
		t.Fatalf("the token-auth read path observed a special-group description nobody published (%q) %d time(s)", sample, unpublished)
	}
}

// TestRegisteredPointerFieldsDecodeInPlace keeps the in-place rule honest for
// fields that do not exist yet.
//
// config.applyConfigMap only avoids republishing a pointer when the pointee
// unmarshals itself (json.Unmarshaler, which is where types.RWMap's lock
// lives). A pointer field added to a registered config whose type does NOT do
// that gets the old treatment — a new pointer published on every option-sync
// tick — and if anything reads it without a lock, that is the same defect
// group_special_usable_group had. This test enumerates the registered structs
// and fails on such a field.
//
// It covers the modules linked into this test binary, which it prints; the
// module list itself is pinned against the Register call sites by
// TestRegisteredConfigModulesAreTheKnownSet in
// internal/adapter/repo/option_validation_gate_test.go.
func TestRegisteredPointerFieldsDecodeInPlace(t *testing.T) {
	modules := []string{
		"gemini", "claude", "global", "fetch_setting", "group_ratio_setting",
		"console_setting", "checkin_setting", "general_setting", "monitor_setting",
		"quota_setting", "discord", "legal", "oidc",
	}

	jsonUnmarshaler := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

	var linked, pointerFields []string
	for _, name := range modules {
		cfg := config.GlobalConfig.Get(name)
		if cfg == nil {
			continue // not linked into this test binary
		}
		linked = append(linked, name)

		val := reflect.ValueOf(cfg)
		if val.Kind() != reflect.Pointer || val.IsNil() {
			t.Errorf("module %s is registered as %T, not a non-nil pointer to a struct", name, cfg)
			continue
		}
		val = val.Elem()
		if val.Kind() != reflect.Struct {
			continue
		}
		typ := val.Type()
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			if field.Kind() != reflect.Pointer {
				continue
			}
			where := name + "." + typ.Field(i).Name
			pointerFields = append(pointerFields, where)
			if !field.Type().Implements(jsonUnmarshaler) {
				t.Errorf("%s is a pointer field whose type %s does not implement json.Unmarshaler, so every config update republishes the pointer word; either give it a locking type like types.RWMap or make sure nothing reads it without the configuration read lock",
					where, field.Type())
			}
			if field.IsNil() {
				t.Errorf("%s is nil at rest; config.applyConfigMap can only decode in place into a non-nil pointee, so a nil one is republished on the next update", where)
			}
		}
	}

	if len(linked) < 8 {
		t.Fatalf("only %d of the %d registered config modules are linked into this binary (%v); the enumeration is too small to mean anything",
			len(linked), len(modules), linked)
	}
	t.Logf("checked %d linked config modules, %d pointer field(s): %v", len(linked), len(pointerFields), pointerFields)
}

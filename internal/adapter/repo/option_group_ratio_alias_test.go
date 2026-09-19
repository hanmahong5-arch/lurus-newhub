package repo

import (
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// option_group_ratio_alias_test.go — one setting, one write path.
//
// ratio_setting registers a GroupRatioSetting struct with the config manager
// and used to hand that struct the very same map objects the package's own
// mutex-guarded accessors read (GetGroupRatio / ContainsGroupRatio /
// GetGroupRatioCopy all take groupRatioMutex). That gave group ratios — a
// price multiplier — two write paths with different locking:
//
//	"GroupRatio"                        -> UpdateGroupRatioByJSONString, under groupRatioMutex
//	"group_ratio_setting.group_ratio"   -> the reflect writer, no lock at all
//
// The second one is reachable by anyone who can PUT /api/option, is not
// validated by CheckGroupRatio (so a negative multiplier gets in), and writes
// the map while relay goroutines read it. It is now rejected, and the struct
// no longer carries the aliased fields, so there is nothing left to alias.
//
// group_special_usable_group stays on the struct: it is the field of
// this config the console writes (web/src/pages/Setting/Ratio/
// GroupRatioSettings.jsx:197 and web/src/components/settings/
// RatioSetting.jsx:58 are the two frontend references to a
// group_ratio_setting.* key).
func TestOptionGroupRatio_HierarchicalAliasIsRejected(t *testing.T) {
	restoreOptionMapForTest(t)

	previous := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		if err := ratio_setting.UpdateGroupRatioByJSONString(previous); err != nil {
			t.Errorf("restore group ratio: %v", err)
		}
	})

	if err := SetOptionMapValue("GroupRatio", `{"default":2}`); err != nil {
		t.Fatalf("SetOptionMapValue(GroupRatio): %v", err)
	}
	if got := ratio_setting.GetGroupRatio("default"); got != 2 {
		t.Fatalf("GetGroupRatio(default) = %v after the canonical write, want 2", got)
	}

	if err := SetOptionMapValue("group_ratio_setting.group_ratio", `{"default":9}`); err == nil {
		t.Errorf("the retired group_ratio_setting.group_ratio key was accepted; it must be rejected")
	}
	if got := ratio_setting.GetGroupRatio("default"); got != 2 {
		t.Fatalf("GetGroupRatio(default) = %v after the retired key was written, want the canonical 2", got)
	}
}

// TestOptionGroupRatio_RegisteredStructHasNoRatioAliases is the structural
// half: as long as the registered struct carries a field tagged group_ratio
// or group_group_ratio, the reflect writer has a second, unlocked door into
// the ratio maps regardless of what the dispatch above rejects.
func TestOptionGroupRatio_RegisteredStructHasNoRatioAliases(t *testing.T) {
	typ := reflect.TypeOf(*ratio_setting.GetGroupRatioSetting())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "group_ratio" || tag == "group_group_ratio" {
			t.Errorf("GroupRatioSetting still carries field %s (json:%q); the reflect writer can reach the ratio maps through it",
				typ.Field(i).Name, tag)
		}
	}
}

// TestOptionGroupRatio_RetiredKeyIsNotCountedOnEveryTick — a row nobody has
// deleted must not be reported forever.
//
// loadOptionsFromDatabase reads every options row on every SyncOptions tick, on
// every replica. An instance that still has a group_ratio_setting.group_ratio
// row would therefore log a line and increment
// option_parse_rejected_total{key=...} once per key per tick per replica, for
// as long as the row exists — a permanently nonzero rate on a counter whose
// whole purpose is to say "something needs attention", plus a log line every 60
// seconds saying the same thing. The key is ignored with one message per
// process instead, and the counter stays for values that really did fail to
// parse.
func TestOptionGroupRatio_RetiredKeyIsNotCountedOnEveryTick(t *testing.T) {
	restoreOptionMapForTest(t)

	const key = "group_ratio_setting.group_ratio"
	before := testutil.ToFloat64(metrics.OptionParseRejectedTotal.WithLabelValues(key))

	for i := 0; i < 3; i++ {
		if err := SetOptionMapValue(key, "{\"default\":9}"); err == nil {
			t.Fatal("the retired key was accepted")
		}
	}

	after := testutil.ToFloat64(metrics.OptionParseRejectedTotal.WithLabelValues(key))
	if after != before {
		t.Errorf("option_parse_rejected_total{key=%s} went %v -> %v; a retired key is ignored, not counted once per tick", key, before, after)
	}
}

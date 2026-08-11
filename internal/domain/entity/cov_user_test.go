package entity

// cov_user_test.go — business-acceptance tests for User's setting
// serialization, access-token pointer accessors, subscriber-tier gate, and
// the daily-quota reset boundary decision (NeedsDailyResetAt), which is the
// pure clock-injectable core the whole daily-quota-reset feature depends on.

import (
	"math"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestUser_ToBaseUser(t *testing.T) {
	u := &User{
		Id: 7, Group: "vip", Quota: 1000, Status: 1, Username: "alice",
		Setting: `{"notify_type":"email"}`, Email: "a@x.com",
		DailyQuota: 500, DailyUsed: 100, LastDailyReset: 123, BaseGroup: "default", FallbackGroup: "free",
	}
	base := u.ToBaseUser()
	if base.Id != u.Id || base.Group != u.Group || base.Quota != u.Quota || base.Status != u.Status ||
		base.Username != u.Username || base.Setting != u.Setting || base.Email != u.Email ||
		base.DailyQuota != u.DailyQuota || base.DailyUsed != u.DailyUsed ||
		base.LastDailyReset != u.LastDailyReset || base.BaseGroup != u.BaseGroup || base.FallbackGroup != u.FallbackGroup {
		t.Fatalf("ToBaseUser() field mismatch: got %#v from source %#v", base, u)
	}
	// TenantId is intentionally NOT copied by ToBaseUser today (it is not in
	// the source-field list). Assert the current (zero-value) behavior so a
	// silent accidental addition/removal of a field is caught either way.
	if base.TenantId != "" {
		t.Fatalf("ToBaseUser().TenantId = %q, want empty (field not populated by ToBaseUser)", base.TenantId)
	}
}

func TestUser_AccessTokenAccessors(t *testing.T) {
	u := &User{}
	if got := u.GetAccessToken(); got != "" {
		t.Fatalf("GetAccessToken() on nil pointer = %q, want empty", got)
	}
	u.SetAccessToken("tok-abc")
	if got := u.GetAccessToken(); got != "tok-abc" {
		t.Fatalf("GetAccessToken() after SetAccessToken = %q, want tok-abc", got)
	}
}

func TestUser_Setting_RoundTrip(t *testing.T) {
	u := &User{}
	if got := u.GetSetting(); got != (dto.UserSetting{}) {
		t.Fatalf("GetSetting() on unset column = %#v, want zero value", got)
	}

	u.SetSetting(dto.UserSetting{NotifyType: "email", QuotaWarningThreshold: 0.2})
	got := u.GetSetting()
	if got.NotifyType != "email" || got.QuotaWarningThreshold != 0.2 {
		t.Fatalf("GetSetting() round trip mismatch: %#v", got)
	}

	// Malformed persisted JSON must degrade to zero value, not panic.
	u2 := &User{Setting: "{not-json"}
	if got := u2.GetSetting(); got != (dto.UserSetting{}) {
		t.Fatalf("GetSetting() on malformed column = %#v, want zero value", got)
	}
}

func TestUser_SetSetting_MarshalFailureLeavesFieldUntouched(t *testing.T) {
	// QuotaWarningThreshold is a float64; NaN is a real value a buggy upstream
	// computation (e.g. 0/0) could produce, and encoding/json rejects it. The
	// write must fail safe: log and leave the persisted Setting column alone
	// rather than writing corrupt/partial JSON.
	u := &User{Setting: "previous-value-should-survive"}
	u.SetSetting(dto.UserSetting{QuotaWarningThreshold: math.NaN()})
	if u.Setting != "previous-value-should-survive" {
		t.Fatalf("SetSetting on marshal failure overwrote Setting: %q", u.Setting)
	}
}

func TestUserBase_GetSetting(t *testing.T) {
	ub := &UserBase{Setting: `{"notify_type":"webhook"}`}
	got := ub.GetSetting()
	if got.NotifyType != "webhook" {
		t.Fatalf("UserBase.GetSetting() = %#v, want notify_type=webhook", got)
	}

	ub2 := &UserBase{Setting: "{broken"}
	if got := ub2.GetSetting(); got != (dto.UserSetting{}) {
		t.Fatalf("UserBase.GetSetting() on malformed column = %#v, want zero value", got)
	}
}

func TestUser_IsSubscriber(t *testing.T) {
	tests := []struct {
		name string
		role int
		want bool
	}{
		{"guest is not subscriber", common.RoleGuestUser, false},
		{"common user is not subscriber", common.RoleCommonUser, false},
		{"subscriber tier exactly at threshold is subscriber", common.RoleSubscriberUser, true},
		{"admin (role above subscriber) counts as subscriber-or-higher", common.RoleAdminUser, true},
		{"root counts as subscriber-or-higher", common.RoleRootUser, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &User{Role: tt.role}
			if got := u.IsSubscriber(); got != tt.want {
				t.Fatalf("IsSubscriber() role=%d = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestNeedsDailyResetAt(t *testing.T) {
	const day = int64(86400)
	tests := []struct {
		name      string
		lastReset int64
		now       int64
		want      bool
	}{
		{"zero last-reset (never reset) always needs reset", 0, 1000, true},
		{"same UTC day as last reset does not need reset", 10 * day, 10*day + 3600, false},
		{"exactly one day later (next UTC day) needs reset", 10 * day, 11 * day, true},
		{"one second before day boundary does not need reset", 10 * day, 11*day - 1, false},
		{"one second after day boundary needs reset", 10 * day, 11*day + 1, true},
		{"now before last reset (clock skew) does not spuriously need reset", 11 * day, 10 * day, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsDailyResetAt(tt.lastReset, tt.now); got != tt.want {
				t.Fatalf("NeedsDailyResetAt(%d, %d) = %v, want %v", tt.lastReset, tt.now, got, tt.want)
			}
		})
	}
}

func TestNeedsDailyReset_UsesRealClock(t *testing.T) {
	// NeedsDailyReset must delegate to NeedsDailyResetAt using the current
	// wall clock: a lastReset of 0 always needs reset regardless of "now".
	if !NeedsDailyReset(0) {
		t.Fatal("NeedsDailyReset(0) = false, want true (never-reset sentinel)")
	}
	// A lastReset far in the future (relative to any real wall clock) must
	// not need reset — proves the function reads a real "now", not a stub.
	farFuture := common.GetTimestamp() + 100*86400
	if NeedsDailyReset(farFuture) {
		t.Fatal("NeedsDailyReset(farFuture) = true, want false")
	}
}

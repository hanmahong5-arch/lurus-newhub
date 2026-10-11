package openrouter_pool

import (
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// baseChannelErr returns a ChannelError that would pass all guards in
// MaybeMarkCooldown so individual tests can flip one field to trigger a
// specific early-return.
func baseChannelErr() types.ChannelError {
	return types.ChannelError{
		ChannelId:   1,
		ChannelType: constant.ChannelTypeOpenRouter,
		IsMultiKey:  true,
		UsingKey:    "sk-or-v1-test-key",
	}
}

// base429Err returns a *types.NewAPIError carrying a 429 with a Retry-After
// header so ParseCooldownUntil returns a positive value (passes the until<=0
// guard). No upstream body is needed for the guard tests.
func base429Err() *types.NewAPIError {
	h := http.Header{}
	h.Set("Retry-After", "60")
	return &types.NewAPIError{
		StatusCode:     429,
		UpstreamHeader: h,
	}
}

// TestMaybeMarkCooldown_NilApiErr_NoOp verifies that a nil *NewAPIError
// causes an immediate return before any repo interaction.
func TestMaybeMarkCooldown_NilApiErr_NoOp(t *testing.T) {
	// If MaybeMarkCooldown panics or reaches the repo (which would panic on nil
	// DB) this test fails — the nil guard must fire first.
	MaybeMarkCooldown(baseChannelErr(), nil)
}

// markSeam swaps the multi-key write for a recorder and returns the call log.
func markSeam(t *testing.T, ok bool) *[]int {
	t.Helper()
	var calls []int
	prev := markMultiKeyCooldownFn
	markMultiKeyCooldownFn = func(id int, key string, until int64, reason string) (bool, int64) {
		calls = append(calls, id)
		return ok, seamAllParkedUntil
	}
	prevKeep := markMultiKeyCooldownKeepFn
	keepCalls = nil
	markMultiKeyCooldownKeepFn = func(id int, key string, until int64, reason string) (bool, int64) {
		keepCalls = append(keepCalls, id)
		return ok, seamAllParkedUntil
	}
	app.ClearChannelCooldowns()
	t.Cleanup(func() {
		markMultiKeyCooldownFn = prev
		markMultiKeyCooldownKeepFn = prevKeep
		app.ClearChannelCooldowns()
	})
	return &calls
}

func withAutoDisable(t *testing.T) {
	t.Helper()
	prev := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = prev })
}

// TestMaybeMarkCooldown_NonOpenRouterMultiKey_Cools pins the generalisation:
// a 429 on a multi-key channel of ANY type writes the per-key cooldown.
func TestMaybeMarkCooldown_NonOpenRouterMultiKey_Cools(t *testing.T) {
	withAutoDisable(t)
	for _, typ := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeAnthropic, constant.ChannelTypeGemini} {
		calls := markSeam(t, true)
		ce := baseChannelErr()
		ce.AutoBan = true
		ce.ChannelType = typ
		MaybeMarkCooldown(ce, base429Err())
		if len(*calls) != 1 || (*calls)[0] != ce.ChannelId {
			t.Fatalf("type %d: multi-key write calls = %v, want one for channel %d", typ, *calls, ce.ChannelId)
		}
	}
}

// TestMaybeMarkCooldown_BalanceExhausted429_NoCooldown pins that a 429 whose
// body says the account is out of money goes to the disable path, not cooldown.
func TestMaybeMarkCooldown_BalanceExhausted429_NoCooldown(t *testing.T) {
	withAutoDisable(t)
	calls := markSeam(t, true)
	ae := types.WithOpenAIError(types.OpenAIError{
		Message: "You exceeded your current quota", Type: "insufficient_quota", Code: "insufficient_quota",
	}, 429)
	ae.UpstreamHeader = base429Err().UpstreamHeader

	ce := baseChannelErr()
	ce.ChannelType = constant.ChannelTypeOpenAI
	MaybeMarkCooldown(ce, ae)
	single := ce
	single.IsMultiKey = false
	single.ChannelId = 77
	MaybeMarkCooldown(single, ae)

	if len(*calls) != 0 {
		t.Fatalf("balance-exhausted 429 wrote a multi-key cooldown: %v", *calls)
	}
	if app.ChannelCoolingUntil(77, 0) != 0 {
		t.Fatal("balance-exhausted 429 wrote a single-key cooldown")
	}
}

// TestMaybeMarkCooldown_SingleKey_WritesChannelCooldown pins that a single-key
// channel is put on the selection-side cooldown instead of being ignored.
func TestMaybeMarkCooldown_SingleKey_WritesChannelCooldown(t *testing.T) {
	withAutoDisable(t)
	calls := markSeam(t, true)
	ce := baseChannelErr()
	ce.ChannelType = constant.ChannelTypeOpenAI
	ce.IsMultiKey = false
	ce.ChannelId = 78
	MaybeMarkCooldown(ce, base429Err())
	if len(*calls) != 0 {
		t.Fatalf("single-key channel hit the multi-key write: %v", *calls)
	}
	if until := app.ChannelCoolingUntil(78, 0); until <= time.Now().Unix() {
		t.Fatalf("single-key cooldown deadline = %d, want in the future", until)
	}
}

// TestMaybeMarkCooldown_NonRateLimit_NoOp verifies that non-429 status codes
// (e.g. 401 billing errors) skip cooldown marking.
func TestMaybeMarkCooldown_NonRateLimit_NoOp(t *testing.T) {
	ae := base429Err()
	ae.StatusCode = 401
	MaybeMarkCooldown(baseChannelErr(), ae)
}

// TestMaybeMarkCooldown_EmptyUsingKey_NoOp verifies the empty-key guard.
func TestMaybeMarkCooldown_EmptyUsingKey_NoOp(t *testing.T) {
	ce := baseChannelErr()
	ce.UsingKey = ""
	MaybeMarkCooldown(ce, base429Err())
}

// TestMaybeMarkCooldown_ZeroStatusCode_NoOp verifies that a zero/unset
// StatusCode is treated like a non-429 and returns early.
func TestMaybeMarkCooldown_ZeroStatusCode_NoOp(t *testing.T) {
	ae := base429Err()
	ae.StatusCode = 0
	MaybeMarkCooldown(baseChannelErr(), ae)
}

// TestMaybeMarkCooldown_AllGuardsPassed_RequiresDB documents the one path that
// reaches repo.MarkMultiKeyCooldown. It needs a live database: the repo call
// dereferences the package-level GORM handle, so an uninitialised repo.DB
// panics. `testing.Short()` alone is not that condition — a plain
// `go test ./...` is neither short nor DB-initialised — so gate on the handle
// itself and treat short mode as an additional skip.
func TestMaybeMarkCooldown_AllGuardsPassed_RequiresDB(t *testing.T) {
	if testing.Short() {
		t.Skip("requires live database; skip in short mode")
	}
	if repo.DB == nil {
		t.Skip("requires an initialised repo.DB; skipping the repo-reaching path")
	}
	// Under a real DB this would succeed or fail gracefully.
	// Without DB the call panics on the GORM nil-pointer — hence the skip above.
	MaybeMarkCooldown(baseChannelErr(), base429Err())
}

// TestMaybeMarkCooldown_TableDriven exercises all guard permutations in a
// single table to make combinatorial coverage explicit. Every case here is
// safe to call without a database because at least one guard fires before the
// repo call.
func TestMaybeMarkCooldown_TableDriven_Guards(t *testing.T) {
	tests := []struct {
		name string
		ce   types.ChannelError
		ae   *types.NewAPIError
	}{
		{
			name: "nil apiErr",
			ce:   baseChannelErr(),
			ae:   nil,
		},
		{
			name: "status 503",
			ce:   baseChannelErr(),
			ae: &types.NewAPIError{
				StatusCode:     503,
				UpstreamHeader: func() http.Header { h := http.Header{}; h.Set("Retry-After", "60"); return h }(),
			},
		},
		{
			name: "status 200 (not an error at all)",
			ce:   baseChannelErr(),
			ae: &types.NewAPIError{
				StatusCode: 200,
			},
		},
		{
			name: "empty UsingKey",
			ce: types.ChannelError{
				ChannelId:   5,
				ChannelType: constant.ChannelTypeOpenRouter,
				IsMultiKey:  true,
				UsingKey:    "",
			},
			ae: base429Err(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The test passes if MaybeMarkCooldown returns without panicking.
			// Any guard that wasn't hit would reach repo.MarkMultiKeyCooldown
			// and panic on nil DB, making the missing guard instantly visible.
			MaybeMarkCooldown(tc.ce, tc.ae)
		})
	}
}

// keepCalls records the status-preserving key write made by markSeam's seam.
var keepCalls []int

// Per-key cooldown is the behaviour for multi-key channels whatever auto_ban
// says. OpenRouter keeps the historical status-flipping write; any other type
// with auto-ban off uses the status-preserving variant. Neither falls back to
// a whole-channel cooldown.
func TestMaybeMarkCooldown_MultiKey_AutoBanVariants_StayPerKey(t *testing.T) {
	withAutoDisable(t)
	cases := []struct {
		name     string
		typ      int
		autoBan  bool
		wantFlip int // calls to the status-flipping write
		wantKeep int // calls to the status-preserving write
	}{
		{"openrouter auto-ban off", constant.ChannelTypeOpenRouter, false, 1, 0},
		{"openrouter auto-ban on", constant.ChannelTypeOpenRouter, true, 1, 0},
		{"other auto-ban on", constant.ChannelTypeOpenAI, true, 1, 0},
		{"other auto-ban off", constant.ChannelTypeOpenAI, false, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := markSeam(t, true)
			ce := baseChannelErr()
			ce.ChannelId = 79
			ce.ChannelType = tc.typ
			ce.AutoBan = tc.autoBan
			MaybeMarkCooldown(ce, base429Err())
			if len(*calls) != tc.wantFlip || len(keepCalls) != tc.wantKeep {
				t.Fatalf("flip=%d keep=%d, want flip=%d keep=%d", len(*calls), len(keepCalls), tc.wantFlip, tc.wantKeep)
			}
			if app.ChannelCoolingUntil(79, 0) != 0 {
				t.Fatal("multi-key channel was put on a whole-channel cooldown")
			}
		})
	}
}

// The first attempt of a request carries IsMultiKey=false on the ChannelError;
// the real key mode must come from the channel lookup, so a multi-key channel
// cools only the used key and the channel itself stays routable.
func TestMaybeMarkCooldown_FirstAttemptFlagFalse_UsesRealKeyMode(t *testing.T) {
	withAutoDisable(t)
	calls := markSeam(t, true)
	prev := channelIsMultiKeyFn
	channelIsMultiKeyFn = func(types.ChannelError) bool { return true }
	t.Cleanup(func() { channelIsMultiKeyFn = prev })

	ce := baseChannelErr()
	ce.ChannelId = 80
	ce.ChannelType = constant.ChannelTypeOpenAI
	ce.IsMultiKey = false // what the first attempt actually carries
	ce.AutoBan = true
	MaybeMarkCooldown(ce, base429Err())
	if len(*calls) != 1 {
		t.Fatalf("per-key write calls = %v, want exactly one", *calls)
	}
	if app.ChannelCoolingUntil(80, 0) != 0 {
		t.Fatal("multi-key channel must not be put on a whole-channel cooldown")
	}
}

// Out-of-money 429s are never cooldowns, even when the automatic-disable switch
// is off and ShouldDisableChannel therefore reports false.
func TestMaybeMarkCooldown_BalanceExhausted_SwitchOff_NoCooldown(t *testing.T) {
	prev := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = false
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = prev })
	calls := markSeam(t, true)
	ae := types.WithOpenAIError(types.OpenAIError{
		Message: "You exceeded your current quota", Type: "insufficient_quota", Code: "insufficient_quota",
	}, 429)
	ae.UpstreamHeader = base429Err().UpstreamHeader
	MaybeMarkCooldown(baseChannelErr(), ae)
	if len(*calls) != 0 {
		t.Fatalf("balance-exhausted 429 became a cooldown with the switch off: %v", *calls)
	}
}

// seamAllParkedUntil is what markSeam's fake write reports as the channel-level
// recovery deadline (0 = a key is still serving).
var seamAllParkedUntil int64

// A 429 that the disable path owns (here an invalid_api_key code, which is not
// in the balance list) must not become a cooldown of either shape.
func TestMaybeMarkCooldown_DisableKeywordHit_NoCooldown(t *testing.T) {
	withAutoDisable(t)
	calls := markSeam(t, true)
	for _, ae := range []*types.NewAPIError{
		types.WithOpenAIError(types.OpenAIError{Message: "Incorrect API key provided", Type: "invalid_request_error", Code: "invalid_api_key"}, 429),
		types.WithOpenAIError(types.OpenAIError{Message: "Your credit balance is too low", Type: "invalid_request_error", Code: "x"}, 429),
	} {
		ae.UpstreamHeader = base429Err().UpstreamHeader
		if !app.ShouldDisableChannel(constant.ChannelTypeOpenAI, ae) {
			t.Fatalf("precondition: %q should hit the disable path", ae.Error())
		}
		multi := baseChannelErr()
		multi.ChannelType = constant.ChannelTypeOpenAI
		MaybeMarkCooldown(multi, ae)
		single := multi
		single.IsMultiKey = false
		single.ChannelId = 81
		MaybeMarkCooldown(single, ae)
		if len(*calls) != 0 || app.ChannelCoolingUntil(81, 0) != 0 {
			t.Fatalf("disable-path 429 %q wrote a cooldown (multi=%v, single=%d)", ae.Error(), *calls, app.ChannelCoolingUntil(81, 0))
		}
	}
}

// Parking the last usable key reports a recovery deadline, which MaybeMarkCooldown
// records as the channel-level slot selection reads; a write that leaves a key
// serving records nothing.
func TestMaybeMarkCooldown_LastKeyParked_WritesChannelSlot(t *testing.T) {
	withAutoDisable(t)
	for _, keep := range []bool{false, true} {
		calls := markSeam(t, true)
		want := time.Now().Unix() + 90
		seamAllParkedUntil = want
		t.Cleanup(func() { seamAllParkedUntil = 0 })
		ce := baseChannelErr()
		ce.ChannelId = 82
		ce.ChannelType = constant.ChannelTypeOpenAI
		ce.AutoBan = !keep
		MaybeMarkCooldown(ce, base429Err())
		if len(*calls)+len(keepCalls) != 1 {
			t.Fatalf("keep=%v: expected one per-key write", keep)
		}
		if got := app.ChannelCoolingUntil(82, 0); got != want {
			t.Fatalf("keep=%v: channel slot = %d, want %d", keep, got, want)
		}

		app.ClearChannelCooldowns()
		seamAllParkedUntil = 0
		MaybeMarkCooldown(ce, base429Err())
		if got := app.ChannelCoolingUntil(82, 0); got != 0 {
			t.Fatalf("keep=%v: channel slot written while a key still serves: %d", keep, got)
		}
	}
}

// The 24h "per day" body heuristic is an OpenRouter free-tier signal. A generic
// upstream whose 429 text merely mentions "per day" must get the short default,
// otherwise a single-key channel is cut off for a day with no way to clear it.
func TestMaybeMarkCooldown_DailyKeywordIsOpenRouterOnly(t *testing.T) {
	withAutoDisable(t)
	for _, tc := range []struct {
		name string
		typ  int
		id   int
		long bool
	}{
		{"openai", constant.ChannelTypeOpenAI, 83, false},
		{"openrouter", constant.ChannelTypeOpenRouter, 84, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			markSeam(t, true)
			ce := baseChannelErr()
			ce.ChannelId = tc.id
			ce.ChannelType = tc.typ
			ce.IsMultiKey = false
			ae := &types.NewAPIError{StatusCode: 429, UpstreamBodyHint: `{"error":{"message":"limit of 200 requests per day reached"}}`}
			MaybeMarkCooldown(ce, ae)
			got := app.ChannelCoolingUntil(tc.id, 0) - time.Now().Unix()
			if tc.long && got < int64(23*time.Hour/time.Second) {
				t.Fatalf("openrouter daily limit cooled for %ds, want ~24h", got)
			}
			if !tc.long && (got < 1 || got > 120) {
				t.Fatalf("generic 429 mentioning 'per day' cooled for %ds, want the short default", got)
			}
		})
	}
}

package openrouter_pool

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
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

// TestMaybeMarkCooldown_WrongChannelType_NoOp verifies the channel-type guard.
func TestMaybeMarkCooldown_WrongChannelType_NoOp(t *testing.T) {
	ce := baseChannelErr()
	ce.ChannelType = constant.ChannelTypeOpenAI // not OpenRouter
	MaybeMarkCooldown(ce, base429Err())
}

// TestMaybeMarkCooldown_NotMultiKey_NoOp verifies that single-key channels
// are ignored.
func TestMaybeMarkCooldown_NotMultiKey_NoOp(t *testing.T) {
	ce := baseChannelErr()
	ce.IsMultiKey = false
	MaybeMarkCooldown(ce, base429Err())
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
			name: "wrong channel type (Anthropic)",
			ce: types.ChannelError{
				ChannelId:   2,
				ChannelType: constant.ChannelTypeAnthropic,
				IsMultiKey:  true,
				UsingKey:    "sk-ant-key",
			},
			ae: base429Err(),
		},
		{
			name: "wrong channel type (Gemini)",
			ce: types.ChannelError{
				ChannelId:   3,
				ChannelType: constant.ChannelTypeGemini,
				IsMultiKey:  true,
				UsingKey:    "gemini-key",
			},
			ae: base429Err(),
		},
		{
			name: "multi-key false",
			ce: types.ChannelError{
				ChannelId:   4,
				ChannelType: constant.ChannelTypeOpenRouter,
				IsMultiKey:  false,
				UsingKey:    "sk-or-v1-key",
			},
			ae: base429Err(),
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
